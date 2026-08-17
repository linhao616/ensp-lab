// ipv6_eval.go 实现「CLIState 层 IPv6 纯函数评估器」
// （P2 第九项，华为 VRP 课程 43/44，T01）。
//
// 背景与约束见 docs/p2-ipv6-prd.md 与 docs/p2-ipv6-design.md。
//
// 架构基线（与 GRE / AAA / STP / DHCP 中继完全同构，见设计 §1.6）：
//
//   - 单一事实源 = state.DeviceConfig（三个命名空间，见设计 §4.1）：
//     全局使能    ipv6:enabled
//     静态路由    ipv6:route-static:<prefix>:<nexthop>   （多键形态，C2，ECMP 前瞻）
//     RIPng       ipv6:ripng:<pid>:enabled
//     OSPFv3      ipv6:ospfv3:<pid>:enabled
//     接口使能    interface:<if>:ipv6-enable
//     接口地址    interface:<if>:ipv6-address            （"<规范地址>/<prefix>"，A7）
//     接口 RIPng  interface:<if>:ripng-<pid>-enable
//     接口 OSPFv3 interface:<if>:ospfv3-<pid>-area
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化
//     （设计改动点 #11：持久化零新增代码）。
//
//   - **严禁在 state.go 新增任何 IPv6 内嵌结构体**（架构铁律，AC12 ⑤）。
//     IPv6AddressView / IPv6RouteStatic / IPv6RouteView 仅为「从 DeviceConfig
//     即时派生的只读视图」，不缓存、不双写（设计 §4.3）。
//
//   - 🔴 **A1 键碰撞红线（本期最高危）**：既有 IPv4 键 `interface:<if>:ip`
//     （parser.go:516、:880）与新增 `interface:<if>:ipv6-address` **共享 `:ip` 子串**。
//     因此本文件**严禁**出现任何基于子串的模糊键匹配（AC12 ④ 静态断言）；
//     全部键解析走本文件的精确 helper：
//     精确前缀 `ipv6:` / 精确中缀 `:ipv6-` / 精确前缀 `ipv6:route-static:` + A3 双段解析
//     （设计 §4.2，AC12 专项）。
//
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import
//     internal/protocol、零新增第三方依赖，用标准库 net/netip），与 gre_eval.go /
//     aaa_eval.go 同一契约（AC13）。
package cli

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// —— 规格常量（设计 §4.5，逐字照抄）——

const (
	// IPv6PrefixMaxLen 是 IPv6 前缀长度的最大值。
	IPv6PrefixMaxLen = 128
	// IPv6StatPlaceholder 是全部运行态统计字段的恒定占位符（诚实占位红线，A4/AC9）。
	IPv6StatPlaceholder = "-"
	// IPv6NotConfiguredPlaceholder 是配置态字段「未配置」的渲染占位符。
	IPv6NotConfiguredPlaceholder = "-"
	// IPv6StaticPreference 是静态路由 Preference（对齐 IPv4）。
	IPv6StaticPreference = 60
	// IPv6DirectPreference 是直连路由 Preference。
	IPv6DirectPreference = 0
)

// —— 错误文案常量（设计 §4.5，QA 逐字断言，逐字照抄）——

