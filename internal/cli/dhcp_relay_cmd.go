// dhcp_relay_cmd.go 实现「DHCP 中继配置命令的副作用落地层」（P2 第六项，T0/T2）。
//
// 与 dhcp_relay_eval.go 的分工（架构铁律 2）：
//   - dhcp_relay_eval.go：**纯函数**评估器，只读 DeviceConfig / Interfaces，不写任何 state；
//   - dhcp_relay_cmd.go ：**唯一副作用出口**，把已校验的解析结果落到 DeviceConfig 单一事实源。
//
// 命令族（全部为**接口视图**命令，对齐官方 VRP 课程 27）：
//
//	dhcp select { global | interface | relay }
//	dhcp relay server-ip <ip-address>
//	dhcp relay information enable
//	dhcp relay information strategy { drop | keep | replace }
//	dhcp relay source-ip <ip-address>
//	undo dhcp select [ global | interface | relay ]
//	undo dhcp relay server-ip [ <ip-address> ]
//	undo dhcp relay information { enable | strategy }
//	undo dhcp relay source-ip
//
// 三层守卫（拍板 #5，设计 §2 改动点 #5）：
//  1. 视图守卫：非接口视图 → 报错引导（顶层 case "dhcp" 已按视图分派）；
//  2. 设备守卫：非 l3Devices()（Router / L3Switch / Firewall / VTEP）→ 能力拒绝，
//     **复用 capabilities.go 的 l3Devices()，严禁重定义**（设计 A2/A3）；
//  3. relay 前置守卫：dhcp-select != relay 时配置任何 `dhcp relay ...` 参数 → 报错拒绝
//     且**不写任何键**（拍板 #1）。
//
// 顶层能力矩阵 capabilities.go 的 "dhcp": switchAndL3() **保持零改动**（拍板 #5），
// 二层 Switch 的既有 dhcp enable / dhcp pool 行为逐字不变（AC10c）。
//
// 回显口径（P2-3，对齐 LAG P0-18）：配置成功一律 **VRP 静默（返回空串）**
// 或规范 `Info:` 短提示，失败才返回 `Error: ...`；
// **禁止自造 "Relay server added OK!" 式欢快文案**。
package cli

import (
	"fmt"
	"strings"
)

// —— 固定文案（全仓唯一定义，便于 grep 与断言）——

const (
	// errDHCPSelectInterfaceView 是系统视图 / 其它视图执行 dhcp select 的报错引导（拍板 #2 / P2-2）。
	errDHCPSelectInterfaceView = "Error: Please run 'dhcp select' in interface view."
	// errUndoDHCPSelectInterfaceView 是系统视图执行 undo dhcp select 的报错引导（设计 A8）。
	errUndoDHCPSelectInterfaceView = "Error: Please run 'undo dhcp select' in interface view."
	// errDHCPRelayInterfaceView 是非接口视图执行 dhcp relay ... 的视图拒绝（P0-7）。
	errDHCPRelayInterfaceView = "Error: must be in interface view"
	// errDHCPSelectRelayFirst 是 relay 前置守卫拒错文案（拍板 #1）。
	errDHCPSelectRelayFirst = "Error: Please run 'dhcp select relay' on this interface first."
	// errDHCPSelectUsage 是 dhcp select 的 usage 提示。
	errDHCPSelectUsage = "Error: usage: dhcp select { global | interface | relay }"
	// errDHCPRelayServerIPUsage 是 dhcp relay server-ip 的 usage 提示。
	errDHCPRelayServerIPUsage = "Error: usage: dhcp relay server-ip <ip-address>"
	// errDHCPRelaySourceIPUsage 是 dhcp relay source-ip 的 usage 提示。
	errDHCPRelaySourceIPUsage = "Error: usage: dhcp relay source-ip <ip-address>"
	// errDHCPRelayInformationUsage 是 dhcp relay information 的 usage 提示。
	errDHCPRelayInformationUsage = "Error: usage: dhcp relay information { enable | strategy { drop | keep | replace } }"
	// errDHCPRelayUsage 是 dhcp relay 子命令族的 usage 提示。
	errDHCPRelayUsage = "Error: usage: dhcp relay { server-ip <ip-address> | information { enable | strategy <strategy> } | source-ip <ip-address> }"
	// errUndoDHCPUsage 是接口视图 undo dhcp 的 usage 提示。
	errUndoDHCPUsage = "Error: usage: undo dhcp { select | relay { server-ip [<ip-address>] | information { enable | strategy } | source-ip } }"
	// errDHCPRelayServerIPNotExist 是 undo 精确摘除不存在地址的拒错文案（P1-6 / AC9）。
	errDHCPRelayServerIPNotExist = "Error: The specified server IP address does not exist."
	// errUnrecognizedCommand 是枚举校验失败的统一文案（P1-2）。
	errUnrecognizedCommand = "Error: unrecognized command"

	// infoDHCPNotEnabled 是全局未 dhcp enable 时的**软提示**（拍板 #6：不阻断、键照写）。
	infoDHCPNotEnabled = "Info: DHCP is not enabled. Run 'dhcp enable' in system view to activate this configuration."
	// infoOption82NotEnabled 是未 information enable 就配 strategy 的软提示（拍板 #6）。
	infoOption82NotEnabled = "Info: Option82 is not enabled. Run 'dhcp relay information enable' to activate this strategy."
)

