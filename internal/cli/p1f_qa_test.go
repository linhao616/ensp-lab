package cli

// p1f_qa_test.go —— 独立 QA 复核（fresh eyes，第二层验证）。
//
// 不修改工程师的 p1f_test.go，仅在本文件新增/补强断言，聚焦：
//  1. P0 九条命令在「声明设备」上不 unknown，且校验关键字段（非只断言非空）；
//  2. P0 九条命令在「非声明设备」上被能力矩阵/case 守卫正确拒绝（不误执行）；
//  3. isis 真实配置 → display isis 关键字段；reset/reload 后回到未配置态；
//  4. undo 系统视图全分支（ospf/vlan/acl/stp/dhcp/bgp/ipv6/notexist）清 state 且不 panic；
//  5. display current-configuration VRP 风格 + 协议摘要块，且不再直排 DeviceConfig 键；
//  6. display bgp peer / diagnostic-information 关键字段；
//  7. tracert 兜底（风险1）：nil 钩子不 panic、不硬编码 2 跳；mock 钩子分支被调用且不崩溃；
//  8. 序列化不丢（风险2）：ISIS 配置随 save/reload 保留 + 反例（无 isis 键则未配置）；
//  9. 未知命令兜底行为不变。
//
// 直接通过 ExecuteCommandOn + ParseCommand 驱动纯逻辑，不依赖网络/引擎。

import (
	"strings"
	"testing"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// TestQAP0DeclaredDevices 验证 §0.5 九条命令在声明设备类型上返回非 unknown，
// 并对关键字段做强断言（不只用"非空"糊弄）。
func TestQAP0DeclaredDevices(t *testing.T) {
	// isis（Router / l3Devices）：进视图 + 写 DeviceConfig 键
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	if out := runOn(r, topology.DeviceRouter, "isis 1"); strings.Contains(out, "unknown command") {
		t.Fatalf("isis should not be unknown on router, got: %q", out)
	}
	if r.CurrentView != ViewISIS {
		t.Errorf("isis 1 should enter ViewISIS, got CurrentView=%s", r.CurrentView)
	}
	if r.ISIS == nil || !r.ISIS.Enabled || r.ISIS.ProcessID != 1 {
		t.Errorf("isis 1 should set ISIS.Enabled=true ProcessID=1, got %+v", r.ISIS)
	}
	if r.DeviceConfig["isis:enabled"] != "true" || r.DeviceConfig["isis:process-id"] != "1" {
		t.Errorf("isis 1 should write isis:enabled/process-id keys, got %v", r.DeviceConfig)
	}

	// quit-cli（allDevices）
	r2 := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(r2, topology.DeviceRouter, "quit-cli"); !strings.Contains(out, "Session closed") {
		t.Errorf("quit-cli should return Session closed, got: %q", out)
	}

	// vlanif（L3Switch / l3SwitchOnly）
	sw := NewCLIStateWithType(topology.DeviceL3Switch)
	if out := runOn(sw, topology.DeviceL3Switch, "vlanif 10"); !strings.Contains(out, "interface Vlanif") {
		t.Errorf("vlanif should guide to interface Vlanif, got: %q", out)
	}

	// port-security（Switch / switchDevices，需接口视图）：校验键写入
	s2 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s2, topology.DeviceSwitch, "system-view")
	runOn(s2, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s2, topology.DeviceSwitch, "port-security enable"); strings.Contains(out, "unknown command") {
		t.Fatalf("port-security should not be unknown on switch, got: %q", out)
	}
	if s2.DeviceConfig["interface:GigabitEthernet0/0/1:port-security"] != "enable" {
		t.Errorf("port-security enable should write interface key, got %v", s2.DeviceConfig)
	}
	// 顶层 port-security 不在接口视图应被守卫拒绝
	s3 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s3, topology.DeviceSwitch, "system-view")
	if out := runOn(s3, topology.DeviceSwitch, "port-security enable"); !strings.Contains(out, "interface view") {
		t.Errorf("port-security outside interface view should be rejected, got: %q", out)
	}

	// nslookup（PC / hostDevices）：有 DNS 解析、无 DNS 提示
	pc := NewCLIStateWithType(topology.DevicePC)
	pc.HostDNS = "8.8.8.8"
	out := runOn(pc, topology.DevicePC, "nslookup a.com")
	if strings.Contains(out, "unknown command") || !strings.Contains(out, "a.com") {
		t.Errorf("nslookup with DNS should resolve, got: %q", out)
	}
	pc2 := NewCLIStateWithType(topology.DevicePC)
	if out := runOn(pc2, topology.DevicePC, "nslookup a.com"); !strings.Contains(out, "DNS server not configured") {
		t.Errorf("nslookup without DNS should report not configured, got: %q", out)
	}

	// http/https/dns/ftp（Server / serverDevices）：各自 service enabled + 键
	srv := NewCLIStateWithType(topology.DeviceServer)
	runOn(srv, topology.DeviceServer, "system-view")
	for _, c := range []string{"http enable", "https enable", "dns enable", "ftp enable"} {
		out := runOn(srv, topology.DeviceServer, c)
		proto := strings.ToUpper(strings.Fields(c)[0])
		if !strings.Contains(out, proto+" service enabled") {
			t.Errorf("%q should return '%s service enabled', got: %q", c, proto, out)
		}
		key := strings.Fields(c)[0] + ":enabled"
		if srv.DeviceConfig[key] != "true" {
			t.Errorf("%q should write %s=true, got %q", c, key, srv.DeviceConfig[key])
		}
	}
	// 不在系统视图启用应被守卫拒绝
	srv2 := NewCLIStateWithType(topology.DeviceServer)
	if out := runOn(srv2, topology.DeviceServer, "http enable"); !strings.Contains(out, "system view") {
		t.Errorf("http enable outside system view should be rejected, got: %q", out)
	}
}

