// stp_eval.go 实现「CLIState 层 STP/RSTP/MSTP 评估器」（P2 第四项，华为 VRP 课程 55/56/57）。
//
// 背景与约束见 docs/p2-stp-prd.md 与 docs/p2-stp-design.md。本评估器把代码库里既有的
// 「仅写 state.STP 结构化 map、不经 DeviceConfig 持久化、无状态机、无诚实占位」的 STP 残桩，
// 升级为一条可对学员实验产生可观测反馈的二层生成树（环路避免）链路。
//
// 架构基线（与端口安全 / NAT / VRRP 完全一致，见设计 §1）：
//   - 单一事实源 = state.DeviceConfig（stp:<field> 系统级 + interface:<iface>:stp:<field> 接口级），
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化，
//     从根上修复残桩 save/reload 丢配置缺陷（P0-1）。
//   - STPInstance / STPPortResult / STPResult 仅为「从 DeviceConfig 即时派生的只读视图」，
//     不缓存、不重复，彻底消除双写不一致风险（state.STP 字段已移除，方案 A）。
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import protocol 包），
//     可单测、可回归，与 acl_eval.go 的 EvaluatePathACL / applyNAT、
//     portsec_eval.go 的 EvaluatePortSecurity / vrrp_eval.go 的 EvaluateVRRP 同一契约。
//   - 任何新代码不得新建对 sim 引擎实例的调用；本文件仅读 sim.EngineModeName()
//     以决定诚实占位注记的 lite/full 两态（与 aclSimNote/natSimNote/portSecSimNote/vrrpSimNote 同口径）。
//
// 诚实边界（主理人拍板，见设计 §0 / §8）：
//   - 真实根桥选举依赖设备间 BPDU 收发，当前 sim 引擎无真实 BPDU 收发与拓扑计算，
//     故本期为「本地静态假设选举」+ 诚实注记；绝不臆造真实收敛态的 Backup/Alternate。
//   - 端口角色按 §1.5 启发式静态分配（DESI/ROOT/ALTE/BACK），每行诚实标注；
//     本桥恒假设为根桥（IsRoot=true，lite 仅一台桥）。
//   - protections / bridge-address / tc-protection 仅配置态 + 展示，不触发真实保护动作 / 不计数。
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// BridgeID 是桥标识（priority + MAC），用于本地静态根桥比较（AC5 / P2 跨设备预留）。
type BridgeID struct {
	Priority int    // 0-61440，4096 倍数
	Address  string // VRP MAC 格式 xxxx-xxxx-xxxx
}

// STPPortResult 是单端口的本地静态评估结果（只读视图）。
//   Role/State 一律为「本地静态假设」，非真实 BPDU 收敛（见拍板 #2 + §1.5）。
type STPPortResult struct {
	Interface    string
	Cost         int    // 端口路径开销（interface:<iface>:stp:cost 或缺省）
	PortPriority int    // 端口优先级（interface:<iface>:stp:port-priority，缺省 128）
	Edged        bool   // 边缘端口
	Down         bool   // 端口 shutdown
	Role         string // DESI | ROOT | ALTE | BACK（本地静态假设）
	State        string // FORWARDING | DISCARDING | DOWN（本地静态假设）
	Note         string // 每行诚实注记
}

// STPInstance 是某 MST 实例（含 CIST，instanceID=0）的派生配置 + 本地静态选举结果。
type STPInstance struct {
	InstanceID    int
	IsActive      bool // region-active（MSTP）
	IsRoot        bool // 本地静态：本桥恒为根桥（lite 仅一台桥，拍板 #2）
	BridgePriority int
	BridgeAddress string
	RootPriority  int // 本地静态：= 本设备（假想根桥）
	RootAddress   string
	RootPathCost  int // 本地静态：= 0
	Ports         []STPPortResult
}

// STPResult 是 EvaluateSTP 的汇总返回（display 渲染用）。
type STPResult struct {
	Enabled       bool
	Mode          string // stp | rstp | mstp
	PathCostStd   string // dot1d-1998 | dot1t | legacy
	IsRoot        bool   // 本地静态：本设备即根桥
	CIST          STPInstance
	Instances     []STPInstance // MSTP 实例（id>0），region 已配置时填充
	BPDUProtection bool
	RootProtection  bool
	LoopProtection  bool
	TCProtection    bool
	TCInterval      int
}

