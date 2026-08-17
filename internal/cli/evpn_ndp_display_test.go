package cli

import (
	"strings"
	"testing"
)

// TestDisplayEVPN 验证 EVPN 展示为诚实占位（AC6）：运行态恒 '-'，恒附 evpnSimNote。
func TestDisplayEVPN(t *testing.T) {
	out := regEvpnDisplay(&CLIState{}, &Command{}, "evpn", "")
	if !strings.Contains(out, "EVPN instance information") {
		t.Fatalf("dis evpn should show EVPN overview, got:\n%s", out)
	}
	if !strings.Contains(out, "Note: EVPN runtime state") {
		t.Fatalf("dis evpn should carry evpnSimNote, got:\n%s", out)
	}

	outVNI := regEvpnDisplay(&CLIState{}, &Command{}, "evpn", "vni")
	if !strings.Contains(outVNI, "VNIs") {
		t.Fatalf("dis evpn vni should mention VNIs, got:\n%s", outVNI)
	}

	// dis bgp evpn 走 regBgpDisplay 的 arg1=="evpn" 分支
	outBgp := regBgpDisplay(&CLIState{}, &Command{}, "bgp", "evpn")
	if !strings.Contains(outBgp, "BGP EVPN: Not configured") {
		t.Fatalf("dis bgp evpn should show BGP EVPN placeholder, got:\n%s", outBgp)
	}
	if !strings.Contains(outBgp, "Note: EVPN runtime state") {
		t.Fatalf("dis bgp evpn should carry evpnSimNote, got:\n%s", outBgp)
	}
}

// TestDisplayNDP 验证 NDP 展示：本端地址来自真实 IPv6 接口，邻居列恒 '-'。
func TestDisplayNDP(t *testing.T) {
	// 无 IPv6 接口 -> 明确提示
	st := &CLIState{DeviceConfig: map[string]string{}}
	out := regNdpDisplay(st, &Command{}, "ndp", "")
	if !strings.Contains(out, "Info: No IPv6 interface configured for NDP") {
		t.Fatalf("dis ndp without IPv6 iface should inform, got:\n%s", out)
	}
	if !strings.Contains(out, "Note: NDP neighbor discovery") {
		t.Fatalf("dis ndp should carry ndpSimNote, got:\n%s", out)
	}

	// 有 IPv6 接口 -> 列出本端接口，邻居列 '-'
	st2 := &CLIState{DeviceConfig: map[string]string{
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable):  "true",
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress): "2001:db8::1/64",
	}}
	out2 := regNdpDisplay(st2, &Command{}, "ndp", "")
	if !strings.Contains(out2, "GigabitEthernet0/0/1") {
		t.Fatalf("dis ndp should list IPv6 interface, got:\n%s", out2)
	}
	// 邻居列恒 '-'（NDP 邻居未仿真）
	if !strings.Contains(out2, "Neighbor") || !strings.Contains(out2, "-") {
		t.Fatalf("dis ndp neighbor column should be '-', got:\n%s", out2)
	}
}

// TestCompletionEVPNNDP 验证新增 display 子命令自动进入补全（注册表驱动，无漂移）。
func TestCompletionEVPNNDP(t *testing.T) {
	st := &CLIState{CurrentView: ViewUser}
	cands := Complete(st, SplitCommandTokens("dis ev"))
	if !containsCand(cands, "evpn") {
		t.Fatalf("dis ev should complete to evpn, got %v", cands)
	}
	cands = Complete(st, SplitCommandTokens("dis nd"))
	if !containsCand(cands, "ndp") {
		t.Fatalf("dis nd should complete to ndp, got %v", cands)
	}
}
