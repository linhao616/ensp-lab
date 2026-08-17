package cli

// ipv6_cmd_test.go —— IPv6 配置命令族（T02）验收测试。
//
// 覆盖（设计 §3.2 T02 验收，全部走 ExecuteCommandOn + ParseCommand 纯逻辑驱动）：
//   - AC1 ①–④ 系统视图全局使能语义修正；
//   - AC2 ①–⑤ 接口视图使能 + 地址 + C1 硬前置；
//   - AC6 ①–③/⑤ ipv6 route-static 校验、存取、幂等（④ undo 在 T04 集成测试）；
//   - AC11a 配置命令按设备类型守卫（PC/Server/二层 Switch 拒绝，Router 放行）；
//   - AC13 RIPng/OSPFv3 键写入 + `ipv6 router rip`（Cisco 别名）拒绝且不写键。
//
// 🔴 A1 红线：本文件键断言一律走 ipv6_eval.go 的精确 key helper / 精确前缀扫描，
// 严禁任何基于子串的模糊键匹配（AC12 ④ 静态断言覆盖 ipv6_*.go）。
//
// 注：AC2 ⑤ 中「系统视图 ipv6 enable」按设计 §7.6 / AC1 ③ 取 A11 引导文案
// （非 must be in interface view），本测试按权威设计矩阵断言。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// ipv6TestEnterInterface 进入 system view 并进入指定接口视图；失败即终止测试。
func ipv6TestEnterInterface(t *testing.T, state *CLIState, dt topology.DeviceType, iface string) {
	t.Helper()
	if out := runOn(state, dt, "system-view"); !strings.Contains(out, "Enter system view") {
		t.Fatalf("enter system view failed: %q", out)
	}
	if out := runOn(state, dt, "interface "+iface); !strings.Contains(out, "Enter interface view") {
		t.Fatalf("enter interface %s failed: %q", iface, out)
	}
}

// ipv6TestRouteStaticKeys 收集 DeviceConfig 中全部 ipv6:route-static: 精确前缀键。
func ipv6TestRouteStaticKeys(dc map[string]string) []string {
	var keys []string
	for k := range dc {
		if strings.HasPrefix(k, ipv6RouteStaticPrefix()) {
			keys = append(keys, k)
		}
	}
	return keys
}

// TestIPv6CmdAC1SystemEnableSemantics 验证 AC1 ①–④（系统视图全局使能语义修正）。
func TestIPv6CmdAC1SystemEnableSemantics(t *testing.T) {
	// ① 系统视图裸 ipv6 → ipv6:enabled=="true" 且回显含 "IPv6 enabled"。
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "ipv6"); !strings.Contains(out, "IPv6 enabled") {
		t.Fatalf("AC1① bare ipv6 echo want contains 'IPv6 enabled', got %q", out)
	}
	if v := st.DeviceConfig[ipv6GlobalKey()]; v != "true" {
		t.Fatalf("AC1① ipv6:enabled want 'true', got %q", v)
	}

	// ② 系统视图 ipv6 garbage → unrecognized 且 ipv6:enabled 键未写入。
	st2 := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st2, topology.DeviceRouter, "system-view")
	if out := runOn(st2, topology.DeviceRouter, "ipv6 garbage"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC1② ipv6 garbage want 'unrecognized command', got %q", out)
	}
	if _, ok := st2.DeviceConfig[ipv6GlobalKey()]; ok {
		t.Fatalf("AC1② ipv6 garbage must NOT write ipv6:enabled key")
	}
	if out := runOn(st2, topology.DeviceRouter, "ipv6 foo"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC1② ipv6 foo want 'unrecognized command', got %q", out)
	}
	if _, ok := st2.DeviceConfig[ipv6GlobalKey()]; ok {
		t.Fatalf("AC1② ipv6 foo must NOT write ipv6:enabled key")
	}

	// ③ 系统视图 ipv6 enable → A11 引导文案。
	if out := runOn(st2, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "Please run 'ipv6'") {
		t.Fatalf("AC1③ ipv6 enable want 'Please run ipv6' guide, got %q", out)
	}

	// ④ 系统视图 ipv6 address → must be in interface view（不再被全局使能分支吞掉）。
	if out := runOn(st2, topology.DeviceRouter, "ipv6 address 2001:db8::1/64"); !strings.Contains(out, "must be in interface view") {
		t.Fatalf("AC1④ system ipv6 address want 'must be in interface view', got %q", out)
	}
}

