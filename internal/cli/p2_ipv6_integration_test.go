package cli

// p2_ipv6_integration_test.go —— IPv6 跨层集成验收（T04：undo 级联 + 持久化贯通）。
//
// 覆盖（设计 §3.2 T04 验收 + PRD AC6④/AC8/AC10/AC12）：
//   - AC10 ①–④ undo 级联矩阵：接口 undo ipv6 address / undo ipv6 enable（C5 级联清地址）/
//     系统 undo ipv6（C6 仅清全局 ipv6: 前缀）/ undo ipv6 route-static <prefix>（A8 多下一跳级联）；
//   - AC10 ⑤ 既有 undo 分支零回归（接口 "shutdown"/"ip address"/"description"，系统 "ospf"）；
//   - AC6 ④ undo ipv6 route-static 精确前缀全部键被 delete（非留空串），多下一跳级联；
//   - AC8 ①–③ save → reload 持久化贯通（键级逐键一致 + display 完整复现 + 快照字节级一致），
//     并补 undo 后再次 save → reload 撤销结果同样持久化；
//   - AC12 ②/③ 键碰撞专项：C2 多键形态路由键双段解析 + undo ipv6 级联清理后
//     异族键（interface:<if>:ip、interface:Bridge-Aggregation1:lag:mode）完好无损。
//
// 🔴 A1 红线：本文件键断言一律走 ipv6_eval.go 的精确 key helper / 精确前缀扫描，
// 严禁任何基于子串的模糊键匹配（AC12 ④ 静态断言覆盖 ipv6_*.go）。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// ipv6TestEnterSystem 进入系统视图（失败即终止）。
func ipv6TestEnterSystem(t *testing.T, state *CLIState, dt topology.DeviceType) {
	t.Helper()
	if out := runOn(state, dt, "system-view"); !strings.Contains(out, "Enter system view") {
		t.Fatalf("enter system view failed: %q", out)
	}
}

