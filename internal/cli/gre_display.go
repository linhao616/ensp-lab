// gre_display.go 实现「GRE 隧道展示层 + 持久化 helper」（P2 第七项，T3/T4）。
//
// 职责边界（设计 A6 三文件分层）：
//   - gre_eval.go   ：纯函数评估器（只读）
//   - gre_cmd.go    ：副作用唯一出口（写 DeviceConfig 键）
//   - gre_display.go：渲染 + 持久化 helper（**只读**，唯一数据源 = EvaluateGRE / collectGRETunnels）
//
// 渲染契约（设计 §7 绑定条款）：
//   - **唯一数据源 = EvaluateGRE / collectGRETunnels**，不直接读散落的 DeviceConfig 键
//     （杜绝 display gre 旧实现的 map 随机遍历缺陷，AC7）；
//   - **输出确定性**：接口按名称升序（collectGRETunnels 已保证），同一状态连续 10 次
//     输出字节级完全一致（AC7）；
//   - **诚实占位（AC8 红线）**：运行态统计 5 字段恒 "-"、PeerReachable 恒 "-"（不 Reachable/Up/Active）、
//     汇总表 State 列**不得裸 Up**（必须带 *）；所有非 Error GRE 输出末尾附 greSimNote()；
//   - **只读、任意设备可读**（AC11b）：display gre tunnel 不加设备类型守卫，
//     PC / Server 上因无 GRE 键而输出空态 Info:，不得返回 is not supported on；
//   - **接口名形态 source/destination 原样回显，绝不推导 IP**（C3）。
package cli

import (
	"fmt"
	"strings"
)

// —— 渲染常量（列宽严格对齐 PRD §4.3 样例）——

const (
	// greSummaryRule 是汇总表分隔线（80 个 '-'）。
	greSummaryRule = "--------------------------------------------------------------------------------"
	// greSummaryRowFmt 是汇总表数据行 / 表头行的格式（对齐 PRD §4.3 列宽：
	// Interface / Protocol / Source / Destination / Key / Keepalive / State）。
	greSummaryRowFmt = "%-18s%-9s%-18s%-18s%-10s%-10s%s\n"
	// greDetailLabelFmt 是运行态统计分组内「标签 : 值」行的格式（2 空格缩进 + 标签域宽 24，
	// 严格对齐 PRD §4.2 样例 `  Keepalive sent          : -`）。
	greDetailLabelFmt = "  %-24s: %s\n"
	// greFieldLabelFmt 是详情块顶层「标签 : 值」行的格式（无缩进 + 标签域宽 16，
	// 严格对齐 PRD §4.2 样例 `GRE key         : 1234`）。
	greFieldLabelFmt = "%-16s: %s\n"
	// greRuntimeStatsHeader 是运行态统计分组标题（C9 保留该分组）。
	greRuntimeStatsHeader = "  --- Tunnel runtime statistics ---"
)

// —— display gre tunnel 渲染入口（T3，C6 重定向落点）——
//
// args 为 `display gre` 之后的剩余参数：
//   - 无参        → 等价 tunnel（拍板 C6：旧 display gre 重定向到汇总表）
//   - tunnel      → 汇总表（PRD §4.3）
//   - 其它        → Error: unrecognized command
//
// 只读、任意设备可读：不做设备类型守卫，PC / Server 上因无隧道而输出空态 Info:，
// 断言不含 "is not supported on"（AC11b）。
func buildGREDisplay(state *CLIState, args []string) string {
	if state == nil {
		return buildGREEmpty()
	}
	if len(args) == 0 {
		return buildGRESummary(state)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "tunnel":
		return buildGRESummary(state)
	}
	return errGREUnrecognized
}

// buildGREEmpty 返回空态输出（Info + 诚实注记）。
func buildGREEmpty() string {
	return infoNoGRE + "\n" + greSimNote() + "\n"
}

// buildGRESummary 渲染 `display gre tunnel` 汇总表（PRD §4.3）。
//
// 列：Interface / Protocol / Source / Destination / Key / Keepalive / State。
// 接口按名称升序（确定性，AC7）；State 列取 greSummaryState（完整→Up*，缺配→Down，大写首字母，
// 区别于 brief 列小写 up*），**不得裸 Up**（AC8 红线）；Key 未配显 "-" 不显 "0"（A7）；
// 末尾附 State 派生脚注与 greSimNote()。
func buildGRESummary(state *CLIState) string {
	ifaces := collectGRETunnels(state)
	if len(ifaces) == 0 {
		return buildGREEmpty()
	}
	var b strings.Builder
	b.WriteString("GRE tunnel information\n")
	b.WriteString(greSummaryRule + "\n")
	b.WriteString(fmt.Sprintf(greSummaryRowFmt,
		"Interface", "Protocol", "Source", "Destination", "Key", "Keepalive", "State"))
	b.WriteString(greSummaryRule + "\n")
	for _, iface := range ifaces {
		res := EvaluateGRE(state, iface)
		cfg := res.Config
		src := cfg.Source
		if src == "" {
			src = greStatPlaceholder
		}
		dst := cfg.Destination
		if dst == "" {
			dst = greStatPlaceholder
		}
		key := cfg.Key
		if key == "" {
			key = greStatPlaceholder
		}
		keepalive := "Disabled"
		if cfg.Keepalive.Enabled {
			keepalive = "Enabled"
		}
		b.WriteString(fmt.Sprintf(greSummaryRowFmt,
			iface,
			strings.ToUpper(cfg.TunnelProtocol),
			src,
			dst,
			key,
			keepalive,
			greSummaryState(cfg),
		))
	}
	b.WriteString(greSummaryRule + "\n")
	b.WriteString(fmt.Sprintf("Total: %d tunnel(s)\n", len(ifaces)))
	b.WriteString("* State 仅由本端配置完整性派生，未与对端协商\n")
	b.WriteString(greSimNote() + "\n")
	return b.String()
}

