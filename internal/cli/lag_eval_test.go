package cli

// lag_eval_test.go —— P2 #5 链路聚合「纯函数评估器」单元测试（T05）。
//
// 仅驱动 lag_eval.go 的纯函数（EvaluateLAG / SelectLACPActivePorts / CompareLACPPort /
// comparePortIndex / collectLAGTrunks / collectLAGMembers / 校验函数 / 常量），
// 不依赖命令分发，可独立回归。
//
// 测试覆盖（任务 T05 指定）：
//   - EvaluateLAG 手工模式 / LACP 静态模式派生结果；
//   - SelectLACPActivePorts 按 maxActive 截断 + 四级"小者优先"选举；
//   - CompareLACPPort 返回值语义（>0=a 胜，锚定 CompareBridgeID）；
//   - comparePortIndex 保证 GE0/0/2 < GE0/0/10（数字序非字符串序）；
//   - collectLAGTrunks / collectLAGMembers 从 interface:<member>:eth-trunk 聚合归属读取；
//   - DefaultLoadBalance == "src-dst-ip"；
//   - validTrunkID / validLoadBalance / validLinkNumber / validLACPPriority 边界。

import (
	"fmt"
	"testing"

	"ensp-lab/internal/topology"
)

// newLAGTestState 构造一个绑定到交换机的空配置 CLIState，供纯函数测试使用。
// 复用 NewCLIStateWithType 以继承默认 Interfaces（含 GE0/0/1 Up、GE0/0/2 Up、GE0/0/3 Down），
// 使 isPortDown 对已知接口名有确定判定。
func newLAGTestState() *CLIState {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	s.DeviceName = "SW"
	s.DeviceConfig = map[string]string{}
	return s
}