// TestQAP0NonDeclaredDevices 验证在非声明设备上，九条命令被能力矩阵/case 守卫正确
// 拒绝（返回 not supported 或 unknown command），且绝不误执行（如 PC 上敲 isis 不应进视图）。
func TestQAP0NonDeclaredDevices(t *testing.T) {
	// isis 在 PC（hostDevices，不在 l3Devices）→ 拒绝，且不进 ISIS 视图
	pc := NewCLIStateWithType(topology.DevicePC)
	pc.HostDNS = "8.8.8.8"
	out := runOn(pc, topology.DevicePC, "isis 1")
	if pc.CurrentView == ViewISIS {
		t.Errorf("isis on PC must NOT enter ISIS view")
	}
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("isis on PC should be rejected, got: %q", out)
	}

	// http 在 PC（serverDevices 之外）→ 拒绝
	pc2 := NewCLIStateWithType(topology.DevicePC)
	out = runOn(pc2, topology.DevicePC, "http enable")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("http on PC should be rejected, got: %q", out)
	}

	// port-security 在 Router（switchDevices 之外）→ 拒绝
	rt := NewCLIStateWithType(topology.DeviceRouter)
	runOn(rt, topology.DeviceRouter, "system-view")
	out = runOn(rt, topology.DeviceRouter, "port-security enable")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("port-security on router should be rejected, got: %q", out)
	}

	// vlanif 在 Router（l3SwitchOnly 之外）→ 拒绝
	rt2 := NewCLIStateWithType(topology.DeviceRouter)
	out = runOn(rt2, topology.DeviceRouter, "vlanif 10")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("vlanif on router should be rejected, got: %q", out)
	}

	// nslookup 在 Router（hostDevices 之外）→ 拒绝
	rt3 := NewCLIStateWithType(topology.DeviceRouter)
	out = runOn(rt3, topology.DeviceRouter, "nslookup a.com")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("nslookup on router should be rejected, got: %q", out)
	}

	// quit-cli 在 PC（allDevices）→ 允许（正向：确认 allDevices 声明生效）
	pc3 := NewCLIStateWithType(topology.DevicePC)
	if out := runOn(pc3, topology.DevicePC, "quit-cli"); !strings.Contains(out, "Session closed") {
		t.Errorf("quit-cli should work on PC (allDevices), got: %q", out)
	}
}

