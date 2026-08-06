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

// ----------------------------------------------------------------------------
// P2 NAT 真实过滤：applyNAT / ComputeL3PathNAT / EvaluatePathACL 跨 NAT（对齐 AC5）
// ----------------------------------------------------------------------------

// newNATServerTopo 构造 nat server 场景拓扑：
//
//	out(203.0.113.50, PC) -- nat(203.0.113.1/192.168.1.1, Router) -- srv(192.168.1.100, Server)
//
// nat 设备配 nat server：公网 203.0.113.10 → 内网 192.168.1.100。
func newNATServerTopo() *topology.Topology {
	t := topology.NewTopology("tnat", "nat-test")
	t.AddDevice(&topology.Device{
		ID:   "out",
		Type: topology.DevicePC,
		Interfaces: map[string]*topology.Interface{
			"out0": {IPAddress: "203.0.113.50", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "nat",
		Type: topology.DeviceRouter,
		Interfaces: map[string]*topology.Interface{
			"nat-out": {IPAddress: "203.0.113.1", Status: "up"},
			"nat-in":  {IPAddress: "192.168.1.1", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "srv",
		Type: topology.DeviceServer,
		Interfaces: map[string]*topology.Interface{
			"srv0": {IPAddress: "192.168.1.100", Status: "up"},
		},
	})
	t.AddLink(&topology.Link{ID: "ln1", SourceDevice: "out", TargetDevice: "nat"})
	t.AddLink(&topology.Link{ID: "ln2", SourceDevice: "nat", TargetDevice: "srv"})
	return t
}

// newNATServerStates 返回 nat server 场景的 CLIState 注册表（nat 设备带 NAT 配置）。
func newNATServerStates() map[string]*CLIState {
	nat := newACLTestState(topology.DeviceRouter, "nat")
	nat.NAT = &NATConfig{
		Enabled: true,
		Servers: []NATServer{{GlobalIP: "203.0.113.10", InsideIP: "192.168.1.100"}},
	}
	out := newACLTestState(topology.DevicePC, "out")
	srv := newACLTestState(topology.DeviceServer, "srv")
	return map[string]*CLIState{"nat": nat, "out": out, "srv": srv}
}

// newNATOutboundTopo 构造 nat outbound 场景拓扑：
//
//	pc(192.168.1.10, PC) -- nat(192.168.1.1/203.0.113.1, Router) -- web(203.0.113.50, Server)
func newNATOutboundTopo() *topology.Topology {
	t := topology.NewTopology("tnat", "nat-out-test")
	t.AddDevice(&topology.Device{
		ID:   "pc",
		Type: topology.DevicePC,
		Interfaces: map[string]*topology.Interface{
			"pc0": {IPAddress: "192.168.1.10", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "nat",
		Type: topology.DeviceRouter,
		Interfaces: map[string]*topology.Interface{
			"nat-in":  {IPAddress: "192.168.1.1", Status: "up"},
			"nat-out": {IPAddress: "203.0.113.1", Status: "up"},
		},
	})
	t.AddDevice(&topology.Device{
		ID:   "web",
		Type: topology.DeviceServer,
		Interfaces: map[string]*topology.Interface{
			"web0": {IPAddress: "203.0.113.50", Status: "up"},
		},
	})
	t.AddLink(&topology.Link{ID: "lo1", SourceDevice: "pc", TargetDevice: "nat"})
	t.AddLink(&topology.Link{ID: "lo2", SourceDevice: "nat", TargetDevice: "web"})
	return t
}

// newNATOutboundState 构造 nat outbound 设备状态：mode="easy-ip" 或 "address-group"。
// ACL 2000 permit 源 192.168.1.0/24（命中内网源）；路由 0.0.0.0/0 朝出接口 nat-out。
func newNATOutboundState(mode string) *CLIState {
	nat := newACLTestState(topology.DeviceRouter, "nat")
	nat.Interfaces = map[string]*InterfaceConfig{
		"GigabitEthernet0/0": {Name: "GigabitEthernet0/0", IP: "192.168.1.1", Mask: "255.255.255.0"},
		"GigabitEthernet0/1": {Name: "GigabitEthernet0/1", IP: "203.0.113.1", Mask: "255.255.255.0"},
	}
	nat.Routes = []*RouteEntry{
		{Destination: "203.0.113.0", Mask: "255.255.255.0", Interface: "GigabitEthernet0/1"},
	}
	nat.ACLs["2000"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "192.168.1.0", SrcWildcard: "0.0.0.255"},
	}
	nat.NAT = &NATConfig{Enabled: true}
	if mode == "address-group" {
		nat.NAT.Outbounds = []NATOutbound{{ACLNum: 2000, Type: "address-group", AddressPool: 1}}
		nat.NAT.AddressPools = []NATAddressPool{{ID: 1, StartIP: "203.0.113.5", EndIP: "203.0.113.10"}}
	} else {
		nat.NAT.Outbounds = []NATOutbound{{ACLNum: 2000, Type: "easy-ip"}}
	}
	return nat
}

// ---- applyNAT：Easy IP 改写 ----

func TestApplyNAT_EasyIP(t *testing.T) {
	nat := newNATOutboundState("easy-ip")
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	got, ok := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "translated", ok, true)
	assertEqual(t, "srcIP", got.SrcIP, "203.0.113.1") // 出接口 IP（朝目标最长前缀匹配）
	assertEqual(t, "dstIP unchanged", got.DstIP, "203.0.113.50")
	// 纯函数：输入 flow 不被修改（值传递）
	assertEqual(t, "input srcIP unchanged", flow.SrcIP, "192.168.1.10")
}

// ---- applyNAT：address-group 首 IP 改写 ----

func TestApplyNAT_AddressGroupFirstIP(t *testing.T) {
	nat := newNATOutboundState("address-group")
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	got, ok := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "translated", ok, true)
	assertEqual(t, "srcIP", got.SrcIP, "203.0.113.5") // 地址池 StartIP（首 IP）
}

// ---- applyNAT：nat server DstIP 改写（仅 inbound 触发）----

func TestApplyNAT_NATServerDstIP(t *testing.T) {
	states := newNATServerStates()
	nat := states["nat"]
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "203.0.113.10", Proto: "icmp"}
	got, ok := applyNAT(nat, DirInbound, flow)
	assertEqual(t, "translated", ok, true)
	assertEqual(t, "dstIP", got.DstIP, "192.168.1.100") // GlobalIP → InsideIP
	assertEqual(t, "srcIP unchanged", got.SrcIP, "203.0.113.50")
	// outbound 方向不触发 nat server（仅 inbound）
	got2, ok2 := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "outbound not translated", ok2, false)
	assertEqual(t, "outbound dstIP unchanged", got2.DstIP, "203.0.113.10")
}

// ---- applyNAT：无匹配不改写 ----

func TestApplyNAT_NoMatch(t *testing.T) {
	nat := newNATOutboundState("easy-ip")
	// SrcIP 不在 ACL 2000 permit 域（192.168.1.0/24）→ 不转换
	flow := PacketTuple{SrcIP: "10.99.99.10", DstIP: "203.0.113.50", Proto: "icmp"}
	got, ok := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "translated", ok, false)
	assertEqual(t, "srcIP unchanged", got.SrcIP, "10.99.99.10")
	// 无 NAT 配置 → 原样返回
	plain := newACLTestState(topology.DeviceRouter, "r1")
	got3, ok3 := applyNAT(plain, DirOutbound, flow)
	assertEqual(t, "no nat translated", ok3, false)
	assertEqual(t, "no nat srcIP unchanged", got3.SrcIP, "10.99.99.10")
}

