// dhcp_relay_eval.go 实现「CLIState 层 DHCP 中继（DHCP Relay）纯函数评估器」
// （P2 第六项，华为 VRP 课程 27，T1）。
//
// 背景与约束见 docs/p2-dhcp-prd.md 与 docs/p2-dhcp-design.md。
//
// 架构基线（与 STP / VRRP / 链路聚合完全同构，见设计 §1.5）：
//   - 单一事实源 = state.DeviceConfig：
//     接口 DHCP 模式  interface:<if>:dhcp-select          值 ∈ global | interface | relay
//     中继服务器列表  interface:<if>:dhcp-relay:server-ips 逗号串，保序去重，上限 8
//     Option82 开关   interface:<if>:dhcp-relay:option82   "true" / "false"
//     Option82 策略   interface:<if>:dhcp-relay:option82-strategy  drop | keep | replace
//     中继源地址      interface:<if>:dhcp-relay:source-ip  合法 IPv4
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化
//     （设计 A7：LoadFromDeviceConfigData 零改动）。
//   - **严禁在 state.go / DHCPConfig 新增任何 relay 内嵌结构体**（架构铁律 1，AC11）。
//     RelayConfig / RelayResult 仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不双写。
//   - **不存在 dhcp-relay:mode 独立键**（设计 A1）：模式唯一事实源 = dhcp-select 键，
//     RelayConfig.Mode 是读出派生的，不写回。
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import internal/protocol），
//     与 stp_eval.go 的 EvaluateSTP、lag_eval.go 的 EvaluateLAG 同一契约。
//   - 本文件仅读 sim.EngineModeName() 决定诚实占位注记的 lite/full 两态。
//   - 复用既有 helper，严禁重定义（同包编译冲突）：
//     isPortDown  (stp_eval.go:175)  Interface status 唯一判定源
//     l3Devices   (capabilities.go)  设备类型守卫集合（在 dhcp_relay_cmd.go 消费）
//
// ⚠️ 键碰撞红线（设计 §1.6）：既有 interface:<if>:dhcp-pool（地址池绑定键，parser.go:2646）
// 与本期新增键**共享 :dhcp 前缀**。本文件所有键解析一律使用「精确后缀 :dhcp-select」
// 与「精确前缀 :dhcp-relay:」匹配，**严禁 strings.Contains(k, "dhcp") 模糊扫描**，
// 否则会把地址池绑定键误判为中继接口（幽灵接口，同 LAG 幽灵组缺陷）。
//
// 诚实占位（主理人拍板 #4，红线）：
//   - 本工具无真实 DHCP 报文转发引擎、无 UDP 67/68 收发、无对端 DHCP 服务器，
//     故 RelayStats 六个字段**类型全部为 string 且恒为 "-"**，
//     结构体内不存在任何 int / 计数器 / 随机数路径——从类型层面杜绝日后填数字（AC8）。
//   - SourceIP 未配时恒 "-"，**绝不推导接口主 IP**（推导即臆造）。
package cli

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"ensp-lab/internal/sim"
)

// —— 缺省值与规格常量（设计 §2 改动点 #9 / 拍板 #6）——

const (
	// DefaultOption82Strategy 是 Option82 中继信息处理策略的生效缺省值。
	// 设计 A5：display 未配时显示该生效缺省值（不是 "-"），
	// 但 current-configuration 仅输出差异值（未配不输出该行）。
	DefaultOption82Strategy = "replace"

	// MaxRelayServerIPs 是单接口 DHCP 中继服务器地址数量上限（拍板 #6）。
	MaxRelayServerIPs = 8

	// DefaultOption82Enabled 是 Option82 中继信息选项的缺省开关状态。
	DefaultOption82Enabled = false
)

// relayStatPlaceholder 是运行态转发统计的诚实占位符（拍板 #4，红线）。
// 仿真环境无真实报文转发，所有转发统计字段恒为该值。
const relayStatPlaceholder = "-"

// relayModeRelay / relayModeGlobal / relayModeInterface 是 dhcp select 的三态取值。
const (
	relayModeGlobal    = "global"
	relayModeInterface = "interface"
	relayModeRelay     = "relay"
)

// dhcpSelectKeySuffix 是接口 DHCP 模式键的**精确后缀**（§1.6 键碰撞红线）。
const dhcpSelectKeySuffix = ":dhcp-select"

// dhcpRelayKeyInfix 是中继参数键的**精确中缀**（§1.6 键碰撞红线）。
const dhcpRelayKeyInfix = ":dhcp-relay:"