const (
	// ErrIPv6Unrecognized 是未识别子命令的统一文案。
	ErrIPv6Unrecognized = "Error: unrecognized command"
	// ErrIPv6MustBeInterfaceView 是非接口视图的视图守卫文案。
	ErrIPv6MustBeInterfaceView = "Error: must be in interface view"
	// ErrIPv6SystemViewEnableGuide 是系统视图 `ipv6 enable` 的报错引导（A11）。
	ErrIPv6SystemViewEnableGuide = "Error: Please run 'ipv6' in system view to enable IPv6 globally, or 'ipv6 enable' in interface view."
	// ErrIPv6EnableFirst 是「ipv6 address 前须 ipv6 enable」的 C1 硬前置文案（%s = 接口名）。
	ErrIPv6EnableFirst = "Error: Please run 'ipv6 enable' on %s first."
	// ErrIPv6InvalidAddress 是 IPv6 地址非法的统一文案（%s = 地址串）。
	ErrIPv6InvalidAddress = "Error: Invalid IPv6 address %s"
	// ErrIPv6InvalidPrefix 是 IPv6 前缀形态非法的统一文案（%s = 前缀串）。
	ErrIPv6InvalidPrefix = "Error: Invalid IPv6 prefix %s"
	// ErrIPv6InvalidPrefixLen 是前缀长度越界的文案（%s = 长度串）。
	ErrIPv6InvalidPrefixLen = "Error: Invalid IPv6 prefix length %s (0-128)"
	// ErrIPv6InvalidInterface 是接口不存在的文案（%s = 接口名）。
	ErrIPv6InvalidInterface = "Error: invalid interface '%s'"
	// ErrIPv6RouteStaticUsage 是 ipv6 route-static 缺参的用法提示。
	ErrIPv6RouteStaticUsage = "Error: usage: ipv6 route-static <prefix>/<len> <nexthop>"
	// ErrRIPngUsage 是系统视图 ripng 缺参的用法提示。
	ErrRIPngUsage = "Error: usage: ripng [<process-id>]"
	// ErrRIPngIfaceUsage 是接口视图 ripng 缺参的用法提示。
	ErrRIPngIfaceUsage = "Error: usage: ripng <process-id> enable"
	// ErrOSPFv3Usage 是系统视图 ospfv3 缺参的用法提示。
	ErrOSPFv3Usage = "Error: usage: ospfv3 [<process-id>]"
	// ErrOSPFv3IfaceUsage 是接口视图 ospfv3 缺参的用法提示。
	ErrOSPFv3IfaceUsage = "Error: usage: ospfv3 <process-id> area <area-id>"
	// InfoNoIPv6Address 是 display ipv6 interface brief 的空态提示。
	InfoNoIPv6Address = "Info: No IPv6 address configured."
	// InfoNoIPv6Route 是 display ipv6 routing-table 的空态提示。
	InfoNoIPv6Route = "Info: No IPv6 route."
)

// —— 键字段常量（ipv6IfaceKey 的 field 入参，避免手写裸串，设计 §4.5）——

const (
	ipv6FieldEnable  = "enable"
	ipv6FieldAddress = "address"
)

// —— 键片段常量（A1 红线：精确匹配专用，全仓拼键 / 解键的唯一素材）——

const (
	// ipv6KeyNamespace 是全局 IPv6 键命名空间前缀（精确前缀）。
	ipv6KeyNamespace = "ipv6:"
	// ipv6IfaceKeyNamespace 是接口键命名空间前缀。
	ipv6IfaceKeyNamespace = "interface:"
	// ipv6KeyInfix 是接口 IPv6 字段键的**精确中缀**。
	// 注意：必须带前导 ':' 与后置 '-'，否则 IPv4 键 `:ip` 不会误命中（§1.5 实证）。
	ipv6KeyInfix = ":ipv6-"
	// ipv6RouteStaticNamespace 是静态路由键的**精确前缀**。
	ipv6RouteStaticNamespace = "ipv6:route-static:"
	// ipv6RIPngNamespace 是 RIPng 全局进程键的**精确前缀**。
	ipv6RIPngNamespace = "ipv6:ripng:"
	// ipv6OSPFv3Namespace 是 OSPFv3 全局进程键的**精确前缀**。
	ipv6OSPFv3Namespace = "ipv6:ospfv3:"
	// ipv6RIPngIfaceInfix 是接口 RIPng 键的**精确中缀**（interface:<if>:ripng-<pid>-enable）。
	ipv6RIPngIfaceInfix = ":ripng-"
	// ipv6OSPFv3IfaceInfix 是接口 OSPFv3 键的**精确中缀**（interface:<if>:ospfv3-<pid>-area）。
	ipv6OSPFv3IfaceInfix = ":ospfv3-"
	// ipv6EnableSuffix 是全局 / 接口使能键的**精确后缀**。
	ipv6EnableSuffix = ":enabled"
)

// —— 地址类型枚举（设计 §4.3）——
//
// 🔴 命名偏差（实现说明）：设计 §4.3 同时给出类型 `IPv6AddressType` 与同名函数
// `IPv6AddressType(addr string) IPv6AddressType`——Go 中类型与函数共享同一命名空间，
// 二者同名无法编译。为保证 AC3 断言所引用的**函数名** `IPv6AddressType(...)` 逐字成立，
// 类型名收敛为 `IPv6AddrType`（枚举常量值与语义不变）。

