// evpn_ctrl_test.go 锁死 P1-1 EVPN-BGP 控制面行为：
// ① 实例视图 + RD/RT + BD/VNI + 三层网关绑定；
// ② BGP l2vpn-family evpn + peer enable + advertise irb；
// ③ quit 链层级（ViewBD→ViewEVPNInstance→ViewSystem；ViewL2VPNEvpn→ViewBGP）；
// ④ undo 清理；⑤ L2 设备守卫；⑥ 诚实占位（运行态 '-'）；⑦ save/reload 重建；
// ⑧ 旧 vxlan:route-distinguisher/vpn-target 模型零回归；⑨ 视图感知补全。
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// execEVPN 是测试用的便捷执行器（dt 固定 L3Switch，EVPN 为 l3SwitchOnly）。
func execEVPN(t *testing.T, st *CLIState, cmd, argStr string) string {
	t.Helper()
	args := strings.Fields(argStr)
	return ExecuteCommandOn(st, &Command{Command: cmd, Args: args}, topology.DeviceL3Switch)
}

func TestEVPNControlPlaneLifecycle(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	execEVPN(t, st, "system-view", "")

	// 进入 EVPN 实例视图
	out := execEVPN(t, st, "evpn", "vpn-instance 1")
	if !strings.Contains(out, "Enter EVPN instance view") {
		t.Fatalf("进入 EVPN 实例视图失败: %q", out)
	}
	if st.CurrentView != ViewEVPNInstance {
		t.Fatalf("CurrentView 应为 ViewEVPNInstance，实为 %q", st.CurrentView)
	}

	// RD / RT（方向前缀）
	if out := execEVPN(t, st, "route-distinguisher", "100:1"); !strings.Contains(out, "100:1") {
		t.Fatalf("route-distinguisher 失败: %q", out)
	}
	if out := execEVPN(t, st, "vpn-target", "100:1 both"); !strings.Contains(out, "both") {
		t.Fatalf("vpn-target 失败: %q", out)
	}
	if st.DeviceConfig["evpn:instance:1:rd"] != "100:1" {
		t.Fatalf("rd 键未写入: %v", st.DeviceConfig)
	}
	if st.DeviceConfig["evpn:instance:1:rt"] != "both:100:1" {
		t.Fatalf("rt 键未写入: %v", st.DeviceConfig)
	}

	// bridge-domain 进入 BD 视图
	out = execEVPN(t, st, "bridge-domain", "10")
	if !strings.Contains(out, "Enter Bridge Domain view") {
		t.Fatalf("进入 BD 视图失败: %q", out)
	}
	if st.CurrentView != ViewBD {
		t.Fatalf("CurrentView 应为 ViewBD，实为 %q", st.CurrentView)
	}
	if st.DeviceConfig["evpn:instance:1:bd"] != "10" {
		t.Fatalf("实例 BD 列表键未写入: %v", st.DeviceConfig)
	}

	// BD 内 vxlan vni
	if out := execEVPN(t, st, "vxlan", "vni 5010"); !strings.Contains(out, "5010") {
		t.Fatalf("BD vxlan vni 失败: %q", out)
	}
	if st.DeviceConfig["evpn:bd:10:vni"] != "5010" {
		t.Fatalf("BD VNI 键未写入: %v", st.DeviceConfig)
	}

	// quit 链：BD → EVPNInstance → System
	execEVPN(t, st, "quit", "")
	if st.CurrentView != ViewEVPNInstance {
		t.Fatalf("BD quit 应回 ViewEVPNInstance，实为 %q", st.CurrentView)
	}
	execEVPN(t, st, "quit", "")
	if st.CurrentView != ViewSystem {
		t.Fatalf("EVPNInstance quit 应回 ViewSystem，实为 %q", st.CurrentView)
	}
	if st.EVPNInstanceID != 0 || st.BridgeDomainID != 0 {
		t.Fatalf("quit 后实例/BD 上下文指针应清零，实为 %d/%d", st.EVPNInstanceID, st.BridgeDomainID)
	}

	// 接口视图绑定三层网关（Vlanif → BD）
	execEVPN(t, st, "interface", "Vlanif 10")
	if st.CurrentView != ViewInterface {
		t.Fatalf("应在 ViewInterface，实为 %q", st.CurrentView)
	}
	if st.CurrentSub != "Vlanif10" {
		t.Fatalf("CurrentSub 应为 Vlanif10，实为 %q", st.CurrentSub)
	}
	if out := execEVPN(t, st, "bridge-domain", "10"); !strings.Contains(out, "bound to interface") {
		t.Fatalf("接口绑定 BD 失败: %q", out)
	}
	if st.DeviceConfig["evpn:bd:10:vlanif"] != "Vlanif10" {
		t.Fatalf("BD vlanif 键未写入: %v", st.DeviceConfig)
	}
}