// TestQAIsisRealConfig 验证 isis 真实配置后 display isis 显示关键字段。
func TestQAIsisRealConfig(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-2")
	runOn(r, topology.DeviceRouter, "import-route static")
	runOn(r, topology.DeviceRouter, "import-route ospf")

	out := runOn(r, topology.DeviceRouter, "display isis")
	for _, want := range []string{"ISIS Process 1", "Network Type: level-2", "State: Running", "Neighbors: 0", "static", "ospf"} {
		if !strings.Contains(out, want) {
			t.Errorf("display isis missing field %q, got: %q", want, out)
		}
	}

	// network level-1-2 改变网络类型
	runOn(r, topology.DeviceRouter, "network level-1-2")
	out = runOn(r, topology.DeviceRouter, "display isis")
	if !strings.Contains(out, "Network Type: level-1-2") {
		t.Errorf("display isis should reflect level-1-2, got: %q", out)
	}
}

// TestQAIsisNotConfigured 验证未配置/重置后 display isis 回到未配置态。
func TestQAIsisNotConfigured(t *testing.T) {
	// 全新状态（never configured）
	fresh := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(fresh, topology.DeviceRouter, "display isis"); !strings.Contains(out, "Not configured") {
		t.Errorf("fresh display isis should be Not configured, got: %q", out)
	}

	// 配置后序列化，再用一个「不含 isis 键」的快照重载 → 回到未配置
	cfg := &topology.DeviceConfigData{
		DeviceName: "R1",
		Interfaces: map[string]string{"sysname": "R1"},
	}
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.ISIS == nil || reloaded.ISIS.Enabled {
		t.Errorf("reload without isis keys should give ISIS.Enabled=false, got %+v", reloaded.ISIS)
	}
	if out := runOn(reloaded, topology.DeviceRouter, "display isis"); !strings.Contains(out, "Not configured") {
		t.Errorf("reloaded (no isis) display isis should be Not configured, got: %q", out)
	}
}

// TestQAUndoSystemFeature 验证 undo 系统视图各分支清理对应 state，且不 panic。
func TestQAUndoSystemFeature(t *testing.T) {
	// undo ospf 1 → OSPF 清理
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "ospf 1")
	runOn(r, topology.DeviceRouter, "undo ospf 1")
	if r.OSPF.Enabled {
		t.Errorf("undo ospf should disable OSPF")
	}
	if out := runOn(r, topology.DeviceRouter, "display ospf"); !strings.Contains(out, "Not configured") {
		t.Errorf("display ospf after undo should be Not configured, got: %q", out)
	}

	// undo vlan 10 → VLAN 删除
	runOn(r, topology.DeviceRouter, "undo vlan 10")
	if _, ok := r.VLANs[10]; ok {
		t.Errorf("undo vlan 10 should delete VLAN 10")
	}

	// undo acl 2000 → 不 panic，返回 removed
	if out := runOn(r, topology.DeviceRouter, "undo acl 2000"); !strings.Contains(out, "removed") {
		t.Errorf("undo acl 2000 should report removed, got: %q", out)
	}

	// undo stp → STP 禁用（方案 A：写 stp:enabled=false，清理全部 stp 键）
	runOn(r, topology.DeviceRouter, "undo stp")
	if r.DeviceConfig["stp:enabled"] != "false" {
		t.Errorf("undo stp should disable STP (stp:enabled should be false)")
	}

	// undo dhcp → DHCP 禁用
	runOn(r, topology.DeviceRouter, "undo dhcp")
	if r.DHCP == nil || r.DHCP.Enabled {
		t.Errorf("undo dhcp should disable DHCP")
	}

	// undo bgp → BGP 清理
	runOn(r, topology.DeviceRouter, "bgp 65001 router-id 1.1.1.1")
	runOn(r, topology.DeviceRouter, "quit") // 退出 BGP 视图回到系统视图
	runOn(r, topology.DeviceRouter, "undo bgp 65001")
	if r.BGP.Enabled {
		t.Errorf("undo bgp should disable BGP")
	}

	// undo ipv6 → 删除 ipv6:enabled 键
	r.DeviceConfig["ipv6:enabled"] = "true"
	runOn(r, topology.DeviceRouter, "undo ipv6")
	if _, ok := r.DeviceConfig["ipv6:enabled"]; ok {
		t.Errorf("undo ipv6 should remove ipv6:enabled key")
	}

	// undo 不支持的子命令 → not supported，不 panic
	if out := runOn(r, topology.DeviceRouter, "undo notexistxyz"); !strings.Contains(out, "is not supported") {
		t.Errorf("undo notexistxyz should report not supported, got: %q", out)
	}

	// undo 无参数 → incomplete command
	if out := runOn(r, topology.DeviceRouter, "undo"); !strings.Contains(out, "incomplete command") {
		t.Errorf("undo with no args should report incomplete, got: %q", out)
	}
}