// IPv6AddrType 描述 IPv6 地址的类别（课程 43 教学重点）。
type IPv6AddrType string

const (
	// IPv6AddrLinkLocal 是链路本地地址（fe80::/10）。
	IPv6AddrLinkLocal IPv6AddrType = "linkLocal"
	// IPv6AddrMulticast 是组播地址（ff00::/8）。
	IPv6AddrMulticast IPv6AddrType = "multicast"
	// IPv6AddrLoopback 是回环地址（::1）。
	IPv6AddrLoopback IPv6AddrType = "loopback"
	// IPv6AddrUnspecified 是未指定地址（::）。
	IPv6AddrUnspecified IPv6AddrType = "unspecified"
	// IPv6AddrGlobalUnicast 是其余非特殊地址（全球单播）。
	IPv6AddrGlobalUnicast IPv6AddrType = "globalUnicast"
	// IPv6AddrUniqueLocal 是唯一本地地址（fc00::/7，P1-3 类型判定入 P0 实现）。
	IPv6AddrUniqueLocal IPv6AddrType = "uniqueLocal"
)

// —— 只读 View 类型（设计 §4.3，即时派生、不缓存、不双写，严禁进 state.go）——

// IPv6AddressView 是单接口 IPv6 配置的只读视图。
type IPv6AddressView struct {
	// Interface 是接口名（规范大小写）。
	Interface string
	// Enable 是否 ipv6 enable（读 :ipv6-enable）。
	Enable bool
	// Address 是规范地址/前缀 或 ""（读 :ipv6-address）。
	Address string
	// LinkLocal 是 "fe80::<EUI64>"（有真实 MAC 键真实计算，C3）或 "-"（无 MAC）。
	LinkLocal string
	// HasMAC 是接口是否存在真实 MAC 键（interface:<if>:mac）。
	HasMAC bool
}

// IPv6RouteStatic 是一条 IPv6 静态路由（多键形态，C2）。
type IPv6RouteStatic struct {
	// Prefix 是规范化 "<addr>/<len>"。
	Prefix string
	// NextHop 是规范化 IPv6 地址。
	NextHop string
}

// IPv6RouteView 是 display ipv6 routing-table 的只读路由视图。
type IPv6RouteView struct {
	// Destination 是网络地址（NetworkFromPrefix 结果）。
	Destination string
	// PrefixLength 是前缀长度。
	PrefixLength int
	// NextHop 是 Static=下一跳 / Direct=接口地址。
	NextHop string
	// Protocol 是 "Static" | "Direct"。
	Protocol string
	// Preference 是 Static=60 / Direct=0。
	Preference int
	// Cost 恒 0。
	Cost int
	// Interface 是 Static="NULL0" / Direct=接口名。
	Interface string
}

// —— 键构造 helper（A1 红线：全仓拼键 / 解键的唯一出口，设计 §4.2）——

// ipv6KeyPrefix 返回全局 IPv6 键命名空间前缀 "ipv6:"。
func ipv6KeyPrefix() string {
	return ipv6KeyNamespace
}

// ipv6GlobalKey 返回全局使能键 "ipv6:enabled"。
func ipv6GlobalKey() string {
	return ipv6KeyNamespace + "enabled"
}

// ipv6IfaceKey 拼接接口 IPv6 字段键：interface:<if>:ipv6-<field>。
// field 请使用本文件的 ipv6Field* 常量，避免手写裸串导致键漂移。
func ipv6IfaceKey(iface, field string) string {
	return ipv6IfaceKeyNamespace + iface + ipv6KeyInfix + field
}

// ipv6RouteStaticPrefix 返回静态路由键命名空间前缀 "ipv6:route-static:"。
func ipv6RouteStaticPrefix() string {
	return ipv6RouteStaticNamespace
}

