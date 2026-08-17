// ipv6_display.go 是 IPv6 展示层（只读，任意设备可读 AC11b）。
// （P2 第九项，华为 VRP 课程 43/44，T03 展示层 + 快照挂载。）
//
// 分层契约（设计 §4.4，严格复刻 gre_display.go 范式）：
//   - ipv6_eval.go   纯函数只读（键 helper / 校验 / 收集器 / 派生），无副作用；
//   - ipv6_cmd.go    副作用唯一出口（写 state.DeviceConfig）；
//   - ipv6_display.go **本文件**：渲染 + 持久化 helper，只读，绝不写任何键。
//
// 全部输出末尾恒附 ipv6SimNote()（P0-7 / AC9 红线）；运行态字段恒 "-"（A4），
// 严禁伪造 fe80:: / 假数字 / 假时间（C3/C4 诚实占位）。
package cli

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// —— display 分发（A13：无参等价 brief）——

// buildIPv6Display 是 display ipv6 的分发入口（A13 / P0-5/6/9）。
//
// args 为 `display ipv6` 之后的原始 token 切片（未做二次归一化，此处自行归一化）。
//   - 无参 / brief                 → buildIPv6InterfaceBriefDisplay（A13 无参等价 brief）；
//   - interface [<if>|brief]       → 详情 / brief；
//   - routing-table [<prefix>]     → 路由表（P1-1 目标过滤）。
//
// 只读：任何设备类型均可执行（AC11b，display 无能力守卫）。
func buildIPv6Display(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	sub := ""
	if len(args) >= 1 {
		sub = normalizeIPv6DisplaySub(strings.ToLower(strings.TrimSpace(args[0])))
	}
	switch sub {
	case "", "brief":
		// A13：display ipv6 / display ipv6 brief 无参等价 brief。
		return buildIPv6InterfaceBriefDisplay(state)
	case "interface":
		rest := args[1:]
		if len(rest) == 0 {
			// A13：display ipv6 interface 无参等价 brief。
			return buildIPv6InterfaceBriefDisplay(state)
		}
		first := strings.ToLower(strings.TrimSpace(rest[0]))
		if res, ok, _ := resolveKeyword(first, []string{"brief"}); ok && res == "brief" {
			return buildIPv6InterfaceBriefDisplay(state)
		}
		return buildIPv6InterfaceDisplay(state, strings.TrimSpace(rest[0]))
	case "routing-table":
		target := ""
		if len(args) >= 2 {
			target = strings.TrimSpace(args[1])
		}
		return buildIPv6RoutingTableDisplay(state, target)
	default:
		return "Error: Incomplete command found at '^' position."
	}
}

// normalizeIPv6DisplaySub 归一化 display ipv6 二级子命令缩写（华为 VRP 规则）。
// 与 normalizeDisplaySubCmd2(parent="ipv6") 口径一致，保证直接调用本分发器也正确。
func normalizeIPv6DisplaySub(s string) string {
	switch s {
	case "int", "inter":
		return "interface"
	case "rt", "route":
		return "routing-table"
	}
	return s
}

// —— P0-5 / P1-2：display ipv6 interface brief ——

// buildIPv6InterfaceBriefDisplay 渲染接口 IPv6 简表（PRD §4.2）。
//
// 数据行仅列出「已 ipv6 enable 或已配地址」的接口（P1-2），接口名升序（AC4 ①）；
// Protocol 列恒 "-"（运行态诚实占位，AC4 ②）；空态 "Info: No IPv6 address configured."（AC4 ③）；
// 无 map 随机遍历 → 同一状态连续调用输出字节级一致（AC4 ④）；末尾恒附 ipv6SimNote()（AC4 ⑤）。
func buildIPv6InterfaceBriefDisplay(state *CLIState) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	ifaces := collectIPv6Interfaces(state)
	if len(ifaces) == 0 {
		return InfoNoIPv6Address + "\n" + ipv6SimNote()
	}
	var out strings.Builder
	out.WriteString("*down: administratively down\n")
	out.WriteString("^down: standby\n")
	out.WriteString("(l): loopback\n")
	out.WriteString("(s): spoofing\n")
	out.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n", "Interface", "Physical", "Protocol", "IPv6 Address"))
	for _, iface := range ifaces {
		view := readIPv6AddressView(state, iface)
		addr := view.Address
		if addr == "" {
			addr = "-"
		}
		physical := strings.ToLower(strings.TrimSpace(state.DeviceConfig[ipv6IfaceKeyNamespace+iface+":status"]))
		if physical == "" {
			physical = "up"
		}
		out.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n", iface, physical, "-", addr))
	}
	out.WriteString(ipv6SimNote())
	return out.String()
}