// TestQACurrentConfigurationVRP 验证 display current-configuration 为 VRP 风格且含协议块，
// 且不再直排内部 DeviceConfig 键（如 isis:enabled）。
func TestQACurrentConfigurationVRP(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-2")

	out := runOn(r, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(out, "#") {
		t.Errorf("current-configuration should contain VRP separator '#', got: %q", out)
	}
	if !strings.Contains(out, "interface") {
		t.Errorf("current-configuration should contain interface blocks, got: %q", out)
	}
	// 协议摘要块应出现 isis 启用信息
	if !strings.Contains(out, "isis 1") || !strings.Contains(out, "network level-2") {
		t.Errorf("current-configuration should include isis protocol block, got: %q", out)
	}
	// 不应再直排内部 key（以冒号分隔的原始 DeviceConfig 键）
	if strings.Contains(out, "isis:enabled") || strings.Contains(out, "isis:process-id") {
		t.Errorf("current-configuration should not dump raw DeviceConfig keys, got: %q", out)
	}
}

// TestQABGPPeerDisplay 验证 display bgp peer 含邻居明细字段。
func TestQABGPPeerDisplay(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "bgp 65001 router-id 1.1.1.1")
	runOn(r, topology.DeviceRouter, "peer 10.0.0.2 65002")
	out := runOn(r, topology.DeviceRouter, "display bgp peer")
	for _, want := range []string{"Peer", "RemoteAS", "10.0.0.2", "Established", "EBGP"} {
		if !strings.Contains(out, want) {
			t.Errorf("display bgp peer missing field %q, got: %q", want, out)
		}
	}

	// BGP 未启用 → Not configured
	r2 := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(r2, topology.DeviceRouter, "display bgp peer"); !strings.Contains(out, "Not configured") {
		t.Errorf("display bgp peer without BGP should be Not configured, got: %q", out)
	}
}

// TestQADiagnosticInfo 验证 display diagnostic-information 聚合关键协议状态。
func TestQADiagnosticInfo(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	out := runOn(r, topology.DeviceRouter, "display diagnostic-information")
	for _, want := range []string{"Diagnostic Information", "Protocol Status", "OSPF:", "BGP :", "ISIS:", "STP :"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagnostic-information missing %q, got: %q", want, out)
		}
	}
	// isis 已配置 → Running
	if !strings.Contains(out, "ISIS: Running") {
		t.Errorf("diagnostic-information should show ISIS Running, got: %q", out)
	}
}

