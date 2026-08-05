package cli

// acl_eval_qa_test.go —— 独立回归测试（QA：严过关，不依赖实现者自测）。
//
// 目标：独立于 acl_eval_test.go 复核评估器行为，并构造真实「中转设备 ACL」
// 场景，验证 P1-C「真实过滤」是否在全路径（含中转路由器）生效。
//
// 关键发现（见 TestQA_TransitDeviceACL_NotEvaluated_Bug）：生产代码曾在
// EvaluatePathACL / CheckReachability 中只把「源设备」的 CLIState 传给评估器，
// 落在中转路由器自身的 traffic-filter ACL 从未被读取。P1-C Round 1 修复后，
// 评估器改为按 deviceID 从拓扑级 CLIState 注册表（API 层 r.cliStates 快照）
// 读取各设备「自身」的 ACL，本用例即验证该修复（中转设备 ACL 生效 → deny）。

import (
	"testing"

	"ensp-lab/internal/topology"
)

// ----------------------------------------------------------------------------
// 1. matchIP 边界（独立构造，补充实现者用例）
// ----------------------------------------------------------------------------

func TestQA_MatchIP_WildcardBoundaries(t *testing.T) {
	cases := []struct {
		name          string
		ip, ruleIP, wc string
		want          bool
	}{
		{"host-wildcard-exact", "10.0.0.5", "10.0.0.0", "0.0.0.0", false}, // /32 仅匹配 10.0.0.0
		{"host-wildcard-any", "0.0.0.0", "0.0.0.0", "255.255.255.255", true}, // /0 匹配任意
		{"slash25-in", "192.168.1.130", "192.168.1.128", "0.0.0.127", true}, // /25 覆盖 .128-.255
		{"slash25-out", "192.168.1.10", "192.168.1.128", "0.0.0.127", false},
		{"slash16-in", "172.16.5.5", "172.16.0.0", "0.0.255.255", true},
		{"empty-ip", "", "1.2.3.4", "0.0.0.0", false},       // 空 IP 视为不匹配
		{"empty-ruleip", "10.0.0.1", "", "0.0.0.0", true},    // 空 ruleIP = 不限该侧
		{"zero-ip-all", "0.0.0.0", "0.0.0.0", "0.0.0.0", true},
	}
	for _, c := range cases {
		assertEqual(t, c.name, matchIP(c.ip, c.ruleIP, c.wc), c.want)
	}
}

// ----------------------------------------------------------------------------
// 2. matchACLRule 协议/方向边界
// ----------------------------------------------------------------------------

func TestQA_MatchACLRule_Boundaries(t *testing.T) {
	// Protocol="" / "ip" 不限协议；精确协议大小写不敏感；src/dst 通配符独立。
	f := PacketTuple{SrcIP: "10.0.0.5", DstIP: "10.0.2.99", Proto: "tcp"}
	// 不限协议
	assertEqual(t, "proto-empty", matchACLRule(&ACLRule{Action: "permit"}, f), true)
	assertEqual(t, "proto-ip", matchACLRule(&ACLRule{Action: "permit", Protocol: "ip"}, f), true)
	// 精确匹配
	assertEqual(t, "proto-tcp", matchACLRule(&ACLRule{Action: "permit", Protocol: "tcp"}, f), true)
	assertEqual(t, "proto-udp-miss", matchACLRule(&ACLRule{Action: "permit", Protocol: "udp"}, f), false)
	assertEqual(t, "proto-icmp-miss", matchACLRule(&ACLRule{Action: "permit", Protocol: "icmp"}, f), false)
	// 大小写不敏感
	assertEqual(t, "proto-upper", matchACLRule(&ACLRule{Action: "permit", Protocol: "TCP"}, f), true)

	// src 通配符约束
	srcRule := &ACLRule{Action: "permit", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}
	assertEqual(t, "src-in", matchACLRule(srcRule, PacketTuple{SrcIP: "10.0.0.9", DstIP: "10.0.2.99", Proto: "icmp"}), true)
	assertEqual(t, "src-out", matchACLRule(srcRule, PacketTuple{SrcIP: "10.0.1.9", DstIP: "10.0.2.99", Proto: "icmp"}), false)

	// dst 通配符约束
	dstRule := &ACLRule{Action: "permit", Protocol: "icmp", DstIP: "10.0.2.0", DstWildcard: "0.0.0.255"}
	assertEqual(t, "dst-in", matchACLRule(dstRule, PacketTuple{SrcIP: "10.0.0.9", DstIP: "10.0.2.99", Proto: "icmp"}), true)
	assertEqual(t, "dst-out", matchACLRule(dstRule, PacketTuple{SrcIP: "10.0.0.9", DstIP: "10.0.3.99", Proto: "icmp"}), false)
}