// ipv6RouteStaticKey 拼接静态路由键：ipv6:route-static:<prefix>:<nexthop>。
// prefix 与 nexthop 须为已校验并规范化的 IPv6 前缀 / 地址（A7）。
func ipv6RouteStaticKey(prefix, nexthop string) string {
	return ipv6RouteStaticNamespace + prefix + ":" + nexthop
}

// ipv6RIPngKey 拼接 RIPng 全局进程键：ipv6:ripng:<pid>:enabled。
func ipv6RIPngKey(pid string) string {
	return ipv6RIPngNamespace + pid + ipv6EnableSuffix
}

// ipv6RIPngIfaceKey 拼接接口 RIPng 使能键：interface:<if>:ripng-<pid>-enable。
func ipv6RIPngIfaceKey(iface, pid string) string {
	return ipv6IfaceKeyNamespace + iface + ipv6RIPngIfaceInfix + pid + "-enable"
}

// ipv6OSPFv3Key 拼接 OSPFv3 全局进程键：ipv6:ospfv3:<pid>:enabled。
func ipv6OSPFv3Key(pid string) string {
	return ipv6OSPFv3Namespace + pid + ipv6EnableSuffix
}

// ipv6OSPFv3IfaceKey 拼接接口 OSPFv3 使能键：interface:<if>:ospfv3-<pid>-area。
func ipv6OSPFv3IfaceKey(iface, pid string) string {
	return ipv6IfaceKeyNamespace + iface + ipv6OSPFv3IfaceInfix + pid + "-area"
}

// —— 键解析 helper（A1/A3，精确匹配，设计 §4.2）——

// ifaceFromIPv6Key 从 interface:<if>:ipv6-<field> 键**精确中缀**解析接口名与字段名。
//
// 判据三重：① 必须以 interface: 开头；② 接口名段之后紧跟 ":ipv6-"；
// ③ 接口名段与字段名段均不得再含 ':'。
// 返回值 (iface, field, ok)；非该形态的键返回 ("", "", false)。
//
// 🔴 精确中缀 ":ipv6-" 天然隔离 IPv4 键 interface:<if>:ip（其不含字面 ":ipv6-"，
// 共享的仅是 ":ip" 子串）——A1/AC12 ①。
func ifaceFromIPv6Key(key string) (iface, field string, ok bool) {
	if !strings.HasPrefix(key, ipv6IfaceKeyNamespace) {
		return "", "", false
	}
	rest := key[len(ipv6IfaceKeyNamespace):]
	idx := strings.Index(rest, ipv6KeyInfix)
	if idx <= 0 {
		return "", "", false
	}
	iface = rest[:idx]
	if iface == "" || strings.Contains(iface, ":") {
		return "", "", false
	}
	field = rest[idx+len(ipv6KeyInfix):]
	if field == "" || strings.Contains(field, ":") {
		return "", "", false
	}
	return iface, field, true
}

// parseIPv6RouteStaticKey 从 ipv6:route-static:<prefix>:<nexthop> 键做 **A3 双段解析**。
//
// 背景：prefix 与 nexthop 都是 IPv6 地址、均含冒号，strings.Split(key, ":") 不可用。
// 算法（设计 §4.2 A3，工程师照抄）：
//
//	rest := strings.TrimPrefix(key, ipv6RouteStaticPrefix())
//	slash := strings.Index(rest, "/")        // prefix 地址段不含 '/'，nexthop 也不含 '/'
//	addrPart := rest[:slash]
//	tail := rest[slash+1:]
//	colon := strings.Index(tail, ":")        // 前缀长度段为纯数字（无 ':'）
//	lenPart := tail[:colon]                  // 须全为十进制数字，1–3 位
//	nexthop := tail[colon+1:]                // IPv6 地址，可含 ':'
//	prefix := addrPart + "/" + lenPart
//
// 非路由键（无 '/'、无 ':'、长度段非纯数字、nexthop 空）一律返回 ok=false，
// 保证 ipv6:enabled / interface:...:ipv6-address 等键不会被误判为路由键（AC12 ②）。
func parseIPv6RouteStaticKey(key string) (prefix, nexthop string, ok bool) {
	if !strings.HasPrefix(key, ipv6RouteStaticNamespace) {
		return "", "", false
	}
	rest := key[len(ipv6RouteStaticNamespace):]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return "", "", false
	}
	addrPart := rest[:slash]
	tail := rest[slash+1:]
	colon := strings.Index(tail, ":")
	if colon <= 0 {
		return "", "", false
	}
	lenPart := tail[:colon]
	if len(lenPart) < 1 || len(lenPart) > 3 {
		return "", "", false
	}
	for i := 0; i < len(lenPart); i++ {
		if lenPart[i] < '0' || lenPart[i] > '9' {
			return "", "", false
		}
	}
	nexthop = tail[colon+1:]
	if nexthop == "" {
		return "", "", false
	}
	prefix = addrPart + "/" + lenPart
	return prefix, nexthop, true
}

