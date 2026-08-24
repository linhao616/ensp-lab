// routing_policy_display.go 是路由策略（route-policy / filter-policy）的只读渲染层
// （P0-2，路由策略补齐）。全部 handler 仅从 DeviceConfig 即时派生，无副作用、不编造数字。
package cli

import (
	"fmt"
	"strings"
)

// rpIfMatchClauses / rpApplyClauses 是 if-match / apply 子句的渲染顺序（确定性）。
var (
	rpIfMatchClauses = []string{"ip-prefix", "acl", "cost", "interface", "tag"}
	rpApplyClauses   = []string{"cost", "preference", "tag", "ip-next-hop", "community", "origin"}
)

// rpFilterProtos 是 filter-policy 渲染时遍历的协议域顺序（确定性）。
var rpFilterProtos = []string{"ospf", "rip", "static", "bgp", "isis", "direct"}

// regRoutePolicyDisplay 渲染 display route-policy [<name>]。
// 无参列出全部策略名；带名列出该策略的节点、if-match / apply 子句。
func regRoutePolicyDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	name := ""
	if len(cmd.Args) >= 2 {
		name = cmd.Args[1]
	}
	var b strings.Builder
	if name == "" {
		names := rpPolicyNames(state)
		if len(names) == 0 {
			b.WriteString("Route-policy: not configured\n")
			return b.String()
		}
		b.WriteString("Route-policy Name\n")
		for _, n := range names {
			b.WriteString(fmt.Sprintf("  %s\n", n))
		}
		return b.String()
	}
	nodes := rpNodeKeys(state, name)
	if len(nodes) == 0 {
		b.WriteString(fmt.Sprintf("Route-policy %s: not configured\n", name))
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Route-policy: %s\n", name))
	for _, node := range nodes {
		action := state.DeviceConfig[fmt.Sprintf("%saction", rpNodeKey(name, node))]
		b.WriteString(fmt.Sprintf("  node %d (%s)\n", node, action))
		for _, c := range rpIfMatchClauses {
			if v, ok := state.DeviceConfig[fmt.Sprintf("%sifmatch:%s", rpNodeKey(name, node), c)]; ok && v != "" {
				b.WriteString(fmt.Sprintf("    if-match %s %s\n", c, v))
			}
		}
		for _, c := range rpApplyClauses {
			if v, ok := state.DeviceConfig[fmt.Sprintf("%sapply:%s", rpNodeKey(name, node), c)]; ok && v != "" {
				b.WriteString(fmt.Sprintf("    apply %s %s\n", c, v))
			}
		}
	}
	return b.String()
}

// regFilterPolicyDisplay 渲染 display filter-policy [<protocol>]。
// 无参列出全部协议域的 filter-policy；带协议域仅列该协议。
func regFilterPolicyDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	want := ""
	if len(cmd.Args) >= 2 {
		want = strings.ToLower(cmd.Args[1])
	}
	var b strings.Builder
	found := false
	for _, proto := range rpFilterProtos {
		if want != "" && want != proto {
			continue
		}
		for _, dir := range []string{"import", "export"} {
			kindKey, valKey := filterPolicyKey(proto, dir)
			kind, kok := state.DeviceConfig[kindKey]
			val, vok := state.DeviceConfig[valKey]
			if !(kok && vok && val != "") {
				continue
			}
			found = true
			b.WriteString(fmt.Sprintf("filter-policy %s %s (%s:%s)\n", kind, val, proto, dir))
		}
	}
	if !found {
		b.WriteString("Filter-policy: not configured\n")
	}
	return b.String()
}

// buildRoutePolicySavedConfig 生成 route-policy 配置块（用于 display current-configuration）。
// 仅渲染配置态；lite 引擎不消费做实际选路过滤，故末尾诚实注明（对齐 EVPN 等占位纪律）。
func buildRoutePolicySavedConfig(state *CLIState) string {
	names := rpPolicyNames(state)
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range names {
		for _, node := range rpNodeKeys(state, n) {
			action := state.DeviceConfig[fmt.Sprintf("%saction", rpNodeKey(n, node))]
			b.WriteString(fmt.Sprintf(" route-policy %s %s node %d\n", n, action, node))
			for _, c := range rpIfMatchClauses {
				if v, ok := state.DeviceConfig[fmt.Sprintf("%sifmatch:%s", rpNodeKey(n, node), c)]; ok && v != "" {
					b.WriteString(fmt.Sprintf("  if-match %s %s\n", c, v))
				}
			}
			for _, c := range rpApplyClauses {
				if v, ok := state.DeviceConfig[fmt.Sprintf("%sapply:%s", rpNodeKey(n, node), c)]; ok && v != "" {
					b.WriteString(fmt.Sprintf("  apply %s %s\n", c, v))
				}
			}
		}
	}
	return b.String()
}
