// link_quality_sync.go 把 CLI 侧的接口链路质量配置（delay / loss）同步到
// 拓扑链路模型，使仿真引擎（internal/sim）能读到生效值（v0.12 链路质量模拟）。
//
// 数据流：
//
//	接口视图 delay/loss  ->  CLIState.DeviceConfig["interface:<if>:delay|loss"]
//	                     ->  syncLinkQualityForInterface（本文件）
//	                     ->  topology.Link.Delay / Link.Loss
//	                     ->  r.syncEngine(topoID) 去抖重建
//	                     ->  引擎 Ping / Traceroute 读链路属性
//
// 两条关键设计约束：
//
//  1. **两端取较大值**：一条链路的两个端口可能各自配置了 delay/loss。
//     取 max 而非「后写覆盖」，保证结果与命令下发顺序无关（确定性），
//     且语义悲观（更差的那一端决定链路质量），便于实验复现。
//
//  2. **按命令触发，不做全量同步**：仅当本次执行的确是链路质量命令时，
//     才同步「该接口所在的那一条链路」。若改成每次 CLI 调用全量扫描全部链路，
//     则通过 REST PUT /api/link 设置过 delay 的链路会在任意无关命令后被清零
//     （REST 侧 `delay != 0` 才更新，无法表达「显式 0」），属行为回归。
package api

import (
	"strings"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/topology"
)

// syncLinkQualityForInterface 把 deviceID/iface 所在链路的 delay/loss 重算并写回 t。
// states 为拓扑内各设备的 CLIState 快照（deviceID -> *CLIState），用于读取对端配置。
// 返回 true 表示链路属性确实发生变化（调用方据此决定是否重建引擎）。
//
// 调用前提：t 必须是调用方已 Clone 的工作副本（写类 handler 铁律），
// 本函数直接修改 t.Links 元素。
func syncLinkQualityForInterface(t *topology.Topology, deviceID, iface string, states map[string]*cli.CLIState) bool {
	if t == nil || deviceID == "" || strings.TrimSpace(iface) == "" {
		return false
	}
	link := findLinkByEndpoint(t, deviceID, iface)
	if link == nil {
		// 接口未连线：配置已落 DeviceConfig（display link-quality 可见），
		// 但没有链路可承载，不算错误，也无需重建引擎。
		return false
	}
	delay, loss := effectiveLinkQuality(link, states)
	changed := false
	if link.Delay != delay {
		link.Delay = delay
		changed = true
	}
	if link.Loss != loss {
		link.Loss = loss
		changed = true
	}
	return changed
}

// findLinkByEndpoint 查找以 (deviceID, iface) 为任一端点的链路。
// 端口名比较大小写不敏感，与 CLI 接口名归一化口径一致。
func findLinkByEndpoint(t *topology.Topology, deviceID, iface string) *topology.Link {
	for _, l := range t.Links {
		if l == nil {
			continue
		}
		if l.SourceDevice == deviceID && strings.EqualFold(l.SourcePort, iface) {
			return l
		}
		if l.TargetDevice == deviceID && strings.EqualFold(l.TargetPort, iface) {
			return l
		}
	}
	return nil
}

// effectiveLinkQuality 计算链路的生效 delay / loss：取两端接口配置的较大值。
// 两端均未配置时返回 (0, 0)，即「无额外时延与丢包」——这正是 undo 后的期望值。
func effectiveLinkQuality(link *topology.Link, states map[string]*cli.CLIState) (int, float64) {
	delay := 0
	loss := 0.0
	for _, ep := range [2]struct {
		device string
		port   string
	}{
		{link.SourceDevice, link.SourcePort},
		{link.TargetDevice, link.TargetPort},
	} {
		st := states[ep.device]
		if st == nil {
			continue
		}
		e := cli.LinkQualityOf(st, ep.port)
		if e.HasDelay && e.DelayMs > delay {
			delay = e.DelayMs
		}
		if e.HasLoss && e.LossPct > loss {
			loss = e.LossPct
		}
	}
	return delay, loss
}