// TestIPv6CmdAC2InterfaceEnableAddress 验证 AC2 ①–⑤（接口视图使能 + 地址 + C1 前置）。
func TestIPv6CmdAC2InterfaceEnableAddress(t *testing.T) {
	const iface = "GigabitEthernet0/0/1"
	enableKey := ipv6IfaceKey(iface, ipv6FieldEnable)
	addrKey := ipv6IfaceKey(iface, ipv6FieldAddress)

	// ① 接口视图裸 ipv6 → unrecognized command。
	st := NewCLIStateWithType(topology.DeviceRouter)
	ipv6TestEnterInterface(t, st, topology.DeviceRouter, iface)
	if out := runOn(st, topology.DeviceRouter, "ipv6"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC2① interface bare ipv6 want 'unrecognized command', got %q", out)
	}

	// ② ipv6 enable → interface:<if>:ipv6-enable=="true" 且回显正确。
	if out := runOn(st, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "IPv6 is enabled on "+iface) {
		t.Fatalf("AC2② ipv6 enable echo want contains 'IPv6 is enabled on %s', got %q", iface, out)
	}
	if v := st.DeviceConfig[enableKey]; v != "true" {
		t.Fatalf("AC2② interface:%s:ipv6-enable want 'true', got %q", iface, v)
	}

	// ③ C1 硬前置：未使能时 ipv6 address → 报错且 :ipv6-address 键未写入。
	st2 := NewCLIStateWithType(topology.DeviceRouter)
	ipv6TestEnterInterface(t, st2, topology.DeviceRouter, iface)
	if out := runOn(st2, topology.DeviceRouter, "ipv6 address 2001:db8::1/64"); !strings.Contains(out, "Please run 'ipv6 enable'") {
		t.Fatalf("AC2③ unenabled ipv6 address want 'Please run ipv6 enable', got %q", out)
	}
	if _, ok := st2.DeviceConfig[addrKey]; ok {
		t.Fatalf("AC2③ unenabled ipv6 address must NOT write :ipv6-address key")
	}

	// ④ 使能后 ipv6 address → 规范化缩写存储。
	if out := runOn(st2, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "IPv6 is enabled on "+iface) {
		t.Fatalf("AC2④ setup enable failed: %q", out)
	}
	if out := runOn(st2, topology.DeviceRouter, "ipv6 address 2001:db8::1/64"); !strings.Contains(out, "IPv6 address 2001:db8::1/64 configured on "+iface) {
		t.Fatalf("AC2④ ipv6 address echo want contains normalized prefix, got %q", out)
	}
	if v := st2.DeviceConfig[addrKey]; v != "2001:db8::1/64" {
		t.Fatalf("AC2④ :ipv6-address want '2001:db8::1/64', got %q", v)
	}
	// A7 规范化：大写/全展开输入也存为 RFC 5952 缩写。
	if out := runOn(st2, topology.DeviceRouter, "ipv6 address 2001:0DB8:0000:0000:0000:0000:0000:0001/64"); !strings.Contains(out, "IPv6 address 2001:db8::1/64") {
		t.Fatalf("AC2④ normalization echo want '2001:db8::1/64', got %q", out)
	}
	if v := st2.DeviceConfig[addrKey]; v != "2001:db8::1/64" {
		t.Fatalf("AC2④ normalized :ipv6-address want '2001:db8::1/64', got %q", v)
	}

	// ⑤ 用户视图 ipv6 enable → must be in interface view（§7.6 / AC2 ⑤）。
	st3 := NewCLIStateWithType(topology.DeviceRouter) // 仍在用户视图
	if out := runOn(st3, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "must be in interface view") {
		t.Fatalf("AC2⑤ user-view ipv6 enable want 'must be in interface view', got %q", out)
	}
	// 系统视图 ipv6 enable → A11 引导（AC1 ③ / §7.6，权威矩阵覆盖 AC2 ⑤ 系统用例）。
	runOn(st3, topology.DeviceRouter, "system-view")
	if out := runOn(st3, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "Please run 'ipv6'") {
		t.Fatalf("AC2⑤/system ipv6 enable want A11 guide, got %q", out)
	}
}

