// ipv6_cmd.go 是 IPv6 配置命令的**副作用唯一出口**
// （P2 第九项，华为 VRP 课程 43/44，T02 配置命令族 / T04 undo handled 族）。
//
// 分层契约（设计 §4.4，严格复刻 gre_cmd.go / aaa_cmd.go）：
//   - ipv6_eval.go   纯函数只读（键 helper / 校验 / 收集器 / 派生），无副作用；
//   - ipv6_cmd.go   **本文件**：唯一允许写 state.DeviceConfig 的 IPv6 代码；
//   - ipv6_display.go 渲染 + 持久化 helper，只读。
//
// 命令族（华为 VRP 真机形态，守卫矩阵见设计 §7.6）：
//
//	系统视图：  ipv6                          （写 ipv6:enabled）
//	            ipv6 route-static <p>/<l> <nh>（写 ipv6:route-static:<p>/<l>:<nh>，多键 C2）
//	            ripng [<pid>]                 （写 ipv6:ripng:<pid>:enabled，C7）
//	            ospfv3 [<pid>]                （写 ipv6:ospfv3:<pid>:enabled，C8）
//	接口视图：  ipv6 enable                   （写 interface:<if>:ipv6-enable）
//	            ipv6 address <a>/<p>          （C1 硬前置 + 规范化存储 A7）
//	            ripng <pid> enable            （写 interface:<if>:ripng-<pid>-enable，C7）
//	            ospfv3 <pid> area <id>        （写 interface:<if>:ospfv3-<pid>-area，C8，必带 area）
//
// 三态守卫顺序固定：视图 → 设备（l3Devices()）→ 前置条件（仅 C1 一条，A9）。
// 🔴 守卫未通过时**绝不写任何键**（AC1 ② / AC2 ③）。
//
// 🔴 A1 键碰撞红线：拼键 / 解键唯一走 ipv6_eval.go 的精确 helper，
// 严禁任何基于子串的模糊键匹配（AC12 ④ 静态断言）。
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// —— 顶层命令名常量（parser.go 顶层 switch 分派键）——

const (
	ipv6CmdRouteStatic = "route-static"
	ipv6CmdEnable      = "enable"
	ipv6CmdAddress     = "address"
	ipv6CmdArea        = "area"
	ipv6CmdRIPng       = "ripng"
	ipv6CmdOSPFv3      = "ospfv3"
)

// —— 回显常量（PRD §4.1 与设计 §7.8，QA 逐字断言）——

const (
	// ipv6EchoSystemEnabled 是系统视图裸 ipv6 的成功回显。
	ipv6EchoSystemEnabled = "IPv6 enabled"
	// ipv6EchoInterfaceEnabled 是接口视图 ipv6 enable 的成功回显（%s = 接口名）。
	ipv6EchoInterfaceEnabled = "IPv6 is enabled on %s"
	// ipv6EchoAddressConfigured 是接口视图 ipv6 address 的成功回显（%s1 = 规范化前缀，%s2 = 接口名）。
	ipv6EchoAddressConfigured = "IPv6 address %s configured on %s"
	// ipv6EchoRouteStaticAdded 是系统视图 ipv6 route-static 的成功回显。
	ipv6EchoRouteStaticAdded = "Static route added"
	// ipv6EchoRIPngEnabled 是系统视图 ripng [<pid>] 的成功回显（%s = pid）。
	ipv6EchoRIPngEnabled = "RIPng process %s enabled"
	// ipv6EchoRIPngIfaceEnabled 是接口视图 ripng <pid> enable 的成功回显（%s1 = pid，%s2 = 接口名）。
	ipv6EchoRIPngIfaceEnabled = "RIPng process %s enabled on %s"
	// ipv6EchoOSPFv3Enabled 是系统视图 ospfv3 [<pid>] 的成功回显（%s = pid）。
	ipv6EchoOSPFv3Enabled = "OSPFv3 process %s enabled"
	// ipv6EchoOSPFv3IfaceEnabled 是接口视图 ospfv3 <pid> area <id> 的成功回显（%s1 = pid，%s2 = area，%s3 = 接口名）。
	ipv6EchoOSPFv3IfaceEnabled = "OSPFv3 process %s area %s enabled on %s"
)