// ---- applyNAT：纯函数无副作用（多次调用一致 + 不改 state）----

func TestApplyNAT_PureNoSideEffect(t *testing.T) {
	nat := newNATOutboundState("easy-ip")
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	g1, ok1 := applyNAT(nat, DirOutbound, flow)
	g2, ok2 := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "ok1", ok1, true)
	assertEqual(t, "ok2", ok2, true)
	assertEqual(t, "srcIP consistent", g1.SrcIP, g2.SrcIP)
	assertEqual(t, "dstIP consistent", g1.DstIP, g2.DstIP)
	// state.NAT 未被修改
	if nat.NAT == nil || !nat.NAT.Enabled {
		t.Errorf("applyNAT 不应修改 state.NAT（Enabled 被改）")
	}
	if len(nat.NAT.Outbounds) != 1 || nat.NAT.Outbounds[0].Type != "easy-ip" {
		t.Errorf("applyNAT 不应修改 NAT.Outbounds，got %+v", nat.NAT.Outbounds)
	}
}

// ---- ComputeL3PathNAT：公网 IP 命中 GlobalIP → [...,natDev,insideDev] 且 natTranslated=true ----

func TestComputeL3PathNAT_ServerGlobalIP(t *testing.T) {
	topo := newNATServerTopo()
	states := newNATServerStates()
	srcState := states["out"]
	path, translated := ComputeL3PathNAT(states, srcState, "203.0.113.10", topo)
	assertEqual(t, "translated", translated, true)
	want := []string{"out", "nat", "srv"}
	if len(path) != len(want) {
		t.Fatalf("ComputeL3PathNAT len=%d want %d (%v)", len(path), len(want), path)
	}
	for i := range want {
		assertEqual(t, "path["+itoa(i)+"]", path[i], want[i])
	}
}