// errDHCPRelayNotSupported 返回设备类型能力拒绝文案（设计 §2 改动点 #5 ②）。
func errDHCPRelayNotSupported(dt string) string {
	return fmt.Sprintf("Error: DHCP relay is not supported on %s", dt)
}

// —— 守卫 ——

// dhcpRelayDeviceSupported 判定当前设备类型是否支持 DHCP 中继（分支内守卫，设计 A2）。
//
// 设备集**直接复用 capabilities.go 的 l3Devices()**（Router / L3Switch / Firewall / VTEP），
// 严禁重定义。与 lagDeviceSupported 同口径：**未绑定设备类型（空串）时放行**，
// 避免单测 / 无拓扑场景被误拒。
func dhcpRelayDeviceSupported(state *CLIState) bool {
	if state == nil || state.DeviceType == "" {
		return true
	}
	return l3Devices()[state.DeviceType]
}

// dhcpNotEnabledHint 返回「全局未 dhcp enable」的软提示（拍板 #6）。
// 已启用时返回空串。**该提示不阻断配置，键照写**。
func dhcpNotEnabledHint(state *CLIState) string {
	if state == nil || state.DHCP == nil || !state.DHCP.Enabled {
		return infoDHCPNotEnabled
	}
	return ""
}

// joinCLILines 把若干回显片段按行拼接，自动丢弃空串。
// 用于「成功静默 + 可选 Info 提示」的组合回显。
func joinCLILines(lines ...string) string {
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// —— 接口视图 dhcp 命令分派（T0 改动点 #1 / T2 改动点 #2）——

// applyDHCPInterfaceCmd 是接口视图下 `dhcp ...` 命令族的分派入口。
//
// 由 parser.go 的 case "dhcp" 在 state.CurrentView == ViewInterface 时调用。
// 只有 select / relay 两个子命令属接口视图语义；其余（enable / disable / pool）
// **保持既有系统视图口径**，返回 "Error: must be in system view"（AC10c 零回归）。
func applyDHCPInterfaceCmd(state *CLIState, args []string) string {
	if state == nil || len(args) == 0 {
		return "Error: need args"
	}
	switch strings.ToLower(args[0]) {
	case "select", "relay":
		// 守卫 ①：视图（理论上调用方已保证，此处兜底防御）
		iface := strings.TrimSpace(state.CurrentSub)
		if state.CurrentView != ViewInterface || iface == "" {
			return errDHCPRelayInterfaceView
		}
		// 守卫 ②：设备类型（A2/A3，二层 Switch / AC / AP 在此被拒）
		if !dhcpRelayDeviceSupported(state) {
			return errDHCPRelayNotSupported(string(state.DeviceType))
		}
		if strings.EqualFold(args[0], "select") {
			return applyDHCPSelect(state, iface, args[1:])
		}
		return applyDHCPRelay(state, iface, args[1:])
	}
	// enable / disable / pool 等系统视图命令：行为与迁移前逐字一致。
	return "Error: must be in system view"
}

// applyDHCPSelect 落地 `dhcp select { global | interface | relay }`（接口视图）。
//
// 单一事实源：interface:<if>:dhcp-select（设计 A1，不存在 dhcp-relay:mode 键）。
// 三态互斥级联清理（拍板 #3）：写入 global / interface 时，
// **删除该接口 interface:<if>:dhcp-relay: 精确前缀的全部键**，杜绝幽灵配置。
// 重复执行幂等（不报错、不产生重复键）。
func applyDHCPSelect(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errDHCPSelectUsage
	}
	mode := strings.ToLower(strings.TrimSpace(args[0]))
	switch mode {
	case relayModeGlobal, relayModeInterface, relayModeRelay:
	default:
		return errDHCPSelectUsage
	}
	state.DeviceConfig[dhcpSelectKey(iface)] = mode
	if mode != relayModeRelay {
		// 拍板 #3：切到非 relay 模式即级联清理该接口全部中继键（避免幽灵配置）。
		clearDHCPRelayKeys(state, iface)
	}
	return dhcpNotEnabledHint(state)
}