// TestIPv6CmdAC6RouteStatic 验证 AC6 ①–③/⑤（route-static 校验、存取、幂等；④ 留 T04）。
func TestIPv6CmdAC6RouteStatic(t *testing.T) {
	const prefix = "2001:db8:2::/64"
	const nexthop = "2001:db8:1::2"
	rsKey := ipv6RouteStaticKey(prefix, nexthop)

	// ① 合法配置 → 多键形态写入 == "true"（C2）。
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "ipv6 route-static "+prefix+" "+nexthop); out != "Static route added" {
		t.Fatalf("AC6① route-static echo want 'Static route added', got %q", out)
	}
	if v := st.DeviceConfig[rsKey]; v != "true" {
		t.Fatalf("AC6① key %s want 'true', got %q", rsKey, v)
	}

	// ② 非法前缀 / 非法下一跳 → 对应 Error 且无任何 ipv6:route-static: 键。
	st2 := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st2, topology.DeviceRouter, "system-view")
	if out := runOn(st2, topology.DeviceRouter, "ipv6 route-static 2001:db8::1/129 2001:db8::1"); !strings.Contains(out, "Invalid IPv6 prefix length") {
		t.Fatalf("AC6② bad prefix len want 'Invalid IPv6 prefix length', got %q", out)
	}
	if got := ipv6TestRouteStaticKeys(st2.DeviceConfig); len(got) != 0 {
		t.Fatalf("AC6② bad prefix len must NOT write any route-static key, got %v", got)
	}
	if out := runOn(st2, topology.DeviceRouter, "ipv6 route-static "+prefix+" 2001:db8::gg"); !strings.Contains(out, "Invalid IPv6 address") {
		t.Fatalf("AC6② bad nexthop want 'Invalid IPv6 address', got %q", out)
	}
	if got := ipv6TestRouteStaticKeys(st2.DeviceConfig); len(got) != 0 {
		t.Fatalf("AC6② bad nexthop must NOT write any route-static key, got %v", got)
	}

	// ③ 同前缀同下一跳重复配置 → 幂等（不报错不覆盖）。
	if out := runOn(st, topology.DeviceRouter, "ipv6 route-static "+prefix+" "+nexthop); out != "Static route added" {
		t.Fatalf("AC6③ repeated route-static want idempotent echo, got %q", out)
	}
	if v := st.DeviceConfig[rsKey]; v != "true" {
		t.Fatalf("AC6③ repeated route-static key must remain 'true', got %q", v)
	}

	// ⑤ 系统视图外（接口视图）执行 → 视图报错。
	st3 := NewCLIStateWithType(topology.DeviceRouter)
	ipv6TestEnterInterface(t, st3, topology.DeviceRouter, "GigabitEthernet0/0/1")
	if out := runOn(st3, topology.DeviceRouter, "ipv6 route-static "+prefix+" "+nexthop); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC6⑤ interface-view route-static want 'unrecognized command', got %q", out)
	}
	if got := ipv6TestRouteStaticKeys(st3.DeviceConfig); len(got) != 0 {
		t.Fatalf("AC6⑤ interface-view route-static must NOT write any route-static key, got %v", got)
	}
}

// TestIPv6CmdAC11aDeviceGuard 验证 AC11a（配置命令按设备类型守卫，l3Devices() 分支内）。
func TestIPv6CmdAC11aDeviceGuard(t *testing.T) {
	// 拒绝集：PC / Server（分支内 l3Devices 拒绝，hostsAndL3 放行 ipv6 顶层）。
	rejectedBranch := []topology.DeviceType{topology.DevicePC, topology.DeviceServer}
	for _, dt := range rejectedBranch {
		st := NewCLIStateWithType(dt)
		ipv6TestEnterInterface(t, st, dt, "GigabitEthernet0/0/1")
		if out := runOn(st, dt, "ipv6 enable"); !strings.Contains(out, "is not supported on") {
			t.Fatalf("AC11a %s ipv6 enable want 'is not supported on', got %q", dt, out)
		}
		if out := runOn(st, dt, "ipv6 address 2001:db8::1/64"); !strings.Contains(out, "is not supported on") {
			t.Fatalf("AC11a %s ipv6 address want 'is not supported on', got %q", dt, out)
		}
		// 系统视图 route-static 同样被分支内守卫拒绝。
		runOn(st, dt, "return")
		runOn(st, dt, "system-view")
		if out := runOn(st, dt, "ipv6 route-static 2001:db8:2::/64 2001:db8::1"); !strings.Contains(out, "is not supported on") {
			t.Fatalf("AC11a %s route-static want 'is not supported on', got %q", dt, out)
		}
		if got := ipv6TestRouteStaticKeys(st.DeviceConfig); len(got) != 0 {
			t.Fatalf("AC11a %s route-static must NOT write any key, got %v", dt, got)
		}
	}

	// 二层 Switch：ipv6 不在 capabilities 矩阵（hostsAndL3 不含 switch）→ 顶层能力拒绝。
	stSw := NewCLIStateWithType(topology.DeviceSwitch)
	ipv6TestEnterInterface(t, stSw, topology.DeviceSwitch, "GigabitEthernet0/0/1")
	if out := runOn(stSw, topology.DeviceSwitch, "ipv6 enable"); !strings.Contains(out, "not supported") {
		t.Fatalf("AC11a switch ipv6 enable want 'not supported', got %q", out)
	}

	// 放行集：Router（其余放行设备 L3Switch/Firewall/VTEP 同源 l3Devices，取代表验证）。
	stR := NewCLIStateWithType(topology.DeviceRouter)
	ipv6TestEnterInterface(t, stR, topology.DeviceRouter, "GigabitEthernet0/0/1")
	if out := runOn(stR, topology.DeviceRouter, "ipv6 enable"); !strings.Contains(out, "IPv6 is enabled on") {
		t.Fatalf("AC11a router ipv6 enable should pass, got %q", out)
	}
}