// —— P0-6 / C3 / C4：display ipv6 interface <if> ——

// buildIPv6InterfaceDisplay 渲染单接口 IPv6 详情（PRD §4.3）。
//
// 存在性：interface:<if>:* 任一键存在即视为存在（AC5 ④ 未命中 → ErrIPv6InvalidInterface）。
// 运行态字段（Line protocol / DAD / ND / 统计）恒 "-"（AC5 ② / AC9）；
// link-local 按 C3 双分支：有真实 MAC 键且已使能 → fe80::<EUI64> 真实计算；否则 "-"（AC5 ⑥）。
// Joined group address(es) 按 C4 P0 恒 "-"（P1-4 后置）。
func buildIPv6InterfaceDisplay(state *CLIState, iface string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	iface = strings.TrimSpace(iface)
	if iface == "" || !ipv6InterfaceExists(state, iface) {
		return fmt.Sprintf(ErrIPv6InvalidInterface, iface)
	}
	status := strings.ToLower(strings.TrimSpace(state.DeviceConfig[ipv6IfaceKeyNamespace+iface+":status"]))
	if status == "" {
		status = "up"
	}
	view := readIPv6AddressView(state, iface)
	linkLocal := "-"
	if view.Enable && view.HasMAC {
		linkLocal = view.LinkLocal
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("%s current state : %s\n", iface, status))
	out.WriteString("Line protocol current state : -\n")
	if view.Enable {
		out.WriteString(fmt.Sprintf("IPv6 is enable, link-local address is %s\n", linkLocal))
	} else {
		out.WriteString("IPv6 is not enable, link-local address is -\n")
	}
	out.WriteString("  Global unicast address(es):\n")
	if view.Address != "" {
		addrPart, lenPart := splitIPv6Prefix(view.Address)
		subnet := "-"
		if network, err := NetworkFromPrefix(view.Address); err == nil {
			subnet = network + "/" + lenPart
		}
		out.WriteString(fmt.Sprintf("    %s, subnet is %s\n", addrPart, subnet))
	} else {
		out.WriteString("    -\n")
	}
	out.WriteString("  Joined group address(es):\n")
	out.WriteString("    -\n")
	out.WriteString("  MTU is 1500 bytes\n")
	out.WriteString("  ND DAD is enabled, number of DAD attempts : -\n")
	out.WriteString("  ND reachable time : - (ms)\n")
	out.WriteString("  ND retransmit interval : - (ms)\n")
	out.WriteString("  IPv6 Packet statistics:\n")
	out.WriteString("    InReceives: -    InErrors: -    InDiscards: -\n")
	out.WriteString("    OutRequests: -   OutDiscards: -\n")
	out.WriteString(ipv6SimNote())
	return out.String()
}