// namesOf 提取成员名列表，便于断言。
func namesOf(ms []LAGMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

// TestEvaluateLAGManualMode 验证手工负载分担模式的派生结果（缺省值合并、成员自然序、
// 物理 down 成员不计入活动口、运行状态实时派生）。
func TestEvaluateLAGManualMode(t *testing.T) {
	s := newLAGTestState()
	s.DeviceConfig["interface:Eth-Trunk1:lag:exists"] = "true"
	s.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"] = "1"
	// GE0/0/3 在默认 Interfaces 中状态为 Down，用于验证物理 down 不计入活动口。
	s.DeviceConfig["interface:GigabitEthernet0/0/3:eth-trunk"] = "1"

	res := EvaluateLAG(s, 1)

	if !res.Exists {
		t.Fatal("Eth-Trunk1 should exist (lag:exists=true)")
	}
	if res.Mode != LAGModeManual {
		t.Errorf("mode want %q got %q", LAGModeManual, res.Mode)
	}
	if res.LoadBalance != DefaultLoadBalance {
		t.Errorf("load-balance want default %q got %q", DefaultLoadBalance, res.LoadBalance)
	}
	if res.HashArithmetic != "SA 源 IP 与目的 IP" {
		t.Errorf("hash arithmetic want 'SA 源 IP 与目的 IP' got %q", res.HashArithmetic)
	}
	if len(res.Members) != 2 {
		t.Fatalf("members want 2 got %d (%v)", len(res.Members), namesOf(res.Members))
	}
	// 成员按接口号自然序：GE0/0/1 在 GE0/0/3 之前。
	if res.Members[0].Name != "GigabitEthernet0/0/1" {
		t.Errorf("member order want GE0/0/1 first, got %q", res.Members[0].Name)
	}
	// 手工模式无选举列，Role 应为空串。
	if res.Members[0].Role != "" {
		t.Errorf("manual mode member Role should be empty, got %q", res.Members[0].Role)
	}
	// GE0/0/3 物理 Down → 仅 1 个活动口。
	if len(res.ActiveMembers) != 1 {
		t.Errorf("active members want 1 (GE0/0/3 down) got %d (%v)", len(res.ActiveMembers), namesOf(res.ActiveMembers))
	}
	if res.ActiveMembers[0].Name != "GigabitEthernet0/0/1" {
		t.Errorf("active want GE0/0/1 got %q", res.ActiveMembers[0].Name)
	}
	if res.UpPortCount != 1 {
		t.Errorf("UpPortCount want 1 got %d", res.UpPortCount)
	}
	// 存在且有 1 个活动口 ≥ least(1) → OperateStatus up。
	if res.OperateStatus != "up" {
		t.Errorf("OperateStatus want up got %q", res.OperateStatus)
	}
}

// TestEvaluateLAGLACPStaticMode 验证 LACP 静态模式派生：WorkinMode=LACP，活动口 =
// 物理 up 成员经本地视图选举（同优先级按端口号自然序），物理 down 成员为 Unselect。
func TestEvaluateLAGLACPStaticMode(t *testing.T) {
	s := newLAGTestState()
	s.DeviceConfig["interface:Eth-Trunk1:lag:exists"] = "true"
	s.DeviceConfig["interface:Eth-Trunk1:lag:mode"] = "lacp-static"
	s.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"] = "1"
	s.DeviceConfig["interface:GigabitEthernet0/0/2:eth-trunk"] = "1"
	s.DeviceConfig["interface:GigabitEthernet0/0/3:eth-trunk"] = "1" // Down

	res := EvaluateLAG(s, 1)

	if res.Mode != LAGModeLACP {
		t.Fatalf("mode want %q got %q", LAGModeLACP, res.Mode)
	}
	if lagWorkingModeName(res.Mode) != "LACP" {
		t.Errorf("WorkingMode want LACP got %q", lagWorkingModeName(res.Mode))
	}
	if len(res.ActiveMembers) != 2 {
		t.Fatalf("active want 2 (up members) got %d (%v)", len(res.ActiveMembers), namesOf(res.ActiveMembers))
	}
	// 同端口优先级 → 端口号小者优先：GE0/0/1 在 GE0/0/2 之前。
	if res.ActiveMembers[0].Name != "GigabitEthernet0/0/1" {
		t.Errorf("active[0] want GE0/0/1 got %q", res.ActiveMembers[0].Name)
	}
	// 角色回填：up 成员 Selected，down 成员 Unselect。
	if res.Members[0].Role != lagRoleSelected {
		t.Errorf("GE0/0/1 Role want Selected got %q", res.Members[0].Role)
	}
	if res.Members[2].Name != "GigabitEthernet0/0/3" {
		t.Fatalf("third member want GE0/0/3 got %q", res.Members[2].Name)
	}
	if res.Members[2].Role != lagRoleUnselect {
		t.Errorf("GE0/0/3 (down) Role want Unselect got %q", res.Members[2].Role)
	}
	if res.UpPortCount != 2 || res.OperateStatus != "up" {
		t.Errorf("UpPortCount/OperateStatus want 2/up got %d/%q", res.UpPortCount, res.OperateStatus)
	}
}

// TestEvaluateLAGEmptyTrunkDown 验证存在但无成员的聚合口运行状态为 down。
func TestEvaluateLAGEmptyTrunkDown(t *testing.T) {
	s := newLAGTestState()
	s.DeviceConfig["interface:Eth-Trunk5:lag:exists"] = "true"

	res := EvaluateLAG(s, 5)
	if !res.Exists {
		t.Fatal("Eth-Trunk5 should exist")
	}
	if len(res.Members) != 0 || len(res.ActiveMembers) != 0 {
		t.Errorf("no members expected, got members=%d active=%d", len(res.Members), len(res.ActiveMembers))
	}
	if res.UpPortCount != 0 {
		t.Errorf("UpPortCount want 0 got %d", res.UpPortCount)
	}
	if res.OperateStatus != "down" {
		t.Errorf("empty trunk OperateStatus want down got %q", res.OperateStatus)
	}
}

// TestEvaluateLAGNilState 验证 nil 状态安全返回缺省结果（不 panic）。
func TestEvaluateLAGNilState(t *testing.T) {
	res := EvaluateLAG(nil, 1)
	if res.Exists {
		t.Error("nil state: Exists should be false")
	}
	if res.Mode != LAGModeManual {
		t.Errorf("nil state: Mode want manual got %q", res.Mode)
	}
	if res.LoadBalance != DefaultLoadBalance {
		t.Errorf("nil state: LoadBalance want %q got %q", DefaultLoadBalance, res.LoadBalance)
	}
	if res.OperateStatus != "down" {
		t.Errorf("nil state: OperateStatus want down got %q", res.OperateStatus)
	}
}

// TestSelectLACPActivePortsTruncation 验证 maxActive 截断 + 端口 LACP 优先级（因子③）选举。
// 成员端口优先级：c=200, a=100, b=50, d=50；e 物理 down 应被排除。
// 期望（升序选举取前 2）：b(GE0/0/2,pri50,idx2) → d(GE0/0/4,pri50,idx4) → a → c。
func TestSelectLACPActivePortsTruncation(t *testing.T) {
	members := []LAGMember{
		{Name: "GigabitEthernet0/0/1", PortLACPPri: 100, PortIndex: parsePortIndex("GigabitEthernet0/0/1"), PhyDown: false},
		{Name: "GigabitEthernet0/0/2", PortLACPPri: 50, PortIndex: parsePortIndex("GigabitEthernet0/0/2"), PhyDown: false},
		{Name: "GigabitEthernet0/0/3", PortLACPPri: 200, PortIndex: parsePortIndex("GigabitEthernet0/0/3"), PhyDown: false},
		{Name: "GigabitEthernet0/0/4", PortLACPPri: 50, PortIndex: parsePortIndex("GigabitEthernet0/0/4"), PhyDown: false},
		{Name: "GigabitEthernet0/0/5", PortLACPPri: 10, PortIndex: parsePortIndex("GigabitEthernet0/0/5"), PhyDown: true},
	}
	sel := SelectLACPActivePorts(members, 2)
	if len(sel) != 2 {
		t.Fatalf("maxActive=2 want 2 selected, got %d (%v)", len(sel), namesOf(sel))
	}
	want := []string{"GigabitEthernet0/0/2", "GigabitEthernet0/0/4"}
	for i, w := range want {
		if sel[i].Name != w {
			t.Errorf("sel[%d] want %q got %q (all=%v)", i, w, sel[i].Name, namesOf(sel))
		}
		if !sel[i].Selected || sel[i].Role != lagRoleSelected {
			t.Errorf("%q should be Selected/Selected role, got Selected=%v Role=%q", sel[i].Name, sel[i].Selected, sel[i].Role)
		}
	}
}

// TestSelectLACPActivePortsSysPri 验证系统 LACP 优先级（因子①）优先于端口优先级。
func TestSelectLACPActivePortsSysPri(t *testing.T) {
	members := []LAGMember{
		{Name: "GigabitEthernet0/0/1", SysLACPPri: 32768, PortLACPPri: 50, PortIndex: parsePortIndex("GigabitEthernet0/0/1"), PhyDown: false},
		{Name: "GigabitEthernet0/0/2", SysLACPPri: 100, PortLACPPri: 9999, PortIndex: parsePortIndex("GigabitEthernet0/0/2"), PhyDown: false},
	}
	sel := SelectLACPActivePorts(members, 1)
	if len(sel) != 1 || sel[0].Name != "GigabitEthernet0/0/2" {
		t.Errorf("factor① sys-pri smallest (GE0/0/2=100) should win, got %v", namesOf(sel))
	}
}

// TestSelectLACPActivePortsDefaultMax 验证 maxActive<=0 时按缺省 8 处理。
func TestSelectLACPActivePortsDefaultMax(t *testing.T) {
	var members []LAGMember
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("GigabitEthernet0/0/%d", i)
		members = append(members, LAGMember{Name: name, PortIndex: parsePortIndex(name), PhyDown: false})
	}
	sel := SelectLACPActivePorts(members, 0)
	if len(sel) != 3 {
		t.Errorf("maxActive<=0 should default to 8, got %d selected", len(sel))
	}
	// 不修改入参：传入切片第一个元素 Selected 应保持 false。
	if members[0].Selected {
		t.Error("SelectLACPActivePorts must not mutate input slice")
	}
}

