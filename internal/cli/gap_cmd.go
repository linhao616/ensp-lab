// gap_cmd.go —— 网闸命令执行器层（设计三件套之二）
//
// 视图层级（VRP 风格）：
//   [dev]                         系统视图
//     gap                           → [dev-gap]
//       channel <1-64>              → [dev-gap-channel-<n>]
//         mapping tcp <src-ip> <src-port> <-> <dst-ip> <dst-port>
//         enable
//         quit                      → [dev-gap]
//       policy <1-64>               → [dev-gap-policy-<n>]
//         permit source <src> dest <dst>
//         enable
//         quit                      → [dev-gap]
//       quit                        → [dev]
//
// 只写 state.DeviceConfig（精确前缀 gap:channel: / gap:policy:，禁 Contains 匹配）。
package cli

import (
	"fmt"
	"regexp"
	"strings"

	"ensp-lab/internal/topology"
)

// gapChannelNumRe / gapPolicyNumRe 校验通道/策略编号（1-64）。
var gapNumRe = regexp.MustCompile(`^([1-9]|[1-5][0-9]|6[0-4])$`)

// isGAPDevice 判断当前设备是否为网闸。
func isGAPDevice(state *CLIState) bool {
	return state.DeviceType == topology.DeviceGAP
}

// execGAPSystem 处理系统视图下的 `gap` 命令：进入网闸配置视图。
// 返回 (是否消费, 输出)。仅网闸设备消费。
func execGAPSystem(state *CLIState, cmd *Command) (bool, string) {
	if !isGAPDevice(state) {
		return false, ""
	}
	if cmd.Command != "gap" {
		return false, ""
	}
	state.CurrentView = ViewGAP
	state.CurrentSub = ""
	return true, fmt.Sprintf("Info: Enter GAP configuration view.\n[%s-gap]", state.DeviceName)
}

// execGAPView 处理 [dev-gap] 视图命令。
func execGAPView(state *CLIState, cmd *Command) string {
	switch cmd.Command {
	case "channel":
		return enterGAPChannel(state, cmd)
	case "policy":
		return enterGAPPolicy(state, cmd)
	case "quit":
		state.CurrentView = ViewSystem
		state.CurrentSub = ""
		return fmt.Sprintf("[%s]", state.DeviceName)
	case "return":
		state.CurrentView = ViewUser
		state.CurrentSub = ""
		return "<" + state.DeviceName + ">"
	case "display":
		// display gap ...（同一命令体系，走到 display 通用分发；此处给友好提示）
		return "Info: Use `display gap channel|policy|statistics` to view GAP status."
	default:
		return fmt.Sprintf("Error: Unrecognized command at '^' position.")
	}
}

// enterGAPChannel 进入通道视图（[dev-gap-channel-<n>]）。Args 不含命令本身。
func enterGAPChannel(state *CLIState, cmd *Command) string {
	if len(cmd.Args) < 1 || !gapNumRe.MatchString(cmd.Args[0]) {
		return "Error: Incomplete command found at '^' position.\n  Usage: channel <1-64>"
	}
	n := cmd.Args[0]
	state.CurrentView = ViewGAPChannel
	state.CurrentSub = n
	return fmt.Sprintf("[%s-gap-channel-%s]", state.DeviceName, n)
}

// enterGAPPolicy 进入策略视图（[dev-gap-policy-<n>]）。Args 不含命令本身。
func enterGAPPolicy(state *CLIState, cmd *Command) string {
	if len(cmd.Args) < 1 || !gapNumRe.MatchString(cmd.Args[0]) {
		return "Error: Incomplete command found at '^' position.\n  Usage: policy <1-64>"
	}
	n := cmd.Args[0]
	state.CurrentView = ViewGAPPolicy
	state.CurrentSub = n
	return fmt.Sprintf("[%s-gap-policy-%s]", state.DeviceName, n)
}