// ifaceFromIPv6RIPngKey 从 interface:<if>:ripng-<pid>-enable 键解析接口名与 pid。
// 精确中缀 ":ripng-" 解析；字段段（"<pid>-enable"）不得含 ':'。
func ifaceFromIPv6RIPngKey(key string) (iface, pid string, ok bool) {
	if !strings.HasPrefix(key, ipv6IfaceKeyNamespace) {
		return "", "", false
	}
	rest := key[len(ipv6IfaceKeyNamespace):]
	idx := strings.Index(rest, ipv6RIPngIfaceInfix)
	if idx <= 0 {
		return "", "", false
	}
	iface = rest[:idx]
	if iface == "" || strings.Contains(iface, ":") {
		return "", "", false
	}
	field := rest[idx+len(ipv6RIPngIfaceInfix):]
	// 字段形态 "<pid>-enable"
	suffix := "-enable"
	if !strings.HasSuffix(field, suffix) {
		return "", "", false
	}
	pid = strings.TrimSuffix(field, suffix)
	if pid == "" || strings.Contains(pid, ":") {
		return "", "", false
	}
	return iface, pid, true
}

// ifaceFromIPv6OSPFv3Key 从 interface:<if>:ospfv3-<pid>-area 键解析接口名与 pid。
// 精确中缀 ":ospfv3-" 解析；字段段（"<pid>-area"）不得含 ':'。
func ifaceFromIPv6OSPFv3Key(key string) (iface, pid string, ok bool) {
	if !strings.HasPrefix(key, ipv6IfaceKeyNamespace) {
		return "", "", false
	}
	rest := key[len(ipv6IfaceKeyNamespace):]
	idx := strings.Index(rest, ipv6OSPFv3IfaceInfix)
	if idx <= 0 {
		return "", "", false
	}
	iface = rest[:idx]
	if iface == "" || strings.Contains(iface, ":") {
		return "", "", false
	}
	field := rest[idx+len(ipv6OSPFv3IfaceInfix):]
	// 字段形态 "<pid>-area"
	suffix := "-area"
	if !strings.HasSuffix(field, suffix) {
		return "", "", false
	}
	pid = strings.TrimSuffix(field, suffix)
	if pid == "" || strings.Contains(pid, ":") {
		return "", "", false
	}
	return iface, pid, true
}

// —— 收集器（确定性升序，禁 map 随机遍历，设计 §4.2）——

// collectIPv6Interfaces 返回存在任意 :ipv6- 键的接口名（升序去重，含 enable/address）。
//
// 🔴 精确中缀 ":ipv6-" 扫描：IPv4 键 interface:<if>:ip 不含该中缀，天然排除（A1/AC12 ①）。
// 纯函数：只读 DeviceConfig。
func collectIPv6Interfaces(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k := range state.DeviceConfig {
		if iface, _, ok := ifaceFromIPv6Key(k); ok {
			seen[iface] = true
		}
	}
	return sortedKeys(seen)
}

// collectIPv6RouteStatics 返回全部 IPv6 静态路由（精确前缀 ipv6:route-static: +
// 值 == "true"，A3 双段解析，按 prefix 升序）。
// 纯函数：只读 DeviceConfig。
func collectIPv6RouteStatics(state *CLIState) []IPv6RouteStatic {
	if state == nil || state.DeviceConfig == nil {
		return []IPv6RouteStatic{}
	}
	routes := make([]IPv6RouteStatic, 0)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, ipv6RouteStaticNamespace) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		prefix, nexthop, ok := parseIPv6RouteStaticKey(k)
		if !ok {
			continue
		}
		routes = append(routes, IPv6RouteStatic{Prefix: prefix, NextHop: nexthop})
	}
	// 确定性：prefix 升序，同 prefix 按 nexthop 升序（杜绝 map 随机遍历，AC7 ⑤）。
	sortIPv6RouteStatics(routes)
	return routes
}

