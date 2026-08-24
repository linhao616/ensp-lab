// evpn_ctrl_display.go 是 EVPN-BGP 控制面（P1-1）的渲染层。
//
// 升级既有 evpn_display.go 的占位函数：display evpn / display bgp evpn 现在渲染
// 真实**配置态**（实例 / RD / RT / BD / VNI / 三层网关 / BGP EVPN peer），运行态
// （Type-2/3/5 路由、邻居会话）仍恒 '-' 并附 evpnCtrlSimNote 诚实注记，绝不编造数字。
package cli

import (
	"fmt"
	"strings"
)

// buildEVPNDisplay 渲染 EVPN 概览 / 二级子命令（vni / peer / routing-table / instance <id>）。
// 读取会话内 state.EVPN 结构化字段（由 cmd 层镜像，reload 后由 LoadFromDeviceConfigData 重建）。
func buildEVPNDisplay(state *CLIState, sub string, instanceID int) string {
	switch sub {
	case "vni":
		return evpnVNIDisplay(state)
	case "peer":
		// EVPN 对等体即 BGP EVPN 对等体，统一在 display bgp evpn 呈现；此处仅注记。
		var out strings.Builder
		out.WriteString("EVPN peer information:\n")
		out.WriteString("  Peers : - (see 'display bgp evpn' for BGP EVPN peers)\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	case "routing-table":
		return evpnRoutingPlaceholder()
	case "instance":
		return evpnInstanceDisplay(state, instanceID)
	default:
		return evpnOverviewDisplay(state)
	}
}

// evpnOverviewDisplay 概览：列出已配置的实例 / BD / VNI（真实配置态）。
func evpnOverviewDisplay(state *CLIState) string {
	var out strings.Builder
	out.WriteString("EVPN instance information:\n")
	if state.EVPN == nil {
		out.WriteString("  Total EVPN instances : 0\n")
		out.WriteString("  Total VNIs           : 0\n")
		out.WriteString("  (no EVPN instance configured)\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	instIDs := sortedIntKeys(state.EVPN.Instances)
	if len(instIDs) == 0 {
		out.WriteString("  Total EVPN instances : 0\n")
		out.WriteString("  Total VNIs           : 0\n")
		out.WriteString("  (no EVPN instance configured)\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	out.WriteString(fmt.Sprintf("  Total EVPN instances : %d\n", len(instIDs)))
	totalVNI := 0
	for _, id := range instIDs {
		inst := state.EVPN.Instances[id]
		out.WriteString(fmt.Sprintf("  Instance %d:\n", id))
		if inst.RD != "" {
			out.WriteString(fmt.Sprintf("    RD  : %s\n", inst.RD))
		}
		if len(inst.RTs) > 0 {
			rts := make([]string, 0, len(inst.RTs))
			for _, rt := range inst.RTs {
				dir, val := parseRTEntry(rt)
				rts = append(rts, fmt.Sprintf("%s(%s)", val, dir))
			}
			out.WriteString(fmt.Sprintf("    RT  : %s\n", strings.Join(rts, ", ")))
		}
		if len(inst.BDs) > 0 {
			out.WriteString(fmt.Sprintf("    BDs : %s\n", joinInts(inst.BDs)))
			for _, bdID := range inst.BDs {
				if bd, ok := state.EVPN.BDs[bdID]; ok && bd.VNI != 0 {
					totalVNI++
					out.WriteString(fmt.Sprintf("      BD %d -> VNI %d\n", bdID, bd.VNI))
				}
			}
		}
	}
	out.WriteString(fmt.Sprintf("  Total VNIs           : %d\n", totalVNI))
	out.WriteString(evpnCtrlSimNote())
	return out.String()
}

// evpnInstanceDisplay 渲染单个实例详情（display evpn instance <id>）。
func evpnInstanceDisplay(state *CLIState, instanceID int) string {
	var out strings.Builder
	var inst *EVPNInstance
	ok := false
	if state.EVPN != nil {
		inst, ok = state.EVPN.Instances[instanceID]
	}
	if !ok {
		out.WriteString(fmt.Sprintf("EVPN instance %d does not exist\n", instanceID))
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	out.WriteString(fmt.Sprintf("EVPN instance %d:\n", instanceID))
	if inst.RD != "" {
		out.WriteString(fmt.Sprintf("  Route Distinguisher : %s\n", inst.RD))
	}
	if len(inst.RTs) > 0 {
		rts := make([]string, 0, len(inst.RTs))
		for _, rt := range inst.RTs {
			dir, val := parseRTEntry(rt)
			rts = append(rts, fmt.Sprintf("%s(%s)", val, dir))
		}
		out.WriteString(fmt.Sprintf("  VPN Targets         : %s\n", strings.Join(rts, ", ")))
	}
	if len(inst.BDs) > 0 {
		out.WriteString(fmt.Sprintf("  Bridge Domains      : %s\n", joinInts(inst.BDs)))
		for _, bdID := range inst.BDs {
			if bd, ok := state.EVPN.BDs[bdID]; ok {
				out.WriteString(fmt.Sprintf("    BD %d: VNI=%d", bdID, bd.VNI))
				if len(bd.VLANs) > 0 {
					out.WriteString(fmt.Sprintf(", VLANs=%s", joinInts(bd.VLANs)))
				}
				if bd.Vlanif != "" {
					out.WriteString(fmt.Sprintf(", Gateway=%s", bd.Vlanif))
				}
				out.WriteString("\n")
			}
		}
	}
	out.WriteString(evpnCtrlSimNote())
	return out.String()
}

// evpnVNIDisplay 渲染 BD↔VNI 映射（display evpn vni）。
func evpnVNIDisplay(state *CLIState) string {
	var out strings.Builder
	out.WriteString("EVPN VNI status:\n")
	if state.EVPN == nil {
		out.WriteString("  VNIs configured : 0\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	bdIDs := sortedIntKeys(state.EVPN.BDs)
	if len(bdIDs) == 0 {
		out.WriteString("  VNIs configured : 0\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	out.WriteString(fmt.Sprintf("  VNIs configured : %d\n", len(bdIDs)))
	for _, bdID := range bdIDs {
		bd := state.EVPN.BDs[bdID]
		out.WriteString(fmt.Sprintf("    BD %d -> VNI %d", bdID, bd.VNI))
		if len(bd.VLANs) > 0 {
			out.WriteString(fmt.Sprintf(", VLANs=%s", joinInts(bd.VLANs)))
		}
		if bd.Vlanif != "" {
			out.WriteString(fmt.Sprintf(", Gateway=%s", bd.Vlanif))
		}
		out.WriteString("\n")
	}
	out.WriteString(evpnCtrlSimNote())
	return out.String()
}

// buildEVPNBGPDisplay 渲染 `dis bgp evpn`（BGP EVPN 地址族配置态）。
// 读取 state.BGP.L2VPNEvpn；peer IP 为真实配置，运行态路由条目恒 '-'。
func buildEVPNBGPDisplay(state *CLIState) string {
	var out strings.Builder
	if state.BGP == nil {
		out.WriteString("BGP EVPN: Not configured\n")
		out.WriteString("  EVPN address-family peers : -\n")
		out.WriteString("  EVPN routes             : -\n")
		out.WriteString("  EVPN VNIs               : -\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	af := state.BGP.L2VPNEvpn
	if af == nil || !af.Enabled {
		out.WriteString("BGP EVPN: Not configured\n")
		out.WriteString("  EVPN address-family peers : -\n")
		out.WriteString("  EVPN routes             : -\n")
		out.WriteString("  EVPN VNIs               : -\n")
		out.WriteString(evpnCtrlSimNote())
		return out.String()
	}
	out.WriteString("BGP EVPN: Configured\n")
	out.WriteString(fmt.Sprintf("  Address-family          : L2VPN EVPN (AS %d)\n", state.BGP.ASNumber))
	peerIPs := sortedStrKeys(af.Peers)
	if len(peerIPs) == 0 {
		out.WriteString("  EVPN address-family peers : (none enabled)\n")
	} else {
		out.WriteString(fmt.Sprintf("  EVPN address-family peers : %d\n", len(peerIPs)))
		for _, ip := range peerIPs {
			out.WriteString(fmt.Sprintf("    %s: enabled\n", ip))
		}
	}
	out.WriteString(fmt.Sprintf("  Advertise IRB           : %v\n", af.AdvertiseIRB))
	out.WriteString("  EVPN routes             : -\n")
	// VNI 列表来自关联 BD（经实例）
	out.WriteString("  EVPN VNIs               : -\n")
	out.WriteString(evpnCtrlSimNote())
	return out.String()
}