// dhcpRelayFieldServerIPs 等为中继参数键的字段名（全仓唯一定义，禁止各处手写字面量）。
const (
	dhcpRelayFieldServerIPs = "server-ips"
	dhcpRelayFieldOption82  = "option82"
	dhcpRelayFieldStrategy  = "option82-strategy"
	dhcpRelayFieldSourceIP  = "source-ip"
)

// dhcpRelayFields 是中继参数键的全部字段名（级联清理与遍历用，顺序即 VRP 输出顺序）。
var dhcpRelayFields = []string{
	dhcpRelayFieldServerIPs,
	dhcpRelayFieldOption82,
	dhcpRelayFieldStrategy,
	dhcpRelayFieldSourceIP,
}

// validOption82Strategies 是 Option82 策略的合法枚举（严格校验，P1-2）。
var validOption82Strategies = map[string]bool{
	"drop":    true,
	"keep":    true,
	"replace": true,
}

// —— 类型定义（设计 §4.2）——

// RelayConfig 是单接口 DHCP 中继配置的**只读派生视图**。
// 全部字段从 DeviceConfig 键即时读出并合并缺省值，不落独立结构体、不双写。
type RelayConfig struct {
	// Mode 派生自 interface:<if>:dhcp-select（设计 A1：不存在 dhcp-relay:mode 独立键）。
	// 取值 "relay" / "global" / "interface" / ""（未配）。
	Mode string
	// ServerIPs 有序、去重、长度 ≤ MaxRelayServerIPs。未配时为空切片（非 nil）。
	ServerIPs []string
	// Option82 对应 interface:<if>:dhcp-relay:option82，缺省 DefaultOption82Enabled。
	Option82 bool
	// Option82Strategy 是**生效值**：未配即 DefaultOption82Strategy（A5：显示 replace，不显示 "-"）。
	Option82Strategy string
	// SourceIP 未配即 ""（渲染层恒显示 "-"，**不得推导接口主 IP**，拍板 #4）。
	SourceIP string
}

// RelayStats 是 DHCP 中继转发统计的**诚实占位**结构（拍板 #4，红线）。
//
// ⚠️ 六个字段类型全部为 string 且恒赋 relayStatPlaceholder（"-"）。
// **结构体内不得出现任何 int / 计数器 / 随机数路径**——从类型层面杜绝日后有人填数字（AC8）。
// 字段名与 PRD §4.2 的 6 个显示标签 1:1 对应，渲染时直接拼标签，避免二次翻译错配。
type RelayStats struct {
	DHCPPacketsForwarded string // 显示 "DHCP packets forwarded"  恒 "-"
	DiscoverForwarded    string // 显示 "DISCOVER forwarded"      恒 "-"
	OfferReceived        string // 显示 "OFFER received"          恒 "-"
	RequestForwarded     string // 显示 "REQUEST forwarded"       恒 "-"
	AckReceived          string // 显示 "ACK received"            恒 "-"
	ServerReachability   string // 显示 "Server reachability"     恒 "-"（严禁 Reachable/Up/Active）
}

// RelayResult 是 EvaluateDHCPRelay 的返回值：单接口中继评估结果。
type RelayResult struct {
	// Interface 是被评估的接口名（原样回传，便于渲染层直接使用）。
	Interface string
	// Config 是已合并缺省值的完整配置。
	Config RelayConfig
	// Active 表示该接口是否为「生效的中继代理」：Mode == relay 且 ServerIPs 非空。
	Active bool
	// SimNote 是诚实占位注记（与 dhcpRelaySimNote() 同源）。
	SimNote string
	// Stats 是转发统计诚实占位：六字段恒 "-"。
	Stats RelayStats
}

// —— 键构造 helper（全仓拼键唯一出口，设计 §4.3）——

// dhcpSelectKey 返回接口 DHCP 模式键：interface:<if>:dhcp-select。
func dhcpSelectKey(iface string) string {
	return fmt.Sprintf("interface:%s%s", iface, dhcpSelectKeySuffix)
}

// dhcpRelayKey 返回中继参数键：interface:<if>:dhcp-relay:<field>。
// field ∈ server-ips | option82 | option82-strategy | source-ip。
func dhcpRelayKey(iface, field string) string {
	return fmt.Sprintf("interface:%s%s%s", iface, dhcpRelayKeyInfix, field)
}

// dhcpRelayKeyPrefix 返回某接口全部中继参数键的**精确前缀**（级联清理用，§1.6）。
func dhcpRelayKeyPrefix(iface string) string {
	return fmt.Sprintf("interface:%s%s", iface, dhcpRelayKeyInfix)
}

