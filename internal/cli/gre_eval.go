// gre_eval.go 实现「CLIState 层 GRE 隧道纯函数评估器」
// （P2 第七项，华为 VRP 课程 69，T1）。
//
// 背景与约束见 docs/p2-gre-prd.md 与 docs/p2-gre-design.md。
//
// 架构基线（与 STP / VRRP / 链路聚合 / DHCP 中继完全同构，见设计 §1.9）：
//
//   - 单一事实源 = state.DeviceConfig：
//     隧道协议      interface:<if>:tunnel-protocol       值固定 "gre"
//     源端          interface:<if>:gre-source            原样存 IP 或接口名（拍板 C3 / 设计 A3）
//     目的端        interface:<if>:gre-destination       原样存 IP 或接口名（拍板 C3 / 设计 A3）
//     GRE key       interface:<if>:gre-key               规范化十进制串，未配缺键（设计 A7）
//     keepalive     interface:<if>:gre-keepalive         "true" / 缺键
//     keepalive 周期 interface:<if>:gre-keepalive-period 仅显式指定时存在
//     keepalive 重试 interface:<if>:gre-keepalive-retry  仅显式指定时存在
//     校验和        interface:<if>:gre-checksum          "true" / 缺键
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化
//     （设计 A9：LoadFromDeviceConfigData 零改动）。
//
//   - **严禁在 state.go 新增任何 GRE / Tunnel 内嵌结构体**（架构铁律 1，AC12）。
//     GRETunnelConfig / GREResult 仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不双写。
//     被删除的旧 GREConfig 名字**禁止复用**（设计 A12）。
//
//   - 🔴 **A1 键碰撞红线（本期最高危）**：`interface:Bridge-Aggregation<id>:lag:<field>` 中
//     `Ag·gre·gation` **本身含 `gre` 子串**。因此本文件**严禁**出现任何
//     `strings.Contains(k, "gre")` 形式的模糊匹配；全部键解析必须走本文件的精确 helper：
//     精确后缀 `:tunnel-protocol` / 精确中缀 `:gre-` / 精确前缀 `interface:<if>:gre-`。
//
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import internal/protocol），
//     与 dhcp_relay_eval.go 的 EvaluateDHCPRelay、stp_eval.go 的 EvaluateSTP 同一契约。
package cli

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// —— 规格常量（设计 §4.4，拍板 C7）——

const (
	// GREKeyMin / GREKeyMax 是 gre key 的合法取值范围（32 位无符号）。
	GREKeyMin uint64 = 0
	GREKeyMax uint64 = 4294967295

	// DefaultGREKeepalivePeriod / DefaultGREKeepaliveRetry 是 keepalive 的生效缺省值。
	// 「生效缺省」= 未显式配置时 display 呈现的值；**键不落盘**（差异值口径，见 §7）。
	DefaultGREKeepalivePeriod = 5
	DefaultGREKeepaliveRetry  = 3

	// keepalive 参数取值范围。
	GREKeepalivePeriodMin = 1
	GREKeepalivePeriodMax = 32767
	GREKeepaliveRetryMin  = 1
	GREKeepaliveRetryMax  = 255

	// GRETunnelProtocolValue 是 tunnel-protocol 键的唯一合法值（拍板 C7：
	// none / ipv4-ipv6 / mpls 本期不实现）。
	GRETunnelProtocolValue = "gre"

	// greStatPlaceholder 是全部运行态统计字段的恒定占位符（诚实占位红线，AC8）。
	greStatPlaceholder = "-"

	// greNotConfiguredPlaceholder 是配置态字段「未配置」的渲染占位符。
	// 特别地：GRE key 未配时显示 "-" 而**不是** "0"（设计 A7，直击 parser.go:3525 旧缺陷）。
	greNotConfiguredPlaceholder = "-"
)

// —— 键片段常量（A1 红线：精确匹配专用，全仓拼键 / 解键的唯一素材）——

