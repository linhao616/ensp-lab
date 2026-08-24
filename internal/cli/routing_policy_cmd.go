// routing_policy_cmd.go 是路由策略（route-policy / filter-policy）的副作用出口层
// （P0-2，路由策略补齐）。每个 handler 仅写 DeviceConfig（精确前缀键），不读运行态、
// 不编造数字（诚实占位原则）；实际选路过滤由 lite 引擎在后端计算时消费这些配置。
package cli

import (
	"fmt"
	"strings"
)

// enterRoutePolicyView 处理系统视图下的 `route-policy <NAME> permit|deny [node] <N>`，
// 进入 route-policy 节点子视图 [<dev>-route-policy-<NAME>-<N>]。
func enterRoutePolicyView(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewSystem {
		return "Error: must be in system view"
	}
	name, action, node, ok := parseRoutePolicyHeader(cmd.Args)
	if !ok {
		return "Error: usage: route-policy <name> {permit|deny} [node] <node-number>"
	}
	state.RoutePolicyName = name
	state.RoutePolicyNode = node
	state.DeviceConfig[fmt.Sprintf("%saction", rpNodeKey(name, node))] = action
	state.CurrentView = ViewRoutePolicy
	state.CurrentSub = fmt.Sprintf("%s-%d", name, node)
	return fmt.Sprintf("Enter route-policy %s node %d (%s) view", name, node, action)
}

// execRoutePolicyIfMatch 处理 route-policy 节点视图下的 if-match 子句。
func execRoutePolicyIfMatch(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewRoutePolicy {
		return "Error: must be in route-policy node view"
	}
	if len(cmd.Args) < 2 {
		return "Error: usage: if-match <ip-prefix|acl|cost|interface|tag> <value>"
	}
	clause := strings.ToLower(cmd.Args[0])
	value := strings.Join(cmd.Args[1:], " ")
	switch clause {
	case "ip-prefix", "acl", "cost", "interface", "tag":
		key := fmt.Sprintf("%sifmatch:%s", rpNodeKey(state.RoutePolicyName, state.RoutePolicyNode), clause)
		state.DeviceConfig[key] = value
		return fmt.Sprintf("if-match %s %s", clause, value)
	default:
		return "Error: unsupported if-match clause (supported: ip-prefix, acl, cost, interface, tag)"
	}
}

// execRoutePolicyApply 处理 route-policy 节点视图下的 apply 子句。
func execRoutePolicyApply(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewRoutePolicy {
		return "Error: must be in route-policy node view"
	}
	if len(cmd.Args) < 2 {
		return "Error: usage: apply <cost|preference|tag|ip-next-hop|community|origin> <value>"
	}
	clause := strings.ToLower(cmd.Args[0])
	value := strings.Join(cmd.Args[1:], " ")
	switch clause {
	case "cost", "preference", "tag", "ip-next-hop", "community", "origin":
		key := fmt.Sprintf("%sapply:%s", rpNodeKey(state.RoutePolicyName, state.RoutePolicyNode), clause)
		state.DeviceConfig[key] = value
		return fmt.Sprintf("apply %s %s", clause, value)
	default:
		return "Error: unsupported apply clause (supported: cost, preference, tag, ip-next-hop, community, origin)"
	}
}

// execFilterPolicy 处理 filter-policy 命令，支持三种上下文：
//   - BGP 视图：filter-policy <acl <num>|ip-prefix <name>> <import|export>
//   - ISIS 视图：同上
//   - 系统视图：filter-policy <acl <num>|ip-prefix <name>> <protocol> <import|export>
//
// 系统视图形式用于 OSPF/RIP 等无独立子视图的协议（协议域限定）；
// BGP/ISIS 视图形式无需协议参数，由视图隐含协议域。
func execFilterPolicy(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewBGP && state.CurrentView != ViewISIS && state.CurrentView != ViewSystem {
		return "Error: must be in BGP/ISIS view or system view"
	}
	args := cmd.Args
	if len(args) < 2 {
		return "Error: usage: filter-policy <acl <num>|ip-prefix <name>> [<protocol>] <import|export>"
	}
	// 解析匹配项：acl <num> 或 ip-prefix <name>
	var kind, value string
	idx := 0
	if strings.ToLower(args[0]) == "ip-prefix" {
		if len(args) < 2 {
			return "Error: usage: filter-policy ip-prefix <name> <import|export>"
		}
		kind, value = "ip-prefix", args[1]
		idx = 2
	} else {
		kind, value = "acl", args[0]
		idx = 1
	}
	rest := args[idx:]

	// 确定协议域与方向
	var proto, dir string
	switch state.CurrentView {
	case ViewBGP:
		proto = "bgp"
	case ViewISIS:
		proto = "isis"
	case ViewSystem:
		// 剩余参数应为 <protocol> <import|export>
		if len(rest) < 2 {
			return "Error: usage: filter-policy <acl <num>> <protocol> <import|export>"
		}
		proto = strings.ToLower(rest[0])
		rest = rest[1:]
	}
	if len(rest) < 1 {
		return "Error: missing import/export"
	}
	dir = strings.ToLower(rest[0])
	if dir != "import" && dir != "export" {
		return "Error: usage: filter-policy ... <import|export>"
	}
	if !isL3RoutingProtocol(proto) {
		return "Error: unsupported protocol (supported: ospf, rip, static, bgp, isis, direct)"
	}

	kindKey, valKey := filterPolicyKey(proto, dir)
	state.DeviceConfig[kindKey] = kind
	state.DeviceConfig[valKey] = value
	return fmt.Sprintf("filter-policy %s %s applied to %s %s", kind, value, proto, dir)
}

// undoRoutePolicy 反向清理某 route-policy 的全部节点键（系统视图 undo route-policy <name>）。
func undoRoutePolicy(state *CLIState, args []string) (string, bool) {
	if len(args) < 2 {
		return "Error: usage: undo route-policy <name>", true
	}
	name := args[1]
	prefix := fmt.Sprintf("%s%s:node:", rpNS, name)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			delete(state.DeviceConfig, k)
		}
	}
	return fmt.Sprintf("Route-policy %s removed", name), true
}