// clearDHCPRelayKeys 级联删除某接口全部 interface:<if>:dhcp-relay:<field> 键。
//
// ⚠️ 只按**精确字段名**逐键删除（§1.6 键碰撞红线），
// 绝不误删 interface:<if>:dhcp-pool（地址池绑定）与 interface:<if>:dhcp-select（模式键）。
func clearDHCPRelayKeys(state *CLIState, iface string) {
	if state == nil || state.DeviceConfig == nil {
		return
	}
	for _, field := range dhcpRelayFields {
		delete(state.DeviceConfig, dhcpRelayKey(iface, field))
	}
	// 防御性兜底：清理任何以精确前缀 interface:<if>:dhcp-relay: 开头的遗留键
	// （例如未来新增字段后的历史残留），仍不触碰 dhcp-pool / dhcp-select。
	prefix := dhcpRelayKeyPrefix(iface)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			delete(state.DeviceConfig, k)
		}
	}
}

// applyDHCPRelay 分派 `dhcp relay ...` 子命令族（接口视图）。
//
// 守卫 ③（拍板 #1）：dhcp-select != relay 时**全部** relay 参数命令一律报错拒绝、
// 不写任何键。该守卫统一施加于 server-ip / information / source-ip 三者，
// 以维持「relay 键 ⟺ relay 模式」不变式——这是拍板 #3 级联清理与
// collectRelayInterfaces 幽灵检测赖以成立的前提。
func applyDHCPRelay(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errDHCPRelayUsage
	}
	sub := strings.ToLower(args[0])
	// 先做 usage（语法）校验，再做语义前置守卫：
	// 缺参属语法错误，任何上下文下都应给 usage 提示（AC5 ⑤）。
	switch sub {
	case "server-ip":
		if len(args) < 2 {
			return errDHCPRelayServerIPUsage
		}
	case "source-ip":
		if len(args) < 2 {
			return errDHCPRelaySourceIPUsage
		}
	case "information":
		// `information` 缺子命令，或 `information strategy` 缺策略值，均属语法错误。
		if len(args) < 2 || (strings.EqualFold(args[1], "strategy") && len(args) < 3) {
			return errDHCPRelayInformationUsage
		}
	default:
		return errUnrecognizedCommand
	}
	// 守卫 ③：relay 前置条件（拍板 #1），未通过则**不写任何键**。
	if dhcpSelectMode(state, iface) != relayModeRelay {
		return errDHCPSelectRelayFirst
	}
	switch sub {
	case "server-ip":
		return applyDHCPRelayServerIP(state, iface, args[1:])
	case "source-ip":
		return applyDHCPRelaySourceIP(state, iface, args[1:])
	default:
		return applyDHCPRelayInformation(state, iface, args[1:])
	}
}

// applyDHCPRelayServerIP 落地 `dhcp relay server-ip <ip-address>`。
//
// 语义（P0-4 / P1-5 / AC3）：追加写入 interface:<if>:dhcp-relay:server-ips 逗号串**尾部**，
// **保序**（先配先列）、**去重**（重复地址幂等不追加）、上限 MaxRelayServerIPs（8）。
func applyDHCPRelayServerIP(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errDHCPRelayServerIPUsage
	}
	ip := strings.TrimSpace(args[0])
	if ok, reason := validRelayServerIP(ip); !ok {
		return reason
	}
	key := dhcpRelayKey(iface, dhcpRelayFieldServerIPs)
	existing := parseRelayServerIPs(state.DeviceConfig[key])
	for _, cur := range existing {
		if cur == ip {
			// 去重：重复地址幂等成功，不追加、不报错。
			return dhcpNotEnabledHint(state)
		}
	}
	if len(existing) >= MaxRelayServerIPs {
		return fmt.Sprintf("Error: The number of DHCP relay server IP addresses exceeds the upper limit (%d).", MaxRelayServerIPs)
	}
	state.DeviceConfig[key] = joinRelayServerIPs(append(existing, ip))
	return dhcpNotEnabledHint(state)
}

// applyDHCPRelaySourceIP 落地 `dhcp relay source-ip <ip-address>`（P1-1）。
// 单值语义：后配覆盖先配；同走 validRelayServerIP 校验。
func applyDHCPRelaySourceIP(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errDHCPRelaySourceIPUsage
	}
	ip := strings.TrimSpace(args[0])
	if ok, reason := validRelayServerIP(ip); !ok {
		return reason
	}
	state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldSourceIP)] = ip
	return dhcpNotEnabledHint(state)
}