// ---- ComputeL3PathNAT：普通 IP → 委托 ComputeL3Path 且 natTranslated=false ----

func TestComputeL3PathNAT_NormalIPDelegates(t *testing.T) {
	topo := newACLTestTopo() // pc1(10.0.0.10)-r1-r2-pc2(10.0.2.10)
	srcState := newACLTestState(topology.DevicePC, "pc1")
	path, translated := ComputeL3PathNAT(map[string]*CLIState{"pc1": srcState}, srcState, "10.0.2.10", topo)
	assertEqual(t, "translated", translated, false)
	want := []string{"pc1", "r1", "r2", "pc2"}
	if len(path) != len(want) {
		t.Fatalf("ComputeL3PathNAT len=%d want %d (%v)", len(path), len(want), path)
	}
	for i := range want {
		assertEqual(t, "path["+itoa(i)+"]", path[i], want[i])
	}
}

// natServerACLStates 为跨 NAT ACL 测试构造注册表：在 nat 设备上按需绑定
// inbound(看 GlobalIP 203.0.113.10) / outbound(看 InsideIP 192.168.1.100) ACL，
// 在 srv 上按需绑定 inbound(看 InsideIP 192.168.1.100) ACL。
func natServerACLStates(inbound, outbound, srvInbound string) map[string]*CLIState {
	states := newNATServerStates()
	nat := states["nat"]
	if inbound != "" {
		nat.ACLs["4000"] = []*ACLRule{{ID: 10, Action: inbound, Protocol: "icmp", DstIP: "203.0.113.10"}}
		nat.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	}
	if outbound != "" {
		nat.ACLs["3000"] = []*ACLRule{{ID: 10, Action: outbound, Protocol: "icmp", DstIP: "192.168.1.100"}}
		nat.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	}
	if srvInbound != "" {
		srv := states["srv"]
		srv.ACLs["5000"] = []*ACLRule{{ID: 10, Action: srvInbound, Protocol: "icmp", DstIP: "192.168.1.100"}}
		srv.DeviceConfig["traffic-filter:inbound:5000"] = "5000"
	}
	return states
}

// NAT 设备 inbound ACL 看转换前的 GlobalIP（命中即 deny）。
func TestEvaluatePathACL_NATInboundSeesGlobalIP(t *testing.T) {
	states := natServerACLStates("deny", "", "")
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "203.0.113.10", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "nat")
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

// NAT 设备 outbound ACL 看转换后的 InsideIP（经 applyNAT(inbound) 改写 DstIP）。
func TestEvaluatePathACL_NATOutboundSeesInsideIP(t *testing.T) {
	states := natServerACLStates("permit", "deny", "")
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "203.0.113.10", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "nat")
	assertEqual(t, "direction", dec.Direction, DirOutbound)
}

// NAT 改写后，下游（srv）inbound ACL 基于转换后 InsideIP 判定 deny。
func TestEvaluatePathACL_NATDownstreamSeesTranslatedIP(t *testing.T) {
	states := natServerACLStates("permit", "", "deny")
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "203.0.113.10", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "srv")
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

// NAT 设备与下游 ACL 均 permit → 全 permit（转换后 IP 仍被正确放行）。
func TestEvaluatePathACL_NATAllPermit(t *testing.T) {
	states := natServerACLStates("permit", "permit", "permit")
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "203.0.113.10", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "permit")
}