// TestIPv6IntegrationAC10UndoCascade 验证 AC10 ①–④（undo 级联矩阵）。
func TestIPv6IntegrationAC10UndoCascade(t *testing.T) {
	const iface = "GigabitEthernet0/0/1"
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	ipv6TestEnterInterface(t, st, dt, iface)
	runOn(st, dt, "ipv6 enable")
	runOn(st, dt, "ipv6 address 2001:db8::1/64")
	runOn(st, dt, "ripng 1 enable")
	runOn(st, dt, "ospfv3 1 area 0")

	// ① undo ipv6 address → 仅清 :ipv6-address，其它接口键完好。
	if out := runOn(st, dt, "undo ipv6 address"); !strings.Contains(out, "deleted") {
		t.Fatalf("AC10① undo ipv6 address echo, got %q", out)
	}
	if _, ok := st.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldAddress)]; ok {
		t.Fatalf("AC10① :ipv6-address must be deleted")
	}
	if v := st.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldEnable)]; v != "true" {
		t.Fatalf("AC10① :ipv6-enable must remain 'true', got %q", v)
	}
	if v := st.DeviceConfig[ipv6RIPngIfaceKey(iface, "1")]; v != "true" {
		t.Fatalf("AC10① :ripng-1-enable must remain 'true', got %q", v)
	}

	// 重新配地址，然后 ② undo ipv6 enable → C5 级联清 :ipv6-enable 与 :ipv6-address，
	// 其它接口键（ripng/ospfv3/mac 等）完好。
	runOn(st, dt, "ipv6 address 2001:db8::1/64")
	runOn(st, dt, "undo ipv6 enable")
	if _, ok := st.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldEnable)]; ok {
		t.Fatalf("AC10② :ipv6-enable must be deleted")
	}
	if _, ok := st.DeviceConfig[ipv6IfaceKey(iface, ipv6FieldAddress)]; ok {
		t.Fatalf("AC10② C5 cascade: :ipv6-address must be deleted with :ipv6-enable")
	}
	if v := st.DeviceConfig[ipv6RIPngIfaceKey(iface, "1")]; v != "true" {
		t.Fatalf("AC10② :ripng-1-enable must remain 'true' after undo ipv6 enable, got %q", v)
	}
	if v := st.DeviceConfig[ipv6OSPFv3IfaceKey(iface, "1")]; v != "0" {
		t.Fatalf("AC10② :ospfv3-1-area must remain '0' after undo ipv6 enable, got %q", v)
	}

	// 接口级 undo ripng / undo ospfv3。
	runOn(st, dt, "undo ripng 1 enable")
	if _, ok := st.DeviceConfig[ipv6RIPngIfaceKey(iface, "1")]; ok {
		t.Fatalf("undo ripng 1 enable must delete :ripng-1-enable")
	}
	runOn(st, dt, "undo ospfv3 1 area")
	if _, ok := st.DeviceConfig[ipv6OSPFv3IfaceKey(iface, "1")]; ok {
		t.Fatalf("undo ospfv3 1 area must delete :ospfv3-1-area")
	}

	// ③ 系统 undo ipv6（C6）：清 ipv6: 精确前缀全部键，interface:<if>:ipv6-* 完好（AC12 ③）。
	stSys := NewCLIStateWithType(dt)
	ipv6TestEnterSystem(t, stSys, dt)
	runOn(stSys, dt, "ipv6")
	runOn(stSys, dt, "ipv6 route-static 2001:db8:2::/64 2001:db8:1::2")
	runOn(stSys, dt, "ipv6 route-static 2001:db8:10::/64 2001:db8:1::3")
	runOn(stSys, dt, "ripng 1")
	runOn(stSys, dt, "ospfv3 1")
	// 接口键作为对照（独立于系统视图操作，直接在 DeviceConfig 植入，模拟已配接口）。
	stSys.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable)] = "true"
	stSys.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)] = "2001:db8::1/64"
	ipv6IfaceIPv4Key := "interface:GigabitEthernet0/0/1:ip"
	stSys.DeviceConfig[ipv6IfaceIPv4Key] = "10.0.0.1"
	lagKey := "interface:Bridge-Aggregation1:lag:mode"
	stSys.DeviceConfig[lagKey] = "lacp-static"

	if out := runOn(stSys, dt, "undo ipv6"); !strings.Contains(out, "IPv6 disabled") {
		t.Fatalf("AC10③ undo ipv6 echo, got %q", out)
	}
	for _, k := range []string{
		ipv6GlobalKey(),
		ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2"),
		ipv6RouteStaticKey("2001:db8:10::/64", "2001:db8:1::3"),
		ipv6RIPngKey("1"),
		ipv6OSPFv3Key("1"),
	} {
		if _, ok := stSys.DeviceConfig[k]; ok {
			t.Errorf("AC10③ undo ipv6 must delete %q", k)
		}
	}
	// C6：interface:<if>:ipv6-* 与异族键完好（AC12 ③）。
	if v := stSys.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable)]; v != "true" {
		t.Errorf("AC10③ interface ipv6-enable must survive undo ipv6, got %q", v)
	}
	if v := stSys.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)]; v != "2001:db8::1/64" {
		t.Errorf("AC10③ interface ipv6-address must survive undo ipv6, got %q", v)
	}
	if v := stSys.DeviceConfig[ipv6IfaceIPv4Key]; v != "10.0.0.1" {
		t.Errorf("AC10③ IPv4 key %s must survive undo ipv6, got %q", ipv6IfaceIPv4Key, v)
	}
	if v := stSys.DeviceConfig[lagKey]; v != "lacp-static" {
		t.Errorf("AC10③ lag key %s must survive undo ipv6, got %q", lagKey, v)
	}

	// ④ undo ipv6 route-static <prefix> → 精确前缀全部键 delete（非留空串），其它路由键完好（AC6 ④ / AC10 ④）。
	stRS := NewCLIStateWithType(dt)
	ipv6TestEnterSystem(t, stRS, dt)
	rsKey1 := ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2")
	rsKey2 := ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::3")
	rsKeyOther := ipv6RouteStaticKey("2001:db8:10::/64", "2001:db8:1::4")
	stRS.DeviceConfig[rsKey1] = "true"
	stRS.DeviceConfig[rsKey2] = "true"
	stRS.DeviceConfig[rsKeyOther] = "true"
	if out := runOn(stRS, dt, "undo ipv6 route-static 2001:db8:2::/64"); !strings.Contains(out, "removed") {
		t.Fatalf("AC6④ undo ipv6 route-static echo, got %q", out)
	}
	if _, ok := stRS.DeviceConfig[rsKey1]; ok {
		t.Errorf("AC6④ multi-nexthop key %q must be deleted (not empty string)", rsKey1)
	}
	if _, ok := stRS.DeviceConfig[rsKey2]; ok {
		t.Errorf("AC6④ multi-nexthop key %q must be deleted (cascade)", rsKey2)
	}
	if v := stRS.DeviceConfig[rsKeyOther]; v != "true" {
		t.Errorf("AC10④ other route key %q must survive, got %q", rsKeyOther, v)
	}
	// 无参 undo ipv6 route-static → 清全部（P1-8）。
	stRS.DeviceConfig[rsKey1] = "true"
	runOn(stRS, dt, "undo ipv6 route-static")
	for k := range stRS.DeviceConfig {
		if strings.HasPrefix(k, ipv6RouteStaticPrefix()) {
			t.Errorf("P1-8 undo ipv6 route-static must clear all route-static keys, left %q", k)
		}
	}
}

