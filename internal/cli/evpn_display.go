// evpn_display.go EVPN 展示层（只读，诚实占位，P2 / 设计 §5 / AC6）。
//
// 入口：
//   - dis evpn [vni|peer|routing-table]      -> regEvpnDisplay
//   - dis bgp evpn [peer|routing-table|vni]   -> regBgpDisplay 的 arg1=="evpn" 分支委托 buildEVPNBGPDisplay
//
// 范围克制（设计 §10）：仅 display 占位，不补 bgp evpn 配置命令（留待 full 引擎）。
// 运行态（邻居 / VNI / 路由）恒 "-"，严禁编造邻居 IP / VNI 列表 / 路由条目。
package cli

import (
	"fmt"
	"strings"
)

// regEvpnDisplay 是 `dis evpn` 的注册表入口（handler 签名与 registry 一致）。
func regEvpnDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	// v0.12.1：二级子命令仅放行白名单（vni/peer/routing-table，无缩写支持），
	// 其余输入（含 dis evpn v / dis evpn xyz 等未支持缩写）报 unknown command
	// 指向完整命令，不再静默回退到 EVPN 概览（此前 dis evpn v 显示概览误导）。
	sub := strings.ToLower(strings.TrimSpace(arg1))
	switch sub {
	case "", "vni", "peer", "routing-table":
	default:
		return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
	}
	return buildEVPNDisplay(state, arg1)
}

// buildEVPNDisplay 渲染 EVPN 概览 / 二级子命令（vni / peer / routing-table）。
func buildEVPNDisplay(state *CLIState, sub string) string {
	sub = normalizeDisplaySubCmd2("evpn", strings.ToLower(strings.TrimSpace(sub)))
	switch sub {
	case "vni":
		return evpnVNIPlaceholder()
	case "peer":
		return evpnPeerPlaceholder()
	case "routing-table":
		return evpnRoutingPlaceholder()
	default:
		return evpnOverviewPlaceholder()
	}
}

// buildEVPNBGPDisplay 渲染 `dis bgp evpn`（BGP EVPN 地址族占位）。
func buildEVPNBGPDisplay(state *CLIState) string {
	var out strings.Builder
	out.WriteString("BGP EVPN: Not configured\n")
	out.WriteString("  EVPN address-family peers : -\n")
	out.WriteString("  EVPN routes             : -\n")
	out.WriteString("  EVPN VNIs               : -\n")
	out.WriteString(evpnSimNote())
	return out.String()
}

func evpnOverviewPlaceholder() string {
	var out strings.Builder
	out.WriteString("EVPN instance information:\n")
	out.WriteString("  Total EVPN instances : -\n")
	out.WriteString("  Total VNIs           : -\n")
	out.WriteString("  Total peers          : -\n")
	out.WriteString(evpnSimNote())
	return out.String()
}

func evpnVNIPlaceholder() string {
	var out strings.Builder
	out.WriteString("EVPN VNI status:\n")
	out.WriteString("  VNIs configured : -\n")
	out.WriteString(evpnSimNote())
	return out.String()
}

func evpnPeerPlaceholder() string {
	var out strings.Builder
	out.WriteString("EVPN peer information:\n")
	out.WriteString("  Peers : -\n")
	out.WriteString(evpnSimNote())
	return out.String()
}

func evpnRoutingPlaceholder() string {
	var out strings.Builder
	out.WriteString("EVPN routing-table:\n")
	out.WriteString("  Routes : -\n")
	out.WriteString(evpnSimNote())
	return out.String()
}

// evpnSimNote 诚实占位注记（运行态未仿真，恒 '-'）。
func evpnSimNote() string {
	return "\nNote: EVPN runtime state (neighbors/VNIs/routes) is not simulated by the lite engine; fields shown as '-' are placeholders."
}
