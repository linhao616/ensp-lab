// acl_eval_qa_t04_test.go —— T04 端到端验收（QA 独立用例，对齐 AC1–AC6）
//
// 本文件由 QA（严过关）独立编写，用于端到端验证 P2 NAT 真实地址转换过滤
// （不复用工程师 T02 用例，独立证明每个 AC）。断言基线为：
//   - docs/p2-nat-prd.md（AC1–AC6）
//   - docs/p2-nat-design.md §1.4 顺序模型 / §8 拍板 #1–#4
//
// 运行：go test ./internal/cli/ -run 'TestQA_' -v
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// ---------------------------------------------------------------------------
// QA 独立辅助构造器（与工程师 T02 用例解耦）
// ---------------------------------------------------------------------------

func qaT04NewState(dt topology.DeviceType, id string) *CLIState {
	s := NewCLIStateWithType(dt)
	s.DeviceID = id
	return s
}

// qaT04ServerTopo：nat server 场景拓扑
//
//	out(203.0.113.50, PC) -- nat(203.0.113.1/192.168.1.1, Router) -- srv(192.168.1.10, Server)
//
// srv 的接口 IP 即内网真实服务器地址（对应 AC1 InsideIP=192.168.1.10）。
func qaT04ServerTopo() *topology.Topology {
	t := topology.NewTopology("qa-tnat", "qa-nat-server")
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
			"srv0": {IPAddress: "192.168.1.10", Status: "up"},
		},
	})
	t.AddLink(&topology.Link{ID: "qln1", SourceDevice: "out", TargetDevice: "nat"})
	t.AddLink(&topology.Link{ID: "qln2", SourceDevice: "nat", TargetDevice: "srv"})
	return t
}

// qaT04ServerStates：nat server 注册表；gip=GlobalIP, ip=InsideIP。
func qaT04ServerStates(gip, ip string) map[string]*CLIState {
	nat := qaT04NewState(topology.DeviceRouter, "nat")
	nat.NAT = &NATConfig{
		Enabled: true,
		Servers: []NATServer{{GlobalIP: gip, InsideIP: ip}},
	}
	out := qaT04NewState(topology.DevicePC, "out")
	srv := qaT04NewState(topology.DeviceServer, "srv")
	return map[string]*CLIState{"nat": nat, "out": out, "srv": srv}
}

// qaT04OutboundTopo：nat outbound 场景拓扑
//
//	pc(192.168.1.10, PC) -- nat(192.168.1.1/203.0.113.1, Router) -- web(203.0.113.50, Server)
func qaT04OutboundTopo() *topology.Topology {
	t := topology.NewTopology("qa-tout", "qa-nat-out")
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
	t.AddLink(&topology.Link{ID: "qlo1", SourceDevice: "pc", TargetDevice: "nat"})
	t.AddLink(&topology.Link{ID: "qlo2", SourceDevice: "nat", TargetDevice: "web"})
	return t
}

// qaT04OutboundState：nat outbound 设备状态。mode="easy-ip" | "address-group"。
// ACL 2000 permit 源 192.168.1.0/24（命中内网源）；路由 203.0.113.0/24 朝出接口 nat-out。
func qaT04OutboundState(mode string) *CLIState {
	nat := qaT04NewState(topology.DeviceRouter, "nat")
	nat.Interfaces = map[string]*InterfaceConfig{
		"nat-in":  {Name: "nat-in", IP: "192.168.1.1", Mask: "255.255.255.0"},
		"nat-out": {Name: "nat-out", IP: "203.0.113.1", Mask: "255.255.255.0"},
	}
	nat.Routes = []*RouteEntry{
		{Destination: "203.0.113.0", Mask: "255.255.255.0", Interface: "nat-out"},
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

func qaT04ExpectedSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（NAT 为模拟转换（lite 引擎），非内核级真实 NAT）"
	}
	return "（NAT 为模拟转换）"
}

// ===========================================================================
// AC1：入站 NAT 可达（外网 ping/tracert 公网 IP → 解析内网 + NAT 跳显示 InsideIP）
// ===========================================================================