func TestEVPNControlPlaneBGP(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	execEVPN(t, st, "system-view", "")
	execEVPN(t, st, "bgp", "65001")
	if st.CurrentView != ViewBGP {
		t.Fatalf("应在 ViewBGP，实为 %q", st.CurrentView)
	}

	// l2vpn-family evpn
	out := execEVPN(t, st, "l2vpn-family", "evpn")
	if !strings.Contains(out, "Enter L2VPN EVPN") {
		t.Fatalf("进入 L2VPN EVPN 失败: %q", out)
	}
	if st.CurrentView != ViewL2VPNEvpn {
		t.Fatalf("应在 ViewL2VPNEvpn，实为 %q", st.CurrentView)
	}
	if st.DeviceConfig["bgp:l2vpn-evpn:enabled"] != "true" {
		t.Fatalf("l2vpn-evpn enabled 键未写入: %v", st.DeviceConfig)
	}

	// peer enable / advertise irb
	if out := execEVPN(t, st, "peer", "10.0.0.2 enable"); !strings.Contains(out, "enabled") {
		t.Fatalf("peer enable 失败: %q", out)
	}
	if st.DeviceConfig["bgp:l2vpn-evpn:peer:10.0.0.2:enabled"] != "true" {
		t.Fatalf("peer enabled 键未写入: %v", st.DeviceConfig)
	}
	if out := execEVPN(t, st, "advertise", "irb"); !strings.Contains(out, "Advertise IRB enabled") {
		t.Fatalf("advertise irb 失败: %q", out)
	}
	if st.DeviceConfig["bgp:l2vpn-evpn:advertise-irb"] != "true" {
		t.Fatalf("advertise-irb 键未写入: %v", st.DeviceConfig)
	}

	// quit 回 BGP 视图
	execEVPN(t, st, "quit", "")
	if st.CurrentView != ViewBGP {
		t.Fatalf("L2VPN EVPN quit 应回 ViewBGP，实为 %q", st.CurrentView)
	}
	// prompt
	if p := GetPrompt(st, "sw1"); !strings.Contains(p, "bgp-65001") {
		t.Fatalf("BGP 视图 prompt 异常: %q", p)
	}

	// display bgp evpn 显示真实配置态
	out = execEVPN(t, st, "display", "bgp evpn")
	for _, want := range []string{"Configured", "10.0.0.2: enabled", "Advertise IRB"} {
		if !strings.Contains(out, want) {
			t.Fatalf("display bgp evpn 缺 %q，got:\n%s", want, out)
		}
	}
}

