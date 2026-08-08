// dhcp_relay_display.go 实现「DHCP 中继展示层 + 持久化 helper」（P2 第六项，T3/T4）。
//
// 职责边界（设计 A6 三文件分层）：
//   - dhcp_relay_eval.go   ：纯函数评估器（只读）
//   - dhcp_relay_cmd.go    ：副作用唯一出口（写 DeviceConfig 键）
//   - dhcp_relay_display.go：渲染 + 持久化 helper（**只读**，唯一数据源 = EvaluateDHCPRelay）
//
// 渲染契约（设计 §7 绑定条款）：
//   - 字段标签 / 列宽严格以 PRD §4.2（单接口详情块）/ §4.3（汇总表）为准；
//   - **唯一数据源 = EvaluateDHCPRelay / collectRelayInterfaces**，不直接读散落的 DeviceConfig 键
//     （杜绝 display ip pool 的 map 随机遍历缺陷）；
//   - **输出确定性**：接口按名称升序（sort.Strings）、server-ip 按配置序（parseRelayServerIPs 保序），
//     同一状态连续 10 次输出字节级完全一致（AC7）；
//   - **诚实占位（AC8 红线）**：Forwarding statistics 6 字段恒 "-"、汇总表 Fwd 列恒 "-"、
//     Source IP 未配恒 "-"（不推导接口主 IP）、全部非 Error 输出末尾附 dhcpRelaySimNote()；
//   - **只读、任意设备可读**（拍板 #5）：不加设备类型守卫，PC / Server 上输出空态 Info:。
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// —— 渲染常量（列宽严格对齐 PRD §4.2 / §4.3 样例）——

const (
	// dhcpRelayDetailRule 是单接口详情块的分隔线（62 个 '-'，对齐 PRD §4.2）。
	dhcpRelayDetailRule = "--------------------------------------------------------------"
	// dhcpRelaySummaryRule 是汇总表分隔线（84 个 '-'，对齐 PRD §4.3）。
	dhcpRelaySummaryRule = "------------------------------------------------------------------------------------"
	// dhcpRelayLabelFmt 是详情块「标签 : 值」行的格式（标签域宽 24，对齐 PRD §4.2）。
	dhcpRelayLabelFmt = "  %-24s: %s\n"
	// dhcpRelayContIndent 是多值（多个 server-ip）续行缩进（2 + 24 + 2 = 28 空格）。
	dhcpRelayContIndent = "                            "
	// dhcpRelaySummaryRowFmt 是汇总表数据行 / 表头行的格式（对齐 PRD §4.3 列宽）。
	dhcpRelaySummaryRowFmt = "%-26s%-8s%-9s%-17s%-10s%-11s%s\n"

	// infoNoDHCPRelayInterface 是空态提示（AC7 / AC10b）。
	infoNoDHCPRelayInterface = "Info: No DHCP relay interface configured."
	// errDHCPRelayDisplayUsage 是 display dhcp relay 的 usage 提示。
	errDHCPRelayDisplayUsage = "Error: usage: display dhcp relay { interface <interface-name> | all }"
	// errDHCPRelayIfaceNotExist 是接口名不存在的拒错文案（AC6）。
	errDHCPRelayIfaceNotExist = "Error: The specified interface does not exist."
)

// —— display dhcp relay 渲染入口（T3）——

// buildDHCPRelayDisplay 渲染 `display dhcp relay { interface <if> | all }`。
//
// args 为 `display dhcp relay` 之后的剩余参数：
//   - 无参        → 等价 all（拍板 #6）
//   - all         → 汇总表（PRD §4.3）
//   - interface X → 单接口详情块（PRD §4.2）
//
// 只读、任意设备可读（拍板 #5）：不做设备类型守卫，PC / Server 上因无中继键而输出空态 Info:，
// 断言不含 "is not supported on"（AC10b）。
func buildDHCPRelayDisplay(state *CLIState, args []string) string {
	if state == nil {
		return buildDHCPRelayEmpty()
	}
	if len(args) == 0 {
		return buildDHCPRelaySummary(state)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "all":
		return buildDHCPRelaySummary(state)
	case "interface":
		if len(args) < 2 {
			return errDHCPRelayDisplayUsage
		}
		// 支持 `interface Vlanif 10` / `interface GigabitEthernet 0/0/1` 的空格形态。
		name := strings.Join(args[1:], "")
		return buildDHCPRelayInterfaceDetail(state, name)
	}
	return errDHCPRelayDisplayUsage
}

// buildDHCPRelayEmpty 返回空态输出（Info + 诚实注记）。
func buildDHCPRelayEmpty() string {
	return infoNoDHCPRelayInterface + "\n" + dhcpRelaySimNote() + "\n"
}