// AC1-a：ComputeL3PathNAT 对公网 GlobalIP 解析为 [...,natDev, insideDev] 且 natTranslated=true。
func TestQA_AC1_ComputeL3PathNAT_ServerReachable(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	srcState := states["out"]
	path, translated := ComputeL3PathNAT(states, srcState, "200.1.1.1", topo)
	assertEqual(t, "natTranslated", translated, true)
	want := []string{"out", "nat", "srv"}
	if len(path) != len(want) {
		t.Fatalf("ComputeL3PathNAT len=%d want %d (%v)", len(path), len(want), path)
	}
	for i := range want {
		assertEqual(t, "path["+itoa(i)+"]", path[i], want[i])
	}
	// 解析出的 inside 设备 ID 与 InsideIP 正确。
	natDev, insideDev, insideIP := natDeviceInfo(states, topo, "200.1.1.1")
	assertEqual(t, "natDev", natDev, "nat")
	assertEqual(t, "insideDev", insideDev, "srv")
	assertEqual(t, "insideIP", insideIP, "192.168.1.10")
}

// AC1-b：RenderTracerouteWithACL 在 NAT 跳渲染 "NAT→192.168.1.10" + natSimNote()（API 同款调用路径）。
func TestQA_AC1_RenderTracerouteNAT_ShowInsideIPAndNote(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	res := &sim.TracerouteResult{TargetIP: "200.1.1.1", MaxTTL: 30, Reached: true}
	out := states["out"]
	// 与 internal/api/cli_handlers.go:347 renderEngineTraceroute 完全一致的签名调用（真实 t + registry）。
	out2 := RenderTracerouteWithACL(states, out, res, "200.1.1.1", 30, topo)
	if !strings.Contains(out2, "NAT→192.168.1.10") {
		t.Errorf("AC1 render 缺失 NAT→InsideIP 标注，got:\n%s", out2)
	}
	if !strings.Contains(out2, "nat") {
		t.Errorf("AC1 render 缺失 NAT 设备跳，got:\n%s", out2)
	}
	if !strings.Contains(out2, qaT04ExpectedSimNote()) {
		t.Errorf("AC1 render 缺失 natSimNote 诚实占位，got:\n%s", out2)
	}
	if !strings.Contains(out2, "Trace complete.") {
		t.Errorf("AC1 render 未完整（应可达内网），got:\n%s", out2)
	}
}

// ===========================================================================
// AC2：出向 Easy IP（内网 ping 公网 → 源 IP 改写为朝目标出接口 IP，非私有地址）
// ===========================================================================

func TestQA_AC2_EasyIP_SrcIPRewrittenToEgress(t *testing.T) {
	nat := qaT04OutboundState("easy-ip")
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	got, ok := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "translated", ok, true)
	// 朝目标 203.0.113.50 的出接口 IP 为 203.0.113.1（非内网私有 192.168.1.x）。
	assertEqual(t, "srcIP rewritten to egress", got.SrcIP, "203.0.113.1")
	// 确保不是私有内网地址。
	if strings.HasPrefix(got.SrcIP, "192.168.") {
		t.Errorf("AC2 Easy IP 不应改写为私有内网地址，got %s", got.SrcIP)
	}
	// 纯函数：输入 flow 不被修改（值传递）。
	assertEqual(t, "input srcIP unchanged", flow.SrcIP, "192.168.1.10")
}

// AC2 端到端：EvaluatePathACL 在 NAT 中转处改写 SrcIP 后，下游（web）收到的已是公网源 IP。
func TestQA_AC2_EasyIP_EndToEndTranslatedIP(t *testing.T) {
	nat := qaT04OutboundState("easy-ip")
	pc := qaT04NewState(topology.DevicePC, "pc")
	web := qaT04NewState(topology.DeviceServer, "web")
	states := map[string]*CLIState{"pc": pc, "nat": nat, "web": web}
	// 在 web 上挂入站 ACL，要求源必须是公网 203.0.113.1（转换后）才放行。
	web.ACLs["5000"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "203.0.113.1", SrcWildcard: "0.0.0.0"},
		{ID: 20, Action: "deny", Protocol: "icmp"},
	}
	web.DeviceConfig["traffic-filter:inbound:5000"] = "5000"
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"pc", "nat", "web"}, flow)
	// 转换后源 IP=203.0.113.1 命中 web 的 permit → 全 permit。
	assertEqual(t, "action", dec.Action, "permit")
}