// STP DeviceConfig 键名约定（单一事实源，方案 A）。详见 design §3.2。
const (
	stpModeDefault      = "mstp"
	stpPriDefault       = 32768
	stpPriMin, stpPriMax = 0, 61440
	stpPriStep          = 4096
	stpPCStdDefault     = "dot1t"
	stpPortPriDefault   = 128
	stpPortPriMin, stpPortPriMax = 0, 240
	stpPortPriStep      = 16
	stpRevDefault       = 0
	stpTCIntervalDefault = 10
	// cost 上界（依 pathcost-standard）
	stpCostMaxLegacy    = 200000
	stpCostMaxDot1d1998 = 65535
	stpCostMaxDot1t     = 200000000
	stpCostMin          = 1
	// 默认端口开销（依 pathcost-standard，1Gbps 缺省）
	stpDefCostDot1t     = 20000
	stpDefCostDot1d1998 = 200000
	stpDefCostLegacy    = 200000
)

// stpKey 拼接系统级键：stp:<field>。
func stpKey(field string) string {
	return "stp:" + field
}

// stpIfaceKey 拼接接口级键：interface:<iface>:stp:<field>。
func stpIfaceKey(iface, field string) string {
	return fmt.Sprintf("interface:%s:stp:%s", iface, field)
}

// stpMode 返回当前 STP 模式（缺省 mstp，拍板 #7）。纯函数（只读 DeviceConfig）。
func stpMode(state *CLIState) string {
	if v := state.DeviceConfig[stpKey("mode")]; v != "" {
		return v
	}
	return stpModeDefault
}

// stpPathCostStd 返回当前 pathcost-standard（缺省 dot1t，拍板 #7）。纯函数（只读）。
func stpPathCostStd(state *CLIState) string {
	if v := state.DeviceConfig[stpKey("pathcost-standard")]; v != "" {
		return v
	}
	return stpPCStdDefault
}

// isSTPEnabled 返回 STP 是否启用（缺省开启，VRP STP 默认开启，§7 #4）。
func isSTPEnabled(state *CLIState) bool {
	return state.DeviceConfig[stpKey("enabled")] != "false"
}

// stpBridgePriority 返回某实例的桥优先级（缺省 32768，须 4096 倍数）。
func stpBridgePriority(state *CLIState, instanceID int) int {
	key := stpKey("priority")
	if instanceID > 0 {
		key = fmt.Sprintf("stp:instance:%d:priority", instanceID)
	}
	if v := state.DeviceConfig[key]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return stpPriDefault
}

// deriveMACFromName 由设备名派生稳定占位 MAC（VRP 格式，确定性）。
// 仅当未配置 bridge-address / bridge-mac 时使用（display-only，诚实边界 O3）。
func deriveMACFromName(name string) string {
	h := uint32(0x811c9dc5)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 0x01000193
	}
	return fmt.Sprintf("4c1f-cc%02x-%02x%02x", byte(h>>16), byte(h>>8), byte(h))
}

// stpDeviceMAC 返回本设备桥 MAC（纯函数，只读 DeviceConfig）。
//   优先 stp:bridge-address（P1-6）；其次 DeviceConfig["bridge-mac"]；否则由 DeviceName 派生。
func stpDeviceMAC(state *CLIState) string {
	if v := state.DeviceConfig[stpKey("bridge-address")]; v != "" {
		return v
	}
	if v := state.DeviceConfig["bridge-mac"]; v != "" {
		return v
	}
	return deriveMACFromName(state.DeviceName)
}

// isPortDown 读端口状态判定端口是否 Down（纯函数，只读 DeviceConfig / Interfaces）。
func isPortDown(state *CLIState, iface string) bool {
	if state == nil {
		return false
	}
	if state.DeviceConfig[fmt.Sprintf("interface:%s:status", iface)] == "Down" {
		return true
	}
	if ic, ok := state.Interfaces[iface]; ok {
		if ic != nil && strings.EqualFold(ic.Status, "Down") {
			return true
		}
	}
	return false
}

// defaultPortCost 返回指定 pathcost-standard 下的缺省端口开销（1Gbps 缺省，§7 #5）。
func defaultPortCost(std string) int {
	switch std {
	case "dot1d-1998":
		return stpDefCostDot1d1998
	case "legacy":
		return stpDefCostLegacy
	default: // dot1t（默认）
		return stpDefCostDot1t
	}
}

// validPriority 校验 STP 系统桥优先级（0–61440，须 4096 倍数，拍板 #4）。
func validPriority(v int) (bool, string) {
	if v < stpPriMin || v > stpPriMax {
		return false, fmt.Sprintf("Error: STP priority %d out of range [%d, %d]", v, stpPriMin, stpPriMax)
	}
	if v%stpPriStep != 0 {
		return false, fmt.Sprintf("Error: STP priority %d must be a multiple of %d", v, stpPriStep)
	}
	return true, ""
}

