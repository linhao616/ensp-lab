// link_quality_display.go 实现链路质量的**渲染层**（v0.12 链路质量模拟，T3）。
//
// 职责边界：只读渲染，绝不写 state.DeviceConfig。
//
// 两条输出通道：
//   - display link-quality        —— 配置态 + 实际生效链路映射 + 运行态诚实占位
//   - display current/saved-configuration —— 接口块内的 delay / loss 差异值行
//
// 诚实占位红线：lite 引擎不采集逐链路实测丢包率与抖动，Measured / Jitter 列
// 恒为 "-"，绝不由配置值反推「实测」数字。
package cli

import (
	"fmt"
	"strings"

	"ensp-lab/internal/topology"
)

// linkQualitySimNote 是仿真扩展声明。华为 VRP 真机接口视图不存在
// delay / loss 命令，必须在输出中明示，避免误导实验者。
func linkQualitySimNote() string {
	return "Note: delay/loss 为本仿真器的扩展命令（VRP 真机接口视图无此命令），\n" +
		"      用于编排链路时延与丢包场景；端到端丢包按路径各段概率累积。\n"
}

// buildLinkQualityDisplay 渲染 display link-quality [interface <name>]。
func buildLinkQualityDisplay(state *CLIState, args []string) string {
	// 指定接口：display link-quality interface GE0/0/1
	if len(args) >= 2 && strings.EqualFold(strings.TrimSpace(args[0]), "interface") {
		return buildLinkQualityInterfaceSection(state, strings.TrimSpace(args[1]))
	}
	entries := linkQualityEntries(state)
	if len(entries) == 0 {
		return buildLinkQualityEmpty()
	}
	var b strings.Builder
	b.WriteString(linkQualitySimNote())
	b.WriteString("\n")
	b.WriteString("Interface            Delay(ms)  Loss(%)   Peer                      Measured  Jitter\n")
	b.WriteString("--------------------------------------------------------------------------------------\n")
	for _, e := range entries {
		peer := linkQualityPeerText(state, e.Interface)
		b.WriteString(fmt.Sprintf("%-20s %-10s %-9s %-25s %-9s %s\n",
			truncateLinkQualityField(e.Interface, 20),
			e.DelayText(),
			e.LossText(),
			truncateLinkQualityField(peer, 25),
			linkQualityStatPlaceholder,
			linkQualityStatPlaceholder))
	}
	b.WriteString("--------------------------------------------------------------------------------------\n")
	b.WriteString(fmt.Sprintf("Total: %d interface(s) with link quality configured\n", len(entries)))
	return b.String()
}

// buildLinkQualityEmpty 是零配置时的输出。
func buildLinkQualityEmpty() string {
	var b strings.Builder
	b.WriteString(linkQualitySimNote())
	b.WriteString("\nNo link quality configured on any interface.\n")
	return b.String()
}

// buildLinkQualityInterfaceSection 渲染单接口详情。
func buildLinkQualityInterfaceSection(state *CLIState, iface string) string {
	name := resolveLinkQualityInterfaceName(state, iface)
	entry := interfaceLinkQuality(state, name)
	var b strings.Builder
	b.WriteString(linkQualitySimNote())
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Interface           : %s\n", name))
	b.WriteString(fmt.Sprintf("One-way delay (ms)  : %s\n", entry.DelayText()))
	b.WriteString(fmt.Sprintf("One-way loss (%%)    : %s\n", entry.LossText()))
	b.WriteString(fmt.Sprintf("Peer                : %s\n", linkQualityPeerText(state, name)))
	b.WriteString(fmt.Sprintf("Applied to link     : %s\n", linkQualityAppliedText(state, name)))
	b.WriteString(fmt.Sprintf("Measured loss (%%)   : %s\n", linkQualityStatPlaceholder))
	b.WriteString(fmt.Sprintf("Jitter (ms)         : %s\n", linkQualityStatPlaceholder))
	if !entry.Configured() {
		b.WriteString("\nNo link quality configured on this interface.\n")
	}
	return b.String()
}