// collectRIPngPIDs 返回已使能的 RIPng 全局进程号（精确前缀 ipv6:ripng: + 值 "true"，pid 升序）。
func collectRIPngPIDs(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, ipv6RIPngNamespace) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		rest := k[len(ipv6RIPngNamespace):]
		pid := rest
		if idx := strings.Index(rest, ":"); idx >= 0 {
			pid = rest[:idx]
		}
		if pid == "" {
			continue
		}
		seen[pid] = true
	}
	return sortedKeys(seen)
}

// collectOSPFv3PIDs 返回已使能的 OSPFv3 全局进程号（精确前缀 ipv6:ospfv3: + 值 "true"，pid 升序）。
func collectOSPFv3PIDs(state *CLIState) []string {
	if state == nil || state.DeviceConfig == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, ipv6OSPFv3Namespace) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		rest := k[len(ipv6OSPFv3Namespace):]
		pid := rest
		if idx := strings.Index(rest, ":"); idx >= 0 {
			pid = rest[:idx]
		}
		if pid == "" {
			continue
		}
		seen[pid] = true
	}
	return sortedKeys(seen)
}

// sortIPv6RouteStatics 就地按 Prefix 升序、同 Prefix 按 NextHop 升序排序（确定性）。
func sortIPv6RouteStatics(routes []IPv6RouteStatic) {
	for i := 1; i < len(routes); i++ {
		for j := i; j > 0; j-- {
			if routes[j-1].Prefix < routes[j].Prefix ||
				(routes[j-1].Prefix == routes[j].Prefix && routes[j-1].NextHop <= routes[j].NextHop) {
				break
			}
			routes[j-1], routes[j] = routes[j], routes[j-1]
		}
	}
}

// —— 配置读取（只读视图派生，纯函数，设计 §4.3）——

// readIPv6AddressView 从 DeviceConfig 即时派生单接口的 IPv6 只读视图。
//
// LinkLocal 口径（C3）：接口存在真实 MAC 键（interface:<if>:mac）→ 用
// EUI64InterfaceID 真实计算 fe80::<EUI64>（真实推导非伪造）；无 MAC 键 → "-"。
// 纯函数：只读 DeviceConfig，调用前后 DeviceConfig 逐键不变（AC13）。
func readIPv6AddressView(state *CLIState, iface string) IPv6AddressView {
	view := IPv6AddressView{
		Interface: strings.TrimSpace(iface),
		LinkLocal: IPv6StatPlaceholder,
	}
	if state == nil || state.DeviceConfig == nil || view.Interface == "" {
		return view
	}
	dc := state.DeviceConfig
	view.Enable = strings.EqualFold(strings.TrimSpace(dc[ipv6IfaceKey(view.Interface, ipv6FieldEnable)]), "true")
	view.Address = strings.TrimSpace(dc[ipv6IfaceKey(view.Interface, ipv6FieldAddress)])
	mac := strings.TrimSpace(dc[ipv6IfaceKeyNamespace+view.Interface+":mac"])
	view.HasMAC = mac != ""
	if view.HasMAC {
		view.LinkLocal = SimulatedLinkLocal(mac)
	}
	return view
}

// —— 核心纯函数（P0-3，零副作用，设计 §4.3）——

