// completion.go 实现 CLI Tab 补全的「单一事实源」：候选完全由后端计算，
// 前端不持有第二份命令字典，杜绝与 parser 漂移（设计 §3 / AC1~AC4）。
//
// 设计要点：
//   - display 子命令候选直接来自 displayRegistry（新增 display 命令自动进入补全）。
//   - 配置视图关键字表（userViewCommands / systemViewCommands / ...）列出该视图下
//     合法下一 token；每个 token 必须是 parser.go 顶层（或视图子）switch 的 case 首别名，
//     由 TestCompletionNoDrift 锁死（静默过时即测试失败）。
//   - 接口名候选来自 state.Interfaces（真实接口列表）。
//   - Complete 只读、零副作用：不执行命令、不改 CLIState（AC4）。
package cli

import (
	"sort"
	"strings"
)

// SplitCommandTokens 按空白切分输入为 token 序列，但保留结尾的空 token，
// 以便补全器区分「dis 」(列出全部子命令) 与「dis」(无空格，最后一个 token 即 dis 自身)。
func SplitCommandTokens(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{""}
	}
	parts := strings.Fields(s)
	if strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\t") {
		parts = append(parts, "")
	}
	return parts
}

// Complete 返回当前输入在给定视图/状态（state.CurrentView）下的补全候选（已排序、去重）。
// tokens = SplitCommandTokens(input) 的结果；最后一段为待补全前缀，其余为上下文。
// 只读、零副作用（不执行命令、不改 CLIState）。
func Complete(state *CLIState, tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	last := tokens[len(tokens)-1]
	prefix := strings.ToLower(last)

	first := strings.ToLower(tokens[0])
	if first == "dis" || first == "display" {
		return completeDisplay(state, tokens, prefix)
	}
	return completeView(state, tokens, prefix)
}

// completeDisplay 处理 dis/display 上下文的补全。
func completeDisplay(state *CLIState, tokens []string, prefix string) []string {
	// 子命令 token：有二级 token 时为 tokens[1]，否则 tokens[0] 本身就是部分子命令
	hasSub := len(tokens) >= 2
	subToken := tokens[0]
	if hasSub {
		subToken = tokens[1]
	}
	normSub := normalizeDisplaySubCmd(strings.ToLower(subToken))

	// dis interface <name> / dis int <name> -> 接口名补全
	if hasSub && normSub == "interface" && len(tokens) >= 3 {
		return completeInterfaceNames(state, prefix)
	}
	// dis ip interface <name> -> 接口名补全
	if hasSub && normSub == "ip" && len(tokens) >= 3 {
		normThird := normalizeDisplaySubCmd2("ip", strings.ToLower(tokens[2]))
		if normThird == "interface" && len(tokens) >= 4 {
			return completeInterfaceNames(state, prefix)
		}
	}

	// 否则补全 display 子命令 key
	keyPrefix := prefix
	if !hasSub {
		// tokens[0] 即 "dis"/"display" 本身（无空格）：按该 token 前缀过滤；
		// 但若就敲了 dis/display 则列出全部。
		keyPrefix = strings.ToLower(tokens[0])
		if keyPrefix == "dis" || keyPrefix == "display" {
			keyPrefix = ""
		}
	}
	return filterDisplayKeys(keyPrefix)
}

// completeView 处理配置视图（user/system/interface/...）关键字补全。
func completeView(state *CLIState, tokens []string, prefix string) []string {
	// 接口名补全：上一 token 为 interface/int（如 system 视图 `interface Gi`）
	if len(tokens) >= 2 {
		prev := strings.ToLower(tokens[len(tokens)-2])
		if prev == "interface" || prev == "int" {
			return completeInterfaceNames(state, prefix)
		}
	}

	var kw []string
	switch state.CurrentView {
	case ViewUser:
		kw = userViewCommands
	case ViewSystem:
		kw = systemViewCommands
	case ViewInterface:
		kw = interfaceViewCommands
	case ViewAAA:
		kw = aaaViewCommands
	case ViewBGP:
		kw = bgpViewCommands
	case ViewACL:
		kw = aclViewCommands
	case ViewVTY:
		kw = vtyViewCommands
	case ViewDHCPPool:
		kw = dhcpPoolViewCommands
	case ViewISIS:
		kw = isisViewCommands
	case ViewMSTRegion:
		kw = mstRegionViewCommands
	case ViewMLAG:
		kw = mlagViewCommands
	default:
		kw = userViewCommands
	}
	return filterPrefix(kw, prefix)
}

// filterDisplayKeys 返回以 prefix 为前缀的 display 注册表 key（排序）。
func filterDisplayKeys(prefix string) []string {
	out := make([]string, 0, len(displayRegistry))
	for k := range displayRegistry {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// completeInterfaceNames 返回以 prefix 为前缀的真实接口名（排序）。
func completeInterfaceNames(state *CLIState, prefix string) []string {
	out := make([]string, 0, len(state.Interfaces))
	for name := range state.Interfaces {
		if prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// filterPrefix 返回以 prefix 为前缀的关键字（排序）。
func filterPrefix(keywords []string, prefix string) []string {
	out := make([]string, 0, len(keywords))
	for _, k := range keywords {
		if prefix == "" || strings.HasPrefix(strings.ToLower(k), prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// —— 配置视图关键字表（每个 token 必须是 parser.go 中某个 case 的首别名；TestCompletionNoDrift 锁死） ——

var userViewCommands = []string{
	"display", "system-view", "user-interface", "interface", "ping",
	"tracert", "quit", "return", "save", "reboot", "reset", "undo",
}

var systemViewCommands = []string{
	"display", "interface", "ip", "undo", "sysname", "vlan", "ospf",
	"stp", "aaa", "bgp", "rip", "ipv6", "dhcp", "ntp", "snmp", "syslog",
	"lldp", "qos", "radius", "dot1x", "bfd", "isis", "m-lag",
	"link-aggregation", "mac-address", "stelnet", "user-interface",
	// 乙主线 100-command 审计新增的 system 视图命令（须与 parser.go case 首别名一致，
	// 受 TestCompletionNoDrift 锁死）。port-isolate/igmp-snooping/loopback-detection/
	// port-group/group-member/info-center 在 parser.go 无对应 case（执行会落到默认错误
	// 分支），属漂移，已移除以免 Tab 补全暗示不存在的命令（诚实占位原则）。
	"startup",
	"quit", "return", "save", "reboot", "reset",
}

var interfaceViewCommands = []string{
	"ip", "ipv6", "shutdown", "undo", "description", "quit", "return",
	// v0.12 链路质量模拟（仿真扩展命令，对应 parser.go case "delay"/"loss"）。
	"delay", "loss",
}

var aaaViewCommands = []string{
	"authentication-scheme", "authorization-scheme", "accounting-scheme",
	"domain", "local-user", "quit", "return",
}

var bgpViewCommands = []string{
	"peer", "import-route", "undo", "quit", "return",
}

var aclViewCommands = []string{
	"rule", "undo", "quit", "return",
}

var vtyViewCommands = []string{
	"authentication-mode", "undo", "quit", "return",
}

var dhcpPoolViewCommands = []string{
	"network", "gateway-list", "dns-list", "lease", "undo", "quit", "return",
}

var isisViewCommands = []string{
	"import-route", "undo", "quit", "return",
}

var mstRegionViewCommands = []string{
	"instance", "active", "undo", "quit", "return",
}

var mlagViewCommands = []string{
	"undo", "quit", "return",
}
