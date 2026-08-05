package cli

// p1f_test.go —— P1-F（VRP CLI 命令广度扩展）实现验证。
//
// 直接通过 ExecuteCommandOn + ParseCommand 驱动纯逻辑，不依赖网络/引擎。
// 覆盖：P0 九条一致性命令不再 unknown、display current-configuration VRP 风格、
// undo ospf 后 display ospf 显示 Not configured、display isis / bgp peer /
// diagnostic-information 非空、quit-cli / vlanif 引导、未知命令仍 unknown、
// 以及 ISIS 配置 save/reload 不丢。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// runOn 是测试便捷封装：解析原始命令并在指定设备类型上执行。
func runOn(state *CLIState, dt topology.DeviceType, raw string) string {
	return ExecuteCommandOn(state, ParseCommand(raw), dt)
}

// TestP0CommandsNoUnknown 验证 §0.5 的 9 条"声明但无分支"命令在对应设备类型上
// 均返回非 unknown command 的有意义回显（P0 完成判据）。
func TestP0CommandsNoUnknown(t *testing.T) {
	// isis（Router / l3Devices）
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	if out := runOn(r, topology.DeviceRouter, "isis 1"); strings.Contains(out, "unknown command") {
		t.Errorf("isis should not be unknown on router, got: %q", out)
	}

	// quit-cli（allDevices）
	r2 := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(r2, topology.DeviceRouter, "quit-cli"); strings.Contains(out, "unknown command") {
		t.Errorf("quit-cli should not be unknown, got: %q", out)
	}

	// vlanif（L3Switch / l3SwitchOnly）
	sw := NewCLIStateWithType(topology.DeviceL3Switch)
	if out := runOn(sw, topology.DeviceL3Switch, "vlanif 10"); strings.Contains(out, "unknown command") {
		t.Errorf("vlanif should not be unknown on L3Switch, got: %q", out)
	}

	// port-security（Switch / switchDevices，需接口视图）
	s2 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s2, topology.DeviceSwitch, "system-view")
	runOn(s2, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s2, topology.DeviceSwitch, "port-security enable"); strings.Contains(out, "unknown command") {
		t.Errorf("port-security should not be unknown on switch, got: %q", out)
	}

	// nslookup（PC / hostDevices）
	pc := NewCLIStateWithType(topology.DevicePC)
	pc.HostDNS = "8.8.8.8"
	if out := runOn(pc, topology.DevicePC, "nslookup a.com"); strings.Contains(out, "unknown command") {
		t.Errorf("nslookup should not be unknown on PC, got: %q", out)
	}

	// http / https / dns / ftp（Server / serverDevices）
	srv := NewCLIStateWithType(topology.DeviceServer)
	runOn(srv, topology.DeviceServer, "system-view")
	for _, c := range []string{"http enable", "https enable", "dns enable", "ftp enable"} {
		if out := runOn(srv, topology.DeviceServer, c); strings.Contains(out, "unknown command") {
			t.Errorf("%q should not be unknown on server, got: %q", c, out)
		}
	}
}

// TestDisplayCurrentConfigVRPStyle 验证 display current-configuration 输出为
// 华为 VRP 风格快照（含 # / interface），不再出现原始 key-value 直排。
func TestDisplayCurrentConfigVRPStyle(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(r, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(out, "#") {
		t.Errorf("current-configuration should contain VRP separator '#', got: %q", out)
	}
	if !strings.Contains(out, "interface") {
		t.Errorf("current-configuration should contain 'interface' blocks, got: %q", out)
	}
	if strings.Contains(out, "DeviceConfig") {
		t.Errorf("current-configuration should not contain raw DeviceConfig dump, got: %q", out)
	}
}

// TestUndoOspfNotConfigured 验证 undo ospf 1 后 display ospf 显示 Not configured。
func TestUndoOspfNotConfigured(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "ospf 1")
	runOn(r, topology.DeviceRouter, "undo ospf 1")
	out := runOn(r, topology.DeviceRouter, "display ospf")
	if !strings.Contains(out, "Not configured") {
		t.Errorf("display ospf after undo should show Not configured, got: %q", out)
	}
}