// —— 设备守卫（设计 A5：复用 l3Devices()，严禁重定义）——

// ipv6DeviceSupported 判定当前设备类型是否支持 IPv6 配置命令（分支内守卫，A5）。
//
// 设备集**直接复用 capabilities.go:174 的 l3Devices()**（Router / L3Switch / Firewall / VTEP），
// 严禁在本文件重定义设备集合；capabilities.go 本期零改动。
func ipv6DeviceSupported(state *CLIState) bool {
	if state == nil || state.DeviceType == "" {
		return true
	}
	return l3Devices()[state.DeviceType]
}

// errIPv6NotSupported 返回设备类型能力拒绝文案（设计 §7.6 守卫矩阵）。
func errIPv6NotSupported(dt string) string {
	return fmt.Sprintf("Error: IPv6 is not supported on %s", dt)
}

// ipv6ViewInterfaceGuard 执行「视图 → 设备」两级守卫（接口视图命令专用）。
//
// 返回 (接口名, 错误文案)；错误文案非空即表示守卫未通过，调用方应直接返回
// 该文案且**不写任何键**（AC2 ③）。前置条件（C1）由各命令自行校验（A9 第三层）。
func ipv6ViewInterfaceGuard(state *CLIState) (string, string) {
	if state == nil {
		return "", "Error: internal state unavailable"
	}
	// ① 视图守卫
	if state.CurrentView != ViewInterface {
		return "", ErrIPv6MustBeInterfaceView
	}
	iface := strings.TrimSpace(state.CurrentSub)
	if iface == "" {
		return "", ErrIPv6MustBeInterfaceView
	}
	// ② 设备守卫
	if !ipv6DeviceSupported(state) {
		return "", errIPv6NotSupported(string(state.DeviceType))
	}
	return iface, ""
}

// ipv6SystemViewGuard 执行「视图 → 设备」两级守卫（系统视图命令专用）。
func ipv6SystemViewGuard(state *CLIState) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if state.CurrentView != ViewSystem {
		return ErrIPv6Unrecognized
	}
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType))
	}
	return ""
}

// validIPv6Pid 校验 RIPng / OSPFv3 进程号（1–65535 十进制，VRP 真机范围）。
func validIPv6Pid(pid string) bool {
	p := strings.TrimSpace(pid)
	if len(p) < 1 || len(p) > 5 {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return false
	}
	return true
}

// normalizeIPv6Prefix 返回 "<addr>/<len>" 的 RFC 5952 规范化形态（A7）。
// 地址段经 CompressIPv6 压缩，长度段原样保留。
func normalizeIPv6Prefix(prefix string) string {
	p := strings.TrimSpace(prefix)
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return p
	}
	return CompressIPv6(p[:idx]) + p[idx:]
}

// —— 命令分派（副作用唯一入口，T02 配置命令族）——

// applyIPv6SystemEnable 处理系统视图裸 `ipv6`（P0-1）。
//
// 视图守卫：仅系统视图；设备守卫：l3Devices()。写 ipv6:enabled = "true"，
// 回显 "IPv6 enabled"（PRD §4.1）。重复执行幂等。
func applyIPv6SystemEnable(state *CLIState, args []string) string {
	if guardErr := ipv6SystemViewGuard(state); guardErr != "" {
		return guardErr
	}
	if len(args) != 0 {
		return ErrIPv6Unrecognized
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6GlobalKey()] = "true"
	return ipv6EchoSystemEnabled
}

// applyIPv6InterfaceEnable 处理接口视图 `ipv6 enable`（P0-2）。
//
// 三态守卫：视图 → 设备 →（无前置）。写 interface:<if>:ipv6-enable = "true"，
// 回显 "IPv6 is enabled on <if>"（PRD §4.1）。
// 🔴 不要求全局 `ipv6` 已使能（§9 待明确 ③，仅 C1 一条前置，A9）。
func applyIPv6InterfaceEnable(state *CLIState, args []string) string {
	iface, guardErr := ipv6ViewInterfaceGuard(state)
	if guardErr != "" {
		return guardErr
	}
	if len(args) != 0 {
		return ErrIPv6Unrecognized
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldEnable)] = "true"
	return fmt.Sprintf(ipv6EchoInterfaceEnabled, iface)
}

