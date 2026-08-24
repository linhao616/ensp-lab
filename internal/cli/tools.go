// tools.go 集中所有「不带协议语义」的纯函数工具：
//   - 数值/IP/掩码/子网换算  (parseNum / parseIP / ipInNetwork / subnet*...)
//   - 字符串归一化          (normalizeDisplaySubCmd*)
//   - 关键词判定            (isKeyword / isRuleKeyword / isPortNumber)
//   - 哈希/取模             (hashString / getIPSuffix)
//   - 路由表生成与格式化    (buildDirectRoutes / formatRoutingTable)
//
// 这些函数不依赖任何外部状态，单元测试可以独立跑。
package cli

import (
	"fmt"
	"strings"
)

func getIPSuffix(name string) int {
	if name == "" {
		return 0
	}
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] >= '0' && name[i] <= '9' {
			return int(name[i] - '0')
		}
	}
	return hashString(name) % 50
}

func hashString(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return h & 0x7fffffff
}

func parseNum(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// displaySubCommands 是 display 子命令的合法全集（华为 VRP 前缀匹配的匹配空间）：
//   - parser.go display 分发 switch 的全部 case（排除 ambiguous/incomplete 两个错误分支）
//   - display_registry.go 注册表键中 switch 未覆盖的（link-quality / port / mlag）
//
// 来源防漂移：TestDisplaySubCommandsNoDrift 断言本表 ⊇ displayRegistry 键全集；
// 新增 display 子命令时必须同步追加（与 displayRegistry 双入口，补全/执行/缩写三处一致）。
var displaySubCommands = []string{
	"aaa", "acl", "arp", "bfd", "bgp", "clock", "cpu-usage", "current-configuration",
	"description", "device", "dhcp", "diagnostic-information", "domain", "dot1x", "duplex",
	"eth-trunk", "evpn", "gap", "gre", "history-command", "interface", "ip", "ipsec", "ipv6", "isis",
	"link-aggregation", "link-quality", "lldp", "local-user", "mac-address", "memory",
	"m-lag", "mlag", "mtu", "nat", "ndp", "netflow", "ntp", "ospf", "ospfv3", "pbr", "port",
	"port-security", "port-vlan", "qos", "radius", "ripng", "routing-table",
	"saved-configuration", "snmp", "speed", "ssh", "startup", "status", "stp", "syslog",
	"sysname", "temperature", "this", "users", "version", "vlan", "vrf", "vrrp", "vxlan",
}

// normalizeDisplaySubCmd 将 display 子命令的缩写映射为完整形式
func normalizeDisplaySubCmd(cmd string) string {
	aliases := map[string]string{
		"cu":           "current-configuration",
		"curr":         "current-configuration",
		"int":          "interface",
		"inter":        "interface",
		"ip-in":        "ip interface",
		"ip-int":       "ip interface",
		"ip inter":     "ip interface",
		"ip interface": "ip interface",
		"rt":           "routing-table",
		"route":        "routing-table",
		"ip rt":        "ip routing-table",
		"ip route":     "ip routing-table",
		"mac":          "mac-address",
		"mac-add":      "mac-address",
		"mac addr":     "mac-address",
		"st":           "stp",
		"vr":           "vrrp",
		"bf":           "bfd",
		"acl":          "acl",
		"ver":          "version",
		"mem":          "memory",
		"cpu":          "cpu-usage",
		"cpu-us":       "cpu-usage",
		"vlan":         "vlan",
		"vl":           "vlan",
		"arp":          "arp",
		"nat":          "nat",
		"lldp":         "lldp",
		"ospf":         "ospf",
		"bgp":          "bgp",
		"snmp":         "snmp",
		"sys":          "sys",
		"sysname":      "sysname",
		"eth-trunk":    "eth-trunk",
		"et":           "eth-trunk",
		"eth":          "eth-trunk",
		"port":         "port",
		"port-vlan":    "port-vlan",
		"port vlan":    "port-vlan",
	}
	if full, ok := aliases[cmd]; ok {
		return full
	}
	// v0.12.1：白名单未命中时不静默展开（华为 VRP 对 display 子命令仅放行官方
	// 允许的缩写，如 dis int / dis cu；其余缩写一律报错，绝不自动补全执行——
	// dis aa 不会被执行成 display aaa，而是落 Unrecognized/unknown 提示指向完整命令）。
	// 多前缀（歧义，如 dis i / dis b）返回 "ambiguous"，由 parser 输出 VRP 风格
	// Ambiguous command found at '^' position.；唯一前缀/零命中均原样返回落 unknown，
	// 由 parser 兜底报错（报错回显完整命令 `dis aa`，不再只显示首 token `dis`）。
	if _, matched, errKind := resolveKeyword(cmd, displaySubCommands); !matched && errKind == "ambiguous" {
		return "ambiguous"
	}
	return cmd
}

// normalizeDisplaySubCmd2 处理二级子命令的缩写（如 display ip int brief 中的 int 和 brief）
func normalizeDisplaySubCmd2(parent, cmd string) string {
	if cmd == "" {
		return ""
	}
	if parent == "ip" {
		aliases := map[string]string{
			"int":   "interface",
			"inter": "interface",
			"rt":    "routing-table",
			"route": "routing-table",
		}
		if full, ok := aliases[cmd]; ok {
			return full
		}
	}
	if parent == "interface" {
		if cmd == "b" || cmd == "br" || cmd == "bri" || cmd == "brief" {
			return "brief"
		}
	}
	return cmd
}

// resolveKeyword 按华为 VRP 缩写规则解析一个输入 token 到关键字集合：
//   - token 等于某关键字        -> 命中该关键字
//   - token 是唯一关键字的前缀  -> 命中该关键字（合法缩写）
//   - token 是多个关键字的前缀  -> 歧义（ambiguous）
//   - token 不是任何关键字的前缀 -> 非法参数（wrong）
//   - token 为空                -> 缺失参数（incomplete）
//
// 返回值：resolved 为解析后的完整关键字（仅 matched=true 时有效），
// errKind 为 incomplete|wrong|ambiguous 之一（仅 matched=false 时有效）。
func resolveKeyword(token string, keywords []string) (resolved string, matched bool, errKind string) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return "", false, "incomplete"
	}
	for _, k := range keywords {
		if strings.ToLower(k) == token {
			return k, true, ""
		}
	}
	var matches []string
	for _, k := range keywords {
		if strings.HasPrefix(strings.ToLower(k), token) {
			matches = append(matches, k)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, ""
	}
	if len(matches) == 0 {
		return "", false, "wrong"
	}
	return "", false, "ambiguous"
}