// ipv6InterfaceExists 判定接口是否存在（interface:<if>:* 任一键）。
func ipv6InterfaceExists(state *CLIState, iface string) bool {
	if state == nil || state.DeviceConfig == nil {
		return false
	}
	prefix := ipv6IfaceKeyNamespace + iface + ":"
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// splitIPv6Prefix 将 "<addr>/<len>" 拆为地址段与长度段；无 '/' 时原样返回地址段。
func splitIPv6Prefix(prefix string) (addr, lenPart string) {
	idx := strings.LastIndex(prefix, "/")
	if idx < 0 {
		return prefix, ""
	}
	return prefix[:idx], prefix[idx+1:]
}

// —— P0-9 / P1-1：display ipv6 routing-table ——

// buildIPv6RoutingTableDisplay 渲染 IPv6 路由表（PRD §4.4）。
//
// 数据源：直连路由（buildIPv6DirectRoutes，由接口地址 + NetworkFromPrefix 推导）
// + 静态路由（collectIPv6RouteStatics，C2 多键形态）。无动态协议条目（AC7 ②）；
// RelayNextHop / TunnelID 恒 "-"（AC7 ③）；空表 "Info: No IPv6 route."（AC7 ④）；
// 目标前缀**数值**升序（AC7 ⑥，netip.Addr.Compare 而非字符串比较）；
// 无 map 随机遍历 → 字节级确定（AC7 ⑤）；末尾恒附 ipv6SimNote()。
func buildIPv6RoutingTableDisplay(state *CLIState, targetPrefix string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	routes := buildIPv6DirectRoutes(state)
	for _, rs := range collectIPv6RouteStatics(state) {
		dest, derr := NetworkFromPrefix(rs.Prefix)
		if derr != nil {
			continue
		}
		_, lenPart := splitIPv6Prefix(rs.Prefix)
		plen, _ := strconv.Atoi(lenPart)
		routes = append(routes, IPv6RouteView{
			Destination:  dest,
			PrefixLength: plen,
			NextHop:      rs.NextHop,
			Protocol:     "Static",
			Preference:   IPv6StaticPreference,
			Cost:         0,
			Interface:    "NULL0",
		})
	}
	sortIPv6RouteViews(routes)
	if strings.TrimSpace(targetPrefix) != "" {
		routes = filterIPv6RoutesByPrefix(routes, targetPrefix)
	}
	if len(routes) == 0 {
		return InfoNoIPv6Route + "\n" + ipv6SimNote()
	}
	var out strings.Builder
	out.WriteString("Route Flags: R - relay, D - download to fib\n")
	out.WriteString("Routing Table : Public\n")
	out.WriteString(fmt.Sprintf("         Destinations : %d        Routes : %d\n", len(routes), len(routes)))
	for _, r := range routes {
		out.WriteString("\n")
		out.WriteString(fmt.Sprintf("Destination  : %-40s PrefixLength : %d\n", r.Destination, r.PrefixLength))
		out.WriteString(fmt.Sprintf("NextHop      : %-40s Preference   : %d\n", r.NextHop, r.Preference))
		out.WriteString(fmt.Sprintf("Cost         : %-40d Protocol     : %s\n", r.Cost, r.Protocol))
		out.WriteString("RelayNextHop : -                                      TunnelID     : -\n")
		out.WriteString(fmt.Sprintf("Interface    : %s\n", r.Interface))
	}
	out.WriteString(ipv6SimNote())
	return out.String()
}

// buildIPv6DirectRoutes 由接口地址推导直连路由（NetworkFromPrefix 取网络地址）。
// 只读，返回 IPv6RouteView 切片（Protocol=Direct / Preference=0，AC7 ①）。
func buildIPv6DirectRoutes(state *CLIState) []IPv6RouteView {
	if state == nil {
		return []IPv6RouteView{}
	}
	routes := make([]IPv6RouteView, 0)
	for _, iface := range collectIPv6Interfaces(state) {
		addr := strings.TrimSpace(state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldAddress)])
		if addr == "" {
			continue
		}
		network, err := NetworkFromPrefix(addr)
		if err != nil {
			continue
		}
		addrPart, lenPart := splitIPv6Prefix(addr)
		plen, _ := strconv.Atoi(lenPart)
		routes = append(routes, IPv6RouteView{
			Destination:  network,
			PrefixLength: plen,
			NextHop:      addrPart,
			Protocol:     "Direct",
			Preference:   IPv6DirectPreference,
			Cost:         0,
			Interface:    iface,
		})
	}
	return routes
}