// buildDHCPRelaySummary 渲染 `display dhcp relay all` 汇总表（PRD §4.3）。
//
// 列：Interface / Mode / Servers / Primary Server / Option82 / Source IP / Fwd。
// 接口按名称升序（确定性，AC7）；Fwd 列恒 "-"（AC8 红线）。
func buildDHCPRelaySummary(state *CLIState) string {
	ifaces := collectRelayInterfaces(state)
	if len(ifaces) == 0 {
		return buildDHCPRelayEmpty()
	}
	var b strings.Builder
	b.WriteString("DHCP relay configuration summary\n")
	b.WriteString(dhcpRelaySummaryRule + "\n")
	b.WriteString(fmt.Sprintf(dhcpRelaySummaryRowFmt,
		"Interface", "Mode", "Servers", "Primary Server", "Option82", "Source IP", "Fwd"))
	b.WriteString(dhcpRelaySummaryRule + "\n")
	for _, iface := range ifaces {
		res := EvaluateDHCPRelay(state, iface)
		cfg := res.Config
		mode := cfg.Mode
		if mode == "" {
			mode = relayStatPlaceholder
		}
		primary := relayStatPlaceholder
		if len(cfg.ServerIPs) > 0 {
			primary = cfg.ServerIPs[0]
		}
		sourceIP := cfg.SourceIP
		if sourceIP == "" {
			sourceIP = relayStatPlaceholder
		}
		b.WriteString(fmt.Sprintf(dhcpRelaySummaryRowFmt,
			iface,
			mode,
			fmt.Sprintf("%d", len(cfg.ServerIPs)),
			primary,
			option82DisplayValue(cfg.Option82),
			sourceIP,
			// Fwd：转发报文总数，仿真无转发引擎 → 恒 "-"（AC8 红线）
			res.Stats.DHCPPacketsForwarded,
		))
	}
	b.WriteString(dhcpRelaySummaryRule + "\n")
	b.WriteString(fmt.Sprintf("Total: %d relay interface(s)\n", len(ifaces)))
	b.WriteString(dhcpRelaySimNote() + "\n")
	return b.String()
}

// buildDHCPRelayInterfaceDetail 渲染 `display dhcp relay interface <if>` 单接口详情块（PRD §4.2）。
//
//   - 接口已配中继       → 完整详情块
//   - 接口存在但未配中继 → 明确 Info 提示（非空串）
//   - 接口名不存在       → Error（不附注记：命令被拒绝，不构成 display 输出）
func buildDHCPRelayInterfaceDetail(state *CLIState, name string) string {
	iface := resolveDHCPInterfaceName(state, name)
	if iface == "" {
		return errDHCPRelayIfaceNotExist
	}
	if !isDHCPRelayInterface(state, iface) {
		return fmt.Sprintf("Info: DHCP relay is not configured on interface %s.\n", iface) + dhcpRelaySimNote() + "\n"
	}
	res := EvaluateDHCPRelay(state, iface)
	cfg := res.Config

	var b strings.Builder
	b.WriteString(fmt.Sprintf("DHCP relay information of interface %s\n", iface))
	b.WriteString(dhcpRelayDetailRule + "\n")

	mode := cfg.Mode
	if mode == "" {
		mode = relayStatPlaceholder
	}
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Relay mode", mode))

	// Server IP address(es)：首地址与标签同行，其余按配置序逐行缩进对齐（保序，AC3/AC6）。
	if len(cfg.ServerIPs) == 0 {
		b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Server IP address(es)", relayStatPlaceholder))
	} else {
		b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Server IP address(es)", cfg.ServerIPs[0]))
		for _, ip := range cfg.ServerIPs[1:] {
			b.WriteString(dhcpRelayContIndent + ip + "\n")
		}
	}

	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Option82 (information)", option82DisplayValue(cfg.Option82)))
	// A5：未配时显示**生效缺省值** replace（不是 "-"）——缺省值是确定可知的事实。
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Option82 strategy", cfg.Option82Strategy))

	sourceIP := cfg.SourceIP
	if sourceIP == "" {
		// 拍板 #4：未配恒 "-"，**绝不推导接口主 IP**（推导即臆造）。
		sourceIP = relayStatPlaceholder
	}
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Source IP address", sourceIP))

	// Interface status 为**真实字段**（本地可判定），复用 isPortDown（stp_eval.go:175）。
	status := "Up"
	if isPortDown(state, iface) {
		status = "Down"
	}
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Interface status", status))

	// —— 诚实占位分组（AC8 红线）：六字段恒 "-"，严禁任何数字 / 可达性词 ——
	b.WriteString("  --- Forwarding statistics ---\n")
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "DHCP packets forwarded", res.Stats.DHCPPacketsForwarded))
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "DISCOVER forwarded", res.Stats.DiscoverForwarded))
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "OFFER received", res.Stats.OfferReceived))
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "REQUEST forwarded", res.Stats.RequestForwarded))
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "ACK received", res.Stats.AckReceived))
	b.WriteString(fmt.Sprintf(dhcpRelayLabelFmt, "Server reachability", res.Stats.ServerReachability))

	b.WriteString(dhcpRelayDetailRule + "\n")
	b.WriteString(res.SimNote + "\n")
	return b.String()
}