func subnetFromCIDR(cidr string) string {
	n, err := parseNum(cidr)
	if err != nil {
		return "255.255.255.0"
	}
	return prefixToSubnet(n)
}

func cidrFromSubnet(subnet string) string {
	switch subnet {
	case "255.0.0.0":
		return "8"
	case "255.128.0.0":
		return "9"
	case "255.192.0.0":
		return "10"
	case "255.224.0.0":
		return "11"
	case "255.240.0.0":
		return "12"
	case "255.248.0.0":
		return "13"
	case "255.252.0.0":
		return "14"
	case "255.254.0.0":
		return "15"
	case "255.255.0.0":
		return "16"
	case "255.255.128.0":
		return "17"
	case "255.255.192.0":
		return "18"
	case "255.255.224.0":
		return "19"
	case "255.255.240.0":
		return "20"
	case "255.255.248.0":
		return "21"
	case "255.255.252.0":
		return "22"
	case "255.255.254.0":
		return "23"
	case "255.255.255.0":
		return "24"
	case "255.255.255.128":
		return "25"
	case "255.255.255.192":
		return "26"
	case "255.255.255.224":
		return "27"
	case "255.255.255.240":
		return "28"
	case "255.255.255.248":
		return "29"
	case "255.255.255.252":
		return "30"
	case "255.255.255.254":
		return "31"
	case "255.255.255.255":
		return "32"
	default:
		return "24"
	}
}

// prefixToSubnet 把前缀长度（0–32）转换为点分十进制子网掩码。
// 此前为有类近似实现（仅 /8 /16 /24 /32 四档），导致 /30 误算成 255.255.255.0；
// 现改为按位精确计算，任意前缀长度均正确（/30 → 255.255.255.252）。
func prefixToSubnet(prefix int) string {
	if prefix <= 0 {
		return "0.0.0.0"
	}
	if prefix > 32 {
		prefix = 32
	}
	mask := uint32(0xFFFFFFFF) << (32 - prefix)
	a := byte(mask >> 24)
	b := byte((mask >> 16) & 0xFF)
	c := byte((mask >> 8) & 0xFF)
	d := byte(mask & 0xFF)
	return fmt.Sprintf("%d.%d.%d.%d", a, b, c, d)
}