const (
	// greIfaceKeyNamespace 是接口键命名空间前缀。
	greIfaceKeyNamespace = "interface:"
	// tunnelProtocolKeySuffix 是隧道协议键的**精确后缀**。
	tunnelProtocolKeySuffix = ":tunnel-protocol"
	// greKeyInfix 是 GRE 字段键的**精确中缀**。
	// 注意：必须带前导 ':' 与后置 '-'，否则 Bridge-Ag·gre·gation 会误命中（§1.7 实证）。
	greKeyInfix = ":gre-"
)

// —— GRE 字段名常量（greKey 的 field 入参，避免手写裸串）——

const (
	greFieldSource          = "source"
	greFieldDestination     = "destination"
	greFieldKey             = "key"
	greFieldKeepalive       = "keepalive"
	greFieldKeepalivePeriod = "keepalive-period"
	greFieldKeepaliveRetry  = "keepalive-retry"
	greFieldChecksum        = "checksum"
)

// greKeepaliveFields 是 `undo keepalive` 需要**枚举式**删除的字段名集合（设计 A10）。
//
// 🔴 严禁改用 `strings.HasPrefix(k, greKey(iface, "keepalive"))` 前缀匹配：
// `gre-keepalive` 本身是 `gre-keepalive-period` 的前缀，前缀写法虽在当下「碰巧正确」，
// 但一旦将来新增语义不同的 `gre-keepalive-xxx` 键即静默误删。枚举是唯一可长期成立的写法。
var greKeepaliveFields = []string{
	greFieldKeepalive,
	greFieldKeepalivePeriod,
	greFieldKeepaliveRetry,
}

// greTunnelNamePrefixes 是 Tunnel 逻辑口的**精确前缀**集合（小写比对）。
// 顺序有意义：长前缀在前，避免 "Tunnel0/0/1" 被 "tun" 分支先行截断判定。
var greTunnelNamePrefixes = []string{"tunnel", "tun"}