// TestIPv6CmdAC13RIPngOSPFv3 验证 AC13（RIPng/OSPFv3 键写入 + Cisco 别名拒绝）。
func TestIPv6CmdAC13RIPngOSPFv3(t *testing.T) {
	const iface = "GigabitEthernet0/0/1"

	// RIPng：系统视图 ripng 1 → ipv6:ripng:1:enabled。
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "ripng 1"); out != "RIPng process 1 enabled" {
		t.Fatalf("AC13 ripng 1 echo want 'RIPng process 1 enabled', got %q", out)
	}
	if v := st.DeviceConfig[ipv6RIPngKey("1")]; v != "true" {
		t.Fatalf("AC13 ipv6:ripng:1:enabled want 'true', got %q", v)
	}
	// 缺省 pid：ripng → pid=1。
	if out := runOn(st, topology.DeviceRouter, "ripng"); out != "RIPng process 1 enabled" {
		t.Fatalf("AC13 ripng default pid echo want 'RIPng process 1 enabled', got %q", out)
	}

	// RIPng：接口视图 ripng 1 enable → interface:<if>:ripng-1-enable。
	ipv6TestEnterInterface(t, st, topology.DeviceRouter, iface)
	if out := runOn(st, topology.DeviceRouter, "ripng 1 enable"); out != "RIPng process 1 enabled on "+iface {
		t.Fatalf("AC13 ripng 1 enable echo want 'RIPng process 1 enabled on %s', got %q", iface, out)
	}
	if v := st.DeviceConfig[ipv6RIPngIfaceKey(iface, "1")]; v != "true" {
		t.Fatalf("AC13 interface:%s:ripng-1-enable want 'true', got %q", iface, v)
	}
	// 接口视图 ripng 裸 / 缺 enable → unrecognized 且不写键（§7.6）。
	if out := runOn(st, topology.DeviceRouter, "ripng 1"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC13 interface ripng 1 want 'unrecognized command', got %q", out)
	}

	// 🔴 Cisco 别名：ipv6 router rip → unrecognized 且不写任何键。
	st2 := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st2, topology.DeviceRouter, "system-view")
	if out := runOn(st2, topology.DeviceRouter, "ipv6 router rip"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC13 ipv6 router rip want 'unrecognized command', got %q", out)
	}
	for k := range st2.DeviceConfig {
		if strings.HasPrefix(k, ipv6KeyPrefix()) || strings.Contains(k, ipv6RIPngIfaceInfix) {
			t.Fatalf("AC13 ipv6 router rip must NOT write any ipv6/ripng key, got %q", k)
		}
	}

	// OSPFv3：系统视图 ospfv3 1 → ipv6:ospfv3:1:enabled。
	st3 := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st3, topology.DeviceRouter, "system-view")
	if out := runOn(st3, topology.DeviceRouter, "ospfv3 1"); out != "OSPFv3 process 1 enabled" {
		t.Fatalf("AC13 ospfv3 1 echo want 'OSPFv3 process 1 enabled', got %q", out)
	}
	if v := st3.DeviceConfig[ipv6OSPFv3Key("1")]; v != "true" {
		t.Fatalf("AC13 ipv6:ospfv3:1:enabled want 'true', got %q", v)
	}

	// OSPFv3：接口视图 ospfv3 1 area 0 → interface:<if>:ospfv3-1-area == "0"。
	ipv6TestEnterInterface(t, st3, topology.DeviceRouter, iface)
	if out := runOn(st3, topology.DeviceRouter, "ospfv3 1 area 0"); out != "OSPFv3 process 1 area 0 enabled on "+iface {
		t.Fatalf("AC13 ospfv3 1 area 0 echo want 'OSPFv3 process 1 area 0 enabled on %s', got %q", iface, out)
	}
	if v := st3.DeviceConfig[ipv6OSPFv3IfaceKey(iface, "1")]; v != "0" {
		t.Fatalf("AC13 interface:%s:ospfv3-1-area want '0', got %q", iface, v)
	}
	// 接口视图裸 ospfv3 → unrecognized 且不写键（C8 / §7.6，AC13 允许 usage 或 unrecognized 类 Error）。
	if out := runOn(st3, topology.DeviceRouter, "ospfv3"); !strings.Contains(out, "unrecognized command") {
		t.Fatalf("AC13 interface bare ospfv3 want 'unrecognized command', got %q", out)
	}
	if _, ok := st3.DeviceConfig[ipv6OSPFv3IfaceKey(iface, "1")]; !ok {
		t.Fatalf("AC13 ospfv3 1 area 0 key must still exist after bare ospfv3 attempt")
	}
}