// validCost 校验接口路径开销（依 pathcost-standard 三档上界，拍板 #5）。
func validCost(v int, std string) (bool, string) {
	max := stpCostMaxDot1t
	switch std {
	case "legacy":
		max = stpCostMaxLegacy
	case "dot1d-1998":
		max = stpCostMaxDot1d1998
	}
	if v < stpCostMin || v > max {
		return false, fmt.Sprintf("Error: STP cost %d out of range [%d, %d] for pathcost-standard %s", v, stpCostMin, max, std)
	}
	return true, ""
}

// validPortPriority 校验接口端口优先级（0–240，步进 16，拍板 #5）。
func validPortPriority(v int) (bool, string) {
	if v < stpPortPriMin || v > stpPortPriMax {
		return false, fmt.Sprintf("Error: STP port priority %d out of range [%d, %d]", v, stpPortPriMin, stpPortPriMax)
	}
	if v%stpPortPriStep != 0 {
		return false, fmt.Sprintf("Error: STP port priority %d must be a multiple of %d", v, stpPortPriStep)
	}
	return true, ""
}

// validInstanceID 校验 MST 实例 ID（0–4094）。
func validInstanceID(v int) (bool, string) {
	if v < 0 || v > 4094 {
		return false, fmt.Sprintf("Error: MST instance ID %d out of range [0, 4094]", v)
	}
	return true, ""
}

// normalizeMACHex 将 VRP MAC（xxxx-xxxx-xxxx / xx:xx:...）归一化为小写连续十六进制串，
// 便于按字典序比较「小者胜」（每组固定 2 字节、大端，去分隔符后字典序==数值序）。
func normalizeMACHex(mac string) string {
	r := strings.NewReplacer("-", "", ":", "")
	return strings.ToLower(r.Replace(mac))
}

// CompareBridgeID 比较两桥 ID 决定胜负（纯函数，无副作用；AC5 / P2 跨设备预留）。
//
// 规则（拍板 #2，已更正 O1：同优先级比 MAC **小者**胜，标准 STP 桥 ID 比较）：
//   - Priority 小者胜；
//   - 同 Priority 比 Address 小者胜（VRP MAC 格式，去短横后按十六进制/字典序比较）；
//   返回 >0 表示 a 胜、<0 表示 b 胜、0 表示完全相等（确定性 tie-break）。
func CompareBridgeID(a, b BridgeID) int {
	if a.Priority != b.Priority {
		if a.Priority < b.Priority {
			return 1
		}
		return -1
	}
	aa := normalizeMACHex(a.Address)
	ab := normalizeMACHex(b.Address)
	if aa == ab {
		return 0
	}
	if aa < ab {
		return 1
	}
	return -1
}

// SelectRootBridge 在两组实例间选根桥（纯函数，包装 CompareBridgeID 于 STPInstance）。
// 返回 >0 表示 a 胜、<0 表示 b 胜、0 表示相等。
func SelectRootBridge(a, b STPInstance) int {
	return CompareBridgeID(
		BridgeID{Priority: a.BridgePriority, Address: a.BridgeAddress},
		BridgeID{Priority: b.BridgePriority, Address: b.BridgeAddress},
	)
}

// stpSimNote 返回 STP「诚实占位」注记（lite/full 两态，口径同 aclSimNote/natSimNote/portSecSimNote/vrrpSimNote）。
//   lite → "（STP 为模拟生成树（lite 引擎），非内核级真实 BPDU 选举 / 无真实拓扑收敛）"
//   full → "（STP 为模拟生成树）"
func stpSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（STP 为模拟生成树（lite 引擎），非内核级真实 BPDU 选举 / 无真实拓扑收敛）"
	}
	return "（STP 为模拟生成树）"
}

// formatBridgeID 拼接桥 ID 展示串：<priority>.<mac>（如 4096.4c1f-cc12-3456）。
func formatBridgeID(pri int, addr string) string {
	return fmt.Sprintf("%d.%s", pri, addr)
}