// ValidateIPv6Address 校验 IPv6 地址合法性。
//
// 判据（设计 A10）：netip.ParseAddr 成功 + **拒绝 zone**（fe80::1%eth0 之类）+
// **拒绝 IPv4-mapped / IPv4-compatible**（::ffff:1.2.3.4 与 ::1.2.3.4 之类内嵌 IPv4 写法）。
// 失败统一返回 ErrIPv6InvalidAddress 文案。纯函数。
func ValidateIPv6Address(s string) error {
	v := strings.TrimSpace(s)
	if v == "" {
		return fmt.Errorf(ErrIPv6InvalidAddress, v)
	}
	addr, err := netip.ParseAddr(v)
	if err != nil {
		return fmt.Errorf(ErrIPv6InvalidAddress, v)
	}
	if addr.Zone() != "" {
		// A10：zone（%eth0）一律拒绝（教学口径，AC3 断言）。
		return fmt.Errorf(ErrIPv6InvalidAddress, v)
	}
	if addr.Is4In6() || isIPv4Compatible(addr) {
		// A10：IPv4-mapped（::ffff:0:0/96）与 IPv4-compatible（::/96）拒绝，
		// 避免 IPv4 内嵌写法歧义。
		return fmt.Errorf(ErrIPv6InvalidAddress, v)
	}
	return nil
}

// isIPv4Compatible 判定 addr 是否为 IPv4-compatible 内嵌写法（::/96 前缀段全零，
// 且非 :: 未指定、非 ::1 回环）。纯函数，仅 ValidateIPv6Address 内部使用。
func isIPv4Compatible(addr netip.Addr) bool {
	b := addr.As16()
	for i := 0; i < 12; i++ {
		if b[i] != 0 {
			return false
		}
	}
	last4 := uint32(b[12])<<24 | uint32(b[13])<<16 | uint32(b[14])<<8 | uint32(b[15])
	return last4 != 0 && last4 != 1
}

// ValidateIPv6Prefix 校验 "<addr>/<len>" 前缀形态。
//
// 判据：含 '/' 且地址部分合法（ValidateIPv6Address）+ len 为十进制数字且 0–128。
// 失败统一返回 ErrIPv6InvalidPrefix / ErrIPv6InvalidPrefixLen 文案。纯函数。
func ValidateIPv6Prefix(prefix string) error {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return fmt.Errorf(ErrIPv6InvalidPrefix, p)
	}
	idx := strings.LastIndex(p, "/")
	if idx <= 0 || idx == len(p)-1 {
		return fmt.Errorf(ErrIPv6InvalidPrefix, p)
	}
	addrPart := p[:idx]
	lenPart := p[idx+1:]
	if err := ValidateIPv6Address(addrPart); err != nil {
		return fmt.Errorf(ErrIPv6InvalidPrefix, p)
	}
	n, err := strconv.Atoi(lenPart)
	if err != nil || n < 0 || n > IPv6PrefixMaxLen {
		return fmt.Errorf(ErrIPv6InvalidPrefixLen, lenPart)
	}
	return nil
}

// CompressIPv6 返回 IPv6 地址的 RFC 5952 规范化压缩串（netip.Addr.String()，幂等）。
//
// 前导零省略、最长全零段压缩为 "::"、每组至少一位、"::" 仅出现一次（AC3）。
// 非法输入原样返回（调用方应先 ValidateIPv6Address）。纯函数。
func CompressIPv6(addr string) string {
	v := strings.TrimSpace(addr)
	a, err := netip.ParseAddr(v)
	if err != nil {
		return v
	}
	return a.String()
}

// ExpandIPv6 返回 IPv6 地址的全展开形态（8 组各 4 位十六进制，小写）。
//
// 例：ExpandIPv6("2001:db8::1") == "2001:0db8:0000:0000:0000:0000:0000:0001"（AC3）。
// 非法输入原样返回。纯函数。
func ExpandIPv6(addr string) string {
	v := strings.TrimSpace(addr)
	a, err := netip.ParseAddr(v)
	if err != nil {
		return v
	}
	b := a.As16()
	return fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7],
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// IPv6AddressType 判定 IPv6 地址的类型（课程 43 教学重点）。
//
// 判定序：linkLocal（fe80::/10）→ multicast（ff00::/8）→ loopback（::1）→
// unspecified（::）→ uniqueLocal（fc00::/7，P1-3）→ globalUnicast（其余）。
// 非法输入不 panic，返回 globalUnicast。纯函数。
func IPv6AddressType(addr string) IPv6AddrType {
	a, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return IPv6AddrGlobalUnicast
	}
	switch {
	case a.IsLinkLocalUnicast():
		return IPv6AddrLinkLocal
	case a.IsMulticast():
		return IPv6AddrMulticast
	case a.IsLoopback():
		return IPv6AddrLoopback
	case a.IsUnspecified():
		return IPv6AddrUnspecified
	case !a.Is4() && a.IsPrivate():
		// netip.IsPrivate 对 IPv6 即 RFC 4193 fc00::/7（唯一本地，P1-3）。
		return IPv6AddrUniqueLocal
	default:
		return IPv6AddrGlobalUnicast
	}
}