// TestCompareLACPPortSemantics 验证返回值语义（>0=a 胜，锚定 CompareBridgeID）。
func TestCompareLACPPortSemantics(t *testing.T) {
	base := LAGMember{
		SysLACPPri:  32768,
		PortLACPPri: 32768,
		SysMAC:      "00e0-fc12-3456",
		PortIndex:   parsePortIndex("GigabitEthernet0/0/2"),
		Name:        "GigabitEthernet0/0/2",
	}
	// 因子③：端口优先级小者胜。
	a := base
	a.PortLACPPri = 100
	b := base
	b.PortLACPPri = 200
	if cmp := CompareLACPPort(a, b); cmp <= 0 {
		t.Errorf("a(pri100) should beat b(pri200): got %d", cmp)
	}
	if cmp := CompareLACPPort(b, a); cmp >= 0 {
		t.Errorf("b(pri200) should lose to a(pri100): got %d", cmp)
	}
	// 完全相等 → 0。
	c := base
	d := base
	if cmp := CompareLACPPort(c, d); cmp != 0 {
		t.Errorf("equal members should compare 0, got %d", cmp)
	}
	// 因子⑤：接口名兜底（GE0/0/1 在自然序上小于 GE0/0/2）。
	e := base
	e.Name = "GigabitEthernet0/0/1"
	f := base
	f.Name = "GigabitEthernet0/0/2"
	if cmp := CompareLACPPort(e, f); cmp <= 0 {
		t.Errorf("GE0/0/1 should beat GE0/0/2 by name tie-break: got %d", cmp)
	}
}