// sortIPv6RouteViews 按目标前缀**数值**升序（netip.Addr.Compare），
// 同前缀按 PrefixLength 升序、再按 NextHop 升序（全序 → 确定性，AC7 ⑤/⑥）。
// 🔴 不能用字符串比较：""2001:db8:2::"" 的 '2'(0x32) < ':'(0x3A)，会把 2:: 排到 :: 之前。
func sortIPv6RouteViews(routes []IPv6RouteView) {
	sort.SliceStable(routes, func(i, j int) bool {
		ai, ei := netip.ParseAddr(routes[i].Destination)
		aj, ej := netip.ParseAddr(routes[j].Destination)
		if ei == nil && ej == nil {
			if c := ai.Compare(aj); c != 0 {
				return c < 0
			}
		} else if routes[i].Destination != routes[j].Destination {
			return routes[i].Destination < routes[j].Destination
		}
		if routes[i].PrefixLength != routes[j].PrefixLength {
			return routes[i].PrefixLength < routes[j].PrefixLength
		}
		return routes[i].NextHop < routes[j].NextHop
	})
}

// filterIPv6RoutesByPrefix 按目标前缀过滤（P1-1）。
// target 可为 "<addr>/<len>"（netip.ParsePrefix）或裸地址（按 /128 精确匹配）。
func filterIPv6RoutesByPrefix(routes []IPv6RouteView, target string) []IPv6RouteView {
	target = strings.TrimSpace(target)
	if target == "" {
		return routes
	}
	tp, err := netip.ParsePrefix(target)
	if err != nil {
		addr, aerr := netip.ParseAddr(target)
		if aerr != nil {
			return []IPv6RouteView{}
		}
		tp = netip.PrefixFrom(addr, 128)
	}
	filtered := make([]IPv6RouteView, 0)
	for _, r := range routes {
		dest, derr := netip.ParseAddr(r.Destination)
		if derr != nil {
			continue
		}
		if tp.Contains(dest) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// —— P0-12：current-configuration 快照挂载 helper（只读）——

// buildSavedIPv6InterfaceConfig 输出接口块内的 IPv6 行（P0-12 / PRD §4.5）。
// 仅输出显式配置：` ipv6 enable`（:ipv6-enable == "true"）与 ` ipv6 address <规范前缀>`，
// 缺省值不冗余。返回空串表示无内容（挂载方据此跳过）。
func buildSavedIPv6InterfaceConfig(state *CLIState, iface string) string {
	if state == nil {
		return ""
	}
	var b strings.Builder
	if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldEnable)]), "true") {
		b.WriteString(" ipv6 enable\n")
	}
	if addr := strings.TrimSpace(state.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldAddress)]); addr != "" {
		b.WriteString(fmt.Sprintf(" ipv6 address %s\n", addr))
	}
	return b.String()
}

