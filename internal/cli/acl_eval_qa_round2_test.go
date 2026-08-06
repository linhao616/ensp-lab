package cli

// acl_eval_qa_round2_test.go —— P1-C 第 2 轮独立回归（QA：严过关）。
//
// 本文件不依赖 acl_eval_qa_test.go 的任何用例构造，独立搭建一套全新拓扑与
// 场景，专门证明工程师（寇豆码）对「中转/目的设备 ACL 未被评估」bug 的修复
// 确实生效（registry 真正按 deviceID 取各设备自身 state），且未引入新回归。
//
// 关键对照思想：
//   - 修复点 = EvaluatePathACL / CheckReachability / Render*WithACL 从「单一源
//     state」改为「拓扑级 deviceID→*CLIState 注册表」。
//   - 因此「仅当某设备的自身 state 被注册进 registry 时，其 ACL 才会生效」。
//     本文件用「含该设备 / 不含该设备」两组 registry 对照，证明生效依赖注册表
//     （而非巧合），并把该证据与 API 层 r.cliStateRegistry() 全量快照对齐。

import (
	"strings"
	"testing"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// ----------------------------------------------------------------------------
// 独立拓扑构造（与 newACLTestTopo 完全不同的设备 ID / IP 段，避免复用其结构）
//
//	h1(PC,172.16.1.10) ── gw1(Router,172.16.1.1/172.16.2.1)
//	                       ── gw2(L3Switch,172.16.2.2/172.16.3.1)
//	                          ── gw3(Router,172.16.3.2/172.16.4.1)
//	                             ── h2(Server,172.16.4.10)
//
// 路径 h1 → h2 = [h1, gw1, gw2, gw3, h2]
// ----------------------------------------------------------------------------

func buildRound2Topo() *topology.Topology {
	tp := topology.NewTopology("rt2", "qa-round2")
	tp.AddDevice(&topology.Device{
		ID:   "h1",
		Type: topology.DevicePC,
		Interfaces: map[string]*topology.Interface{
			"Ethernet0": {IPAddress: "172.16.1.10", Status: "up"},
		},
	})
	tp.AddDevice(&topology.Device{
		ID:   "gw1",
		Type: topology.DeviceRouter,
		Interfaces: map[string]*topology.Interface{
			"GE0/0/0": {IPAddress: "172.16.1.1", Status: "up"},
			"GE0/0/1": {IPAddress: "172.16.2.1", Status: "up"},
		},
	})
	tp.AddDevice(&topology.Device{
		ID:   "gw2",
		Type: topology.DeviceL3Switch,
		Interfaces: map[string]*topology.Interface{
			"GE0/0/0": {IPAddress: "172.16.2.2", Status: "up"},
			"GE0/0/1": {IPAddress: "172.16.3.1", Status: "up"},
		},
	})
	tp.AddDevice(&topology.Device{
		ID:   "gw3",
		Type: topology.DeviceRouter,
		Interfaces: map[string]*topology.Interface{
			"GE0/0/0": {IPAddress: "172.16.3.2", Status: "up"},
			"GE0/0/1": {IPAddress: "172.16.4.1", Status: "up"},
		},
	})
	tp.AddDevice(&topology.Device{
		ID:   "h2",
		Type: topology.DeviceServer,
		Interfaces: map[string]*topology.Interface{
			"Ethernet0": {IPAddress: "172.16.4.10", Status: "up"},
		},
	})
	tp.AddLink(&topology.Link{ID: "rl1", SourceDevice: "h1", TargetDevice: "gw1"})
	tp.AddLink(&topology.Link{ID: "rl2", SourceDevice: "gw1", TargetDevice: "gw2"})
	tp.AddLink(&topology.Link{ID: "rl3", SourceDevice: "gw2", TargetDevice: "gw3"})
	tp.AddLink(&topology.Link{ID: "rl4", SourceDevice: "gw3", TargetDevice: "h2"})
	return tp
}

// newRound2States 构造含全部设备自身 state 的注册表（对齐 API 层 r.cliStateRegistry 全量快照）。
func newRound2States() map[string]*CLIState {
	return map[string]*CLIState{
		"h1":  newACLTestState(topology.DevicePC, "h1"),
		"gw1": newACLTestState(topology.DeviceRouter, "gw1"),
		"gw2": newACLTestState(topology.DeviceL3Switch, "gw2"),
		"gw3": newACLTestState(topology.DeviceRouter, "gw3"),
		"h2":  newACLTestState(topology.DeviceServer, "h2"),
	}
}

const (
	round2SrcIP = "172.16.1.10"
	round2DstIP = "172.16.4.10"
)

func round2Flow() PacketTuple {
	return PacketTuple{SrcIP: round2SrcIP, DstIP: round2DstIP, Proto: "icmp"}
}

// ----------------------------------------------------------------------------
// 1. 【核心 bug 修复·独立证明】中间中转设备(gw2)自身 state 入向 ACL 生效
//    且生效「依赖 registry 含该设备」：含 gw2 → deny；不含 gw2 → permit。
// ----------------------------------------------------------------------------

func TestR2_TransitMiddleDeviceACL_RegistryBound(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	gw2State := newRound2States()["gw2"]
	gw2State.ACLs["2010"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	gw2State.DeviceConfig["traffic-filter:inbound:2010"] = "2010"

	path := ComputeL3Path(h1State, round2DstIP, tp)
	if len(path) == 0 {
		t.Fatalf("ComputeL3Path 返回空，拓扑推导失败")
	}

	// 注册表含 gw2 自身 state → 应 deny（修复生效）。
	statesWith := newRound2States()
	statesWith["gw2"] = gw2State
	decWith := EvaluatePathACL(statesWith, path, round2Flow())
	assertEqual(t, "with-gw2 action", decWith.Action, "deny")
	assertEqual(t, "with-gw2 device", decWith.DeviceID, "gw2")
	assertEqual(t, "with-gw2 direction", decWith.Direction, DirInbound)

	// 注册表不含 gw2（模拟旧 bug：仅源 state 被注册）→ 应 permit（gw2 ACL 未被读取）。
	statesWithout := newRound2States()
	delete(statesWithout, "gw2")
	decWithout := EvaluatePathACL(statesWithout, path, round2Flow())
	assertEqual(t, "without-gw2 action", decWithout.Action, "permit")

	// 结论性断言：同一流，仅因「gw2 是否进入 registry」而改变结果，
	// 证明确实按 deviceID 读各设备自身 state（非源 state 通吃）。
	if decWith.Action == decWithout.Action {
		t.Errorf("registry 是否含 gw2 未改变判定（%s），修复未真正按设备隔离", decWith.Action)
	}
}

// ----------------------------------------------------------------------------
// 2. 目的设备(h2)自身 state 入向 ACL 生效（验证 registry 也覆盖末端设备）。
// ----------------------------------------------------------------------------

func TestR2_DestinationDeviceACL_RegistryBound(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	h2State := newRound2States()["h2"]
	h2State.ACLs["2020"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "172.16.4.0", DstWildcard: "0.0.0.255"},
	}
	h2State.DeviceConfig["traffic-filter:inbound:2020"] = "2020"

	path := ComputeL3Path(h1State, round2DstIP, tp)
	states := newRound2States()
	states["h2"] = h2State
	dec := EvaluatePathACL(states, path, round2Flow())
	assertEqual(t, "dst action", dec.Action, "deny")
	assertEqual(t, "dst device", dec.DeviceID, "h2")
	assertEqual(t, "dst direction", dec.Direction, DirInbound)
}

// ----------------------------------------------------------------------------
// 3. 源设备自身 outbound ACL 仍生效（对照：bug 仅影响中转/目的，源向来正确）。
// ----------------------------------------------------------------------------

func TestR2_SourceDeviceACL_StillWorks(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP
	h1State.ACLs["2030"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	h1State.DeviceConfig["traffic-filter:outbound:2030"] = "2030"

	path := ComputeL3Path(h1State, round2DstIP, tp)
	states := newRound2States()
	states["h1"] = h1State
	dec := EvaluatePathACL(states, path, round2Flow())
	assertEqual(t, "src action", dec.Action, "deny")
	assertEqual(t, "src device", dec.DeviceID, "h1")
	assertEqual(t, "src direction", dec.Direction, DirOutbound)
}

// ----------------------------------------------------------------------------
// 4. 三路介入全验证。
// ----------------------------------------------------------------------------

// 4a. ping 路径：中转 gw1 入向 deny → 返回 "unreachable (ACL 拦截...)"
func TestR2_PingIntegration_TransitDenied(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	gw1State := newRound2States()["gw1"]
	gw1State.ACLs["2040"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	gw1State.DeviceConfig["traffic-filter:inbound:2040"] = "2040"

	states := newRound2States()
	states["gw1"] = gw1State

	out := executePingWithContext(states, h1State, round2DstIP, tp)
	t.Logf("ping output = %q", out)
	if !strings.Contains(out, "unreachable") {
		t.Errorf("ping 应不可达，实际输出: %q", out)
	}
	if !strings.Contains(out, "ACL 拦截") {
		t.Errorf("ping 应标注 ACL 拦截，实际输出: %q", out)
	}
	if !strings.Contains(out, "gw1") || !strings.Contains(out, "2040") {
		t.Errorf("ping 拦截信息应含设备 gw1 与 acl 2040，实际输出: %q", out)
	}
}

// 4b. tracert 路径：命中 gw2 入向 deny → 前跳正常、命中跳起 "* * *" + 注记
func TestR2_TracerouteIntegration_TransitDenied(t *testing.T) {
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	gw2State := newRound2States()["gw2"]
	gw2State.ACLs["2010"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	gw2State.DeviceConfig["traffic-filter:inbound:2010"] = "2010"

	res := &sim.TracerouteResult{
		TargetIP: round2DstIP,
		Reached:  true,
		Hops: []sim.TracerouteHop{
			{Hop: 1, DeviceID: "gw1", IP: "172.16.2.1", DelayMs: 1},
			{Hop: 2, DeviceID: "gw2", IP: "172.16.3.1", DelayMs: 1},
			{Hop: 3, DeviceID: "gw3", IP: "172.16.4.1", DelayMs: 1},
		},
	}
	states := newRound2States()
	states["gw2"] = gw2State

	out := RenderTracerouteWithACL(states, h1State, res, round2DstIP, 30, nil)
	t.Logf("tracert output =\n%s", out)
	if !strings.Contains(out, "ACL 拦截") {
		t.Errorf("tracert 应含 ACL 拦截注记，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "Request timed out") {
		t.Errorf("tracert 命中跳后应出现 * * * 超时行，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "blocked by ACL") {
		t.Errorf("tracert 应标注 blocked by ACL，实际输出:\n%s", out)
	}
}

// 4c. CheckReachability：中转 gw2 入向 deny → 返回 false（视为不可达）
func TestR2_CheckReachability_TransitDenied(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	gw2State := newRound2States()["gw2"]
	gw2State.ACLs["2010"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	gw2State.DeviceConfig["traffic-filter:inbound:2010"] = "2010"

	states := newRound2States()
	states["gw2"] = gw2State

	if CheckReachability(states, h1State, round2DstIP, tp) {
		t.Errorf("CheckReachability 在中转 gw2 入向 deny 时应返回 false")
	}

	// 对照：移除 gw2 ACL → 可达（true）
	clean := newRound2States()
	if !CheckReachability(clean, h1State, round2DstIP, tp) {
		t.Errorf("CheckReachability 在无 ACL 时应为 true（可达）")
	}
}

// ----------------------------------------------------------------------------
// 5. 隐式 deny any：已绑定 ACL 但 flow 未命中 permit → deny；未绑定 → permit。
// ----------------------------------------------------------------------------

func TestR2_ImplicitDenyAny_Independent(t *testing.T) {
	s := newACLTestState(topology.DeviceRouter, "gw1")
	s.ACLs["2050"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "10.10.0.0", SrcWildcard: "0.0.0.255"},
	}
	s.DeviceConfig["traffic-filter:inbound:2050"] = "2050"
	dec := EvaluateDeviceACL(s, "gw1", DirInbound, round2Flow())
	assertEqual(t, "implicit-deny action", dec.Action, "deny")
	assertEqual(t, "implicit-deny matched", dec.Matched, false)
	assertEqual(t, "implicit-deny acl", dec.ACLNum, "2050")

	// 未绑定 → permit（不经 DefaultACLTerminalAction）
	s2 := newACLTestState(topology.DeviceRouter, "gw1")
	dec2 := EvaluateDeviceACL(s2, "gw1", DirInbound, round2Flow())
	assertEqual(t, "unbound action", dec2.Action, "permit")
	assertEqual(t, "unbound acl", dec2.ACLNum, "")
}

// ----------------------------------------------------------------------------
// 6. 方向模型：src=outbound / 中转=inbound+outbound / dst=inbound / 首 deny 即停。
// ----------------------------------------------------------------------------

func TestR2_DirectionModel_TransitBothDirections(t *testing.T) {
	// 中转 gw2：inbound permit + outbound deny，flow 应被 outbound 拦截。
	s := newACLTestState(topology.DeviceL3Switch, "gw2")
	s.ACLs["2060"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp"}}
	s.DeviceConfig["traffic-filter:inbound:2060"] = "2060"
	s.ACLs["2061"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"}}
	s.DeviceConfig["traffic-filter:outbound:2061"] = "2061"

	// gw2 作为唯一中转（路径 [h1, gw2, h2]）。
	states := map[string]*CLIState{"gw2": s}
	dec := EvaluatePathACL(states, []string{"h1", "gw2", "h2"}, round2Flow())
	assertEqual(t, "transit action", dec.Action, "deny")
	assertEqual(t, "transit device", dec.DeviceID, "gw2")
	// gw2 是中转：先 inbound（permit）再 outbound（deny）→ 命中 outbound。
	assertEqual(t, "transit direction", dec.Direction, DirOutbound)
}

func TestR2_FirstDenyStops(t *testing.T) {
	// 源 h1 outbound deny 与目的 h2 inbound deny 同时存在，应停在首 deny（源 outbound）。
	srcState := newACLTestState(topology.DevicePC, "h1")
	srcState.HostIP = round2SrcIP
	srcState.ACLs["2070"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp"}}
	srcState.DeviceConfig["traffic-filter:outbound:2070"] = "2070"

	dstState := newACLTestState(topology.DeviceServer, "h2")
	dstState.ACLs["2071"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp"}}
	dstState.DeviceConfig["traffic-filter:inbound:2071"] = "2071"

	states := map[string]*CLIState{"h1": srcState, "h2": dstState}
	dec := EvaluatePathACL(states, []string{"h1", "gw1", "gw2", "gw3", "h2"}, round2Flow())
	assertEqual(t, "first-deny action", dec.Action, "deny")
	assertEqual(t, "first-deny device", dec.DeviceID, "h1")
	assertEqual(t, "first-deny direction", dec.Direction, DirOutbound)
}

// ----------------------------------------------------------------------------
// 7. RenderPingWithACL 渲染层：中转 deny → "destination unreachable (ACL 拦截...)"
// ----------------------------------------------------------------------------

func TestR2_RenderPingWithACL_TransitDenied(t *testing.T) {
	tp := buildRound2Topo()
	h1State := newACLTestState(topology.DevicePC, "h1")
	h1State.HostIP = round2SrcIP

	gw1State := newRound2States()["gw1"]
	gw1State.ACLs["2040"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "172.16.1.0", SrcWildcard: "0.0.0.255"},
	}
	gw1State.DeviceConfig["traffic-filter:inbound:2040"] = "2040"
	states := newRound2States()
	states["gw1"] = gw1State

	res := &sim.PingResult{Sent: 4, Received: 4, Details: []string{"reply from 172.16.4.10"}}
	out := RenderPingWithACL(states, h1State, res, round2DstIP, tp)
	t.Logf("renderPing output = %q", out)
	if !strings.Contains(out, "destination unreachable") {
		t.Errorf("RenderPingWithACL 应渲染不可达，实际: %q", out)
	}
	if !strings.Contains(out, "ACL 拦截") {
		t.Errorf("RenderPingWithACL 应标注 ACL 拦截，实际: %q", out)
	}
}