// option82DisplayValue 把 Option82 开关映射为 VRP 展示串。纯查表函数。
func option82DisplayValue(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

// isDHCPRelayInterface 判定接口是否出现在中继接口集合中（唯一口径 = collectRelayInterfaces）。
func isDHCPRelayInterface(state *CLIState, iface string) bool {
	for _, name := range collectRelayInterfaces(state) {
		if name == iface {
			return true
		}
	}
	return false
}

// —— 接口名解析（只读）——

// dhcpKnownInterfaceNames 返回设备上「已知接口名」的升序去重列表。
// 来源：state.Interfaces 与 DeviceConfig 的 interface:<if>:* 键（含 reload 后未重建 Interfaces 的场景）。
func dhcpKnownInterfaceNames(state *CLIState) []string {
	if state == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for name := range state.Interfaces {
		if strings.TrimSpace(name) != "" {
			seen[name] = true
		}
	}
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") {
			continue
		}
		rest := k[len("interface:"):]
		idx := strings.Index(rest, ":")
		if idx <= 0 {
			continue
		}
		seen[rest[:idx]] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resolveDHCPInterfaceName 把用户输入的接口名归一化为设备上已知的规范接口名（忽略大小写）。
// 未找到匹配时返回 ""（调用方据此给出 Error）。
func resolveDHCPInterfaceName(state *CLIState, name string) string {
	n := strings.TrimSpace(name)
	if n == "" || state == nil {
		return ""
	}
	known := dhcpKnownInterfaceNames(state)
	for _, k := range known {
		if k == n {
			return k
		}
	}
	for _, k := range known {
		if strings.EqualFold(k, n) {
			return k
		}
	}
	return ""
}

// —— 持久化 helper（T4，current-configuration 中继段）——

// buildSavedDHCPRelayInterfaceConfig 输出单接口的 DHCP 中继配置行（**已缩进，无 interface 包装**，
// 口径完全对齐 buildSavedVRRPConfig / buildSavedLAGInterfaceConfig）。
//
// VRP 固定顺序（PRD §4.4）：
//
//	dhcp select relay
//	dhcp relay server-ip 10.1.1.1
//	dhcp relay server-ip 10.1.1.2
//	dhcp relay information enable
//	dhcp relay information strategy replace
//	dhcp relay source-ip 10.2.2.254
//
// **缺省值不冗余输出**（设计 A5）：option82-strategy 等于生效缺省值 replace 时不输出该行。
// 纯函数：只读 DeviceConfig，无副作用。
func buildSavedDHCPRelayInterfaceConfig(state *CLIState, iface string) string {
	if state == nil || strings.TrimSpace(iface) == "" {
		return ""
	}
	cfg := readRelayConfig(state, iface)
	var b strings.Builder
	if cfg.Mode != "" {
		b.WriteString(fmt.Sprintf(" dhcp select %s\n", cfg.Mode))
	}
	for _, ip := range cfg.ServerIPs {
		b.WriteString(fmt.Sprintf(" dhcp relay server-ip %s\n", ip))
	}
	if cfg.Option82 != DefaultOption82Enabled {
		b.WriteString(" dhcp relay information enable\n")
	}
	if cfg.Option82Strategy != DefaultOption82Strategy {
		b.WriteString(fmt.Sprintf(" dhcp relay information strategy %s\n", cfg.Option82Strategy))
	}
	if cfg.SourceIP != "" {
		b.WriteString(fmt.Sprintf(" dhcp relay source-ip %s\n", cfg.SourceIP))
	}
	return b.String()
}

// buildSavedDHCPRelayConfig 是 DHCP 中继的**独立输出通道**
// （复用 buildSavedVRRPConfig / buildSavedLAGConfig 的 vrrpInterfaces 范式，parser.go:5415-5433）。
//
// 作用：save→reload 后 state.Interfaces 可能未重建中继接口，若只遍历 state.Interfaces
// 就会丢掉中继配置。本通道按 DeviceConfig 单一事实源补齐 interface 块，
// 保证 display current-configuration 在 reload 后仍完整复现（AC2 ③）。
//
// 输出确定性：接口按名称升序；已在主循环（state.Interfaces）输出的接口跳过，避免重复 interface 标题。
func buildSavedDHCPRelayConfig(state *CLIState) string {
	if state == nil {
		return ""
	}
	var b strings.Builder
	for _, iface := range collectDHCPSelectInterfaces(state) {
		if _, ok := state.Interfaces[iface]; ok {
			continue
		}
		lines := buildSavedDHCPRelayInterfaceConfig(state, iface)
		if lines == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", iface))
		b.WriteString(lines)
		b.WriteString("#\n")
	}
	return b.String()
}
