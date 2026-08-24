// gap_display.go —— 网闸 display 渲染层（设计三件套之三）
//
// display gap channel / policy / statistics：配置态真实（读 DeviceConfig），
// 运行态统计恒 "-"（诚实占位，不编造数字）。
package cli

import (
	"fmt"
	"strings"

	"ensp-lab/internal/topology"
)

// regGapDisplay 渲染 display gap [channel|policy|statistics]。
// arg1 为空时列出全部子命令用途摘要；否则按子命令分发。
func regGapDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	if state.DeviceType != topology.DeviceGAP {
		return "Error: display gap is only supported on GAP devices."
	}
	switch strings.ToLower(arg1) {
	case "channel":
		if args := cmd.Args; len(args) > 2 {
			return renderGapChannel(state, args[2])
		}
		return renderGapChannels(state)
	case "policy":
		if args := cmd.Args; len(args) > 2 {
			return renderGapPolicy(state, args[2])
		}
		return renderGapPolicies(state)
	case "statistics":
		return "GAP Statistics:\n" + GAPStatsDisplay()
	case "":
		return "  channel      Display GAP channel (ferry) information\n" +
			"  policy       Display GAP ferry policy information\n" +
			"  statistics   Display GAP ferry statistics"
	default:
		return fmt.Sprintf("Error: Unrecognized gap display command '%s'.", arg1)
	}
}

// renderGapChannels 列出全部通道（类似 display aaa 汇总风格）。
func renderGapChannels(state *CLIState) string {
	list := GAPChannelList(state.DeviceConfig)
	if len(list) == 0 {
		return "  Total GAP channels: 0\n  (None configured. Use `gap channel <number>` to create one.)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  Total GAP channels: %d\n", len(list))
	b.WriteString("  ----------------------------------------------------------\n")
	for _, ch := range list {
		status := ch.Status
		if status == "" {
			status = "Down"
		}
		fmt.Fprintf(&b, "  Channel %-4s %-8s %s\n", ch.Number, status, ch.Mapping)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderGapChannel 渲染单个通道详情。
func renderGapChannel(state *CLIState, n string) string {
	ch := GAPChannelStatus(state.DeviceConfig, n)
	if ch == "" {
		return fmt.Sprintf("  GAP channel %s does not exist.", n)
	}
	mapping := state.DeviceConfig["gap:channel:"+n+":mapping"]
	enabled := state.DeviceConfig["gap:channel:"+n+":enable"] == "true"
	status := ch
	if status == "" {
		status = "Down"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  GAP Channel %s\n", n)
	fmt.Fprintf(&b, "  ----------------------------------------------------------\n")
	fmt.Fprintf(&b, "  Mapping : %s\n", mapping)
	fmt.Fprintf(&b, "  Enabled : %v\n", enabled)
	fmt.Fprintf(&b, "  Status  : %s\n", status)
	return strings.TrimRight(b.String(), "\n")
}

// renderGapPolicies 列出全部策略。
func renderGapPolicies(state *CLIState) string {
	list := GAPPolicyList(state.DeviceConfig)
	if len(list) == 0 {
		return "  Total GAP policies: 0\n  (None configured. Use `gap policy <number>` to create one.)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  Total GAP policies: %d\n", len(list))
	b.WriteString("  ----------------------------------------------------------\n")
	for _, p := range list {
		en := p["enabled"] == "true"
		flag := "Disable"
		if en {
			flag = "Enable "
		}
		fmt.Fprintf(&b, "  Policy %-4s %s %s\n", p["number"], flag, p["rule"])
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderGapPolicy 渲染单个策略详情。
func renderGapPolicy(state *CLIState, n string) string {
	rule := state.DeviceConfig["gap:policy:"+n+":rule"]
	if rule == "" {
		return fmt.Sprintf("  GAP policy %s does not exist.", n)
	}
	enabled := state.DeviceConfig["gap:policy:"+n+":enable"] == "true"
	var b strings.Builder
	fmt.Fprintf(&b, "  GAP Policy %s\n", n)
	fmt.Fprintf(&b, "  ----------------------------------------------------------\n")
	fmt.Fprintf(&b, "  Rule   : %s\n", rule)
	fmt.Fprintf(&b, "  Enabled: %v\n", enabled)
	return strings.TrimRight(b.String(), "\n")
}