// applyDHCPRelayInformation 落地 `dhcp relay information { enable | strategy <s> }`
// （P0-6 / P1-2）。strategy 取值严格枚举校验，非法值 → Error: unrecognized command。
//
// 拍板 #6：未 `information enable` 就配 strategy → **允许 + Info 软提示**（不做顺序强耦合）。
func applyDHCPRelayInformation(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errDHCPRelayInformationUsage
	}
	switch strings.ToLower(args[0]) {
	case "enable":
		state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldOption82)] = "true"
		return dhcpNotEnabledHint(state)
	case "strategy":
		if len(args) < 2 {
			return errDHCPRelayInformationUsage
		}
		strategy := strings.ToLower(strings.TrimSpace(args[1]))
		if !validOption82Strategies[strategy] {
			return errUnrecognizedCommand
		}
		state.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldStrategy)] = strategy
		hint := ""
		if !readRelayConfig(state, iface).Option82 {
			hint = infoOption82NotEnabled
		}
		return joinCLILines(dhcpNotEnabledHint(state), hint)
	}
	return errUnrecognizedCommand
}

// —— 接口视图 undo 分派（T2 改动点 #8，handled 模式，范式 lag_cmd.go:773）——

// applyUndoDHCPInterface 在接口视图统一拦截 DHCP 相关的 undo 子命令。
// 返回 (回显, 是否已处理)；未命中时 handled=false，交回既有 undo 分支。
//
// 覆盖分支（AC9）：
//
//	undo dhcp select [ global | interface | relay ]  清 dhcp-select + **级联清理** relay 键
//	undo dhcp relay server-ip [ <ip> ]               带参精确摘除并保序；无参清空全部
//	undo dhcp relay information enable               清 option82 键，回落 Disabled
//	undo dhcp relay information strategy             清 option82-strategy 键，回落 replace
//	undo dhcp relay source-ip                        清 source-ip 键
func applyUndoDHCPInterface(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 || !strings.EqualFold(args[0], "dhcp") {
		return "", false
	}
	iface := strings.TrimSpace(state.CurrentSub)
	if state.CurrentView != ViewInterface || iface == "" {
		return "", false
	}
	if !dhcpRelayDeviceSupported(state) {
		return errDHCPRelayNotSupported(string(state.DeviceType)), true
	}
	rest := args[1:]
	if len(rest) == 0 {
		return errUndoDHCPUsage, true
	}
	switch strings.ToLower(rest[0]) {
	case "select":
		// 清除模式键并**级联清理**全部中继键（拍板 #3：undo select 同样级联）。
		delete(state.DeviceConfig, dhcpSelectKey(iface))
		clearDHCPRelayKeys(state, iface)
		return "", true
	case "relay":
		return applyUndoDHCPRelay(state, iface, rest[1:]), true
	}
	return errUnrecognizedCommand, true
}

// applyUndoDHCPRelay 处理 `undo dhcp relay ...` 各分支。
func applyUndoDHCPRelay(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errUndoDHCPUsage
	}
	switch strings.ToLower(args[0]) {
	case "server-ip":
		return applyUndoDHCPRelayServerIP(state, iface, args[1:])
	case "source-ip":
		delete(state.DeviceConfig, dhcpRelayKey(iface, dhcpRelayFieldSourceIP))
		return ""
	case "information":
		if len(args) < 2 {
			return errDHCPRelayInformationUsage
		}
		switch strings.ToLower(args[1]) {
		case "enable":
			delete(state.DeviceConfig, dhcpRelayKey(iface, dhcpRelayFieldOption82))
			return ""
		case "strategy":
			delete(state.DeviceConfig, dhcpRelayKey(iface, dhcpRelayFieldStrategy))
			return ""
		}
		return errUnrecognizedCommand
	}
	return errUnrecognizedCommand
}

// applyUndoDHCPRelayServerIP 处理 `undo dhcp relay server-ip [ <ip> ]`（P1-6 / AC9 ①）。
//
//   - 无参 = **清空全部**（拍板 #6）；
//   - 带参 = 精确摘除该地址，**其余顺序不变**；地址不存在 → Error: ...does not exist.；
//   - 删至空列表时 **delete(map, key) 而非留空串键**（AC9 断言 _, ok := ...; ok == false）。
func applyUndoDHCPRelayServerIP(state *CLIState, iface string, args []string) string {
	key := dhcpRelayKey(iface, dhcpRelayFieldServerIPs)
	if len(args) == 0 {
		delete(state.DeviceConfig, key)
		return ""
	}
	target := strings.TrimSpace(args[0])
	existing := parseRelayServerIPs(state.DeviceConfig[key])
	remain := make([]string, 0, len(existing))
	found := false
	for _, cur := range existing {
		if cur == target {
			found = true
			continue
		}
		remain = append(remain, cur)
	}
	if !found {
		return errDHCPRelayServerIPNotExist
	}
	if len(remain) == 0 {
		delete(state.DeviceConfig, key)
		return ""
	}
	state.DeviceConfig[key] = joinRelayServerIPs(remain)
	return ""
}