// —— 读取端纯函数 ——

// dhcpSelectMode 读取接口 DHCP 模式（仅读 dhcp-select 键；无键返 ""）。
// 纯函数：只读 DeviceConfig，无副作用。
func dhcpSelectMode(state *CLIState, iface string) string {
	if state == nil || state.DeviceConfig == nil || strings.TrimSpace(iface) == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(state.DeviceConfig[dhcpSelectKey(iface)]))
}

// readRelayConfig 读出单接口中继配置并合并缺省值（设计 A5）。
// 纯函数：只读 DeviceConfig，无副作用；返回值中 ServerIPs 恒为非 nil 切片。
func readRelayConfig(state *CLIState, iface string) RelayConfig {
	cfg := RelayConfig{
		ServerIPs:        []string{},
		Option82:         DefaultOption82Enabled,
		Option82Strategy: DefaultOption82Strategy,
	}
	if state == nil || state.DeviceConfig == nil || strings.TrimSpace(iface) == "" {
		return cfg
	}
	cfg.Mode = dhcpSelectMode(state, iface)
	cfg.ServerIPs = parseRelayServerIPs(state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)])
	if v := strings.ToLower(strings.TrimSpace(state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldOption82)])); v != "" {
		cfg.Option82 = v == "true"
	}
	if v := strings.ToLower(strings.TrimSpace(state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldStrategy)])); v != "" && validOption82Strategies[v] {
		cfg.Option82Strategy = v
	}
	cfg.SourceIP = strings.TrimSpace(state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldSourceIP)])
	return cfg
}

// EvaluateDHCPRelay 是 DHCP 中继评估主入口（纯函数，只读，无副作用）。
//
// 返回已合并缺省值的 Config、本地可判定的 Active、诚实注记 SimNote，
// 以及**恒为 "-" 的转发统计** Stats（拍板 #4，红线：不臆造任何运行态数字）。
func EvaluateDHCPRelay(state *CLIState, iface string) RelayResult {
	cfg := readRelayConfig(state, iface)
	return RelayResult{
		Interface: iface,
		Config:    cfg,
		Active:    cfg.Mode == relayModeRelay && len(cfg.ServerIPs) > 0,
		SimNote:   dhcpRelaySimNote(),
		Stats:     newRelayStats(),
	}
}

// newRelayStats 构造诚实占位的转发统计：六字段全部恒 "-"（AC8 红线）。
func newRelayStats() RelayStats {
	return RelayStats{
		DHCPPacketsForwarded: relayStatPlaceholder,
		DiscoverForwarded:    relayStatPlaceholder,
		OfferReceived:        relayStatPlaceholder,
		RequestForwarded:     relayStatPlaceholder,
		AckReceived:          relayStatPlaceholder,
		ServerReachability:   relayStatPlaceholder,
	}
}

// —— 多接口聚合（display ... all 与持久化用）——

// collectRelayInterfaces 收集全部「DHCP 中继接口」，按接口名**升序**返回（确定性，AC7）。
//
// 判定口径（union，覆盖幽灵残留检测）：
//  1. interface:<if>:dhcp-select 值为 relay；或
//  2. 存在任意 interface:<if>:dhcp-relay:<field> 键。
//
// ⚠️ 一律使用精确后缀 :dhcp-select 与精确中缀 :dhcp-relay: 匹配，
// **绝不误伤既有 interface:<if>:dhcp-pool 地址池绑定键**（§1.6 键碰撞红线）。
func collectRelayInterfaces(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k, v := range state.DeviceConfig {
		if iface, ok := ifaceFromDHCPSelectKey(k); ok {
			if strings.ToLower(strings.TrimSpace(v)) == relayModeRelay {
				seen[iface] = true
			}
			continue
		}
		if iface, ok := ifaceFromDHCPRelayKey(k); ok {
			seen[iface] = true
		}
	}
	return sortedKeys(seen)
}

// collectDHCPSelectInterfaces 收集全部「配置过任意 DHCP 接口模式或中继参数」的接口，
// 按接口名升序返回。用于 current-configuration 的独立输出通道（T4）——
// select global / select interface 的接口同样需要落盘，故判定口径比 collectRelayInterfaces 更宽。
func collectDHCPSelectInterfaces(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k := range state.DeviceConfig {
		if iface, ok := ifaceFromDHCPSelectKey(k); ok {
			seen[iface] = true
			continue
		}
		if iface, ok := ifaceFromDHCPRelayKey(k); ok {
			seen[iface] = true
		}
	}
	return sortedKeys(seen)
}