// TestComparePortIndexNaturalOrder 保证数字序而非字符串序：GE0/0/2 < GE0/0/10。
func TestComparePortIndexNaturalOrder(t *testing.T) {
	// 数字序：2 < 10。
	if c := comparePortIndex(parsePortIndex("GE0/0/2"), parsePortIndex("GE0/0/10")); c <= 0 {
		t.Errorf("GE0/0/2 should be < GE0/0/10 numerically, got %d", c)
	}
	// 对照：朴素的字符串比较会误判 "10" < "2"，本实现不能如此。
	if "10" < "2" {
		// 说明字符串序的陷阱；下面断言确保我们走的是数值序。
		t.Log("note: naive string order puts '10' before '2'; implementation must avoid this")
	}
	// 相等。
	if c := comparePortIndex(parsePortIndex("GigabitEthernet0/0/5"), parsePortIndex("GigabitEthernet0/0/5")); c != 0 {
		t.Errorf("equal indices should compare 0, got %d", c)
	}
	// 短者优先（长度不同）。
	if c := comparePortIndex(parsePortIndex("GE0/0"), parsePortIndex("GE0/0/2")); c <= 0 {
		t.Errorf("shorter index should win, got %d", c)
	}
}

// TestCollectLAGTrunksAndMembers 验证从 interface:<member>:eth-trunk 聚合归属读取组与成员。
func TestCollectLAGTrunksAndMembers(t *testing.T) {
	s := newLAGTestState()
	s.DeviceConfig["interface:Eth-Trunk1:lag:exists"] = "true"
	s.DeviceConfig["interface:Eth-Trunk2:lag:exists"] = "true"
	s.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"] = "1"
	s.DeviceConfig["interface:GigabitEthernet0/0/2:eth-trunk"] = "1"
	s.DeviceConfig["interface:GigabitEthernet0/0/3:eth-trunk"] = "2"

	trunks := collectLAGTrunks(s)
	if len(trunks) != 2 || trunks[0] != 1 || trunks[1] != 2 {
		t.Fatalf("collectLAGTrunks want [1 2] got %v", trunks)
	}
	m1 := collectLAGMembers(s, 1)
	if len(m1) != 2 || m1[0].Name != "GigabitEthernet0/0/1" {
		t.Errorf("trunk1 members want [GE0/0/1 GE0/0/2] got %v", namesOf(m1))
	}
	m2 := collectLAGMembers(s, 2)
	if len(m2) != 1 || m2[0].Name != "GigabitEthernet0/0/3" {
		t.Errorf("trunk2 members want [GE0/0/3] got %v", namesOf(m2))
	}
	// 成员归属键缺失时应返回空。
	if got := collectLAGMembers(s, 9); got != nil {
		t.Errorf("trunk9 (no members) should return nil, got %v", namesOf(got))
	}
}

