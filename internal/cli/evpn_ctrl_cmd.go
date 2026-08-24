// evpn_ctrl_cmd.go 是 EVPN-BGP 控制面（P1-1）的副作用出口层。
//
// 每个 handler 仅写 DeviceConfig 精确前缀键（单一事实源），并镜像到会话内
// state.EVPN / state.BGP.L2VPNEvpn 结构化字段以便 display 渲染；运行态由
// lite 引擎在后端计算时消费这些配置，本层不读运行态、不编造数字。
package cli

import (
	"fmt"
	"strings"
)

// enterEVPNInstanceView 处理系统视图下的 `evpn vpn-instance <id>`，
// 进入 EVPN 实例视图 [<dev>-evpn-instance-<id>]。
func enterEVPNInstanceView(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewSystem {
		return "Error: must be in system view"
	}
	if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "vpn-instance" {
		return "Error: usage: evpn vpn-instance <id>"
	}
	id, ok := parsePositiveID(cmd.Args[1])
	if !ok {
		return "Error: invalid EVPN instance id (positive integer)"
	}
	ensureEVPNInstance(state, id)
	state.EVPNInstanceID = id
	state.CurrentView = ViewEVPNInstance
	state.CurrentSub = fmt.Sprintf("evpn-instance-%d", id)
	return fmt.Sprintf("Enter EVPN instance view, instance %d", id)
}

// execRouteDistinguisher 处理 route-distinguisher 命令，上下文感知：
//   - ViewEVPNInstance：写 evpn:instance:<id>:rd（P1-1 新控制面）
//   - ViewSystem：写 vxlan:route-distinguisher（既有 VXLAN EVPN 旧模型，display vxlan 消费，零回归）
func execRouteDistinguisher(state *CLIState, cmd *Command) string {
	if state.CurrentView == ViewEVPNInstance {
		if state.EVPNInstanceID == 0 {
			return "Error: no EVPN instance in context"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: route-distinguisher <rd-value>"
		}
		rd := strings.Join(cmd.Args, " ")
		inst := ensureEVPNInstance(state, state.EVPNInstanceID)
		inst.RD = rd
		state.DeviceConfig[evpnInstanceFieldKey(state.EVPNInstanceID, "rd")] = rd
		return fmt.Sprintf("Route distinguisher set to %s", rd)
	}
	if state.CurrentView == ViewSystem {
		if len(cmd.Args) == 0 {
			return "Error: usage: route-distinguisher <auto|rd-value>"
		}
		rdVal := strings.Join(cmd.Args, " ")
		state.DeviceConfig["vxlan:route-distinguisher"] = rdVal
		return fmt.Sprintf("Route distinguisher set to %s", rdVal)
	}
	return "Error: must be in EVPN instance view or system view"
}

// execVPNTarget 处理 vpn-target 命令，上下文感知：
//   - ViewEVPNInstance：写 evpn:instance:<id>:rt（方向前缀 both/import/export）
//   - ViewSystem：写 vxlan:vpn-target（既有 VXLAN EVPN 旧模型，display vxlan 消费，零回归）
func execVPNTarget(state *CLIState, cmd *Command) string {
	if state.CurrentView == ViewEVPNInstance {
		if state.EVPNInstanceID == 0 {
			return "Error: no EVPN instance in context"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: vpn-target <rt-value> [import|export|both]"
		}
		value := strings.TrimSpace(cmd.Args[0])
		dir := "both"
		if len(cmd.Args) >= 2 {
			dir = rtDirection(cmd.Args[1])
		}
		entry := fmt.Sprintf("%s:%s", dir, value)
		inst := ensureEVPNInstance(state, state.EVPNInstanceID)
		inst.RTs = appendUniqueRT(inst.RTs, entry)
		state.DeviceConfig[evpnInstanceFieldKey(state.EVPNInstanceID, "rt")] = strings.Join(inst.RTs, ",")
		return fmt.Sprintf("VPN target %s set (%s)", value, dir)
	}
	if state.CurrentView == ViewSystem {
		if len(cmd.Args) == 0 {
			return "Error: usage: vpn-target <auto|rt-value>"
		}
		rtVal := strings.Join(cmd.Args, " ")
		state.DeviceConfig["vxlan:vpn-target"] = rtVal
		return fmt.Sprintf("VPN target set to %s", rtVal)
	}
	return "Error: must be in EVPN instance view or system view"
}