// splitInterfaceIPConfig 把 `interface:<if>:ip` 配置值拆成 (IP, Mask)。
// 该键以空格形态存储（"IP MASK"，见 parser.go 的 `ip address` 写入路径），
// 同时为兼容历史 CIDR 形态（"IP/MASK"）也一并处理。display 渲染时 IP 与 Mask
// 必须分别填充，否则会把整串当作 IP 后又补 "/Mask" 造成掩码重复渲染
// （既有技术债：物理口 display interface 详情的 Internet Address 行重复输出掩码）。
func splitInterfaceIPConfig(v string) (ip, mask string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ""
	}
	if idx := strings.Index(v, "/"); idx > 0 {
		return v[:idx], v[idx+1:]
	}
	fields := strings.Fields(v)
	if len(fields) >= 2 {
		return fields[0], fields[1]
	}
	return fields[0], ""
}

func subnetToPrefix(subnet string) int {
	switch subnet {
	case "255.255.255.255":
		return 32
	case "255.255.255.0":
		return 24
	case "255.255.0.0":
		return 16
	case "255.0.0.0":
		return 8
	default:
		return 24
	}
}

func ipToCIDR(ip, subnet string) string {
	if ip == "" {
		return ""
	}
	prefix := subnetToPrefix(subnet)
	return fmt.Sprintf("%s/%d", ip, prefix)
}

// IPToCIDR 是 ipToCIDR 的导出版本，供 REST API 等非 CLI 上下文复用。
func IPToCIDR(ip, subnet string) string {
	return ipToCIDR(ip, subnet)
}

func isKeyword(s string) bool {
	kw := []string{"source", "src", "destination", "dest"}
	for _, k := range kw {
		if strings.ToLower(s) == k {
			return true
		}
	}
	return false
}

func isRuleKeyword(s string) bool {
	kw := []string{"source", "src", "destination", "dest", "destination-port", "source-port", "eq", "neq", "gt", "lt", "range", "permit", "deny", "tcp", "udp", "icmp", "ip", "gre", "ospf", "ah", "esp", "pim", "igmp", "ipsec"}
	for _, k := range kw {
		if strings.ToLower(s) == k {
			return true
		}
	}
	return false
}

func isPortNumber(s string) bool {
	// 检查是否是端口号或知名端口名
	knownPorts := map[string]bool{
		"http": true, "https": true, "ssh": true, "telnet": true, "ftp": true,
		"dns": true, "smtp": true, "pop3": true, "imap": true, "snmp": true,
		"ldap": true, "tftp": true, "80": true,
		"443": true, "22": true, "23": true, "21": true, "20": true,
		"53": true, "25": true, "110": true, "143": true, "161": true,
		"389": true, "67": true, "68": true, "69": true,
	}
	if knownPorts[s] {
		return true
	}
	// 检查是否是数字
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseIP(ipStr string) uint32 {
	var a, b, c, d byte
	fmt.Sscanf(ipStr, "%d.%d.%d.%d", &a, &b, &c, &d)
	return uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d)
}

// ipInNetwork 检查目标IP是否在指定网络内
func ipInNetwork(targetIP, networkIP uint32, maskPrefix int) bool {
	if maskPrefix <= 0 {
		return true
	}
	if maskPrefix > 32 {
		maskPrefix = 32
	}
	mask := ^uint32(0) << (32 - maskPrefix)
	return (targetIP & mask) == (networkIP & mask)
}

// getNetworkAddress 根据 IP 和掩码长度计算网络地址
func getNetworkAddress(ipStr string, maskLen int) string {
	ip := parseIP(ipStr)
	if maskLen <= 0 {
		return ipStr
	}
	if maskLen > 32 {
		maskLen = 32
	}
	mask := ^uint32(0) << (32 - maskLen)
	networkIP := ip & mask
	a := byte(networkIP >> 24)
	b := byte((networkIP >> 16) & 0xFF)
	c := byte((networkIP >> 8) & 0xFF)
	d := byte(networkIP & 0xFF)
	return fmt.Sprintf("%d.%d.%d.%d", a, b, c, d)
}