// TestDefaultLoadBalance 验证缺省负载分担算法为 src-dst-ip（修正残桩误用的 src-dst-mac）。
func TestDefaultLoadBalance(t *testing.T) {
	if DefaultLoadBalance != "src-dst-ip" {
		t.Errorf("DefaultLoadBalance want 'src-dst-ip' got %q", DefaultLoadBalance)
	}
}

// TestValidTrunkID 验证 Eth-Trunk ID ∈ [0,63] 边界。
func TestValidTrunkID(t *testing.T) {
	if ok, _ := validTrunkID(0); !ok {
		t.Error("0 should be valid")
	}
	if ok, _ := validTrunkID(63); !ok {
		t.Error("63 should be valid")
	}
	if ok, msg := validTrunkID(-1); ok || msg != errLAGInvalidTrunkID {
		t.Errorf("-1 should be invalid with %q, got ok=%v msg=%q", errLAGInvalidTrunkID, ok, msg)
	}
	if ok, msg := validTrunkID(64); ok || msg != errLAGInvalidTrunkID {
		t.Errorf("64 should be invalid with %q, got ok=%v msg=%q", errLAGInvalidTrunkID, ok, msg)
	}
}

// TestValidLoadBalance 验证六值枚举。
func TestValidLoadBalance(t *testing.T) {
	for _, v := range []string{"dst-ip", "dst-mac", "src-ip", "src-mac", "src-dst-ip", "src-dst-mac"} {
		if ok, _ := validLoadBalance(v); !ok {
			t.Errorf("%q should be valid", v)
		}
	}
	if ok, msg := validLoadBalance("foo"); ok || msg != errLAGUnrecognized {
		t.Errorf("'foo' should be invalid with %q, got ok=%v msg=%q", errLAGUnrecognized, ok, msg)
	}
}

// TestValidLinkNumber 验证 least/max active-linknumber ∈ [1,8]。
func TestValidLinkNumber(t *testing.T) {
	if ok, _ := validLinkNumber(1); !ok {
		t.Error("1 should be valid")
	}
	if ok, _ := validLinkNumber(8); !ok {
		t.Error("8 should be valid")
	}
	if ok, _ := validLinkNumber(0); ok {
		t.Error("0 should be invalid")
	}
	if ok, _ := validLinkNumber(9); ok {
		t.Error("9 should be invalid")
	}
}

// TestValidLACPPriority 验证 LACP 优先级 ∈ [0,65535]。
func TestValidLACPPriority(t *testing.T) {
	if ok, _ := validLACPPriority(0); !ok {
		t.Error("0 should be valid")
	}
	if ok, _ := validLACPPriority(65535); !ok {
		t.Error("65535 should be valid")
	}
	if ok, _ := validLACPPriority(-1); ok {
		t.Error("-1 should be invalid")
	}
	if ok, _ := validLACPPriority(65536); ok {
		t.Error("65536 should be invalid")
	}
}

// TestHashArithmeticMapping 验证 load-balance → Hash arithmetic 展示串映射。
func TestHashArithmeticMapping(t *testing.T) {
	cases := map[string]string{
		"src-mac":    "SMAC 源 MAC",
		"dst-mac":    "DMAC 目的 MAC",
		"src-dst-mac": "SMAC 源 MAC 与目的 MAC",
		"src-ip":     "SA 源 IP",
		"dst-ip":     "DA 目的 IP",
		"src-dst-ip": "SA 源 IP 与目的 IP",
	}
	for in, want := range cases {
		if got := hashArithmetic(in); got != want {
			t.Errorf("hashArithmetic(%q) want %q got %q", in, want, got)
		}
	}
	if got := hashArithmetic("nope"); got != lagPlaceholder {
		t.Errorf("unknown load-balance want placeholder %q got %q", lagPlaceholder, got)
	}
}

// TestWorkingModeName 验证 WorkingMode 展示值。
func TestWorkingModeName(t *testing.T) {
	if lagWorkingModeName(LAGModeManual) != "Normal" {
		t.Errorf("manual want Normal got %q", lagWorkingModeName(LAGModeManual))
	}
	if lagWorkingModeName(LAGModeLACP) != "LACP" {
		t.Errorf("lacp want LACP got %q", lagWorkingModeName(LAGModeLACP))
	}
}