// TestQATracertFallback 验证 tracert 兜底（风险1）：nil 钩子不 panic、不硬编码 2 跳；
// mock 钩子分支被调用且不崩溃。
func TestQATracertFallback(t *testing.T) {
	// nil 钩子：在支持 tracert 的设备（Server）上敲，应返回合理提示而非 2 跳假路径
	srv := NewCLIStateWithType(topology.DeviceServer)
	srv.ResolveTraceroute = nil
	out := runOn(srv, topology.DeviceServer, "tracert 8.8.8.8")
	if !strings.Contains(out, "no result from engine") {
		t.Errorf("tracert with nil hook should report no result from engine, got: %q", out)
	}

	// mock 钩子返回 nil 结果：应走钩子分支、不 panic
	hookCalled := false
	gotTarget := ""
	srv2 := NewCLIStateWithType(topology.DeviceServer)
	srv2.ResolveTraceroute = func(target string) *sim.TracerouteResult {
		hookCalled = true
		gotTarget = target
		return nil
	}
	out = runOn(srv2, topology.DeviceServer, "tracert 1.1.1.1")
	if !hookCalled {
		t.Errorf("tracert should invoke ResolveTraceroute hook")
	}
	if gotTarget != "1.1.1.1" {
		t.Errorf("hook should receive target 1.1.1.1, got %q", gotTarget)
	}
	if !strings.Contains(out, "no result from engine") {
		t.Errorf("tracert with nil-result hook should report no result, got: %q", out)
	}

	// mock 钩子返回真实结果：应渲染路径且不崩溃
	srv3 := NewCLIStateWithType(topology.DeviceServer)
	srv3.ResolveTraceroute = func(target string) *sim.TracerouteResult {
		return &sim.TracerouteResult{
			TargetIP: target,
			MaxTTL:   30,
			Hops:     []sim.TracerouteHop{{Hop: 1, DeviceID: "r1", IP: target, DelayMs: 1}},
			Reached:  true,
		}
	}
	out = runOn(srv3, topology.DeviceServer, "tracert 9.9.9.9")
	if !strings.Contains(out, "Trace complete.") || !strings.Contains(out, "9.9.9.9") {
		t.Errorf("tracert with real hook should render path, got: %q", out)
	}
}

// TestQANetworkImportRouteGuard 验证顶层 network/import-route 仅在 ISIS 视图生效，
// 否则安全拒绝（不误执行、不破坏其它视图）。
func TestQANetworkImportRouteGuard(t *testing.T) {
	// 系统视图下敲 network → 必须被守卫拒绝
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	if out := runOn(r, topology.DeviceRouter, "network level-2"); !strings.Contains(out, "ISIS view") {
		t.Errorf("network outside ISIS view should be rejected, got: %q", out)
	}
	if out := runOn(r, topology.DeviceRouter, "import-route static"); !strings.Contains(out, "ISIS view") {
		t.Errorf("import-route outside ISIS view should be rejected, got: %q", out)
	}
}

// TestQAISISPersistRoundTrip 验证 ISIS 配置随 Serialize/Load 落盘不丢（风险2，独立复核 + 反例）。
func TestQAISISPersistRoundTrip(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-1-2")
	runOn(r, topology.DeviceRouter, "import-route static")
	cfg := r.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.ISIS == nil || !reloaded.ISIS.Enabled {
		t.Fatalf("ISIS should persist after reload, got Enabled=%v", reloaded.ISIS != nil && reloaded.ISIS.Enabled)
	}
	if reloaded.ISIS.ProcessID != 1 {
		t.Errorf("ISIS ProcessID should be 1, got %d", reloaded.ISIS.ProcessID)
	}
	if reloaded.ISIS.NetworkType != "level-1-2" {
		t.Errorf("ISIS NetworkType should be level-1-2, got %q", reloaded.ISIS.NetworkType)
	}
	if len(reloaded.ISIS.ImportRoutes) != 1 || reloaded.ISIS.ImportRoutes[0] != "static" {
		t.Errorf("ISIS ImportRoutes should be [static], got %v", reloaded.ISIS.ImportRoutes)
	}

	// 反例：序列化产物必须包含 isis: 键（否则重载必然丢配置）
	if cfg.Interfaces["isis:enabled"] != "true" || cfg.Interfaces["isis:process-id"] != "1" {
		t.Errorf("serialized config should carry isis:* keys, got %v", cfg.Interfaces)
	}
}