func TestEVPNControlPlaneDisplay(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	execEVPN(t, st, "system-view", "")
	execEVPN(t, st, "evpn", "vpn-instance 1")
	execEVPN(t, st, "route-distinguisher", "100:1")
	execEVPN(t, st, "vpn-target", "100:1 both")
	execEVPN(t, st, "bridge-domain", "10")
	execEVPN(t, st, "vxlan", "vni 5010")
	execEVPN(t, st, "quit", "") // BD -> instance
	execEVPN(t, st, "quit", "") // instance -> system

	out := execEVPN(t, st, "display", "evpn")
	for _, want := range []string{"Instance 1", "RD  : 100:1", "BD 10 -> VNI 5010"} {
		if !strings.Contains(out, want) {
			t.Fatalf("display evpn 缺 %q，got:\n%s", want, out)
		}
	}
	out = execEVPN(t, st, "display", "evpn instance 1")
	for _, want := range []string{"Route Distinguisher : 100:1", "VPN Targets         : 100:1(both)", "Bridge Domains      : 10"} {
		if !strings.Contains(out, want) {
			t.Fatalf("display evpn instance 1 缺 %q，got:\n%s", want, out)
		}
	}
	out = execEVPN(t, st, "display", "evpn vni")
	if !strings.Contains(out, "BD 10 -> VNI 5010") {
		t.Fatalf("display evpn vni 缺 BD->VNI，got:\n%s", out)
	}

	// 诚实占位：运行态路由恒 '-' + 注记
	out = execEVPN(t, st, "display", "evpn routing-table")
	if !strings.Contains(out, "Routes : -") || !strings.Contains(out, "Note: EVPN runtime state") {
		t.Fatalf("display evpn routing-table 应诚实占位，got:\n%s", out)
	}
}

func TestEVPNControlPlaneQuitChainPrompts(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	st.CurrentView = ViewEVPNInstance
	st.EVPNInstanceID = 1
	if p := GetPrompt(st, "sw1"); p != "[sw1-evpn-instance-1]" {
		t.Fatalf("EVPNInstance prompt 异常: %q", p)
	}
	st.CurrentView = ViewBD
	st.BridgeDomainID = 10
	if p := GetPrompt(st, "sw1"); p != "[sw1-bd-10]" {
		t.Fatalf("BD prompt 异常: %q", p)
	}
	st.CurrentView = ViewL2VPNEvpn
	st.CurrentSub = "bgp-65001-l2vpn-evpn"
	if p := GetPrompt(st, "sw1"); p != "[sw1-bgp-65001-l2vpn-evpn]" {
		t.Fatalf("L2VPNEvpn prompt 异常: %q", p)
	}
}

func TestEVPNControlPlaneUndo(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	execEVPN(t, st, "system-view", "")
	execEVPN(t, st, "evpn", "vpn-instance 1")
	execEVPN(t, st, "route-distinguisher", "100:1")
	execEVPN(t, st, "quit", "")

	out := execEVPN(t, st, "undo", "evpn vpn-instance 1")
	if !strings.Contains(out, "removed") {
		t.Fatalf("undo evpn 失败: %q", out)
	}
	if _, ok := st.DeviceConfig["evpn:instance:1:rd"]; ok {
		t.Fatalf("undo 未清理 rd 键: %v", st.DeviceConfig)
	}
	if _, ok := st.EVPN.Instances[1]; ok {
		t.Fatalf("undo 未删除实例结构")
	}
}

func TestEVPNControlPlaneL2Guard(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceSwitch)
	execEVPN(t, st, "system-view", "")
	for _, c := range []struct{ cmd, args string }{
		{"evpn", "vpn-instance 1"},
		{"bridge-domain", "10"},
		{"l2vpn-family", "evpn"},
	} {
		out := ExecuteCommandOn(st, &Command{Command: c.cmd, Args: strings.Fields(c.args)}, topology.DeviceSwitch)
		if !strings.Contains(out, "not supported") {
			t.Fatalf("二层交换机应拒绝 %s，got: %q", c.cmd, out)
		}
	}
}