// sortedInterfaceNames 返回所有接口名（取自 state.Interfaces）按字典序升序。
// 纯函数（只读 state.Interfaces）。
func sortedInterfaceNames(state *CLIState) []string {
	if state == nil || len(state.Interfaces) == 0 {
		return nil
	}
	names := make([]string, 0, len(state.Interfaces))
	for k := range state.Interfaces {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// collectSTPInstances 返回已配置实例 ID 列表（含恒有的 0=CIST + region 中配置的 id>0）。
// 纯函数（只读 DeviceConfig）。id>0 仅当 region-active 后置纳入（§7 #11）。
func collectSTPInstances(state *CLIState) []int {
	set := map[int]bool{0: true}
	if state == nil {
		return []int{0}
	}
	active := state.DeviceConfig[stpKey("region-active")] == "true"
	prefix := "stp:instance:"
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix) // "<id>:vlans"
		idx := strings.Index(rest, ":")
		if idx <= 0 {
			continue
		}
		id, err := strconv.Atoi(rest[:idx])
		if err != nil || id <= 0 {
			continue
		}
		if active {
			set[id] = true
		}
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// collectSTPPorts 从 DeviceConfig / Interfaces 派生各端口的本地静态角色（只读，无副作用）。
// 端口角色启发式（§1.5）：Down 端口 → `--`/`DOWN`；edged 端口 → DESI/FORWARDING；
// 其余 active 非边缘端口（按接口名升序）首端口 → ROOT/FORWARDING、末端口 → ALTE/DISCARDING、
// 中间 → DESI/FORWARDING；每行附诚实注记。
func collectSTPPorts(state *CLIState, instanceID int) []STPPortResult {
	names := sortedInterfaceNames(state)
	std := stpPathCostStd(state)

	type portTmp struct {
		name    string
		down    bool
		edged   bool
		cost    int
		portPri int
	}
	all := make([]portTmp, 0, len(names))
	for _, iface := range names {
		down := isPortDown(state, iface)
		edged := state.DeviceConfig[stpIfaceKey(iface, "edged-port")] == "enable"
		cost := defaultPortCost(std)
		if v := state.DeviceConfig[stpIfaceKey(iface, "cost")]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				cost = n
			}
		}
		portPri := stpPortPriDefault
		if v := state.DeviceConfig[stpIfaceKey(iface, "port-priority")]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				portPri = n
			}
		}
		all = append(all, portTmp{iface, down, edged, cost, portPri})
	}

	// 候选（active 非边缘端口）索引，保持 names 升序。
	var candidates []int
	for i, p := range all {
		if !p.down && !p.edged {
			candidates = append(candidates, i)
		}
	}

	ports := make([]STPPortResult, 0, len(all))
	for i, p := range all {
		port := STPPortResult{
			Interface:    p.name,
			Cost:         p.cost,
			PortPriority: p.portPri,
			Edged:        p.edged,
			Down:         p.down,
		}
		if p.down {
			port.Role = "--"
			port.State = "DOWN"
			port.Note = ""
		} else if p.edged {
			port.Role = "DESI"
			port.State = "FORWARDING"
			port.Note = "(edged)"
		} else {
			pos := -1
			for ci, idx := range candidates {
				if idx == i {
					pos = ci
					break
				}
			}
			switch {
			case len(candidates) > 0 && pos == 0:
				port.Role = "ROOT"
				port.State = "FORWARDING"
				port.Note = "(本地静态假设，非真实 BPDU 选举)"
			case len(candidates) > 1 && pos == len(candidates)-1:
				port.Role = "ALTE"
				port.State = "DISCARDING"
				port.Note = "(本地静态阻塞，非真实拓扑收敛)"
			default:
				port.Role = "DESI"
				port.State = "FORWARDING"
				port.Note = "(本地静态假设，非真实 BPDU 选举)"
			}
		}
		ports = append(ports, port)
	}
	return ports
}

// EvaluateSTP 本地静态根桥/端口角色选举纯函数（无副作用、不写引擎、不 import protocol、可单测）。
//
// 规则（拍板 #2 + 设计 §1.5）：
//   - 根桥（Root Bridge）= 本设备自身：lite 仿真只有本设备一台桥，本地静态假设「本桥即网内最小桥 ID」
//     → RootPriority/RootAddress = 本设备 BridgeID，RootPathCost = 0，IsRoot = true。
//   - 端口角色按 collectSTPPorts 启发式静态分配（确定性、带标注）。
//   - 不修改任何 state 字段、不写 DeviceConfig、不 import sim 引擎实例；
//     仅 stpSimNote 读 sim.EngineModeName() 决定 lite/full 注记。
func EvaluateSTP(state *CLIState, instanceID int) STPInstance {
	inst := STPInstance{InstanceID: instanceID, IsActive: instanceID == 0}
	if state == nil {
		return inst
	}
	pri := stpBridgePriority(state, instanceID)
	addr := stpDeviceMAC(state)
	inst.BridgePriority = pri
	inst.BridgeAddress = addr
	inst.RootPriority = pri
	inst.RootAddress = addr
	inst.RootPathCost = 0
	inst.IsRoot = true
	if instanceID > 0 {
		inst.IsActive = state.DeviceConfig[stpKey("region-active")] == "true"
	}
	inst.Ports = collectSTPPorts(state, instanceID)
	return inst
}