// buildDirectRoutes 根据接口配置自动生成直连路由
func buildDirectRoutes(state *CLIState) []*RouteEntry {
	var routes []*RouteEntry

	for ifaceName, iface := range state.Interfaces {
		if iface.IP == "" {
			continue
		}

		ipAddr, subnet := parseIPFormat(iface.IP)
		if ipAddr == "" {
			continue
		}

		maskLen := subnetToPrefix(subnet)

		networkAddr := getNetworkAddress(ipAddr, maskLen)

		routes = append(routes, &RouteEntry{
			Destination: networkAddr,
			Mask:        subnet,
			MaskLength:  maskLen,
			Protocol:    "direct",
			Pre:         0,
			Cost:        0,
			Flags:       "D",
			NextHop:     ipAddr,
			Interface:   ifaceName,
		})

		routes = append(routes, &RouteEntry{
			Destination: ipAddr,
			Mask:        "255.255.255.255",
			MaskLength:  32,
			Protocol:    "direct",
			Pre:         0,
			Cost:        0,
			Flags:       "D",
			NextHop:     "127.0.0.1",
			Interface:   ifaceName,
		})
	}

	routes = append(routes, &RouteEntry{
		Destination: "127.0.0.0",
		Mask:        "255.0.0.0",
		MaskLength:  8,
		Protocol:    "direct",
		Pre:         0,
		Cost:        0,
		Flags:       "D",
		NextHop:     "127.0.0.1",
		Interface:   "InLoopBack0",
	})
	routes = append(routes, &RouteEntry{
		Destination: "127.0.0.1",
		Mask:        "255.255.255.255",
		MaskLength:  32,
		Protocol:    "direct",
		Pre:         0,
		Cost:        0,
		Flags:       "D",
		NextHop:     "127.0.0.1",
		Interface:   "InLoopBack0",
	})

	return routes
}

// formatRoutingTable 生成华为格式的路由表输出
func formatRoutingTable(state *CLIState, targetIP string, verbose bool) string {
	directRoutes := buildDirectRoutes(state)

	allRoutes := append(directRoutes, state.Routes...)

	for i, r := range allRoutes {
		if r.MaskLength == 0 && r.Mask != "" {
			allRoutes[i].MaskLength = subnetToPrefix(r.Mask)
		}
		if r.Pre == 0 && r.Protocol == "static" {
			allRoutes[i].Pre = 60
		}
		if r.Flags == "" && r.Protocol == "direct" {
			allRoutes[i].Flags = "D"
		}
		if r.Flags == "" && r.Protocol == "static" {
			allRoutes[i].Flags = "RD"
		}
	}

	var filteredRoutes []*RouteEntry
	if targetIP != "" {
		targetIPParsed := parseIP(targetIP)
		for _, r := range allRoutes {
			destIPParsed := parseIP(r.Destination)
			maskPrefix := r.MaskLength
			if maskPrefix == 0 && r.Mask != "" {
				maskPrefix = subnetToPrefix(r.Mask)
			}
			if maskPrefix > 32 {
				maskPrefix = 32
			}
			if ipInNetwork(targetIPParsed, destIPParsed, maskPrefix) {
				filteredRoutes = append(filteredRoutes, r)
			}
		}
	} else {
		filteredRoutes = allRoutes
	}

	destinations := make(map[string]bool)
	for _, r := range filteredRoutes {
		destinations[r.Destination+"/"+fmt.Sprintf("%d", r.MaskLength)] = true
	}

	var out strings.Builder
	out.WriteString("Route Flags: R - relay, D - download to fib\n")
	out.WriteString(fmt.Sprintf("Routing Tables: Public\n"))
	out.WriteString(fmt.Sprintf("         Destinations : %d        Routes : %d\n", len(destinations), len(filteredRoutes)))
	out.WriteString("\n")

	if verbose {
		out.WriteString("Destination/Mask    Proto  Pre  Cost  Flags  NextHop         Interface       Age       Source\n")
		out.WriteString("-----------------------------------------------------------------------------------------------------\n")
	} else {
		out.WriteString("Destination/Mask    Proto  Pre  Cost  Flags  NextHop         Interface\n")
	}

	for _, r := range filteredRoutes {
		proto := strings.ToUpper(r.Protocol)
		if proto == "DIRECT" {
			proto = "Direct"
		} else if proto == "STATIC" {
			proto = "Static"
		}

		destMask := fmt.Sprintf("%s/%d", r.Destination, r.MaskLength)

		if verbose {
			age := "00h00m00s"
			source := "local"
			if r.Protocol == "static" {
				source = "configuration"
			}
			out.WriteString(fmt.Sprintf("%-19s %-6s %-4d %-5d %-7s %-15s %-18s %-10s %s\n",
				destMask,
				proto,
				r.Pre,
				r.Cost,
				r.Flags,
				r.NextHop,
				r.Interface,
				age,
				source,
			))
		} else {
			out.WriteString(fmt.Sprintf("%-19s %-6s %-4d %-5d %-7s %-15s %s\n",
				destMask,
				proto,
				r.Pre,
				r.Cost,
				r.Flags,
				r.NextHop,
				r.Interface,
			))
		}
	}

	if targetIP != "" && len(filteredRoutes) == 0 {
		out.WriteString(fmt.Sprintf("%% Route not found for %s\n", targetIP))
	}

	return out.String()
}