func TestEVPNControlPlaneReload(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	cfg := &topology.DeviceConfigData{Interfaces: map[string]string{
		"evpn:instance:1:id":                   "1",
		"evpn:instance:1:rd":                   "100:1",
		"evpn:instance:1:rt":                   "both:100:1",
		"evpn:instance:1:bd":                   "10",
		"evpn:bd:10:vni":                       "5010",
		"evpn:bd:10:vlanif":                    "Vlanif10",
		"bgp:l2vpn-evpn:enabled":               "true",
		"bgp:l2vpn-evpn:peer:10.0.0.2:enabled": "true",
		"bgp:l2vpn-evpn:advertise-irb":         "true",
	}}
	st.LoadFromDeviceConfigData(cfg)

	if st.EVPN.Instances[1] == nil || st.EVPN.Instances[1].RD != "100:1" {
		t.Fatalf("reload 后实例 RD 未重建: %+v", st.EVPN.Instances)
	}
	if len(st.EVPN.Instances[1].RTs) != 1 || st.EVPN.Instances[1].RTs[0] != "both:100:1" {
		t.Fatalf("reload 后 RT 未重建: %v", st.EVPN.Instances[1].RTs)
	}
	if st.EVPN.BDs[10] == nil || st.EVPN.BDs[10].VNI != 5010 || st.EVPN.BDs[10].Vlanif != "Vlanif10" {
		t.Fatalf("reload 后 BD 未重建: %+v", st.EVPN.BDs)
	}
	if st.BGP.L2VPNEvpn == nil || !st.BGP.L2VPNEvpn.Enabled {
		t.Fatalf("reload 后 L2VPNEvpn 未启用")
	}
	if st.BGP.L2VPNEvpn.Peers["10.0.0.2"] == nil || !st.BGP.L2VPNEvpn.Peers["10.0.0.2"].Enabled {
		t.Fatalf("reload 后 EVPN peer 未重建: %+v", st.BGP.L2VPNEvpn.Peers)
	}
	if !st.BGP.L2VPNEvpn.AdvertiseIRB {
		t.Fatalf("reload 后 advertise-irb 未重建")
	}

	// reload 后 display 一致
	out := execEVPN(t, st, "display", "bgp evpn")
	if !strings.Contains(out, "10.0.0.2: enabled") {
		t.Fatalf("reload 后 display bgp evpn 缺 peer，got:\n%s", out)
	}
}

func TestEVPNLegacyVxlanKeysUntouched(t *testing.T) {
	// 系统视图下 route-distinguisher / vpn-target 维持旧 vxlan: 键（display vxlan 消费），零回归。
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	execEVPN(t, st, "system-view", "")
	if out := execEVPN(t, st, "route-distinguisher", "200:1"); !strings.Contains(out, "200:1") {
		t.Fatalf("旧 route-distinguisher 失败: %q", out)
	}
	if st.DeviceConfig["vxlan:route-distinguisher"] != "200:1" {
		t.Fatalf("旧 vxlan:route-distinguisher 键未写入: %v", st.DeviceConfig)
	}
	if _, ok := st.DeviceConfig["evpn:instance:1:rd"]; ok {
		t.Fatalf("系统视图 route-distinguisher 不应写新 evpn 键")
	}
	if out := execEVPN(t, st, "vpn-target", "200:1"); !strings.Contains(out, "200:1") {
		t.Fatalf("旧 vpn-target 失败: %q", out)
	}
	if st.DeviceConfig["vxlan:vpn-target"] != "200:1" {
		t.Fatalf("旧 vxlan:vpn-target 键未写入: %v", st.DeviceConfig)
	}
}

func TestEVPNControlPlaneCompletion(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	st.CurrentView = ViewEVPNInstance
	st.EVPNInstanceID = 1
	if cands := Complete(st, SplitCommandTokens("r")); !containsCand(cands, "route-distinguisher") {
		t.Fatalf("evpn-instance 视图 'r' 应补全 route-distinguisher，got: %v", cands)
	}
	if cands := Complete(st, SplitCommandTokens("b")); !containsCand(cands, "bridge-domain") {
		t.Fatalf("evpn-instance 视图 'b' 应补全 bridge-domain，got: %v", cands)
	}
	st.CurrentView = ViewBD
	st.BridgeDomainID = 10
	if cands := Complete(st, SplitCommandTokens("v")); !containsCand(cands, "vxlan") {
		t.Fatalf("BD 视图 'v' 应补全 vxlan，got: %v", cands)
	}
	st.CurrentView = ViewL2VPNEvpn
	if cands := Complete(st, SplitCommandTokens("a")); !containsCand(cands, "advertise") {
		t.Fatalf("l2vpn-evpn 视图 'a' 应补全 advertise，got: %v", cands)
	}
	if cands := Complete(st, SplitCommandTokens("p")); !containsCand(cands, "peer") {
		t.Fatalf("l2vpn-evpn 视图 'p' 应补全 peer，got: %v", cands)
	}
}