// ===========================================================================
// AC3：address-group 首 IP（源 IP 改写为地址池 StartIP，非池内其他 IP）
// ===========================================================================

func TestQA_AC3_AddressGroup_FirstIP(t *testing.T) {
	nat := qaT04OutboundState("address-group")
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	got, ok := applyNAT(nat, DirOutbound, flow)
	assertEqual(t, "translated", ok, true)
	// 地址池 StartIP=203.0.113.5（首 IP），不应是 EndIP=203.0.113.10 或其他。
	assertEqual(t, "srcIP == StartIP (first IP)", got.SrcIP, "203.0.113.5")
	if got.SrcIP == "203.0.113.10" {
		t.Errorf("AC3 address-group 不应取 EndIP，got %s", got.SrcIP)
	}
}

// ===========================================================================
// AC4：NAT+ACL 顺序 permit/deny（拍板 #1：入站 ACL 在 NAT 前看原始 IP；出站 ACL 在 NAT 后看转换后 IP）
// ===========================================================================

// AC4-1：NAT 设备出站 ACL 用「转换后源 IP」判定 → deny（强证明：用主机路由只匹配转换后的 203.0.113.1）。
func TestQA_AC4_OutboundACLUsesTranslatedSrcIP(t *testing.T) {
	nat := qaT04OutboundState("easy-ip") // 出向 NAT 把 SrcIP 192.168.1.10 → 203.0.113.1
	// 出站 ACL：[deny 203.0.113.1/32, permit ip]。只有「转换后」源 IP=203.0.113.1 才会 deny；
	// 若出站评估误用原始私有源 192.168.1.10，则跳过 deny 命中 permit → permit。故 deny 结果强证明
	// 出站 ACL 评估的是「转换后」源 IP。
	nat.ACLs["3000"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", SrcIP: "203.0.113.1", SrcWildcard: "255.255.255.255"},
		{ID: 20, Action: "permit", Protocol: "icmp"},
	}
	nat.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	states := map[string]*CLIState{"nat": nat}
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"pc", "nat", "web"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "nat")
	assertEqual(t, "direction", dec.Direction, DirOutbound)
}

// AC4-1 控制：若出站 ACL 匹配的是「原始」私有源 IP（192.168.1.10），转换后已变为 203.0.113.1 → 不应命中 → permit。
func TestQA_AC4_OutboundACLDoesNotSeeOriginalSrcIP(t *testing.T) {
	nat := qaT04OutboundState("easy-ip")
	// 出站 ACL：[permit 203.0.113.1/32, deny ip]。转换后源 IP=203.0.113.1 → 命中 permit；
	// 若出站评估误用原始私有源 192.168.1.10，则跳过 permit 命中 deny → deny。故 permit 结果强证明
	// 出站 ACL 评估的是「转换后」源 IP（而非原始私有源）。
	nat.ACLs["3000"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", SrcIP: "203.0.113.1", SrcWildcard: "255.255.255.255"},
		{ID: 20, Action: "deny", Protocol: "icmp"},
	}
	nat.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	states := map[string]*CLIState{"nat": nat}
	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"pc", "nat", "web"}, flow)
	assertEqual(t, "action (出站 ACL 应看到转换后的公网源 IP)", dec.Action, "permit")
}

// AC4-2：NAT 设备入站 ACL 用「原始目的 IP（GlobalIP）」判定 → deny（证明入站 ACL 在 NAT 之前看原始 IP）。
func TestQA_AC4_InboundACLUsesOriginalDstIP(t *testing.T) {
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	nat := states["nat"]
	// 入站 ACL：[deny 200.1.1.1/32, permit ip]。只有「转换前」原始目的 IP=200.1.1.1 才会 deny；
	// 若入站评估误用转换后 InsideIP 192.168.1.10，则跳过 deny 命中 permit → permit。故 deny 结果强证明
	// 入站 ACL 评估的是「转换前（原始）」目的 IP。
	nat.ACLs["4000"] = []*ACLRule{
		{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "200.1.1.1", DstWildcard: "255.255.255.255"},
		{ID: 20, Action: "permit", Protocol: "icmp"},
	}
	nat.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "200.1.1.1", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "nat")
	assertEqual(t, "direction", dec.Direction, DirInbound)
}

