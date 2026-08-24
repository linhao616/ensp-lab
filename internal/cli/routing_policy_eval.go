// routing_policy_eval.go 是路由策略（route-policy / filter-policy）的纯函数 / 键 /
// 解析层（P0-2，路由策略补齐）。
//
// 设计对齐三件套约定（*_eval.go 纯函数/键/校验 + *_cmd.go 仅写 DeviceConfig +
// *_display.go 只读渲染），并遵守架构红线：
//   - 所有配置以 DeviceConfig 单一事实源存储，save/reload 自动往返（序列化整体拷贝
//     DeviceConfig 键集合，无需额外字段）。
//   - 键命名精确前缀，禁用 Contains 模糊扫描（键碰撞红线）；策略名/节点号均写入精确前缀。
//
// 键命名空间：
//   route-policy（routing:route-policy:<name>:node:<n>:...）：
//     ...:action        = permit|deny
//     ...:ifmatch:<type> = <value>   (type ∈ ip-prefix|acl|cost|interface|tag)
//     ...:apply:<type>   = <value>   (type ∈ cost|preference|tag|ip-next-hop|community|origin)
//   filter-policy（<proto>:filter-policy:<dir>:...，proto=协议域, dir=import|export）：
//     ...:kind  = acl|ip-prefix
//     ...:value = <acl-num|ip-prefix-name>
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// rpNS 是 route-policy 键的命名空间前缀（精确，禁 Contains）。
const rpNS = "routing:route-policy:"

// parseRoutePolicyHeader 解析 route-policy 进入命令：
//
//	route-policy <NAME> {permit|deny} [node] <N>
//
// 返回 (name, action, node, ok)。node 序号范围 0–65535（对齐 VRP）。
func parseRoutePolicyHeader(args []string) (name, action string, node int, ok bool) {
	if len(args) < 3 {
		return "", "", 0, false
	}
	name = args[0]
	switch strings.ToLower(args[1]) {
	case "permit", "deny":
		action = strings.ToLower(args[1])
	default:
		return "", "", 0, false
	}
	idx := 2
	if strings.ToLower(args[idx]) == "node" {
		idx++
	}
	if idx >= len(args) {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(args[idx])
	if err != nil || n < 0 || n > 65535 {
		return "", "", 0, false
	}
	return name, action, n, true
}

// rpNodeKey 返回 route-policy 节点的基础键前缀（精确，禁 Contains 模糊扫描）。
// 形如 routing:route-policy:<name>:node:<n>:
func rpNodeKey(name string, node int) string {
	return fmt.Sprintf("%s%s:node:%d:", rpNS, name, node)
}

// rpNodeKeys 返回某 route-policy 下所有 node 编号（升序、去重、确定性）。
func rpNodeKeys(state *CLIState, name string) []int {
	prefix := fmt.Sprintf("%s%s:node:", rpNS, name)
	seen := map[int]bool{}
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:colon])
		if err != nil {
			continue
		}
		seen[n] = true
	}
	nodes := make([]int, 0, len(seen))
	for n := range seen {
		nodes = append(nodes, n)
	}
	sort.Ints(nodes)
	return nodes
}

// rpPolicyNames 返回已配置的全部 route-policy 名称（升序、确定性）。
func rpPolicyNames(state *CLIState) []string {
	seen := map[string]bool{}
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, rpNS) {
			continue
		}
		// k = routing:route-policy:<name>:node:<n>:...
		rest := k[len(rpNS):]
		nodeIdx := strings.Index(rest, ":node:")
		if nodeIdx < 0 {
			continue
		}
		seen[rest[:nodeIdx]] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// filterPolicyKey 返回 filter-policy 存储键（按协议域 + 方向）。
// dir ∈ {import, export}；proto 为协议域前缀（ospf/rip/static/bgp/isis/direct）。
func filterPolicyKey(proto, dir string) (kindKey, valKey string) {
	base := fmt.Sprintf("%s:filter-policy:%s", proto, dir)
	return base + ":kind", base + ":value"
}

// isL3RoutingProtocol 校验 filter-policy 系统视图形式可接受的协议域。
func isL3RoutingProtocol(proto string) bool {
	switch proto {
	case "ospf", "rip", "static", "bgp", "isis", "direct":
		return true
	}
	return false
}