// execGAPToggle 通道/策略视图共用的 enable/disable 开关（按当前视图写对应键）。
func execGAPToggle(state *CLIState, cmd *Command) string {
	n := state.CurrentSub
	switch state.CurrentView {
	case ViewGAPChannel:
		if cmd.Command == "enable" {
			state.DeviceConfig["gap:channel:"+n+":enable"] = "true"
			return "Info: Channel enabled. Data ferry traffic will be forwarded once mapping is valid."
		}
		state.DeviceConfig["gap:channel:"+n+":enable"] = "false"
		return "Info: Channel disabled."
	case ViewGAPPolicy:
		if cmd.Command == "enable" {
			state.DeviceConfig["gap:policy:"+n+":enable"] = "true"
			return "Info: Policy enabled."
		}
		state.DeviceConfig["gap:policy:"+n+":enable"] = "false"
		return "Info: Policy disabled."
	default:
		return "Error: Unrecognized command found at '^' position."
	}
}

// execGAPChannelView 处理通道视图命令（mapping / enable / quit）。
func execGAPChannelView(state *CLIState, cmd *Command) string {
	n := state.CurrentSub
	switch cmd.Command {
	case "mapping":
		return gapChannelMapping(state, n, cmd)
	case "quit":
		state.CurrentView = ViewGAP
		state.CurrentSub = ""
		return fmt.Sprintf("[%s-gap]", state.DeviceName)
	case "return":
		state.CurrentView = ViewUser
		state.CurrentSub = ""
		return "<" + state.DeviceName + ">"
	default:
		return fmt.Sprintf("Error: Unrecognized command at '^' position.")
	}
}

// gapChannelMapping 解析 `mapping tcp <src-ip> <src-port> <-> <dst-ip> <dst-port>`。
// Args = [tcp, src-ip, src-port, <->, dst-ip, dst-port]（不含命令本身）。
func gapChannelMapping(state *CLIState, n string, cmd *Command) string {
	a := cmd.Args
	if len(a) < 6 || a[0] != "tcp" || a[3] != "<->" {
		return "Error: Invalid mapping. Usage: mapping tcp <src-ip> <src-port> <-> <dst-ip> <dst-port>"
	}
	srcIP, srcPort, dstIP, dstPort := a[1], a[2], a[4], a[5]
	if !validIPv4(srcIP) || !validIPv4(dstIP) {
		return "Error: Invalid IP address in mapping."
	}
	state.DeviceConfig["gap:channel:"+n+":mapping"] =
		fmt.Sprintf("tcp %s:%s <-> %s:%s", srcIP, srcPort, dstIP, dstPort)
	return fmt.Sprintf("Info: Mapping configured: tcp %s:%s <-> %s:%s", srcIP, srcPort, dstIP, dstPort)
}

// execGAPPolicyView 处理策略视图命令（permit / enable / quit）。
func execGAPPolicyView(state *CLIState, cmd *Command) string {
	n := state.CurrentSub
	switch cmd.Command {
	case "permit":
		return gapPolicyPermit(state, n, cmd)
	case "quit":
		state.CurrentView = ViewGAP
		state.CurrentSub = ""
		return fmt.Sprintf("[%s-gap]", state.DeviceName)
	case "return":
		state.CurrentView = ViewUser
		state.CurrentSub = ""
		return "<" + state.DeviceName + ">"
	default:
		return fmt.Sprintf("Error: Unrecognized command at '^' position.")
	}
}

// gapPolicyPermit 解析 `permit source <src> dest <dst>`。
// Args = [source, src, dest, dst]（不含命令本身）。
func gapPolicyPermit(state *CLIState, n string, cmd *Command) string {
	a := cmd.Args
	if len(a) < 4 || a[0] != "source" || a[2] != "dest" {
		return "Error: Invalid policy. Usage: permit source <src-network> dest <dst-network>"
	}
	src, dst := a[1], a[3]
	state.DeviceConfig["gap:policy:"+n+":rule"] = fmt.Sprintf("permit %s -> %s", src, dst)
	return fmt.Sprintf("Info: Policy configured: permit %s -> %s", src, dst)
}

// validIPv4 简单 IPv4 校验（点分十进制 4 段）。
func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