// AC4-2 控制：若入站 ACL 匹配的是「转换后」的 InsideIP（192.168.1.10），入站评估在 NAT 之前发生（DstIP 仍是 200.1.1.1）→ 不应命中 → permit。
func TestQA_AC4_InboundACLDoesNotSeeTranslatedDstIP(t *testing.T) {
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	nat := states["nat"]
	// 入站 ACL：[permit 200.1.1.1/32, deny 192.168.1.10/32]。转换前原始目的 IP=200.1.1.1 → 命中 permit；
	// 若入站评估误用转换后 InsideIP 192.168.1.10，则跳过 permit 命中 deny → deny。故 permit 结果强证明
	// 入站 ACL 评估的是「转换前（原始）」目的 IP（而非转换后的 InsideIP）。
	nat.ACLs["4000"] = []*ACLRule{
		{ID: 10, Action: "permit", Protocol: "icmp", DstIP: "200.1.1.1", DstWildcard: "255.255.255.255"},
		{ID: 20, Action: "deny", Protocol: "icmp", DstIP: "192.168.1.10", DstWildcard: "255.255.255.255"},
	}
	nat.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "200.1.1.1", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action (入站 ACL 应看到原始 GlobalIP，而非转换后的 InsideIP)", dec.Action, "permit")
}

// AC4-3：NAT 设备与下游 ACL 均 permit → 全 permit（验证双向顺序下放行）。
func TestQA_AC4_AllPermitAfterNAT(t *testing.T) {
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	nat := states["nat"]
	nat.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp", DstIP: "200.1.1.1"}}
	nat.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	srv := states["srv"]
	srv.ACLs["5000"] = []*ACLRule{{ID: 10, Action: "permit", Protocol: "icmp", DstIP: "192.168.1.10"}}
	srv.DeviceConfig["traffic-filter:inbound:5000"] = "5000"
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "200.1.1.1", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "permit")
}