// EUI64InterfaceID 由 48 位 MAC 生成 64 位接口标识（EUI-64，课程 43 教学重点）。
//
// 输入接受两种形态（C9）："00e0-fc12-0aaa"（连字符）与 "00e0fc120aaa"（无分隔），
// 大小写不敏感；本实现对分隔符做宽容处理（'-' / ':' / 空格均可忽略），因此
// 亦兼容既有真实 MAC 键的 "00-0C-29-01-02-03" 形态（parser.go:473）。
//
// 算法：剥离非十六进制字符 → 校验 12 位十六进制 → 插入 0xff 0xfe → 翻转 U/L 位
// （首字节 ^ 0x02，设计 C9：00→02）。
//
// 输出统一小写冒号分段：02e0:fcff:fe12:0aaa（AC3）。纯函数。
func EUI64InterfaceID(mac string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(mac))
	hexStr := ""
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == ':' || c == '-' || c == ' ' {
			continue
		}
		hexStr += string(c)
	}
	if len(hexStr) != 12 {
		return "", fmt.Errorf("invalid MAC address %s", mac)
	}
	bytes := make([]byte, 6)
	for i := 0; i < 6; i++ {
		hi, ok1 := hexNibble(hexStr[i*2])
		lo, ok2 := hexNibble(hexStr[i*2+1])
		if !ok1 || !ok2 {
			return "", fmt.Errorf("invalid MAC address %s", mac)
		}
		bytes[i] = hi<<4 | lo
	}
	// EUI-64：MAC 前 3 字节 + ff:fe + MAC 后 3 字节（插入 0xff 0xfe）。
	id := []byte{bytes[0], bytes[1], bytes[2], 0xff, 0xfe, bytes[3], bytes[4], bytes[5]}
	// 翻转 U/L 位（首字节 ^ 0x02，设计 C9：00→02）。
	id[0] ^= 0x02
	return fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		id[0], id[1], id[2], id[3], id[4], id[5], id[6], id[7]), nil
}

// hexNibble 把单个十六进制字符转为数值。
func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// SimulatedLinkLocal 返回 "fe80::"+EUI64（仅接口存在真实 MAC 键时调用，C3）。
//
// 无真实 MAC 键一律不调用本函数；MAC 非法（EUI-64 失败）时返回 "-"（诚实占位）。
// 纯函数。
func SimulatedLinkLocal(mac string) string {
	id, err := EUI64InterfaceID(mac)
	if err != nil {
		return IPv6StatPlaceholder
	}
	return "fe80::" + id
}

// NetworkFromPrefix 返回前缀的网络地址（netip.Prefix.Masked().Addr()，直连路由用）。
//
// 例：NetworkFromPrefix("2001:db8::1/64") == "2001:db8::"（AC3）。纯函数。
func NetworkFromPrefix(prefix string) (string, error) {
	p := strings.TrimSpace(prefix)
	parsed, err := netip.ParsePrefix(p)
	if err != nil {
		return "", fmt.Errorf(ErrIPv6InvalidPrefix, p)
	}
	return parsed.Masked().Addr().String(), nil
}

// —— 诚实占位注记 ——

// ipv6SimNote 返回 IPv6「诚实占位」注记（lite / full 两态，
// 口径同 greSimNote / aaaSimNote，读 sim.EngineModeName()）。
//
// 全部 display ipv6 interface [brief] / display ipv6 routing-table /
// display ripng / display ospfv3 输出**末尾必须附加该注记**（P0-7 / AC9 红线）。
func ipv6SimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（IPv6 为静态配置模拟（lite 引擎），无真实 ND 邻居发现 / DAD / 动态路由学习，链路本地与运行态字段不可用）"
	}
	return "（IPv6 为静态配置模拟，无真实协议栈与动态路由状态机）"
}