// applyIPv6InterfaceAddress 处理接口视图 `ipv6 address <addr>/<prefix>`（P0-2）。
//
// 三态守卫顺序：视图 → 设备 → C1 前置（A9）。
//   - C1：未 `ipv6 enable` 配地址 → ErrIPv6EnableFirst，**不写任何键**（AC2 ③）；
//   - 校验：ValidateIPv6Prefix（非法地址 / 非法前缀长度拒绝，AC3）；
//   - 存储：A7 规范化（CompressIPv6(addr) + "/" + len），回显规范化前缀（PRD §4.1）。
func applyIPv6InterfaceAddress(state *CLIState, args []string) string {
	iface, guardErr := ipv6ViewInterfaceGuard(state)
	if guardErr != "" {
		return guardErr
	}
	if len(args) != 1 {
		return ErrIPv6Unrecognized
	}
	prefix := strings.TrimSpace(args[0])
	// C1 硬前置：必须先 ipv6 enable（不做隐式自动使能，保留教学点）。
	if !strings.EqualFold(strings.TrimSpace(state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldEnable)]), "true") {
		return fmt.Sprintf(ErrIPv6EnableFirst, iface)
	}
	if err := ValidateIPv6Prefix(prefix); err != nil {
		return err.Error()
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	norm := normalizeIPv6Prefix(prefix)
	state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldAddress)] = norm
	return fmt.Sprintf(ipv6EchoAddressConfigured, norm, iface)
}