// TestIPv6IntegrationAC10ExistingUndoUnchanged 验证 AC10 ⑤（既有 undo 分支零回归）。
func TestIPv6IntegrationAC10ExistingUndoUnchanged(t *testing.T) {
	dt := topology.DeviceRouter
	// 接口视图：undo ip address / undo shutdown / undo description 行为逐字不变。
	st := NewCLIStateWithType(dt)
	ipv6TestEnterInterface(t, st, dt, "GigabitEthernet0/0/1")
	runOn(st, dt, "shutdown")
	if out := runOn(st, dt, "undo shutdown"); !strings.Contains(out, "Interface is up") {
		t.Errorf("AC10⑤ undo shutdown must keep existing behavior, got %q", out)
	}
	if out := runOn(st, dt, "undo ip address"); !strings.Contains(out, "IP address deleted") {
		t.Errorf("AC10⑤ undo ip address must keep existing behavior, got %q", out)
	}
	if out := runOn(st, dt, "undo description"); !strings.Contains(out, "deleted") {
		t.Errorf("AC10⑤ undo description must keep existing behavior, got %q", out)
	}
	// 系统视图：undo ospf 行为逐字不变。
	stSys := NewCLIStateWithType(dt)
	ipv6TestEnterSystem(t, stSys, dt)
	runOn(stSys, dt, "ospf 1")
	if out := runOn(stSys, dt, "undo ospf"); !strings.Contains(out, "OSPF process removed") {
		t.Errorf("AC10⑤ undo ospf must keep existing behavior, got %q", out)
	}
}

// TestIPv6IntegrationAC8PersistenceUndo 验证 AC8 ①–③ + undo 结果同样持久化。
func TestIPv6IntegrationAC8PersistenceUndo(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	ipv6TestEnterSystem(t, st, dt)
	runOn(st, dt, "ipv6")
	runOn(st, dt, "interface GigabitEthernet0/0/1")
	runOn(st, dt, "ipv6 enable")
	runOn(st, dt, "ipv6 address 2001:db8::1/64")
	runOn(st, dt, "quit")
	runOn(st, dt, "ipv6 route-static 2001:db8:2::/64 2001:db8:1::2")

	// ① save→reload 键级逐键一致。
	cfg := st.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(dt, cfg, "R1")
	wantKeys := []string{
		ipv6GlobalKey(),
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable),
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress),
		ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2"),
	}
	for _, k := range wantKeys {
		if reloaded.DeviceConfig[k] != st.DeviceConfig[k] {
			t.Errorf("AC8① key %q not restored: %q != %q", k, reloaded.DeviceConfig[k], st.DeviceConfig[k])
		}
	}
	// ② reload 后 display 完整复现。
	for _, c := range []string{
		"display ipv6 interface brief",
		"display ipv6 interface GigabitEthernet0/0/1",
		"display ipv6 routing-table",
	} {
		out := runOn(reloaded, dt, c)
		if strings.Contains(out, "Info: No IPv6") {
			t.Errorf("AC8② post-reload %q must reproduce IPv6 config, got:\n%s", c, out)
		}
	}
	// ③ reload 后 current-configuration 复现 §4.5 全部 IPv6 行 + 快照字节级一致。
	before := runOn(st, dt, "display current-configuration")
	after := runOn(reloaded, dt, "display current-configuration")
	for _, want := range []string{
		"interface GigabitEthernet0/0/1",
		" ipv6 enable",
		" ipv6 address 2001:db8::1/64",
		"ipv6 route-static 2001:db8:2::/64 2001:db8:1::2",
	} {
		if !strings.Contains(before, want) || !strings.Contains(after, want) {
			t.Errorf("AC8③ current-configuration missing %q (before=%v after=%v)\n--- after ---\n%s",
				want, strings.Contains(before, want), strings.Contains(after, want), after)
		}
	}
	if before != after {
		t.Errorf("AC8③ snapshot must be byte-identical before/after reload")
	}

	// undo 后再次 save→reload：撤销结果同样持久化（删除的键 reload 后仍不存在）。
	runOn(reloaded, dt, "system-view")
	runOn(reloaded, dt, "undo ipv6 route-static 2001:db8:2::/64")
	cfg2 := reloaded.SerializeToDeviceConfigData()
	reloaded2 := NewCLIStateFromDeviceConfig(dt, cfg2, "R1")
	if _, ok := reloaded2.DeviceConfig[ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2")]; ok {
		t.Errorf("AC8 undo persistence: deleted route-static key must stay absent after reload")
	}
}

