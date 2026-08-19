// link_quality_eval.go 实现「接口链路质量（延迟 / 丢包）纯函数评估器」
// （v0.12 链路质量模拟，T1）。
//
// 定位说明（诚实原则）：华为 VRP 真机接口视图**不存在** delay / loss 命令，
// 链路时延与丢包在 eNSP 中由图形化链路属性设置。本项目是**仿真器**，
// 为了让实验者能在 CLI 内直接编排链路质量场景，把两者实现为接口视图的
// **仿真扩展命令**，并在 display 输出中显式标注为仿真扩展，避免让使用者
// 误以为真机存在同名命令。
//
// 架构基线（与 GRE / IPv6 / LAG / DHCP 中继完全同构）：
//
//   - 单一事实源 = state.DeviceConfig：
//     单向时延  interface:<if>:delay   规范化十进制毫秒串，未配缺键
//     单向丢包  interface:<if>:loss    规范化百分比串（最多一位小数），未配缺键
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化。
//
//   - **严禁在 state.go 新增任何链路质量内嵌结构体或字段**（架构铁律）。
//     LinkQualityEntry 仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不双写。
//
//   - 🔴 键碰撞红线：`delay` / `loss` 均为**短词**，极易被模糊匹配误伤
//     （例：`interface:<if>:gre-keepalive-period` 不含 delay，但未来若出现
//     `...:lag:delay` 之类三段以上键，Contains 匹配就会串味）。因此本文件
//     **严禁**任何 `strings.Contains(k, "delay")` / `Contains(k, "loss")`；
//     键解析一律走 parseInterfaceQualityKey：要求 `interface:<if>:<field>`
//     **恰好三段**且 field 精确等于 "delay" / "loss"。
//
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例）。
package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// —— 规格常量 ——

const (
	// LinkDelayMinMs / LinkDelayMaxMs 是单向链路时延的合法范围（毫秒）。
	// 上限 10000ms 足以覆盖卫星链路/跨洲链路教学场景，同时避免用户误输入
	// 天文数字导致 ping 超时语义失真（perTimeout = 3s）。
	LinkDelayMinMs = 0
	LinkDelayMaxMs = 10000

	// LinkLossMinPct / LinkLossMaxPct 是单向链路丢包率的合法范围（百分比）。
	LinkLossMinPct = 0.0
	LinkLossMaxPct = 100.0

	// linkQualityUnsetPlaceholder 是「未配置」的渲染占位符。
	// 未配置 ≠ 0：显示 "-" 以区分「显式配 0」与「从未配置」。
	linkQualityUnsetPlaceholder = "-"

	// linkQualityStatPlaceholder 是运行态统计字段的恒定占位符（诚实占位红线）：
	// lite 引擎不采集逐链路实测丢包/抖动，一律 "-"，绝不编造数字。
	linkQualityStatPlaceholder = "-"
)