// applyIPv6RouteStatic 处理系统视图 `ipv6 route-static <prefix>/<len> <nexthop>`（P0-8，C2 多键）。
//
// 三态守卫：视图（系统）→ 设备 →（无前置）。
//   - 校验：前缀与下一跳均须合法（非法 → 对应 Error 且**不写任何键**，AC6 ②）；
//   - 存储：ipv6:route-static:<规范化前缀>:<规范化下一跳> = "true"（A7）；
//   - 幂等：同前缀同下一跳重复配置不报错不覆盖（A8，AC6 ③）。
func applyIPv6RouteStatic(state *CLIState, args []string) string {
	if guardErr := ipv6SystemViewGuard(state); guardErr != "" {
		return guardErr
	}
	if len(args) != 2 {
		return ErrIPv6RouteStaticUsage
	}
	prefix := strings.TrimSpace(args[0])
	nexthop := strings.TrimSpace(args[1])
	if err := ValidateIPv6Prefix(prefix); err != nil {
		return err.Error()
	}
	if err := ValidateIPv6Address(nexthop); err != nil {
		return fmt.Sprintf(ErrIPv6InvalidAddress, nexthop)
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	normPrefix := normalizeIPv6Prefix(prefix)
	normNexthop := CompressIPv6(nexthop)
	key := ipv6RouteStaticKey(normPrefix, normNexthop)
	if _, exists := state.DeviceConfig[key]; !exists {
		state.DeviceConfig[key] = "true"
	}
	return ipv6EchoRouteStaticAdded
}

// applyRIPng 处理系统视图 `ripng [<pid>]`（P0-13，C7 华为 VRP 真机形态）。
//
// 不进入进程子视图（§9 待明确 ⑤），仅写全局进程键 ipv6:ripng:<pid>:enabled + 回显。
// pid 缺省为 "1"；非法 pid / 多余参数 → ErrRIPngUsage，不写任何键。
func applyRIPng(state *CLIState, args []string) string {
	if guardErr := ipv6SystemViewGuard(state); guardErr != "" {
		return guardErr
	}
	pid := "1"
	switch len(args) {
	case 0:
	case 1:
		if !validIPv6Pid(args[0]) {
			return ErrRIPngUsage
		}
		pid = strings.TrimSpace(args[0])
	default:
		return ErrRIPngUsage
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6RIPngKey(pid)] = "true"
	return fmt.Sprintf(ipv6EchoRIPngEnabled, pid)
}

// applyRIPngInterface 处理接口视图 `ripng <pid> enable`（P0-13，C7）。
//
// 写 interface:<if>:ripng-<pid>-enable = "true"。参数形态不符 / pid 非法 →
// ErrRIPngIfaceUsage，不写任何键。
func applyRIPngInterface(state *CLIState, args []string) string {
	iface, guardErr := ipv6ViewInterfaceGuard(state)
	if guardErr != "" {
		return guardErr
	}
	if len(args) != 2 || !strings.EqualFold(strings.TrimSpace(args[1]), ipv6CmdEnable) {
		return ErrRIPngIfaceUsage
	}
	pid := strings.TrimSpace(args[0])
	if !validIPv6Pid(pid) {
		return ErrRIPngIfaceUsage
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6RIPngIfaceKey(iface, pid)] = "true"
	return fmt.Sprintf(ipv6EchoRIPngIfaceEnabled, pid, iface)
}

// applyOSPFv3 处理系统视图 `ospfv3 [<pid>]`（P0-14，C8 华为 VRP 真机形态）。
//
// 不进入进程子视图（§9 待明确 ⑤），仅写全局进程键 ipv6:ospfv3:<pid>:enabled + 回显。
// pid 缺省为 "1"；非法 pid / 多余参数 → ErrOSPFv3Usage，不写任何键。
func applyOSPFv3(state *CLIState, args []string) string {
	if guardErr := ipv6SystemViewGuard(state); guardErr != "" {
		return guardErr
	}
	pid := "1"
	switch len(args) {
	case 0:
	case 1:
		if !validIPv6Pid(args[0]) {
			return ErrOSPFv3Usage
		}
		pid = strings.TrimSpace(args[0])
	default:
		return ErrOSPFv3Usage
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6OSPFv3Key(pid)] = "true"
	return fmt.Sprintf(ipv6EchoOSPFv3Enabled, pid)
}

// applyOSPFv3Interface 处理接口视图 `ospfv3 <pid> area <area-id>`（P0-14，C8）。
//
// 接口裸 `ospfv3` **不合法**（必须带 pid + area，C8）；写
// interface:<if>:ospfv3-<pid>-area = "<area-id>"。参数形态不符 / pid 非法 →
// ErrOSPFv3IfaceUsage，不写任何键。
func applyOSPFv3Interface(state *CLIState, args []string) string {
	iface, guardErr := ipv6ViewInterfaceGuard(state)
	if guardErr != "" {
		return guardErr
	}
	if len(args) < 3 || !strings.EqualFold(strings.TrimSpace(args[1]), ipv6CmdArea) {
		return ErrOSPFv3IfaceUsage
	}
	pid := strings.TrimSpace(args[0])
	area := strings.TrimSpace(strings.Join(args[2:], " "))
	if !validIPv6Pid(pid) || area == "" {
		return ErrOSPFv3IfaceUsage
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	state.DeviceConfig[ipv6OSPFv3IfaceKey(iface, pid)] = area
	return fmt.Sprintf(ipv6EchoOSPFv3IfaceEnabled, pid, area, iface)
}

// —— undo handled 族（T04，设计 §4.4 / §7.5，返回 (string, bool)）——
//
// handled 模式对齐 applyUndoGREInterface（parser.go:866）：未命中返回 ("", false)，
// 交回既有 undo 分支，零回归（AC10 ⑤）。命中但参数形态不符时返回 ("", true) 的 Error
// 文案（已吞掉该命令，不让其漏到既有分支产生歧义）。

// applyUndoIPv6Interface 处理接口视图 undo ipv6 enable / undo ipv6 address /
// undo ripng <pid> enable / undo ospfv3 <pid> area（C5 / P0-10 / P0-13 / P0-14）。
//
// 级联口径（设计 §7.5）：
//   - undo ipv6 enable → 清 interface:<if>:ipv6-enable **+ 级联清 interface:<if>:ipv6-address**（C5）；
//   - undo ipv6 address → 清 interface:<if>:ipv6-address（P0-10）；
//   - undo ripng <pid> enable → 清 interface:<if>:ripng-<pid>-enable（P0-13）；
//   - undo ospfv3 <pid> area → 清 interface:<if>:ospfv3-<pid>-area（P0-14）。
//
// 🔴 A1 红线：head 精确匹配 "ipv6" / "ripng" / "ospfv3"（决不含 "ip"），
// 未命中 → ("", false) 交回既有接口 undo switch sub 分支（"ip address" 等零回归）。
func applyUndoIPv6Interface(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 {
		return "", false
	}
	if state.CurrentView != ViewInterface {
		return "", false
	}
	iface := strings.TrimSpace(state.CurrentSub)
	if iface == "" {
		return "", false
	}
	head := strings.ToLower(strings.TrimSpace(args[0]))
	switch head {
	case "ipv6", ipv6CmdRIPng, ipv6CmdOSPFv3:
	default:
		return "", false
	}
	// 设备守卫（分支内 l3Devices()，A5）：undo 配置命令同样按设备类型守卫（§7.6）。
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "", true
	}
	switch head {
	case "ipv6":
		if len(args) < 2 {
			return ErrIPv6Unrecognized, true
		}
		switch strings.ToLower(strings.TrimSpace(args[1])) {
		case ipv6CmdEnable:
			// C5：级联清地址（其它接口键 :ripng-* / :ospfv3-* / :mac 等完好）。
			delete(state.DeviceConfig, ipv6IfaceKey(iface, ipv6FieldEnable))
			delete(state.DeviceConfig, ipv6IfaceKey(iface, ipv6FieldAddress))
			return "IPv6 disabled on " + iface, true
		case ipv6CmdAddress:
			delete(state.DeviceConfig, ipv6IfaceKey(iface, ipv6FieldAddress))
			return "IPv6 address deleted on " + iface, true
		}
		return ErrIPv6Unrecognized, true
	case ipv6CmdRIPng:
		// undo ripng <pid> enable（接口视图，P0-13）。
		if len(args) != 3 || !strings.EqualFold(strings.TrimSpace(args[2]), ipv6CmdEnable) {
			return ErrRIPngIfaceUsage, true
		}
		pid := strings.TrimSpace(args[1])
		if !validIPv6Pid(pid) {
			return ErrRIPngIfaceUsage, true
		}
		delete(state.DeviceConfig, ipv6RIPngIfaceKey(iface, pid))
		return fmt.Sprintf("RIPng process %s disabled on %s", pid, iface), true
	case ipv6CmdOSPFv3:
		// undo ospfv3 <pid> area（接口视图，P0-14）。
		if len(args) != 3 || !strings.EqualFold(strings.TrimSpace(args[2]), ipv6CmdArea) {
			return ErrOSPFv3IfaceUsage, true
		}
		pid := strings.TrimSpace(args[1])
		if !validIPv6Pid(pid) {
			return ErrOSPFv3IfaceUsage, true
		}
		delete(state.DeviceConfig, ipv6OSPFv3IfaceKey(iface, pid))
		return fmt.Sprintf("OSPFv3 process %s area removed on %s", pid, iface), true
	}
	return "", false
}

// applyUndoIPv6System 处理系统视图 `undo ipv6`（C6/A12）。
//
// 仅清 `ipv6:` **精确前缀**全部键（enabled + route-static:* + ripng:* + ospfv3:*），
// 严禁波及 interface:<if>:ipv6-* 与异族键（AC12 ③）。仅接受纯 `undo ipv6`；
// 子命令（route-static 等）由 applyUndoSystemFeature 分派给专门函数，未命中返回 ("", false)。
func applyUndoIPv6System(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) != 1 {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), "ipv6") {
		return "", false
	}
	if state.CurrentView != ViewSystem {
		return "", false
	}
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "IPv6 disabled", true
	}
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, ipv6KeyPrefix()) {
			delete(state.DeviceConfig, k)
		}
	}
	return "IPv6 disabled", true
}