// —— display interface Tunnel<x> GRE 段（T3，A11 追加块）——
//
// 由 parser.go `display interface <if>` 详情块在「Internet Address is」行之后调用。
//   - 非 Tunnel 口         → 返回 ""（详情块逐字不变，零回归）；
//   - Tunnel 口未配 GRE    → 仅输出 Info 提示，不输出统计分组（A11③）；
//   - Tunnel 口已配 GRE    → 完整 GRE 详情段（含 --- Tunnel runtime statistics --- 5 字段恒 -，C9）
//     与 greSimNote() 注记；**不输出 GRE 专有 1476 MTU 行**（C9）。
func buildGREInterfaceSection(state *CLIState, iface string) string {
	if !isTunnelInterface(iface) {
		return "" // 非 Tunnel 口：逐字不变，零回归
	}
	if state == nil || state.DeviceConfig[tunnelProtocolKey(iface)] != "gre" {
		// Tunnel 口但未配 tunnel-protocol gre → 仅 Info 提示，不输出统计分组（A11③）
		return "\n" + infoGREOnIfaceNotCfg + "\n"
	}
	cfg := EvaluateGRE(state, iface).Config
	var b strings.Builder
	b.WriteString("\n")
	src := cfg.Source
	if src == "" {
		src = greStatPlaceholder
	}
	dst := cfg.Destination
	if dst == "" {
		dst = greStatPlaceholder
	}
	// C3：source/destination 原样回显（IP 或接口名），绝不推导 IP
	b.WriteString(fmt.Sprintf("Tunnel source %s, destination %s\n", src, dst))
	b.WriteString("Tunnel protocol/transport GRE/IP\n")

	// GRE key：未配显 "-"，**严禁显示 0**（PRD §4.2 / 设计 A7）
	key := cfg.Key
	if key == "" {
		key = greStatPlaceholder
	}
	b.WriteString(fmt.Sprintf(greFieldLabelFmt, "GRE key", key))

	// Keepalive：C2 仅配置态；启用时附生效 period / retry（显式值或缺省 5 / 3）
	keepalive := "Disabled"
	if cfg.Keepalive.Enabled {
		keepalive = fmt.Sprintf("Enabled  (period %d, retry-times %d)",
			cfg.Keepalive.Period, cfg.Keepalive.Retry)
	}
	b.WriteString(fmt.Sprintf(greFieldLabelFmt, "Keepalive", keepalive))

	checksum := "Disabled"
	if cfg.Checksum {
		checksum = "Enabled"
	}
	b.WriteString(fmt.Sprintf("Checksumming of packets : %s\n", checksum))

	// C9：保留 --- Tunnel runtime statistics --- 分组，5 运行态字段恒 "-" + greSimNote() 注记。
	// 🔴 AC8 红线：以下 5 行的值只能来自 newGREStats()（恒 "-"），严禁任何计数器 / 随机数。
	b.WriteString(greRuntimeStatsHeader + "\n")
	stats := newGREStats()
	b.WriteString(fmt.Sprintf(greDetailLabelFmt, "Keepalive sent", stats.KeepaliveSent))
	b.WriteString(fmt.Sprintf(greDetailLabelFmt, "Keepalive received", stats.KeepaliveReceived))
	b.WriteString(fmt.Sprintf(greDetailLabelFmt, "Packets encapsulated", stats.PacketsEncapsulated))
	b.WriteString(fmt.Sprintf(greDetailLabelFmt, "Packets decapsulated", stats.PacketsDecapsulated))
	b.WriteString(fmt.Sprintf(greDetailLabelFmt, "Peer reachability", stats.PeerReachable))
	b.WriteString(greSimNote() + "\n")
	return b.String()
}

// —— 持久化 helper（T4，current-configuration GRE 段）——