// TestQAUnknownCommandUnchanged 验证未知命令兜底行为不变（零回归）。
func TestQAUnknownCommandUnchanged(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(r, topology.DeviceRouter, "foobar-xyz")
	want := "Error: unknown command 'foobar-xyz'"
	if out != want {
		t.Errorf("unknown command fallback changed; want %q, got %q", want, out)
	}
}

// TestQAPortSecurityExtended 独立复核端口安全扩展命令：范围拒错、键写入、能力拒绝。
func TestQAPortSecurityExtended(t *testing.T) {
	// 交换机接口视图：合法 accept + 键写入
	sw := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(sw, topology.DeviceSwitch, "system-view")
	runOn(sw, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	for _, c := range []string{
		"port-security enable",
		"port-security max-mac-num 4096",
		"port-security protect-action shutdown",
		"port-security aging-time 1440",
		"port-security mac-address sticky 00e0-fc12-3456 vlan 10",
	} {
		if out := runOn(sw, topology.DeviceSwitch, c); strings.Contains(out, "Error") {
			t.Errorf("switch should accept %q, got: %q", c, out)
		}
	}
	if sw.DeviceConfig["interface:GigabitEthernet0/0/1:port-security-max-mac"] != "4096" {
		t.Errorf("max-mac 4096 boundary should be accepted, got %v", sw.DeviceConfig)
	}
	if sw.DeviceConfig["interface:GigabitEthernet0/0/1:port-security-aging-time"] != "1440" {
		t.Errorf("aging 1440 boundary should be accepted, got %v", sw.DeviceConfig)
	}

	// 范围边界拒错
	sw2 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(sw2, topology.DeviceSwitch, "system-view")
	runOn(sw2, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	for _, bad := range []string{
		"port-security max-mac-num 4097",
		"port-security aging-time 1441",
		"port-security protect-action invalid",
	} {
		if out := runOn(sw2, topology.DeviceSwitch, bad); !strings.Contains(out, "Error") {
			t.Errorf("switch should reject %q, got: %q", bad, out)
		}
	}

	// 路由器：端口安全与 simulate 均被能力拒绝
	rt := NewCLIStateWithType(topology.DeviceRouter)
	runOn(rt, topology.DeviceRouter, "system-view")
	runOn(rt, topology.DeviceRouter, "interface GigabitEthernet0/0/1")
	out := runOn(rt, topology.DeviceRouter, "port-security enable")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("router port-security should be rejected, got: %q", out)
	}
	out = runOn(rt, topology.DeviceRouter, "simulate frame 00e0-fc12-3456")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("router simulate should be rejected, got: %q", out)
	}
}

// TestQAPortSecurityPersistReload 复核端口安全配置 save→reload 往返保留。
func TestQAPortSecurityPersistReload(t *testing.T) {
	sw := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(sw, topology.DeviceSwitch, "system-view")
	runOn(sw, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(sw, topology.DeviceSwitch, "port-security enable")
	runOn(sw, topology.DeviceSwitch, "port-security max-mac-num 3")
	runOn(sw, topology.DeviceSwitch, "port-security protect-action restrict")
	runOn(sw, topology.DeviceSwitch, "port-security aging-time 20")
	runOn(sw, topology.DeviceSwitch, "save")
	runOn(sw, topology.DeviceSwitch, "y")
	cfg := sw.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")

	for _, k := range []string{
		"interface:GigabitEthernet0/0/1:port-security",
		"interface:GigabitEthernet0/0/1:port-security-max-mac",
		"interface:GigabitEthernet0/0/1:port-security-protect-action",
		"interface:GigabitEthernet0/0/1:port-security-aging-time",
	} {
		if _, ok := reloaded.DeviceConfig[k]; !ok {
			t.Errorf("reload should preserve port-security key %s, got %v", k, reloaded.DeviceConfig)
		}
	}
	disp := runOn(reloaded, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(disp, "enable") || !strings.Contains(disp, "restrict") || !strings.Contains(disp, "20") {
		t.Errorf("reloaded display should reproduce config, got: %q", disp)
	}
}