// ----------------------------------------------------------------------------
// 3. 隐式 deny any（最易错点，独立验证）
// ----------------------------------------------------------------------------

func TestQA_ImplicitDenyAny_Independent(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 已绑定 ACL，但 permit 规则针对另一网段；本流未命中任何 permit → 隐式 deny any。
	s.ACLs["2000"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "192.168.99.0", SrcWildcard: "0.0.0.255"},
	}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "matched", dec.Matched, false)
	assertEqual(t, "acl", dec.ACLNum, "2000")

	// 已绑定 ACL 但规则列表为空 → 同样隐式 deny any。
	s2 := newACLTestState(topology.DeviceRouter, "r1")
	s2.ACLs["2000"] = []*ACLRule{}
	s2.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	dec2 := EvaluateDeviceACL(s2, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "empty-action", dec2.Action, "deny")
}

func TestQA_UnboundPermit_Independent(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	// 设备未绑定任何 traffic-filter → 放行，不经 DefaultACLTerminalAction。
	dec := EvaluateDeviceACL(s, "r1", DirInbound, PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"})
	assertEqual(t, "action", dec.Action, "permit")
	assertEqual(t, "matched", dec.Matched, false)
	assertEqual(t, "acl", dec.ACLNum, "")
}

// ----------------------------------------------------------------------------
// 4. 方向模型（src=outbound / 中转=inbound+outbound / dst=inbound）
//    说明：以下用例把 ACL 全部放在「单一被传入的 state」中，验证方向索引逻辑正确。
//    真正的中转设备各自 state 未被读取的问题见 §5。
// ----------------------------------------------------------------------------

func TestQA_DirectionModel_SrcOutboundOnly(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["3000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	// 路径首设备=源，仅评估 outbound → 命中 deny。
	dec := EvaluatePathACL(map[string]*CLIState{"r1": s}, []string{"r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "r1")
	assertEqual(t, "direction", dec.Direction, DirOutbound)
}

func TestQA_DirectionModel_DstInboundOnly(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "10.0.2.0", DstWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	// 末设备=目的，仅评估 inbound → 命中 deny。
	dec := EvaluatePathACL(map[string]*CLIState{"pc2": s}, []string{"pc1", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "pc2")
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

func TestQA_DirectionModel_AllPermit(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "r1")
	s.ACLs["2000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp"}}
	s.DeviceConfig["traffic-filter:inbound:2000"] = "2000"
	s.DeviceConfig["traffic-filter:outbound:2000"] = "2000"
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	dec := EvaluatePathACL(map[string]*CLIState{"pc1": s, "r1": s, "r2": s, "pc2": s}, []string{"pc1", "r1", "r2", "pc2"}, flow)
	assertEqual(t, "action", dec.Action, "permit")
}

// ----------------------------------------------------------------------------
// 5. 【关键】真实中转设备 ACL 未被评估（源码 bug 证据）
//
// 场景（贴近 PRD 用户故事 #2）：拓扑 pc1 - r1 - r2 - pc2。
//   - 在 r1（中转路由器）入向接口配置 traffic-filter inbound acl 2000 deny
//     源 10.0.0.0/24（即 PC 所在网段）。
//   - 从 pc1 ping pc2，报文进入 r1 入向应被拦截。
//
// 生产调用方式（mirror parser.go / diagnostic_handlers.go / cli_handlers.go）：
//   源设备 state 传入 EvaluatePathACL，路径含全部设备。
// 由于评估器只用「源设备」的 CLIState 读取 ACL/DeviceConfig，而 r1 的 ACL 在
// r1 自己的 CLIState 中，因此本应 deny 的流被错误放行（permit）。
// ----------------------------------------------------------------------------

func TestQA_TransitDeviceACL_NotEvaluated_Bug(t *testing.T) {
	topo := newACLTestTopo()

	// 源设备 pc1 的 state：无 ACL 配置（PC 上不会配 traffic-filter）。
	pc1State := newACLTestState(topology.DevicePC, "pc1")
	pc1State.HostIP = "10.0.0.10"

	// 中转路由器 r1 自己的 state：入向 deny 源 10.0.0.0/24（真实配置落点）。
	r1State := newACLTestState(topology.DeviceRouter, "r1")
	r1State.ACLs["2000"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"},
	}
	r1State.DeviceConfig["traffic-filter:inbound:2000"] = "2000"

	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}

	// 先证明规则本身正确：用 r1 自己的 state 评估 r1 入向 → deny。
	ruleDec := EvaluateDeviceACL(r1State, "r1", DirInbound, flow)
	assertEqual(t, "rule-correct-on-r1state", ruleDec.Action, "deny")

	// 复刻生产调用：评估器按 deviceID 从拓扑级 CLIState 注册表读取各设备「自身」
	// 的 CLIState（API 层 r.cliStates），而非仅用源设备 state。
	path := ComputeL3Path(pc1State, "10.0.2.10", topo)
	t.Logf("derived path = %v", path)
	states := map[string]*CLIState{
		"pc1": pc1State,
		"r1":  r1State,
		"r2":  newACLTestState(topology.DeviceL3Switch, "r2"),
		"pc2": newACLTestState(topology.DeviceServer, "pc2"),
	}
	dec := EvaluatePathACL(states, path, flow)

	// 期望：r1 入向 deny 生效 → dec.Action=="deny"（PRD P0-5：途径 L3 设备入向 ACL）。
	// 修复 P1-C Round 1：评估器现按 deviceID 读取各设备自身 state，中转 ACL 生效。
	assertEqual(t, "transit-acl action", dec.Action, "deny")
	assertEqual(t, "transit-acl device", dec.DeviceID, "r1")
	assertEqual(t, "transit-acl direction", dec.Direction, DirInbound)
}

// TestQA_SourceDeviceACL_Works 作为对照：ACL 确实落在「源设备」state 时，
// 现有评估器能正确拦截。用以证明 bug 仅针对「中转/目的设备」自带 ACL 的场景。
func TestQA_SourceDeviceACL_Works(t *testing.T) {
	topo := newACLTestTopo()
	pc1State := newACLTestState(topology.DevicePC, "pc1")
	pc1State.HostIP = "10.0.0.10"
	// 在源设备自身出向绑 deny（模拟「本机就是路由器且 ACL 在本机」的情形）。
	pc1State.ACLs["2000"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "10.0.0.0", SrcWildcard: "0.0.0.255"},
	}
	pc1State.DeviceConfig["traffic-filter:outbound:2000"] = "2000"
	path := ComputeL3Path(pc1State, "10.0.2.10", topo)
	flow := PacketTuple{SrcIP: "10.0.0.10", DstIP: "10.0.2.10", Proto: "icmp"}
	states := map[string]*CLIState{
		"pc1": pc1State,
		"r1":  newACLTestState(topology.DeviceRouter, "r1"),
		"r2":  newACLTestState(topology.DeviceL3Switch, "r2"),
		"pc2": newACLTestState(topology.DeviceServer, "pc2"),
	}
	dec := EvaluatePathACL(states, path, flow)
	assertEqual(t, "src-bound action", dec.Action, "deny")
}
