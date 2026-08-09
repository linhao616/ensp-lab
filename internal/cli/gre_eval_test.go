package cli

// gre_eval_test.go —— P2 第七项（GRE 隧道，华为 VRP 课程 69）纯函数单测（T5）。
//
// 覆盖 gre_eval.go 的键 helper 精确匹配、isTunnelInterface、validGRETunnelEndpoint、
// validGRETunnelIP、normalizeGREKeyValue、greLineProtocolState/Brief、greSimNote，
// 以及「纯函数无副作用」（调用前后 DeviceConfig 不变、连续两次结果一致）。
//
// 全部经直接调用纯函数驱动，不依赖网络与真实引擎；不涉及命令行分派。

import (
	"reflect"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— isTunnelInterface（精确前缀 + 数字，严禁 Contains）——
func TestIsTunnelInterface(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Tunnel0/0/1", true},
		{"Tunnel1/0/0", true},
		{"Tun0/0/1", true},
		{"Tun2", true},
		{"TunnelX", false}, // 无数字
		{"TunX", false},    // 无数字
		{"Tunnel", false},  // 无数字
		{"Ethernet0/0/1", false},
		{"GigabitEthernet0/0/1", false},
		{"Ten-GigabitEthernet1/0/1", false},
		{"Bridge-Aggregation1", false},
		{"Eth-Trunk1", false},
		{"LoopBack0", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTunnelInterface(c.name); got != c.want {
			t.Errorf("isTunnelInterface(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// —— 键 helper 精确匹配（A1 红线）——
func TestGREKeyHelpers(t *testing.T) {
	const iface = "Tunnel0/0/1"
	if got := tunnelProtocolKey(iface); got != "interface:Tunnel0/0/1:tunnel-protocol" {
		t.Errorf("tunnelProtocolKey = %q", got)
	}
	if got := greKey(iface, "source"); got != "interface:Tunnel0/0/1:gre-source" {
		t.Errorf("greKey(source) = %q", got)
	}
	if got := greKey(iface, "keepalive-period"); got != "interface:Tunnel0/0/1:gre-keepalive-period" {
		t.Errorf("greKey(keepalive-period) = %q", got)
	}
	if got := greKeyPrefix(iface); got != "interface:Tunnel0/0/1:gre-" {
		t.Errorf("greKeyPrefix = %q", got)
	}
	// 反解析：精确后缀
	if ifc, ok := ifaceFromTunnelProtocolKey("interface:Tunnel0/0/1:tunnel-protocol"); !ok || ifc != iface {
		t.Errorf("ifaceFromTunnelProtocolKey = (%q,%v), want (%q,true)", ifc, ok, iface)
	}
	// 反解析：精确中缀
	if ifc, ok := ifaceFromGREKey("interface:Tunnel0/0/1:gre-source"); !ok || ifc != iface {
		t.Errorf("ifaceFromGREKey = (%q,%v), want (%q,true)", ifc, ok, iface)
	}
}

// —— 键碰撞红线（A1 / AC12）：Bridge-Aggregation 含 gre 子串但不得误判 ——
func TestGREKeyCollisionRedline(t *testing.T) {
	const lagKey = "interface:Bridge-Aggregation1:lag:mode"
	// 既非 tunnel-protocol 精确后缀，也非 :gre- 精确中缀
	if _, ok := ifaceFromTunnelProtocolKey(lagKey); ok {
		t.Errorf("ifaceFromTunnelProtocolKey 误判聚合口键 %q", lagKey)
	}
	if _, ok := ifaceFromGREKey(lagKey); ok {
		t.Errorf("ifaceFromGREKey 误判聚合口键 %q（键碰撞红线被突破！）", lagKey)
	}
	// 确认正则/模糊扫描不存在：整个 gre_eval.go / gre_cmd.go / gre_display.go 不得出现 Contains(...,"gre")
	// （由 p2_gre_qa_test.go 的静态 grep 断言覆盖。）
}

// —— validGRETunnelEndpoint（C3 双形态 + A3③ 统一文案）——
func TestValidGRETunnelEndpoint(t *testing.T) {
	cases := []struct {
		in     string
		kind   string
		ok     bool
		reason string
	}{
		{"202.1.1.1", "ip", true, ""},
		{"172.16.0.254", "ip", true, ""},
		{"192.168.1.1", "ip", true, ""},
		{"GigabitEthernet0/0/0", "interface", true, ""},
		{"LoopBack0", "interface", true, ""},
		{"300.1.1.1", "", false, "Error: Invalid IP address 300.1.1.1"},
		{"10.1.1", "", false, "Error: Invalid IP address 10.1.1"},
		{"abc", "", false, "Error: Invalid IP address abc"},
		{"10.1.1.1/24", "", false, "Error: Invalid IP address 10.1.1.1/24"},
		{"2001:db8::1", "", false, "Error: Invalid IP address 2001:db8::1"},
	}
	for _, c := range cases {
		kind, ok, reason := validGRETunnelEndpoint(c.in)
		if kind != c.kind || ok != c.ok || reason != c.reason {
			t.Errorf("validGRETunnelEndpoint(%q) = (%q,%v,%q), want (%q,%v,%q)",
				c.in, kind, ok, reason, c.kind, c.ok, c.reason)
		}
	}
}

// —— validGRETunnelIP（A6 特殊地址）——
func TestValidGRETunnelIP(t *testing.T) {
	cases := []struct {
		in     string
		ok     bool
		reason string
	}{
		{"202.1.1.1", true, ""},
		{"0.0.0.0", false, "Error: 0.0.0.0 is not a valid tunnel address."},
		{"255.255.255.255", false, "Error: 255.255.255.255 is not a valid tunnel address."},
		{"127.0.0.1", false, "Error: 127.0.0.1 is not a valid tunnel address."},
		{"224.0.0.1", false, "Error: 224.0.0.1 is not a valid tunnel address."},
		{"2001:db8::1", false, "Error: Invalid IP address 2001:db8::1"},
	}
	for _, c := range cases {
		ok, reason := validGRETunnelIP(c.in)
		if ok != c.ok || reason != c.reason {
			t.Errorf("validGRETunnelIP(%q) = (%v,%q), want (%v,%q)", c.in, ok, reason, c.ok, c.reason)
		}
	}
}

// —— normalizeGREKeyValue（A7 红线：严禁 int 零值歧义）——
func TestNormalizeGREKeyValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0", "0", true},
		{"1234", "1234", true},
		{"007", "7", true},
		{"4294967295", "4294967295", true},
		{"4294967296", "", false}, // 溢出 uint32
		{"-1", "", false},
		{"abc", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeGREKeyValue(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normalizeGREKeyValue(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// —— greLineProtocolState / Brief（C4 / A4）——
func TestGRELineProtocol(t *testing.T) {
	complete := GRETunnelConfig{
		Interface:      "Tunnel0/0/1",
		TunnelProtocol: "gre",
		Source:         "202.1.1.1",
		Destination:    "202.2.2.2",
	}
	if got := greLineProtocolState(complete); got != "UP (config complete, peer not verified)" {
		t.Errorf("greLineProtocolState(complete) = %q", got)
	}
	if got := greLineProtocolBrief(complete); got != "up*" {
		t.Errorf("greLineProtocolBrief(complete) = %q, want up*", got)
	}

	// 缺 source
	missingSrc := GRETunnelConfig{Interface: "Tunnel0/0/1", TunnelProtocol: "gre", Destination: "202.2.2.2"}
	if got := greLineProtocolState(missingSrc); got != "DOWN (source/destination not configured)" {
		t.Errorf("greLineProtocolState(missingSrc) = %q", got)
	}
	if got := greLineProtocolBrief(missingSrc); got != "down" {
		t.Errorf("greLineProtocolBrief(missingSrc) = %q, want down", got)
	}

	// 未配 tunnel-protocol
	noProto := GRETunnelConfig{Interface: "Tunnel0/0/1", Source: "202.1.1.1", Destination: "202.2.2.2"}
	if got := greLineProtocolState(noProto); got != "DOWN (source/destination not configured)" {
		t.Errorf("greLineProtocolState(noProto) = %q", got)
	}
}

// —— greSimNote（lite/full 两态）——
func TestGRESimNote(t *testing.T) {
	note := greSimNote()
	if !strings.Contains(note, "GRE 隧道为配置态模拟") {
		t.Errorf("greSimNote 缺少诚实占位注记：%q", note)
	}
	// 本构建为非 gont（ns-x 仿真子集），引擎级别为 lite。
	if !strings.Contains(note, "lite 引擎") {
		t.Errorf("greSimNote 应为 lite 引擎注记，got %q", note)
	}
}

// —— 纯函数无副作用：EvaluateGRE 不改变 DeviceConfig，且连续两次结果一致 ——
func TestEvaluateGRENoSideEffect(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "interface Tunnel0/0/1")
	runOn(st, topology.DeviceRouter, "tunnel-protocol gre")
	runOn(st, topology.DeviceRouter, "source 202.1.1.1")
	runOn(st, topology.DeviceRouter, "destination 202.2.2.2")
	runOn(st, topology.DeviceRouter, "gre key 1234")

	before := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		before[k] = v
	}

	r1 := EvaluateGRE(st, "Tunnel0/0/1")
	r2 := EvaluateGRE(st, "Tunnel0/0/1")

	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("EvaluateGRE 两次调用结果不一致：\n%#v\n%#v", r1, r2)
	}
	if !reflect.DeepEqual(st.DeviceConfig, before) {
		t.Errorf("EvaluateGRE 产生了副作用，DeviceConfig 被修改")
	}
	// 运行态统计恒 "-"（字段名以 PRD §4.2 五字段为准：
	// Keepalive sent / Keepalive received / Packets encapsulated /
	// Packets decapsulated / Peer reachability）
	if r1.Stats.KeepaliveSent != "-" || r1.Stats.KeepaliveReceived != "-" ||
		r1.Stats.PacketsEncapsulated != "-" || r1.Stats.PacketsDecapsulated != "-" ||
		r1.Stats.PeerReachable != "-" {
		t.Errorf("GREStats 运行态字段未恒为 -：%#v", r1.Stats)
	}
}