// TestIPv6IntegrationAC12KeyCollision 验证 AC12 ②/③（键碰撞专项跨层）。
func TestIPv6IntegrationAC12KeyCollision(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	// 构造 AC12 五键状态（逐字对照 PRD AC12）。
	ipv4Key := "interface:GigabitEthernet0/0/1:ip"
	ipv6AddrKey := ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)
	rsKey := ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2")
	lagKey := "interface:Bridge-Aggregation1:lag:mode"
	st.DeviceConfig[ipv4Key] = "10.0.0.1"
	st.DeviceConfig[ipv6AddrKey] = "2001:db8::1/64"
	st.DeviceConfig[ipv6GlobalKey()] = "true"
	st.DeviceConfig[rsKey] = "true"
	st.DeviceConfig[lagKey] = "lacp-static"

	// ② A3 双段解析：C2 多键形态正确解析，且不会把 ipv6:enabled / :ipv6-address 误判为路由键。
	if p, nh, ok := parseIPv6RouteStaticKey(rsKey); !ok || p != "2001:db8:2::/64" || nh != "2001:db8:1::2" {
		t.Errorf("AC12② parseIPv6RouteStaticKey(%q) = (%q, %q, %v), want (2001:db8:2::/64, 2001:db8:1::2, true)", rsKey, p, nh, ok)
	}
	for _, k := range []string{ipv6GlobalKey(), ipv6AddrKey, ipv4Key} {
		if _, _, ok := parseIPv6RouteStaticKey(k); ok {
			t.Errorf("AC12② key %q must NOT parse as route-static key", k)
		}
	}
	// ② collectIPv6Interfaces 只命中 :ipv6- 中缀，IPv4 键 :ip 被隔离。
	ifaces := collectIPv6Interfaces(st)
	if len(ifaces) != 1 || ifaces[0] != "GigabitEthernet0/0/1" {
		t.Errorf("AC12② collectIPv6Interfaces = %v, want [GigabitEthernet0/0/1]", ifaces)
	}
	// collectIPv6RouteStatics 只命中 route-static 精确前缀，且正确解析。
	routes := collectIPv6RouteStatics(st)
	if len(routes) != 1 || routes[0].Prefix != "2001:db8:2::/64" || routes[0].NextHop != "2001:db8:1::2" {
		t.Errorf("AC12② collectIPv6RouteStatics = %+v, want single route 2001:db8:2::/64 -> 2001:db8:1::2", routes)
	}

	// ③ undo ipv6 级联清理后：interface:<if>:ip、ipv6:enabled 已清、异族键完好（AC12 ③）。
	// 注：ipv6:enabled 属于 ipv6: 前缀，undo ipv6 后应被删除；IPv4 键与 lag 键完好。
	ipv6TestEnterSystem(t, st, dt)
	runOn(st, dt, "undo ipv6")
	if _, ok := st.DeviceConfig[ipv6GlobalKey()]; ok {
		t.Errorf("AC12③ ipv6:enabled must be deleted by undo ipv6")
	}
	if v := st.DeviceConfig[ipv4Key]; v != "10.0.0.1" {
		t.Errorf("AC12③ IPv4 key %q must survive undo ipv6, got %q", ipv4Key, v)
	}
	if v := st.DeviceConfig[lagKey]; v != "lacp-static" {
		t.Errorf("AC12③ lag key %q must survive undo ipv6, got %q", lagKey, v)
	}
}