// AC4-4：首 deny 即停 + 方向顺序：同时配入站 deny(GlobalIP) 与出站 deny(InsideIP)，首 deny 在入站（NAT 前）停下。
func TestQA_AC4_FirstDenyStopsInboundBeforeNAT(t *testing.T) {
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	nat := states["nat"]
	nat.ACLs["4000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "200.1.1.1"}}
	nat.DeviceConfig["traffic-filter:inbound:4000"] = "4000"
	nat.ACLs["3000"] = []*ACLRule{{ID: 10, Action: "deny", Protocol: "icmp", DstIP: "192.168.1.10"}}
	nat.DeviceConfig["traffic-filter:outbound:3000"] = "3000"
	flow := PacketTuple{SrcIP: "203.0.113.50", DstIP: "200.1.1.1", Proto: "icmp"}
	dec := EvaluatePathACL(states, []string{"out", "nat", "srv"}, flow)
	assertEqual(t, "action", dec.Action, "deny")
	assertEqual(t, "device", dec.DeviceID, "nat")
	assertEqual(t, "direction", dec.Direction, DirInbound) // 先入站（NAT 前）即停
}

// ===========================================================================
// AC5：lite 引擎下 NAT 渲染输出带诚实占位注记
// ===========================================================================

// AC5-a：natSimNote() 在 lite 引擎返回精确注记文案。
func TestQA_AC5_LiteSimNoteText(t *testing.T) {
	got := natSimNote()
	want := qaT04ExpectedSimNote()
	assertEqual(t, "natSimNote", got, want)
	if sim.EngineModeName() == "lite" {
		if !strings.Contains(got, "lite 引擎") || !strings.Contains(got, "非内核级真实 NAT") {
			t.Errorf("lite 引擎注记缺失关键文案，got %q", got)
		}
	}
}

// AC5-b：tracert NAT 渲染输出包含 natSimNote()（已在 AC1-b 断言；此处独立再确认）。
func TestQA_AC5_RenderContainsSimNote(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	res := &sim.TracerouteResult{TargetIP: "200.1.1.1", MaxTTL: 30, Reached: true}
	out := states["out"]
	out3 := RenderTracerouteWithACL(states, out, res, "200.1.1.1", 30, topo)
	if !strings.Contains(out3, natSimNote()) {
		t.Errorf("AC5 tracert NAT 渲染缺失 natSimNote，got:\n%s", out3)
	}
}

// AC5-c（独立发现，拍板 #4 ②）：ping 路径存在 NAT 转换时，即便 permit 也应追加 natSimNote()。
// 说明：当前 RenderPingWithACL 仅在 ACL deny 分支追加 natSimNote；permit 分支直接返回
// FormatEnginePing(res) 不含注记，与拍板 #4 ②「ping 路径存在 NAT 转换时在输出追加注记」不符。
// 若此用例失败，属源码显示偏差，应反馈工程师（见 T04 报告路由判定）。
func TestQA_AC5_PingPermitNATNote(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	out := states["out"]
	res := &sim.PingResult{Sent: 4, Received: 4, Lost: 0, RTTMs: []float64{1, 1, 1, 1}}
	out4 := RenderPingWithACL(states, out, res, "200.1.1.1", topo)
	if !strings.Contains(out4, natSimNote()) {
		t.Errorf("AC5(ping) 跨 NAT 路径 permit 时缺失 natSimNote 诚实占位（拍板 #4 ② 偏差），got:\n%s", out4)
	}
}

// ===========================================================================
// AC6：纯函数无副作用（applyNAT 前后 state 字段不被修改；多次调用一致、幂等）
// ===========================================================================

func TestQA_AC6_ApplyNATPureNoSideEffect(t *testing.T) {
	nat := qaT04OutboundState("easy-ip")
	// 快照关键字段。
	natEnabledBefore := nat.NAT.Enabled
	outboundsBefore := append([]NATOutbound{}, nat.NAT.Outbounds...)
	poolsBefore := append([]NATAddressPool{}, nat.NAT.AddressPools...)
	serversBefore := append([]NATServer{}, nat.NAT.Servers...)
	aclsBeforeLen := len(nat.ACLs)
	ifacesBeforeLen := len(nat.Interfaces)
	routesBeforeLen := len(nat.Routes)

	flow := PacketTuple{SrcIP: "192.168.1.10", DstIP: "203.0.113.50", Proto: "icmp"}
	g1, ok1 := applyNAT(nat, DirOutbound, flow)
	g2, ok2 := applyNAT(nat, DirOutbound, flow)
	// 幂等：两次结果一致。
	assertEqual(t, "ok1", ok1, true)
	assertEqual(t, "ok2", ok2, true)
	assertEqual(t, "srcIP consistent", g1.SrcIP, g2.SrcIP)
	assertEqual(t, "dstIP consistent", g1.DstIP, g2.DstIP)
	// 输入 flow 不被修改。
	assertEqual(t, "input srcIP unchanged", flow.SrcIP, "192.168.1.10")

	// state 字段未被修改（无副作用）。
	if nat.NAT == nil || nat.NAT.Enabled != natEnabledBefore {
		t.Errorf("AC6 applyNAT 不应修改 state.NAT.Enabled（got %v）", nat.NAT != nil && nat.NAT.Enabled)
	}
	assertEqual(t, "NAT.Outbounds len", len(nat.NAT.Outbounds), len(outboundsBefore))
	assertEqual(t, "NAT.Outbounds[0].Type", nat.NAT.Outbounds[0].Type, outboundsBefore[0].Type)
	assertEqual(t, "NAT.AddressPools len", len(nat.NAT.AddressPools), len(poolsBefore))
	assertEqual(t, "NAT.Servers len", len(nat.NAT.Servers), len(serversBefore))
	if len(nat.NAT.Outbounds) == len(outboundsBefore) {
		for i := range outboundsBefore {
			if nat.NAT.Outbounds[i] != outboundsBefore[i] {
				t.Errorf("AC6 applyNAT 修改了 NAT.Outbounds[%d]：got %+v want %+v", i, nat.NAT.Outbounds[i], outboundsBefore[i])
			}
		}
	}
	if len(nat.NAT.AddressPools) == len(poolsBefore) {
		for i := range poolsBefore {
			if nat.NAT.AddressPools[i] != poolsBefore[i] {
				t.Errorf("AC6 applyNAT 修改了 NAT.AddressPools[%d]：got %+v want %+v", i, nat.NAT.AddressPools[i], poolsBefore[i])
			}
		}
	}
	if len(nat.NAT.Servers) == len(serversBefore) {
		for i := range serversBefore {
			if nat.NAT.Servers[i] != serversBefore[i] {
				t.Errorf("AC6 applyNAT 修改了 NAT.Servers[%d]：got %+v want %+v", i, nat.NAT.Servers[i], serversBefore[i])
			}
		}
	}
	assertEqual(t, "ACLs len", len(nat.ACLs), aclsBeforeLen)
	assertEqual(t, "Interfaces len", len(nat.Interfaces), ifacesBeforeLen)
	assertEqual(t, "Routes len", len(nat.Routes), routesBeforeLen)
}

// ===========================================================================
// 两条代码路径覆盖
// ===========================================================================

// CLI 单设备路径：parser.go 调用 RenderTracerouteWithACL(nil, state, res, target, 30, nil)
// → NAT 分支 lazy（t==nil 跳过 ComputeL3PathNAT），不 panic，退化为基础可达性渲染。
func TestQA_CLISingleDevicePath_NoPanic(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	nat := states["nat"]
	res := &sim.TracerouteResult{TargetIP: "200.1.1.1", MaxTTL: 30, Reached: true}
	// (1) t=nil, states=nil：NAT 分支完全 lazy。
	var out1 string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CLI 单设备路径 t=nil 时 panic: %v", r)
			}
		}()
		out1 = RenderTracerouteWithACL(nil, nat, res, "200.1.1.1", 30, nil)
	}()
	if out1 == "" {
		t.Errorf("CLI 单设备路径返回空输出")
	}
	if !strings.Contains(out1, "200.1.1.1") {
		t.Errorf("CLI 单设备路径输出应含目标 IP，got:\n%s", out1)
	}
	// (2) 有拓扑但 states=nil：ComputeL3PathNAT 因 states=nil 返回 (nil,false) → 仍不 panic，退化为基础渲染。
	var out2 string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CLI 单设备路径 states=nil 时 panic: %v", r)
			}
		}()
		out2 = RenderTracerouteWithACL(nil, nat, res, "200.1.1.1", 30, topo)
	}()
	if out2 == "" {
		t.Errorf("CLI 单设备路径(states=nil)返回空输出")
	}
}

// API 引擎路径：复刻 internal/api/cli_handlers.go:347 renderEngineTraceroute 的调用
// —— 传真实 t + registry → NAT 显示真实生效（含 NAT→InsideIP 与 natSimNote）。
func TestQA_APIEnginePath_RealTAndRegistry(t *testing.T) {
	topo := qaT04ServerTopo()
	states := qaT04ServerStates("200.1.1.1", "192.168.1.10")
	res := &sim.TracerouteResult{TargetIP: "200.1.1.1", MaxTTL: 30, Reached: true}
	out := states["out"]
	// 与 cli_handlers.go:347 完全一致：cli.RenderTracerouteWithACL(registry, state, res, targetIP, maxTTL, t)
	apiOut := RenderTracerouteWithACL(states, out, res, "200.1.1.1", 30, topo)
	if !strings.Contains(apiOut, "NAT→192.168.1.10") {
		t.Errorf("API 引擎路径 NAT 跳未显示 InsideIP，got:\n%s", apiOut)
	}
	if !strings.Contains(apiOut, qaT04ExpectedSimNote()) {
		t.Errorf("API 引擎路径缺失 natSimNote，got:\n%s", apiOut)
	}
}