// greInterfaceNamePattern 判定「接口名形态」的端点（拍板 C3 / 设计 A3）。
//
// 规则：字母开头 → 允许字母与连字符 → 必须以数字段结尾 → 可跟 /槽位 与 .子接口。
// 该模式刻意**拒绝**全部 AC4 反例：
//
//	300.1.1.1 / 10.1.1 / 10.1.1.1/24 → 数字开头，不匹配
//	abc                              → 无结尾数字段，不匹配
//	2001:db8::1                      → 数字开头 + 含 ':'，不匹配
//
// 正例：GigabitEthernet0/0/0、LoopBack0、Vlanif10、Eth-Trunk1、Tunnel0/0/1。
var greInterfaceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z\-]*[0-9]+(/[0-9]+)*(\.[0-9]+)?$`)

// —— 端点形态标识（validGRETunnelEndpoint 的 kind 返回值）——

const (
	greEndpointKindIP        = "ip"
	greEndpointKindInterface = "interface"
)

// —— 协议态派生文案（拍板 C4 / 设计 A4：纯 display 期派生，绝不写键）——

const (
	// greLineProtocolUp 是配置完整时的协议态。**必须带诚实限定语**，
	// 严禁退化为裸 "UP"（AC9 ②）。
	greLineProtocolUp = "UP (config complete, peer not verified)"
	// greLineProtocolDown 是 source / destination 缺配时的协议态。
	greLineProtocolDown = "DOWN (source/destination not configured)"

	// greBriefUp / greBriefDown 是 display interface brief 的 Protocol 列短态。
	// `up*` 的星号对应表尾脚注，严禁裸 `up` 让学员误判隧道已通（AC8）。
	greBriefUp   = "up*"
	greBriefDown = "down"
)

// —— 评估层类型（设计 §4.2；A12：禁止复用被删的 GREConfig 名）——

// GRETunnelConfig 是从 DeviceConfig 即时派生的 GRE 隧道**只读配置视图**。
// 不缓存、不写回；每次 display / 持久化时重新读取，保证单一事实源。
type GRETunnelConfig struct {
	// Interface 是 Tunnel 口名，如 "Tunnel0/0/1"。
	Interface string
	// TunnelProtocol 来自 interface:<if>:tunnel-protocol，本期仅 "gre"。
	TunnelProtocol string
	// Source 原样存储：IP 或接口名（拍板 C3 / A3）。**绝不由接口名推导 IP**。
	Source string
	// Destination 原样存储：IP 或接口名（拍板 C3 / A3）。
	Destination string
	// Key 是规范化十进制串；未配为 ""，渲染层显示 "-"。
	// 🔴 类型必须是 string：用 int + 零值会让「未配置」与「key 0」不可区分（设计 A7）。
	Key string
	// Keepalive 是保活配置态（拍板 C2：仅配置态，不引入 timer / goroutine）。
	Keepalive GREKeepalive
	// Checksum 对应 interface:<if>:gre-checksum（P2-1）。
	Checksum bool
}

// GREKeepalive 是 keepalive 的配置态视图。Period / Retry 为**生效值**
// （显式配置值，或未配时的生效缺省 5 / 3）。
type GREKeepalive struct {
	Enabled bool
	Period  int
	Retry   int
}

// GREStats 是 GRE 隧道运行态统计的**诚实占位**结构（拍板 C2 + C9，AC8 红线）。
//
// 🔴 5 个字段类型**必须**是 string 且**恒为** greStatPlaceholder（"-"）。
// 结构体内**不得出现任何 int / 计数器 / 随机数路径** —— 从类型层面杜绝编造运行数据。
// 反面教材：internal/protocol/protocol.go:1388 的 GRETunnel{Status: "up"}（包外死代码，本期不碰）。
type GREStats struct {
	// KeepaliveSent 恒 "-"：无真实保活报文发送引擎。
	KeepaliveSent string
	// KeepaliveReceived 恒 "-"：无真实保活报文接收引擎。
	KeepaliveReceived string
	// PacketsEncapsulated 恒 "-"：无真实 GRE 封装路径。
	PacketsEncapsulated string
	// PacketsDecapsulated 恒 "-"：无真实 GRE 解封装路径。
	PacketsDecapsulated string
	// PeerReachable 恒 "-"：无 ICMP / 无跨设备状态。
	// 🔴 严禁取值 "Reachable" / "Unreachable" / "Up" / "Active"（AC8）。
	PeerReachable string
}

// GREResult 是 EvaluateGRE 的完整评估结果。
type GREResult struct {
	// Config 是配置态视图（真实，来自 DeviceConfig）。
	Config GRETunnelConfig
	// LineProtocol 是协议态派生值（拍板 C4 / A4，带诚实限定语，display 期派生不落键）。
	LineProtocol string
	// Brief 是 display interface brief 的 Protocol 列短态。
	Brief string
	// Stats 是运行态占位（恒 "-"）。
	Stats GREStats
}

// —— 键 helper（A1 红线：全仓拼键 / 解键的唯一出口）——

// tunnelProtocolKey 拼接隧道协议键：interface:<if>:tunnel-protocol。
func tunnelProtocolKey(iface string) string {
	return greIfaceKeyNamespace + iface + tunnelProtocolKeySuffix
}

// greKey 拼接 GRE 字段键：interface:<if>:gre-<field>。
// field 请使用本文件的 greField* 常量，避免手写裸串导致键漂移。
func greKey(iface, field string) string {
	return greIfaceKeyNamespace + iface + greKeyInfix + field
}

// greKeyPrefix 返回该接口 GRE 键的**精确前缀** interface:<if>:gre-，
// 专供 undo tunnel-protocol 的级联清理使用。
//
// 🔴 级联清理只能用本前缀，绝不可退化为 Contains("gre")：后者会连
// interface:Bridge-Aggregation1:lag:mode 一起删掉（§1.7 实证，AC12 ②）。
func greKeyPrefix(iface string) string {
	return greIfaceKeyNamespace + iface + greKeyInfix
}

// ifaceFromTunnelProtocolKey 从 interface:<if>:tunnel-protocol 键**精确后缀**解析接口名。
// 非该形态的键返回 ("", false)——包括任何仅含 gre 子串的聚合口键。
func ifaceFromTunnelProtocolKey(key string) (string, bool) {
	if !strings.HasPrefix(key, greIfaceKeyNamespace) || !strings.HasSuffix(key, tunnelProtocolKeySuffix) {
		return "", false
	}
	iface := key[len(greIfaceKeyNamespace) : len(key)-len(tunnelProtocolKeySuffix)]
	if iface == "" || strings.Contains(iface, ":") {
		return "", false
	}
	return iface, true
}

// ifaceFromGREKey 从 interface:<if>:gre-<field> 键**精确中缀**解析接口名。
//
// 判据三重：① 必须以 interface: 开头；② 接口名段之后紧跟 ":gre-"；
// ③ 接口名段与字段名段均不得再含 ':'（防止 interface:X:dhcp-relay:gre-y 之类误判）。
func ifaceFromGREKey(key string) (string, bool) {
	if !strings.HasPrefix(key, greIfaceKeyNamespace) {
		return "", false
	}
	rest := key[len(greIfaceKeyNamespace):]
	idx := strings.Index(rest, greKeyInfix)
	if idx <= 0 {
		return "", false
	}
	iface := rest[:idx]
	if iface == "" || strings.Contains(iface, ":") {
		return "", false
	}
	field := rest[idx+len(greKeyInfix):]
	if field == "" || strings.Contains(field, ":") {
		return "", false
	}
	return iface, true
}

// —— 集合扫描（确定性输出：升序去重，杜绝 map 随机遍历，直击 parser.go:3521 旧缺陷）——

// collectGRETunnels 返回**已启用 GRE 隧道**的接口名（升序去重）。
//
// 严格口径：按精确后缀 :tunnel-protocol 扫描**且值为 gre**。
// 仅有 gre-source 之类残留键、但未配 tunnel-protocol 的接口**不计入**
// （对应 display gre tunnel 的「隧道」语义）。
//
// 纯函数：只读 DeviceConfig。
func collectGRETunnels(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k, v := range state.DeviceConfig {
		iface, ok := ifaceFromTunnelProtocolKey(k)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), GRETunnelProtocolValue) {
			continue
		}
		seen[iface] = true
	}
	return sortedKeys(seen)
}

// collectGREConfiguredInterfaces 返回**存在任意 GRE 相关键**的接口名（并集口径，升序去重）：
// :tunnel-protocol 键存在 **或** 存在任意 :gre- 中缀键。
//
// 用途：① 持久化独立输出通道（T4，为 reload 后未重建的 Tunnel 口补齐 interface 块）；
// ② QA 幽灵残留检测。**不用于 display gre tunnel 汇总**（那里用严格口径）。
func collectGREConfiguredInterfaces(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k := range state.DeviceConfig {
		if iface, ok := ifaceFromTunnelProtocolKey(k); ok {
			seen[iface] = true
			continue
		}
		if iface, ok := ifaceFromGREKey(k); ok {
			seen[iface] = true
		}
	}
	return sortedKeys(seen)
}

// —— 配置读取与评估 ——

// greExplicitKeepalivePeriod 报告该接口是否**显式配置**过 keepalive period。
// 供持久化「缺省值不冗余输出」判定使用（区别于 readGREConfig 给出的生效值）。
func greExplicitKeepalivePeriod(state *CLIState, iface string) (int, bool) {
	return greExplicitIntKey(state, greKey(iface, greFieldKeepalivePeriod), GREKeepalivePeriodMin, GREKeepalivePeriodMax)
}

// greExplicitKeepaliveRetry 报告该接口是否**显式配置**过 keepalive retry-times。
func greExplicitKeepaliveRetry(state *CLIState, iface string) (int, bool) {
	return greExplicitIntKey(state, greKey(iface, greFieldKeepaliveRetry), GREKeepaliveRetryMin, GREKeepaliveRetryMax)
}

// greExplicitIntKey 读取整型键并做范围校验；键缺失或越界均返回 ok=false。
func greExplicitIntKey(state *CLIState, key string, min, max int) (int, bool) {
	if state == nil || state.DeviceConfig == nil {
		return 0, false
	}
	raw, ok := state.DeviceConfig[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < min || n > max {
		return 0, false
	}
	return n, true
}

// readGREConfig 从 DeviceConfig 读出该接口的 GRE 配置视图，并合并生效缺省值。
//
// 生效缺省（拍板 C7）：keepalive period / retry 未显式配置时填 5 / 3。
// 缺省值**只在读出侧合并**，不回写键（差异值口径，对照 DHCP 设计 A5）。
//
// 纯函数：只读 DeviceConfig，调用前后 DeviceConfig 逐键不变（AC12）。
func readGREConfig(state *CLIState, iface string) GRETunnelConfig {
	cfg := GRETunnelConfig{
		Interface: strings.TrimSpace(iface),
		Keepalive: GREKeepalive{
			Period: DefaultGREKeepalivePeriod,
			Retry:  DefaultGREKeepaliveRetry,
		},
	}
	if state == nil || state.DeviceConfig == nil || cfg.Interface == "" {
		return cfg
	}
	dc := state.DeviceConfig
	cfg.TunnelProtocol = strings.TrimSpace(dc[tunnelProtocolKey(cfg.Interface)])
	cfg.Source = strings.TrimSpace(dc[greKey(cfg.Interface, greFieldSource)])
	cfg.Destination = strings.TrimSpace(dc[greKey(cfg.Interface, greFieldDestination)])
	cfg.Key = strings.TrimSpace(dc[greKey(cfg.Interface, greFieldKey)])
	cfg.Keepalive.Enabled = strings.EqualFold(strings.TrimSpace(dc[greKey(cfg.Interface, greFieldKeepalive)]), "true")
	if n, ok := greExplicitKeepalivePeriod(state, cfg.Interface); ok {
		cfg.Keepalive.Period = n
	}
	if n, ok := greExplicitKeepaliveRetry(state, cfg.Interface); ok {
		cfg.Keepalive.Retry = n
	}
	cfg.Checksum = strings.EqualFold(strings.TrimSpace(dc[greKey(cfg.Interface, greFieldChecksum)]), "true")
	return cfg
}

// newGREStats 构造恒 "-" 的运行态占位（AC8 红线的唯一构造入口）。
func newGREStats() GREStats {
	return GREStats{
		KeepaliveSent:       greStatPlaceholder,
		KeepaliveReceived:   greStatPlaceholder,
		PacketsEncapsulated: greStatPlaceholder,
		PacketsDecapsulated: greStatPlaceholder,
		PeerReachable:       greStatPlaceholder,
	}
}

// EvaluateGRE 评估指定接口的 GRE 隧道状态，返回配置态 + 派生协议态 + 诚实占位统计。
//
// 纯函数契约（AC12）：不写 state、不碰 sim 引擎实例、不 import internal/protocol；
// 连续两次调用结果一致；调用前后 DeviceConfig deep-equal。
func EvaluateGRE(state *CLIState, iface string) GREResult {
	cfg := readGREConfig(state, iface)
	return GREResult{
		Config:       cfg,
		LineProtocol: greLineProtocolState(cfg),
		Brief:        greLineProtocolBrief(cfg),
		Stats:        newGREStats(),
	}
}

// —— 判定与校验纯函数 ——

// greIsEnabled 报告该配置视图是否已启用 GRE（tunnel-protocol == gre）。
func greIsEnabled(cfg GRETunnelConfig) bool {
	return strings.EqualFold(cfg.TunnelProtocol, GRETunnelProtocolValue)
}

// isTunnelInterface 判定接口名是否为 Tunnel 逻辑口。
//
// 判据：**精确前缀** Tunnel / Tun（大小写不敏感）**且其后紧跟数字**。
// 🔴 严禁使用 strings.Contains —— 范式对照 lag_eval.go:181 的 isTrunkFamilyInterface
// （"ET" 前缀误判 "Ethernet" 的同类风险）。
//
//	Tunnel0/0/1 ✅   Tun0/0/1 ✅   TunnelX ❌   Tunnel ❌   Ethernet0/0/1 ❌
func isTunnelInterface(name string) bool {
	s := strings.TrimSpace(name)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range greTunnelNamePrefixes {
		if !strings.HasPrefix(lower, p) {
			continue
		}
		rest := s[len(p):]
		if rest == "" {
			continue
		}
		if rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// validGRETunnelIP 校验隧道端点 IPv4 地址的合法性（设计 A6）。
//
// 校验范式锚定 parser.go:4539（VRRP virtual-ip）与 dhcp_relay_eval.go:357：
// net.ParseIP(x) != nil && x.To4() != nil；另拒绝特殊 IPv4 地址
// 0.0.0.0 / 255.255.255.255 / 127.0.0.0/8 / 224.0.0.0/4。
//
// 返回 (ok, reason)：ok=false 时 reason 为完整的 `Error: ...` 文案。纯函数。
func validGRETunnelIP(ip string) (bool, string) {
	s := strings.TrimSpace(ip)
	if s == "" {
		return false, fmt.Sprintf(errGREInvalidIP, s)
	}
	parsed := net.ParseIP(s)
	if parsed == nil {
		return false, fmt.Sprintf(errGREInvalidIP, s)
	}
	v4 := parsed.To4()
	if v4 == nil {
		// IPv6（如 2001:db8::1）本期一律拒绝（拍板 C8：GRE over IPv6 out-of-scope）。
		return false, fmt.Sprintf(errGREInvalidIP, s)
	}
	if v4.Equal(net.IPv4zero) || v4.Equal(net.IPv4bcast) || v4.IsLoopback() || v4.IsMulticast() {
		return false, fmt.Sprintf(errGREInvalidTunnelAddr, s)
	}
	return true, ""
}

// validGRETunnelEndpoint 判定 source / destination 端点形态（拍板 C3 / 设计 A3 双形态）。
//
// 判定序：
//  1. 能 net.ParseIP + To4() → IP 形态，再过 validGRETunnelIP 的特殊地址校验；
//  2. 否则匹配接口名模式 → 接口形态，**原样存储、原样回显、绝不推导 IP**；
//  3. 均不匹配 → 统一返回 errGREInvalidIP 文案（保住 AC4 的 `Invalid IP address` 子串断言）。
//
// 返回 (kind, ok, reason)：kind ∈ ip | interface；ok=false 时 reason 为完整 `Error: ...`。
func validGRETunnelEndpoint(s string) (string, bool, string) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", false, fmt.Sprintf(errGREInvalidIP, v)
	}
	if parsed := net.ParseIP(v); parsed != nil && parsed.To4() != nil {
		if ok, reason := validGRETunnelIP(v); !ok {
			return greEndpointKindIP, false, reason
		}
		return greEndpointKindIP, true, ""
	}
	if greInterfaceNamePattern.MatchString(v) {
		return greEndpointKindInterface, true, ""
	}
	return "", false, fmt.Sprintf(errGREInvalidIP, v)
}

// greSameEndpoint 判定两个端点是否为「同一地址」（拍板 C5 / 设计 A5）。
//
// **仅当两端均为 IP 形态且数值相等**时返回 true。
// 接口名形态永不触发同址拒绝（无法也不应推导其 IP）。
func greSameEndpoint(a, b string) bool {
	x := strings.TrimSpace(a)
	y := strings.TrimSpace(b)
	if x == "" || y == "" {
		return false
	}
	ipA := net.ParseIP(x)
	ipB := net.ParseIP(y)
	if ipA == nil || ipB == nil || ipA.To4() == nil || ipB.To4() == nil {
		return false
	}
	return ipA.Equal(ipB)
}

// normalizeGREKeyValue 校验并规范化 gre key（设计 A7）。
//
// 范围 0–4294967295，用 strconv.ParseUint(s, 10, 32)。
// 🔴 不得改用既有 parseNum：其 int 语义会放过 "-1"（负数在 32 位无符号语义下非法）。
// 另显式拒绝含符号 / 空白的输入（ParseUint 会接受 "+1"）。
func normalizeGREKeyValue(s string) (string, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return "", false
		}
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return "", false
	}
	if n < GREKeyMin || n > GREKeyMax {
		return "", false
	}
	return strconv.FormatUint(n, 10), true
}

// —— 协议态派生（拍板 C4 / 设计 A4：纯 display 期派生，**绝不写任何 DeviceConfig 键**）——

// greLineProtocolState 由 tunnel-protocol + gre-source + gre-destination 三者本地派生
// Tunnel 口的 Line protocol current state。
//
// 完整（三者齐备）→ "UP (config complete, peer not verified)"
// 缺配             → "DOWN (source/destination not configured)"
//
// 🔴 诚实性：UP 态**必须**带 "peer not verified" 限定语 —— 本端配置完整不代表隧道已通，
// 仿真环境无对端协商能力，裸 UP 即编造（AC9 ②）。
//
// 🔴 不落键：本函数**不写** interface:<if>:status（那是 shutdown / undo shutdown 的管理态
// 事实源，混写即双写事实源 + 污染既有语义，设计 A4）。
func greLineProtocolState(cfg GRETunnelConfig) string {
	if greIsEnabled(cfg) && cfg.Source != "" && cfg.Destination != "" {
		return greLineProtocolUp
	}
	return greLineProtocolDown
}

// greLineProtocolBrief 是 greLineProtocolState 的短态形式，
// 供 display interface brief 的 Protocol 列使用（改动点 #11）。
func greLineProtocolBrief(cfg GRETunnelConfig) string {
	if greIsEnabled(cfg) && cfg.Source != "" && cfg.Destination != "" {
		return greBriefUp
	}
	return greBriefDown
}

// greSummaryState 返回 display gre tunnel 汇总表 **State 列**的派生态（PRD §4.3）。
//
// 与 greLineProtocolBrief（display interface brief 的 Protocol 列，小写 up*/down）不同，
// 汇总表 State 列按 PRD §4.3 样例使用**大写首字母**的 Up*/Down，以示它是「配置完整性」态、
// 并非接口 brief 列的小写物理态。两者各自独立、不得混淆（A3 / AC8 红线：均不得裸 Up）。
//
// 🔴 诚实性：Up* 必须带星号，对应表尾脚注「仅由本端配置完整性派生，未与对端协商」，
// 严禁裸 Up 让学员误判隧道已通。
func greSummaryState(cfg GRETunnelConfig) string {
	if greIsEnabled(cfg) && cfg.Source != "" && cfg.Destination != "" {
		return "Up*"
	}
	return "Down"
}

// —— 诚实占位注记 ——

// greSimNote 返回 GRE 隧道「诚实占位」注记（lite / full 两态，
// 口径同 dhcpRelaySimNote / lagSimNote / stpSimNote，读 sim.EngineModeName()）。
//
// 全部 display gre tunnel 与 display interface Tunnel<x> 的 GRE 段输出
// **末尾必须附加该注记**（P0-9 / AC8 红线）。
func greSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（GRE 隧道为配置态模拟（lite 引擎），无真实报文封装/解封装与对端协商，隧道状态与保活统计不可用）"
	}
	return "（GRE 隧道为配置态模拟，无真实报文封装/解封装与对端协商）"
}