// delayArgPattern / lossArgPattern 限定入参词法：时延为 1~5 位整数；
// 丢包为最多三位整数部分 + 可选一位小数。先词法后范围，错误文案可区分。
var (
	delayArgPattern = regexp.MustCompile(`^[0-9]{1,5}$`)
	lossArgPattern  = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9])?$`)
)

// —— 精确键 helper（红线：禁止 Contains 模糊匹配）——

// linkDelayKey 返回接口单向时延的 DeviceConfig 键。
func linkDelayKey(iface string) string {
	return fmt.Sprintf("interface:%s:delay", iface)
}

// linkLossKey 返回接口单向丢包率的 DeviceConfig 键。
func linkLossKey(iface string) string {
	return fmt.Sprintf("interface:%s:loss", iface)
}

// parseInterfaceQualityKey 精确解析链路质量键。
// 仅当 key 形如 `interface:<if>:delay` 或 `interface:<if>:loss`
// （**恰好三段**，第三段精确匹配字段名）时返回 ok=true。
// 形如 `interface:X:lag:delay`（四段）或 `interface:X:delay-extra` 一律拒绝。
func parseInterfaceQualityKey(key string) (iface, field string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] != "interface" || parts[1] == "" {
		return "", "", false
	}
	switch parts[2] {
	case "delay", "loss":
		return parts[1], parts[2], true
	}
	return "", "", false
}

// —— 入参校验（纯函数）——

// ValidateLinkDelay 校验 delay 命令实参，返回 (毫秒值, 错误文案)。
// 错误文案为空表示校验通过。
func ValidateLinkDelay(arg string) (int, string) {
	s := strings.TrimSpace(arg)
	if s == "" {
		return 0, "Error: incomplete command, usage: delay <0-10000>"
	}
	if !delayArgPattern.MatchString(s) {
		return 0, fmt.Sprintf("Error: invalid delay value '%s', expect integer milliseconds", arg)
	}
	ms, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Sprintf("Error: invalid delay value '%s'", arg)
	}
	if ms < LinkDelayMinMs || ms > LinkDelayMaxMs {
		return 0, fmt.Sprintf("Error: delay out of range (%d-%d ms)", LinkDelayMinMs, LinkDelayMaxMs)
	}
	return ms, ""
}

// ValidateLinkLoss 校验 loss 命令实参，返回 (百分比值, 错误文案)。
// 接受最多一位小数（如 0.5）；错误文案为空表示校验通过。
func ValidateLinkLoss(arg string) (float64, string) {
	s := strings.TrimSpace(arg)
	if s == "" {
		return 0, "Error: incomplete command, usage: loss <0-100>[.0-.9]"
	}
	if !lossArgPattern.MatchString(s) {
		return 0, fmt.Sprintf("Error: invalid loss value '%s', expect percent with at most one decimal", arg)
	}
	pct, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Sprintf("Error: invalid loss value '%s'", arg)
	}
	if pct < LinkLossMinPct || pct > LinkLossMaxPct {
		return 0, "Error: loss out of range (0-100 percent)"
	}
	return pct, ""
}

// FormatLinkLoss 把丢包率规范化为落盘/展示串：整数不带小数点，
// 非整数保留一位小数（0.5 -> "0.5"，10 -> "10"）。保证键值确定性，
// display saved-configuration 字节级稳定。
func FormatLinkLoss(pct float64) string {
	if pct == float64(int(pct)) {
		return strconv.Itoa(int(pct))
	}
	return strconv.FormatFloat(pct, 'f', 1, 64)
}

// —— 只读派生视图（不缓存，每次从 DeviceConfig 即时计算）——

// LinkQualityEntry 是单个接口的链路质量配置只读视图。
// HasDelay / HasLoss 用于区分「显式配 0」与「从未配置」。
type LinkQualityEntry struct {
	Interface string
	DelayMs   int
	HasDelay  bool
	LossPct   float64
	HasLoss   bool
}

// Configured 表示该接口至少配置了一项链路质量参数。
func (e LinkQualityEntry) Configured() bool {
	return e.HasDelay || e.HasLoss
}

// DelayText 渲染时延列（未配置为 "-"）。
func (e LinkQualityEntry) DelayText() string {
	if !e.HasDelay {
		return linkQualityUnsetPlaceholder
	}
	return strconv.Itoa(e.DelayMs)
}

// LossText 渲染丢包列（未配置为 "-"）。
func (e LinkQualityEntry) LossText() string {
	if !e.HasLoss {
		return linkQualityUnsetPlaceholder
	}
	return FormatLinkLoss(e.LossPct)
}

// interfaceLinkQuality 返回单个接口的链路质量视图。
func interfaceLinkQuality(state *CLIState, iface string) LinkQualityEntry {
	entry := LinkQualityEntry{Interface: iface}
	if state == nil || state.DeviceConfig == nil {
		return entry
	}
	if v, ok := state.DeviceConfig[linkDelayKey(iface)]; ok {
		if ms, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			entry.DelayMs = ms
			entry.HasDelay = true
		}
	}
	if v, ok := state.DeviceConfig[linkLossKey(iface)]; ok {
		if pct, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			entry.LossPct = pct
			entry.HasLoss = true
		}
	}
	return entry
}

// linkQualityInterfaces 返回所有配置了链路质量的接口名（升序，确定性）。
func linkQualityInterfaces(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return nil
	}
	seen := make(map[string]bool)
	for k := range state.DeviceConfig {
		if iface, _, ok := parseInterfaceQualityKey(k); ok {
			seen[iface] = true
		}
	}
	out := make([]string, 0, len(seen))
	for iface := range seen {
		out = append(out, iface)
	}
	sort.Strings(out)
	return out
}

// linkQualityEntries 返回所有已配置接口的链路质量视图（按接口名升序）。
func linkQualityEntries(state *CLIState) []LinkQualityEntry {
	names := linkQualityInterfaces(state)
	out := make([]LinkQualityEntry, 0, len(names))
	for _, name := range names {
		out = append(out, interfaceLinkQuality(state, name))
	}
	return out
}

// LinkQualityOf 是 interfaceLinkQuality 的导出封装，供 api 层把 CLI 配置
// 同步到 topology.Link（跨包只读访问，不暴露内部键格式）。
func LinkQualityOf(state *CLIState, iface string) LinkQualityEntry {
	return interfaceLinkQuality(state, iface)
}

// IsLinkQualityCommand 判断一条命令是否属于链路质量命令族：
// delay / loss / undo delay / undo loss。
//
// api 层据此决定「是否需要把配置同步到拓扑链路并重建引擎」。之所以按命令
// 而不是按「每次 CLI 调用全量同步」触发：全量同步会把通过 REST
// PUT /api/link 设置的 delay 在任意无关命令（如 dis version）后清零，
// 属行为回归。按命令触发则 undo 能正确清零、REST 设置不被误伤。
func IsLinkQualityCommand(cmd *Command) bool {
	if cmd == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(cmd.Command))
	if isLinkQualityCommandName(name) {
		return true
	}
	if name == "undo" && len(cmd.Args) > 0 {
		return isLinkQualityCommandName(cmd.Args[0])
	}
	return false
}

// clearLinkQualityKeys 删除指定接口的全部链路质量键（undo 用）。
// 走精确键，绝不前缀扫描，避免误删同接口其它配置。
func clearLinkQualityKeys(state *CLIState, iface string) {
	if state == nil || state.DeviceConfig == nil {
		return
	}
	delete(state.DeviceConfig, linkDelayKey(iface))
	delete(state.DeviceConfig, linkLossKey(iface))
}