// buildSavedIPv6RouteConfig 输出系统级 ipv6 route-static 行（P0-12 / PRD §4.5）。
// collectIPv6RouteStatics 已按 prefix 升序（确定性，AC8 ③ 字节级一致）。
func buildSavedIPv6RouteConfig(state *CLIState) string {
	if state == nil {
		return ""
	}
	routes := collectIPv6RouteStatics(state)
	if len(routes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, rs := range routes {
		b.WriteString(fmt.Sprintf("ipv6 route-static %s %s\n", rs.Prefix, rs.NextHop))
	}
	return b.String()
}

// —— P0-13 / P0-14：display ripng / display ospfv3（诚实占位）——

// buildRIPngDisplay 渲染 display ripng [<pid>]（AC13）。
// 配置态（进程号 / 使能接口）读真实键；邻居数 / 路由计数恒 "-"（运行态诚实占位）+ 注记。
// pid 缺省取已使能的最小进程号；未配置 → "RIPng: Not configured" + 注记。
func buildRIPngDisplay(state *CLIState, pid string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	pid = strings.TrimSpace(pid)
	if pid == "" {
		pids := collectRIPngPIDs(state)
		if len(pids) == 0 {
			return "RIPng: Not configured\n" + ipv6SimNote()
		}
		pid = pids[0]
	}
	if !strings.EqualFold(strings.TrimSpace(state.DeviceConfig[ipv6RIPngKey(pid)]), "true") {
		return fmt.Sprintf("RIPng: process %s is not configured\n%s", pid, ipv6SimNote())
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("RIPng process %s\n", pid))
	out.WriteString("  State : Running\n")
	// 列出「该 pid 已使能」的接口：精确中缀 ":ripng-" 扫描 interface:<if>:ripng-<pid>-enable，
	// 接口名升序（AC12 精确键纪律，确定性——不能用 collectIPv6Interfaces，会漏掉
	// 仅配 ripng 而未配 ipv6-* 键的接口）。
	ripngIfaces := make([]string, 0)
	ripngSeen := make(map[string]bool)
	for k, v := range state.DeviceConfig {
		if !strings.EqualFold(strings.TrimSpace(v), "true") {
			continue
		}
		if iface, ipid, ok := ifaceFromIPv6RIPngKey(k); ok && ipid == pid && !ripngSeen[iface] {
			ripngSeen[iface] = true
			ripngIfaces = append(ripngIfaces, iface)
		}
	}
	sort.Strings(ripngIfaces)
	for _, iface := range ripngIfaces {
		out.WriteString(fmt.Sprintf("  Interface %s : Enabled\n", iface))
	}
	out.WriteString("  Neighbors : -\n")
	out.WriteString("  Routes : -\n")
	out.WriteString(ipv6SimNote())
	return out.String()
}

// buildOSPFv3Display 渲染 display ospfv3 [<pid>]（AC13）。
// 配置态（进程号 / 接口区域）读真实键；邻居 / LSA 计数恒 "-"（运行态诚实占位）+ 注记。
// pid 缺省取已使能的最小进程号；未配置 → "OSPFv3: Not configured" + 注记。
func buildOSPFv3Display(state *CLIState, pid string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	pid = strings.TrimSpace(pid)
	if pid == "" {
		pids := collectOSPFv3PIDs(state)
		if len(pids) == 0 {
			return "OSPFv3: Not configured\n" + ipv6SimNote()
		}
		pid = pids[0]
	}
	if !strings.EqualFold(strings.TrimSpace(state.DeviceConfig[ipv6OSPFv3Key(pid)]), "true") {
		return fmt.Sprintf("OSPFv3: process %s is not configured\n%s", pid, ipv6SimNote())
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("OSPFv3 process %s\n", pid))
	out.WriteString("  State : Running\n")
	// 列出「该 pid 已绑定区域」的接口：精确中缀 ":ospfv3-" 扫描 interface:<if>:ospfv3-<pid>-area，
	// 接口名升序（AC12 精确键纪律，确定性——不能用 collectIPv6Interfaces，会漏掉
	// 仅配 ospfv3 而未配 ipv6-* 键的接口）。
	ospfIfaces := make([]string, 0)
	ospfSeen := make(map[string]bool)
	for k := range state.DeviceConfig {
		if iface, ipid, ok := ifaceFromIPv6OSPFv3Key(k); ok && ipid == pid && !ospfSeen[iface] {
			ospfSeen[iface] = true
			ospfIfaces = append(ospfIfaces, iface)
		}
	}
	sort.Strings(ospfIfaces)
	for _, iface := range ospfIfaces {
		area := strings.TrimSpace(state.DeviceConfig[ipv6OSPFv3IfaceKey(iface, pid)])
		out.WriteString(fmt.Sprintf("  Interface %s : area %s\n", iface, area))
	}
	out.WriteString("  Neighbors : -\n")
	out.WriteString("  LSAs : -\n")
	out.WriteString(ipv6SimNote())
	return out.String()
}
