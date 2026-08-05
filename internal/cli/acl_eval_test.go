package cli

import (
	"testing"

	"ensp-lab/internal/topology"
)

// newACLTestTopo 构造一个用于 ACL 评估器单测的拓扑：
//
//	pc1(10.0.0.10) -- r1(10.0.0.1/10.0.1.1) -- r2(10.0.1.2/10.0.2.1) -- pc2(10.0.2.10)
func newACLTestTopo() *topology.Topology {
	t := topology.NewTopology("t1", "test")
	t.AddDevice(&topology.Device{
		ID:   "pc1",
		Type: topology.DevicePC,
		Interfaces: map[string]*topology.Interface{
			"Ethernet0": {IPAddress: "10.0.0.10", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "r1",
		Type: topology.DeviceRouter,
		Interfaces: map[string]*topology.Interface{
			"GE0/0/0": {IPAddress: "10.0.0.1", Status: "up"},
			"GE0/0/1": {IPAddress: "10.0.1.1", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "r2",
		Type: topology.DeviceL3Switch,
		Interfaces: map[string]*topology.Interface{
			"GE0/0/0": {IPAddress: "10.0.1.2", Status: "up"},
			"GE0/0/1": {IPAddress: "10.0.2.1", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "pc2",
		Type: topology.DeviceServer,
		Interfaces: map[string]*topology.Interface{
			"Ethernet0": {IPAddress: "10.0.2.10", Status: "up"},
		},
	})
	t.AddLink(&topology.Link{ID: "l1", SourceDevice: "pc1", TargetDevice: "r1"})
	t.AddLink(&topology.Link{ID: "l2", SourceDevice: "r1", TargetDevice: "r2"})
	t.AddLink(&topology.Link{ID: "l3", SourceDevice: "r2", TargetDevice: "pc2"})
	return t
}

func newACLTestState(dt topology.DeviceType, deviceID string) *CLIState {
	s := NewCLIStateWithType(dt)
	s.DeviceID = deviceID
	return s
}

func assertEqual(t *testing.T, name string, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// ----------------------------------------------------------------------------
// wildcardToMask：位级对齐 protocol.wildcardToMask
// ----------------------------------------------------------------------------

func TestWildcardToMask(t *testing.T) {
	cases := []struct {
		wc   string
		want int
	}{
		{"0.0.0.255", 24},
		{"0.0.0.0", 32},
		{"0.0.255.255", 16},
		{"255.255.255.255", 0},
		{"0.0.0.127", 25},
		{"", 8},  // 空串 Split 得 1 段，Atoi 失败按 0 → 8 位掩码（与 protocol 一致）
		{"abc", 8}, // 非法通配符按 0 处理，单段 → 8 位掩码（与 protocol 一致）
	}
	for _, c := range cases {
		assertEqual(t, "wildcardToMask("+c.wc+")", wildcardToMask(c.wc), c.want)
	}
}

// ----------------------------------------------------------------------------
// matchIP
// ----------------------------------------------------------------------------

func TestMatchIP(t *testing.T) {
	cases := []struct {
		ip, ruleIP, wc string
		want           bool
	}{
		{"192.168.1.10", "192.168.1.0", "0.0.0.255", true},
		{"10.0.0.5", "192.168.1.0", "0.0.0.255", false},
		{"192.168.1.10", "192.168.1.10", "", true}, // 空通配符=主机路由
		{"192.168.2.10", "192.168.1.0", "0.0.0.255", false},
	}
	for _, c := range cases {
		assertEqual(t, "matchIP", matchIP(c.ip, c.ruleIP, c.wc), c.want)
	}
}

// ----------------------------------------------------------------------------
// matchACLRule：协议号匹配（ip/icmp/tcp/udp）
// ----------------------------------------------------------------------------

func TestMatchACLRule_Protocol(t *testing.T) {
	base := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	cases := []struct {
		proto string
		want  bool
	}{
		{"icmp", true},
		{"tcp", false},
		{"udp", false},
		{"ip", true},  // ip = 任意 IP 协议
		{"", true},    // 空 = 不限制协议
		{"ICMP", true}, // 大小写不敏感
	}
	for _, c := range cases {
		rule := &ACLRule{ID: 10, Action: "permit", Protocol: c.proto}
		assertEqual(t, "protocol="+c.proto, matchACLRule(rule, base), c.want)
	}
	// tcp/udp 各自的精确匹配
	assertEqual(t, "tcp match", matchACLRule(&ACLRule{Action: "permit", Protocol: "tcp"}, PacketTuple{Proto: "tcp"}), true)
	assertEqual(t, "udp match", matchACLRule(&ACLRule{Action: "permit", Protocol: "udp"}, PacketTuple{Proto: "udp"}), true)
}

// ----------------------------------------------------------------------------
// EvaluateDeviceACL：未绑定=permit、隐式 deny、permit 命中、deny 命中、多规则顺序
// ----------------------------------------------------------------------------

func TestEvaluateDeviceACL_UnboundPermit(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 无 traffic-filter 绑定 → 放行
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "permit")
	assertEqual(t, "matched", dec.Matched, false)
}

func TestEvaluateDeviceACL_ImplicitDeny(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "192.168.99.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	// 报文未命中 permit 规则 → 隐式 deny any（拍板 #2）
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "matched", dec.Matched, false)
	assertEqual(t, "acl", dec.ACLNum, "2000")
}

func TestEvaluateDeviceACL_PermitMatch(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "permit")
	assertEqual(t, "matched", dec.Matched, true)
}

func TestEvaluateDeviceACL_DenyMatch(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "matched", dec.Matched, true)
}

func TestEvaluateDeviceACL_RuleOrder(t *testing.T) {
	// permit 在前 → 命中 permit
	s1 := newACLTestState(topology.DeviceRouter, "r1")
	s1.ACLs["2000"] = []*ACLRule{
		{ID: 5, Action: "permit", Protocol: "icmp", SrcIP: "10.0.0.10"},
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"},
	}
	s1.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec1 := EvaluateDeviceACL(s1, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "permit-first", dec1.Action, "permit")

	// deny 在前 → 首 deny 即停（命中被丢弃）
	s2 := newACLTestState(topology.DeviceRouter, "r1")
	s2.ACLs["2000"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"},
		{ID: 5, Action: "permit", Protocol: "icmp", SrcIP: "10.0.0.10"},
	}
	s2.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec2 := EvaluateDeviceACL(s2, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "deny-first", dec2.Action, "deny")
}

// ----------------------------------------------------------------------------
// EvaluatePathACL：方向模型（src=outbound、中转=inbound+outbound、dst=inbound）、首 deny 即停
// ----------------------------------------------------------------------------

func TestEvaluatePathACL_SourceOutbound(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 源设备（path[0]=r1）outbound ACL deny
	s.ACLs["3000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"r1": s}, []string{"r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "r1")
	assertEqual(t, "direction", dec.Direction, DirOutbound)
}

// TestEvaluateDeviceACL_TransitBothDirections 验证「中转设备逐方向独立评估」：
// 同一设备的 inbound 与 outbound 各绑定不同 ACL，分别判定。
func TestEvaluateDeviceACL_TransitBothDirections(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	s.ACLs["3000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	inDec := EvaluateDeviceACL(s, "r1", DirInbound, flow)
	assertEqual(t, "inbound action", inDec.Action, "permit")
	outDec := EvaluateDeviceACL(s, "r1", DirOutbound, flow)
	assertEqual(t, "outbound action", outDec.Action, "deny")
}

func TestEvaluatePathACL_TransitInbound(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 仅 inbound deny（dst 10.0.2.0/24）。路径中首个含 inbound 评估的设备是 r1。
	s.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "10.0.2.0", DstWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"r1": s}, []string{"pc1", "r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "r1") // 首个含 inbound 评估的设备
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

func TestEvaluatePathACL_DestinationInbound(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 目的设备（路径末端）入向 deny
	s.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "10.0.2.0", DstWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"pc2": s}, []string{"pc1", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "pc2")
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

// TestEvaluatePathACL_FirstDenyStops 验证「沿途首 deny 即停」（取交集，任一 deny 即丢）。
// 本评估器以单一 state 的 ACL 配置作用于路径各设备（设计 §3.2），故 deny 在
// 首个含对应方向评估的设备处被捕获。
func TestEvaluatePathACL_FirstDenyStops(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "10.0.2.0", DstWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"r1": s}, []string{"pc1", "r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "r1") // 首 deny 在 r1，不会继续到 pc2
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

func TestEvaluatePathACL_AllPermit(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	s.DeviceConfig["traffic-filter:outbound:2000"] = "2000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"pc1": s, "r1": s, "r2": s, "pc2": s}, []string{"pc1", "r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "permit")
}

// ----------------------------------------------------------------------------
// ResolveSourceIP / ComputeL3Path
// ----------------------------------------------------------------------------

func TestResolveSourceIP_Terminal(t *testing.T) {
	s := newACLTestState(topology.DevicePC, "pc1")
	s.HostIP = "10.0.0.10"
	assertEqual(t, "terminal src", ResolveSourceIP(s, "10.0.2.10", nil), "10.0.0.10")
}

func TestResolveSourceIP_L3LongestPrefix(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.Interfaces = map[string]*InterfaceConfig{
		"GE0/0/0": {Name: "GE0/0/0", IP: "10.0.0.1", Mask: "255.255.255.0"},
		"GE0/0/1": {Name: "GE0/0/1", IP: "10.0.1.1", Mask: "255.255.255.0"},
	}
	s.Routes = []*RouteEntry{
		{Destination: "10.0.2.0", Mask: "255.255.255.0", Interface: "GE0/0/1"},
		{Destination: "0.0.0.0", Mask: "0.0.0.0", Interface: "GE0/0/0"},
	}
	// 最长前缀命中 10.0.2.0/24 → 出口 GE0/0/1 → IP 10.0.1.1
	assertEqual(t, "l3 src", ResolveSourceIP(s, "10.0.2.10", nil), "10.0.1.1")
}

func TestComputeL3Path(t *testing.T) {
	topo := newACLTestTopo()
	s := newACLTestState(topology.DevicePC, "pc1")
	path := ComputeL3Path(s, "10.0.2.10", topo)
	want := []string{"pc1", "r1", "r2", "pc2"}
	if len(path) != len(want) {
		t.Fatalf("ComputeL3Path len = %d, want %d (%v)", len(path), len(want), path)
	}
	for i := range want {
		assertEqual(t, "path["+itoa(i)+"]", path[i], want[i])
	}
}

func TestIntegration_PingBlockedByACL(t *testing.T) {
	topo := newACLTestTopo()
	s := newACLTestState(topology.DevicePC, "pc1")
	s.HostIP = "10.0.0.10"
	// r1 入向 ACL 2000 deny 源 10.0.0.0/24
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	// 经拓扑推导路径：pc1 -> r1 -> r2 -> pc2
	path := ComputeL3Path(s, "10.0.2.10", topo)
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"r1": s}, path, flow)
	assertEqual(t, "integration action", dec.Action, "deny")
	assertEqual(t, "integration device", dec.DeviceID, "r1")
	assertEqual(t, "integration direction", dec.Direction, DirInbound)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	return string(rune('0' + i))
}