// buildSavedGREInterfaceConfig 输出单接口的 GRE 隧道配置行（**已缩进，无 interface 包装**，
// 口径完全对齐 buildSavedDHCPRelayInterfaceConfig / buildSavedVRRPConfig）。
//
// VRP 固定顺序（PRD §4.4 / 设计 §2 改动点 #9）：
//
//	tunnel-protocol gre
//	source <x>
//	destination <x>
//	gre key <n>
//	keepalive [period <p> retry-times <r>]
//	gre checksum
//
// **缺省值不冗余输出**（设计 A5 同族）：未配 key / keepalive / checksum 时不输出对应行；
// keepalive 无显式 period/retry 键时只输出 `keepalive`（生效缺省 5/3 由读端回填，不落盘）。
// 纯函数：只读 DeviceConfig，无副作用。
func buildSavedGREInterfaceConfig(state *CLIState, iface string) string {
	if state == nil || strings.TrimSpace(iface) == "" {
		return ""
	}
	cfg := readGREConfig(state, iface)
	if cfg.TunnelProtocol != "gre" {
		return "" // 未配 tunnel-protocol gre 则不输出任何 GRE 行
	}
	var b strings.Builder
	b.WriteString(" tunnel-protocol gre\n")
	if cfg.Source != "" {
		b.WriteString(fmt.Sprintf(" source %s\n", cfg.Source))
	}
	if cfg.Destination != "" {
		b.WriteString(fmt.Sprintf(" destination %s\n", cfg.Destination))
	}
	if cfg.Key != "" {
		b.WriteString(fmt.Sprintf(" gre key %s\n", cfg.Key))
	}
	if cfg.Keepalive.Enabled {
		hasP := state.DeviceConfig[greKey(iface, "keepalive-period")] != ""
		hasR := state.DeviceConfig[greKey(iface, "keepalive-retry")] != ""
		switch {
		case hasP && hasR:
			b.WriteString(fmt.Sprintf(" keepalive period %d retry-times %d\n", cfg.Keepalive.Period, cfg.Keepalive.Retry))
		case hasP:
			b.WriteString(fmt.Sprintf(" keepalive period %d\n", cfg.Keepalive.Period))
		case hasR:
			b.WriteString(fmt.Sprintf(" keepalive retry-times %d\n", cfg.Keepalive.Retry))
		default:
			b.WriteString(" keepalive\n")
		}
	}
	if cfg.Checksum {
		b.WriteString(" gre checksum\n")
	}
	return b.String()
}

// savedInterfaceIPLine 由 interface:<if>:ip 键还原 ` ip address <ip> <mask>` 行。
//
// 仅用于 buildSavedGREConfig 的补齐场景（state.Interfaces 未重建时）。
// 复用 host.go 的 parseIPFormat，兼容 "<ip> <mask>" 与 "<ip>/<prefix>" 两种存储形态，
// 输出统一为 VRP 的点分掩码形态（对齐 PRD §4.4 样例）。
// 纯函数：只读 DeviceConfig，无副作用。
func savedInterfaceIPLine(state *CLIState, iface string) string {
	raw := strings.TrimSpace(state.DeviceConfig[fmt.Sprintf("interface:%s:ip", iface)])
	if raw == "" {
		return ""
	}
	ipAddr, mask := parseIPFormat(raw)
	if ipAddr == "" {
		return ""
	}
	return fmt.Sprintf(" ip address %s %s\n", ipAddr, mask)
}

// buildSavedGREConfig 是 GRE 隧道的**独立输出通道**
// （复用 buildSavedDHCPRelayConfig 的范式，parser.go:5462-5465 之后挂载）。
//
// 作用：save→reload 后 state.Interfaces 可能未重建 Tunnel 口，若只遍历 state.Interfaces
// 就会丢掉 GRE 配置。本通道按 DeviceConfig 单一事实源补齐 interface 块，
// 保证 display current-configuration 在 reload 后仍完整复现（AC2 ③）。
//
// 输出确定性：接口按名称升序；已在主循环（state.Interfaces）输出的接口跳过，避免重复 interface 标题。
func buildSavedGREConfig(state *CLIState) string {
	if state == nil {
		return ""
	}
	var b strings.Builder
	for _, iface := range collectGREConfiguredInterfaces(state) {
		if _, ok := state.Interfaces[iface]; ok {
			continue
		}
		lines := buildSavedGREInterfaceConfig(state, iface)
		if lines == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", iface))
		// PRD §4.4 固定顺序：ip address 必须排在 tunnel-protocol 之前。
		// 主循环（state.Interfaces）会自行输出 ip 行，但本通道服务的正是
		// 「reload 后 state.Interfaces 未重建」的接口，故需由 DeviceConfig 补齐，
		// 否则 Tunnel 块会丢失 ip address（AC2 ③ §4.4 全部行）。
		if ipLine := savedInterfaceIPLine(state, iface); ipLine != "" {
			b.WriteString(ipLine)
		}
		b.WriteString(lines)
		b.WriteString("#\n")
	}
	return b.String()
}