// appendUniqueRT 向 RT 列表追加去重条目。
func appendUniqueRT(list []string, entry string) []string {
	for _, e := range list {
		if e == entry {
			return list
		}
	}
	return append(list, entry)
}

// enterBDView 处理 bridge-domain 命令，上下文感知：
//   - ViewEVPNInstance：将 BD 关联进实例并进入 BD 视图 [<dev>-bd-<bd-id>]
//   - ViewInterface：将 BD 绑定到当前三层网关接口（写 evpn:bd:<bd-id>:vlanif）
func enterBDView(state *CLIState, cmd *Command) string {
	if len(cmd.Args) < 1 {
		return "Error: usage: bridge-domain <bd-id>"
	}
	bdID, ok := parsePositiveID(cmd.Args[0])
	if !ok {
		return "Error: invalid Bridge Domain id (positive integer)"
	}
	if state.CurrentView == ViewEVPNInstance {
		if state.EVPNInstanceID == 0 {
			return "Error: no EVPN instance in context"
		}
		inst := ensureEVPNInstance(state, state.EVPNInstanceID)
		inst.BDs = appendUniqueInt(inst.BDs, bdID)
		state.DeviceConfig[evpnInstanceFieldKey(state.EVPNInstanceID, "bd")] = joinInts(inst.BDs)
		ensureBridgeDomain(state, bdID)
		state.BridgeDomainID = bdID
		state.CurrentView = ViewBD
		state.CurrentSub = fmt.Sprintf("bd-%d", bdID)
		return fmt.Sprintf("Enter Bridge Domain view, BD %d", bdID)
	}
	if state.CurrentView == ViewInterface {
		bd := ensureBridgeDomain(state, bdID)
		bd.Vlanif = state.CurrentSub
		state.DeviceConfig[evpnBDFieldKey(bdID, "vlanif")] = state.CurrentSub
		return fmt.Sprintf("Bridge Domain %d bound to interface %s", bdID, state.CurrentSub)
	}
	return "Error: bridge-domain must be used in EVPN instance view or interface view"
}

// appendUniqueInt 向 int 列表追加去重元素。
func appendUniqueInt(list []int, v int) []int {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// execBDVxlanVNI 处理 BD 视图下的 `vxlan vni <vni>`（写 evpn:bd:<bd-id>:vni）。
func execBDVxlanVNI(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewBD {
		return "Error: must be in Bridge Domain view"
	}
	if state.BridgeDomainID == 0 {
		return "Error: no Bridge Domain in context"
	}
	if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "vni" {
		return "Error: usage: vxlan vni <vni-id>"
	}
	vni, err := parseIntStrict(cmd.Args[1])
	if err != nil {
		return "Error: invalid VNI"
	}
	bd := ensureBridgeDomain(state, state.BridgeDomainID)
	bd.VNI = vni
	state.DeviceConfig[evpnBDFieldKey(state.BridgeDomainID, "vni")] = fmt.Sprintf("%d", vni)
	return fmt.Sprintf("VXLAN VNI %d configured for BD %d", vni, state.BridgeDomainID)
}

// enterL2VPNEvpnView 处理 BGP 视图下的 `l2vpn-family evpn`，
// 进入 L2VPN EVPN 子视图 [<dev>-bgp-<as>-l2vpn-evpn]。
func enterL2VPNEvpnView(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewBGP {
		return "Error: must be in BGP view"
	}
	if len(cmd.Args) < 1 || strings.ToLower(cmd.Args[0]) != "evpn" {
		return "Error: usage: l2vpn-family evpn"
	}
	if state.BGP.L2VPNEvpn == nil {
		state.BGP.L2VPNEvpn = &L2VPNEvpnAF{Peers: make(map[string]*EvpnPeer)}
	}
	state.BGP.L2VPNEvpn.Enabled = true
	state.DeviceConfig[bgpL2NS+"enabled"] = "true"
	state.CurrentView = ViewL2VPNEvpn
	state.CurrentSub = fmt.Sprintf("bgp-%d-l2vpn-evpn", state.BGP.ASNumber)
	return fmt.Sprintf("Enter L2VPN EVPN address-family view, AS %d", state.BGP.ASNumber)
}

// execL2VPNPeerEnable 处理 L2VPN EVPN 视图下的 `peer <ip> enable`。
func execL2VPNPeerEnable(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewL2VPNEvpn {
		return "Error: must be in L2VPN EVPN view"
	}
	if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[1]) != "enable" {
		return "Error: usage: peer <ip> enable"
	}
	ip := cmd.Args[0]
	if state.BGP.L2VPNEvpn == nil {
		state.BGP.L2VPNEvpn = &L2VPNEvpnAF{Peers: make(map[string]*EvpnPeer)}
	}
	state.BGP.L2VPNEvpn.Peers[ip] = &EvpnPeer{IP: ip, Enabled: true}
	state.DeviceConfig[bgpL2PeerEnabledKey(ip)] = "true"
	return fmt.Sprintf("BGP EVPN peer %s enabled", ip)
}