// applyUndoIPv6RouteStatic 处理系统视图 `undo ipv6 route-static [<prefix>]`（A8/C2/P1-8）。
//
//   - 带 <prefix>：清 `ipv6:route-static:<prefix>:` **精确前缀**全部键（多下一跳级联，AC6 ④），
//     其它前缀路由键完好（AC10 ④）；prefix 先经 ValidateIPv6Prefix + normalize（A7）再删，
//     保证与配置时规范化存储的键一致。
//   - 无参：清 `ipv6:route-static:` 前缀全部键（P1-8）。
func applyUndoIPv6RouteStatic(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) < 2 {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), "ipv6") ||
		!strings.EqualFold(strings.TrimSpace(args[1]), ipv6CmdRouteStatic) {
		return "", false
	}
	if state.CurrentView != ViewSystem {
		return "", false
	}
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "IPv6 static route removed", true
	}
	switch len(args) {
	case 2:
		// P1-8：无参清全部静态路由键。
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, ipv6RouteStaticPrefix()) {
				delete(state.DeviceConfig, k)
			}
		}
		return "All IPv6 static routes removed", true
	case 3:
		// A8/C2：精确前缀级联（多下一跳）。
		prefix := strings.TrimSpace(args[2])
		if err := ValidateIPv6Prefix(prefix); err != nil {
			return err.Error(), true
		}
		norm := normalizeIPv6Prefix(prefix)
		delPrefix := ipv6RouteStaticPrefix() + norm + ":"
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, delPrefix) {
				delete(state.DeviceConfig, k)
			}
		}
		return fmt.Sprintf("IPv6 static route %s removed", norm), true
	default:
		return ErrIPv6Unrecognized, true
	}
}

