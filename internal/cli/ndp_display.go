// ndp_display.go NDP（邻居发现协议）展示层（只读，诚实占位，P2 / 设计 §5）。
//
// 入口：dis ndp -> regNdpDisplay
//
// 数据来源：读真实接口 IPv6 地址（ipv6 键派生）列出本端链路本地地址；邻居列
// 恒 "-"（NDP 邻居发现未仿真，严禁编造对端 MAC / IPv6）。末尾恒附 ndpSimNote()。
package cli

import (
	"fmt"
	"strings"
)

// regNdpDisplay 是 `dis ndp` 的注册表入口。
func regNdpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	return buildNDPDisplay(state)
}

// buildNDPDisplay 渲染 NDP 邻居表（本端地址来自真实 IPv6 接口，邻居列恒 '-'）。
func buildNDPDisplay(state *CLIState) string {
	ifaces := collectIPv6Interfaces(state)
	var out strings.Builder
	out.WriteString("NDP Neighbor Table:\n")
	if len(ifaces) == 0 {
		out.WriteString("Info: No IPv6 interface configured for NDP.\n")
		out.WriteString(ndpSimNote())
		return out.String()
	}
	out.WriteString(fmt.Sprintf("%-20s %-42s %s\n", "Interface", "IPv6 Address", "Neighbor"))
	for _, iface := range ifaces {
		view := readIPv6AddressView(state, iface)
		addr := view.LinkLocal
		if !view.Enable || !view.HasMAC {
			addr = "-"
		}
		out.WriteString(fmt.Sprintf("%-20s %-42s %s\n", iface, addr, "-"))
	}
	out.WriteString(ndpSimNote())
	return out.String()
}

// ndpSimNote 诚实占位注记。
func ndpSimNote() string {
	return "\nNote: NDP neighbor discovery is not simulated by the lite engine; neighbor entries shown as '-' are placeholders."
}