// sortedKeys 把接口名集合转为升序切片（确定性输出，杜绝 map 随机遍历，R2）。
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ifaceFromDHCPSelectKey 从 interface:<if>:dhcp-select 键**精确后缀**解析接口名。
// 非该形态的键返回 ("", false)——包括 interface:<if>:dhcp-pool（§1.6 不得误判）。
func ifaceFromDHCPSelectKey(key string) (string, bool) {
	if !strings.HasPrefix(key, "interface:") || !strings.HasSuffix(key, dhcpSelectKeySuffix) {
		return "", false
	}
	iface := key[len("interface:") : len(key)-len(dhcpSelectKeySuffix)]
	if iface == "" || strings.Contains(iface, ":") {
		return "", false
	}
	return iface, true
}

// ifaceFromDHCPRelayKey 从 interface:<if>:dhcp-relay:<field> 键**精确中缀**解析接口名。
// 非该形态的键返回 ("", false)——包括 interface:<if>:dhcp-pool / :dhcp-select（§1.6 不得误判）。
func ifaceFromDHCPRelayKey(key string) (string, bool) {
	if !strings.HasPrefix(key, "interface:") {
		return "", false
	}
	idx := strings.Index(key, dhcpRelayKeyInfix)
	if idx <= len("interface:")-1 {
		return "", false
	}
	iface := key[len("interface:"):idx]
	if iface == "" || strings.Contains(iface, ":") {
		return "", false
	}
	// 字段名必须非空（排除 interface:<if>:dhcp-relay: 这样的畸形键）
	if strings.TrimSpace(key[idx+len(dhcpRelayKeyInfix):]) == "" {
		return "", false
	}
	return iface, true
}

// —— 解析 / 校验纯函数（T5 单测核心）——

// parseRelayServerIPs 解析逗号分隔的服务器地址串：**保序、去重、过滤空串**。
// 纯函数；返回值恒为非 nil 切片（空输入返回长度 0 的切片）。
func parseRelayServerIPs(raw string) []string {
	out := make([]string, 0, MaxRelayServerIPs)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		ip := strings.TrimSpace(part)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}
	return out
}

// joinRelayServerIPs 把有序地址列表拼为逗号串（落盘 / 回显用）。纯函数。
func joinRelayServerIPs(ips []string) string {
	return strings.Join(ips, ",")
}

// validRelayServerIP 校验 DHCP 服务器 / 中继源地址的合法性。
//
// 校验范式锚定 parser.go:4539（VRRP virtual-ip）：net.ParseIP(x) != nil && x.To4() != nil。
// 另按设计 A4 拒绝特殊 IPv4 地址：0.0.0.0 / 255.255.255.255 / 127.0.0.0/8 / 224.0.0.0/4。
//
// 返回 (ok, reason)：ok=false 时 reason 为完整的 `Error: ...` 文案。纯函数。
func validRelayServerIP(ip string) (bool, string) {
	s := strings.TrimSpace(ip)
	if s == "" {
		return false, "Error: Invalid IP address"
	}
	parsed := net.ParseIP(s)
	if parsed == nil {
		return false, fmt.Sprintf("Error: Invalid IP address %s", s)
	}
	v4 := parsed.To4()
	if v4 == nil {
		// IPv6 地址（如 2001:db8::1）一律拒绝
		return false, fmt.Sprintf("Error: Invalid IP address %s", s)
	}
	// 设计 A4：特殊 IPv4 地址拒绝（真机同样不接受）
	if v4.Equal(net.IPv4zero) || v4.Equal(net.IPv4bcast) || v4.IsLoopback() || v4.IsMulticast() {
		return false, fmt.Sprintf("Error: %s is not a valid DHCP server address.", s)
	}
	return true, ""
}

// —— 诚实占位注记 ——

// dhcpRelaySimNote 返回 DHCP 中继「诚实占位」注记（lite/full 两态，
// 口径同 lagSimNote / stpSimNote / vrrpSimNote，读 sim.EngineModeName()）。
//
//	lite → "（DHCP 中继为配置态模拟（lite 引擎），无真实 DHCP 报文转发与服务器交互，转发统计不可用）"
//	full → "（DHCP 中继为配置态模拟，无真实报文转发引擎）"
//
// 全部 display dhcp relay* 输出末尾必须附加该注记（P0-9 / AC8 红线）。
func dhcpRelaySimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（DHCP 中继为配置态模拟（lite 引擎），无真实 DHCP 报文转发与服务器交互，转发统计不可用）"
	}
	return "（DHCP 中继为配置态模拟，无真实报文转发引擎）"
}