// TestDisplayIsisBgpPeerDiagnostic 验证 display isis / display bgp peer /
// display diagnostic-information 均返回非空内容。
func TestDisplayIsisBgpPeerDiagnostic(t *testing.T) {
	// display isis
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-2")
	runOn(r, topology.DeviceRouter, "import-route static")
	isisOut := runOn(r, topology.DeviceRouter, "display isis")
	if isisOut == "" || strings.Contains(isisOut, "unknown command") {
		t.Errorf("display isis should be non-empty, got: %q", isisOut)
	}
	if !strings.Contains(isisOut, "ISIS Process 1") {
		t.Errorf("display isis should show process id, got: %q", isisOut)
	}
	if !strings.Contains(isisOut, "static") {
		t.Errorf("display isis should show imported route, got: %q", isisOut)
	}

	// display bgp peer
	r2 := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r2, topology.DeviceRouter, "system-view")
	runOn(r2, topology.DeviceRouter, "bgp 65001 router-id 1.1.1.1")
	runOn(r2, topology.DeviceRouter, "peer 10.0.0.2 65002")
	peerOut := runOn(r2, topology.DeviceRouter, "display bgp peer")
	if peerOut == "" || strings.Contains(peerOut, "unknown command") {
		t.Errorf("display bgp peer should be non-empty, got: %q", peerOut)
	}
	if !strings.Contains(peerOut, "10.0.0.2") {
		t.Errorf("display bgp peer should list neighbor, got: %q", peerOut)
	}

	// display diagnostic-information
	diag := runOn(r, topology.DeviceRouter, "display diagnostic-information")
	if diag == "" || strings.Contains(diag, "unknown command") {
		t.Errorf("display diagnostic-information should be non-empty, got: %q", diag)
	}
	if !strings.Contains(diag, "Protocol Status") {
		t.Errorf("diagnostic-information should contain protocol status, got: %q", diag)
	}
}

// TestQuitCliAndVlanifGuidance 验证 quit-cli 返回会话关闭提示、vlanif 返回引导提示。
func TestQuitCliAndVlanifGuidance(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(r, topology.DeviceRouter, "quit-cli"); !strings.Contains(out, "Session closed") {
		t.Errorf("quit-cli should return Session closed, got: %q", out)
	}
	sw := NewCLIStateWithType(topology.DeviceL3Switch)
	if out := runOn(sw, topology.DeviceL3Switch, "vlanif 10"); !strings.Contains(out, "interface Vlanif") {
		t.Errorf("vlanif should return guidance to interface Vlanif, got: %q", out)
	}
}

// TestUnknownCommandStillUnknown 验证未知命令仍返回 unknown command（零回归）。
func TestUnknownCommandStillUnknown(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(r, topology.DeviceRouter, "foobar-xyz")
	if !strings.Contains(out, "unknown command") {
		t.Errorf("unknown command should remain unknown, got: %q", out)
	}
}

// TestISISPersistAcrossReload 验证 ISIS 配置随 Serialize/Load 落盘不丢（T01）。
func TestISISPersistAcrossReload(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-1-2")
	cfg := r.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.ISIS == nil || !reloaded.ISIS.Enabled {
		t.Fatalf("ISIS should persist after reload, got Enabled=%v", reloaded.ISIS != nil && reloaded.ISIS.Enabled)
	}
	if reloaded.ISIS.ProcessID != 1 {
		t.Errorf("ISIS ProcessID should be 1 after reload, got %d", reloaded.ISIS.ProcessID)
	}
	if reloaded.ISIS.NetworkType != "level-1-2" {
		t.Errorf("ISIS NetworkType should be level-1-2 after reload, got %q", reloaded.ISIS.NetworkType)
	}
}

// TestServerServiceEnabled 验证 http/https/dns/ftp enable 写出 <PROTO> service enabled 回显。
func TestServerServiceEnabled(t *testing.T) {
	srv := NewCLIStateWithType(topology.DeviceServer)
	runOn(srv, topology.DeviceServer, "system-view")
	if out := runOn(srv, topology.DeviceServer, "http enable"); !strings.Contains(out, "HTTP service enabled") {
		t.Errorf("http enable should return 'HTTP service enabled', got: %q", out)
	}
	if v := srv.DeviceConfig["http:enabled"]; v != "true" {
		t.Errorf("http:enabled should be 'true', got %q", v)
	}
}

// TestUndoUnsupported 验证 undo 不支持的子命令返回 not supported（不静默吞）。
func TestUndoUnsupported(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	out := runOn(r, topology.DeviceRouter, "undo bogus-feature")
	if !strings.Contains(out, "is not supported") {
		t.Errorf("undo unsupported feature should report not supported, got: %q", out)
	}
}