// resolveLinkQualityInterfaceName 把用户输入的接口名（可能大小写不一致）
// 归一到 state.Interfaces 中的真实名字；找不到时原样返回。
func resolveLinkQualityInterfaceName(state *CLIState, input string) string {
	if state == nil || state.Interfaces == nil {
		return input
	}
	for name := range state.Interfaces {
		if strings.EqualFold(name, input) {
			return name
		}
	}
	return input
}

// linkQualityLink 在拓扑中查找承载该接口的链路。
// state.Topology 由 api 层注入，可能为 nil（单元测试 / 无拓扑上下文）。
func linkQualityLink(state *CLIState, iface string) *topology.Link {
	if state == nil || state.Topology == nil || state.DeviceID == "" {
		return nil
	}
	for _, l := range state.Topology.Links {
		if l == nil {
			continue
		}
		if l.SourceDevice == state.DeviceID && strings.EqualFold(l.SourcePort, iface) {
			return l
		}
		if l.TargetDevice == state.DeviceID && strings.EqualFold(l.TargetPort, iface) {
			return l
		}
	}
	return nil
}

// linkQualityPeerText 渲染对端「设备名 端口」。无拓扑上下文或未连线时为 "-"。
func linkQualityPeerText(state *CLIState, iface string) string {
	l := linkQualityLink(state, iface)
	if l == nil {
		return linkQualityUnsetPlaceholder
	}
	peerDev, peerPort := l.TargetDevice, l.TargetPort
	if l.TargetDevice == state.DeviceID && strings.EqualFold(l.TargetPort, iface) {
		peerDev, peerPort = l.SourceDevice, l.SourcePort
	}
	if state.Topology != nil {
		if dev, ok := state.Topology.Devices[peerDev]; ok && dev != nil && dev.Name != "" {
			peerDev = dev.Name
		}
	}
	return strings.TrimSpace(peerDev + " " + peerPort)
}

// linkQualityAppliedText 说明配置是否已落到拓扑链路上，并回显链路实际取值。
// 配置生效链路为「两端接口取较大值」，因此此处显示的可能是对端接口的配置值。
func linkQualityAppliedText(state *CLIState, iface string) string {
	l := linkQualityLink(state, iface)
	if l == nil {
		return linkQualityUnsetPlaceholder
	}
	return fmt.Sprintf("%s (delay %d ms, loss %s%%)", l.ID, l.Delay, FormatLinkLoss(l.Loss))
}

// truncateLinkQualityField 限宽，避免长接口名/设备名撑破表格对齐。
func truncateLinkQualityField(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "+"
}

// —— saved / current configuration 输出通道 ——

// buildSavedLinkQualityInterfaceConfig 输出接口块内的链路质量差异值行。
// 未配置则返回空串（VRP「只落差异值」口径）。
func buildSavedLinkQualityInterfaceConfig(state *CLIState, iface string) string {
	entry := interfaceLinkQuality(state, iface)
	if !entry.Configured() {
		return ""
	}
	var b strings.Builder
	if entry.HasDelay {
		b.WriteString(fmt.Sprintf(" delay %s\n", entry.DelayText()))
	}
	if entry.HasLoss {
		b.WriteString(fmt.Sprintf(" loss %s\n", entry.LossText()))
	}
	return b.String()
}

// buildSavedLinkQualityConfig 是链路质量的**独立输出通道**：对「拥有 delay/loss
// 键但 state.Interfaces 未重建」的接口（典型为 save→reload 后）补齐 interface 块，
// 保证 display current-configuration 在 reload 后仍完整复现配置。
func buildSavedLinkQualityConfig(state *CLIState) string {
	var b strings.Builder
	for _, iface := range linkQualityInterfaces(state) {
		if state.Interfaces != nil {
			if _, ok := state.Interfaces[iface]; ok {
				continue
			}
		}
		lines := buildSavedLinkQualityInterfaceConfig(state, iface)
		if lines == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", iface))
		b.WriteString(lines)
		b.WriteString("#\n")
	}
	return b.String()
}