// execL2VNAdvertiseIRB 处理 L2VPN EVPN 视图下的 `advertise irb`。
func execL2VNAdvertiseIRB(state *CLIState, cmd *Command) string {
	if state.CurrentView != ViewL2VPNEvpn {
		return "Error: must be in L2VPN EVPN view"
	}
	if len(cmd.Args) < 1 || strings.ToLower(cmd.Args[0]) != "irb" {
		return "Error: usage: advertise irb"
	}
	if state.BGP.L2VPNEvpn == nil {
		state.BGP.L2VPNEvpn = &L2VPNEvpnAF{Peers: make(map[string]*EvpnPeer)}
	}
	state.BGP.L2VPNEvpn.AdvertiseIRB = true
	state.DeviceConfig[bgpL2NS+"advertise-irb"] = "true"
	return "Advertise IRB enabled"
}

// undoEVPNInstance 反向清理某 EVPN 实例的全部键（系统视图 undo evpn vpn-instance <id>）。
func undoEVPNInstance(state *CLIState, args []string) (string, bool) {
	// args: ["evpn", "vpn-instance", "<id>"]
	if len(args) < 3 || strings.ToLower(args[1]) != "vpn-instance" {
		return "Error: usage: undo evpn vpn-instance <id>", true
	}
	id, ok := parsePositiveID(args[2])
	if !ok {
		return "Error: invalid EVPN instance id", true
	}
	prefix := fmt.Sprintf("%s%d:", evpnInstanceNS, id)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			delete(state.DeviceConfig, k)
		}
	}
	delete(state.EVPN.Instances, id)
	if state.EVPNInstanceID == id {
		state.EVPNInstanceID = 0
	}
	return fmt.Sprintf("EVPN instance %d removed", id), true
}

// buildEVPNControlPlaneSavedConfig 渲染 EVPN-BGP 控制面配置块（供 formatProtocolBlocks 汇出）。
// 仅渲染配置态（诚实占位，lite 引擎不做实际选路/转发）。
func buildEVPNControlPlaneSavedConfig(state *CLIState) string {
	var b strings.Builder
	// EVPN 实例
	ids := sortedIntKeys(state.EVPN.Instances)
	for _, id := range ids {
		inst := state.EVPN.Instances[id]
		b.WriteString(fmt.Sprintf(" evpn vpn-instance %d\n", id))
		if inst.RD != "" {
			b.WriteString(fmt.Sprintf("  route-distinguisher %s\n", inst.RD))
		}
		for _, rt := range inst.RTs {
			dir, val := parseRTEntry(rt)
			b.WriteString(fmt.Sprintf("  vpn-target %s %s\n", val, dir))
		}
		for _, bdID := range inst.BDs {
			b.WriteString(fmt.Sprintf("  bridge-domain %d\n", bdID))
			if bd, ok := state.EVPN.BDs[bdID]; ok && bd.VNI != 0 {
				b.WriteString(fmt.Sprintf("   vxlan vni %d\n", bd.VNI))
			}
		}
	}
	// BGP L2VPN EVPN 地址族
	if state.BGP.L2VPNEvpn != nil && state.BGP.L2VPNEvpn.Enabled {
		b.WriteString(fmt.Sprintf(" bgp %d\n", state.BGP.ASNumber))
		b.WriteString("  l2vpn-family evpn\n")
		peerIPs := sortedStrKeys(state.BGP.L2VPNEvpn.Peers)
		for _, ip := range peerIPs {
			b.WriteString(fmt.Sprintf("   peer %s enable\n", ip))
		}
		if state.BGP.L2VPNEvpn.AdvertiseIRB {
			b.WriteString("   advertise irb\n")
		}
	}
	return b.String()
}

// parseIntStrict 解析整数（复用既有 parseNum 语义，避免使用未导入符号）。
func parseIntStrict(s string) (int, error) {
	return parseNum(s)
}