// applyUndoRIPng 处理系统视图 `undo ripng [<pid>]`（P0-13）。
//
//   - 带 <pid>：清 ipv6:ripng:<pid>:enabled（精确键）；pid 非法 → ErrRIPngUsage；
//   - 无 pid：清 `ipv6:ripng:` 前缀全部键。
//   接口 :ripng-<pid>-enable 键不在清理范围（§7.5 只列全局进程键）。
func applyUndoRIPng(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), ipv6CmdRIPng) {
		return "", false
	}
	if state.CurrentView != ViewSystem {
		return "", false
	}
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "RIPng disabled", true
	}
	switch len(args) {
	case 1:
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, ipv6RIPngNamespace) {
				delete(state.DeviceConfig, k)
			}
		}
		return "RIPng disabled", true
	case 2:
		pid := strings.TrimSpace(args[1])
		if !validIPv6Pid(pid) {
			return ErrRIPngUsage, true
		}
		delete(state.DeviceConfig, ipv6RIPngKey(pid))
		return fmt.Sprintf("RIPng process %s disabled", pid), true
	default:
		return ErrRIPngUsage, true
	}
}

// applyUndoOSPFv3 处理系统视图 `undo ospfv3 [<pid>]`（P0-14）。
//
//   - 带 <pid>：清 ipv6:ospfv3:<pid>:enabled（精确键）；pid 非法 → ErrOSPFv3Usage；
//   - 无 pid：清 `ipv6:ospfv3:` 前缀全部键。
//   接口 :ospfv3-<pid>-area 键不在清理范围（§7.5 只列全局进程键）。
func applyUndoOSPFv3(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), ipv6CmdOSPFv3) {
		return "", false
	}
	if state.CurrentView != ViewSystem {
		return "", false
	}
	if !ipv6DeviceSupported(state) {
		return errIPv6NotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "OSPFv3 disabled", true
	}
	switch len(args) {
	case 1:
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, ipv6OSPFv3Namespace) {
				delete(state.DeviceConfig, k)
			}
		}
		return "OSPFv3 disabled", true
	case 2:
		pid := strings.TrimSpace(args[1])
		if !validIPv6Pid(pid) {
			return ErrOSPFv3Usage, true
		}
		delete(state.DeviceConfig, ipv6OSPFv3Key(pid))
		return fmt.Sprintf("OSPFv3 process %s disabled", pid), true
	default:
		return ErrOSPFv3Usage, true
	}
}
