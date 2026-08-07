package cli

// p2_lag_qa_test.go —— P2 #5 链路聚合端到端回归验收（T06，覆盖验收 AC4/AC5/AC6/AC8/AC9/AC10）。
//
// 本文件为独立新增测试，不修改 T05 的 lag_eval_test.go / p2_lag_test.go（AC1/AC2/AC3/AC7/AC11/AC12）。
//
// 测试通过 ExecuteCommandOn + ParseCommand（复用 p1f_test.go 的 runOn）真实驱动命令分发，
// 并对 EvaluateLAG 纯函数做确定性断言。覆盖：
//   AC4  LACP 静态模式完整态：Local 块 actor/partner 字段、Selected/Unselect 判定、
//        端口优先级（因子③）与系统优先级（因子①）在选举中的体现；
//   AC5  聚合口 up/down 阈值：手工模式「全部物理 up 成员即活动」；LACP 模式按 least
//        active-linknumber 判定，活动口 < 下限即 down；display 成员顺序确定性；
//   AC6  load-balance 六值枚举 + display Hash arithmetic 映射；缺省 src-dst-ip；
//   AC8  undo 路径：undo eth-trunk / undo trunkport / undo interface Eth-Trunk <id>（有成员拒绝、
//        无成员删除）状态回滚、成员归属键清除；
//   AC9  lacp 扩展：系统视图 lacp priority|preempt|timeout，成员口 lacp priority，聚合口
//        lacp preempt|preempt delay|timeout，配置落键正确、非法值报错；
//   AC10 H3C 变体：Bridge-Aggregation<N> 经 port link-aggregation group / link-aggregation mode
//        写 agg-family="h3c"，display link-aggregation summary 按 agg-family 归类，
//        未配置的华为侧不编造 Eth-Trunk<N>。

import (
	"fmt"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// qaNewSwitch 构造绑定交换机的空会话（L2/L3 交换机支持链路聚合命令族）。
func qaNewSwitch() *CLIState {
	return NewCLIStateWithType(topology.DeviceSwitch)
}

// qaSetupTrunk 在系统视图下创建聚合口并按需设置 mode（空 mode 仅建组，不写 :lag:mode）。
func qaSetupTrunk(t *testing.T, s *CLIState, trunkID int, mode string) {
	t.Helper()
	runOn(s, topology.DeviceSwitch, "system-view")
	if out := runOn(s, topology.DeviceSwitch, fmt.Sprintf("interface Eth-Trunk %d", trunkID)); strings.Contains(out, "Error") {
		t.Fatalf("create Eth-Trunk %d failed: %q", trunkID, out)
	}
	if mode != "" {
		if out := runOn(s, topology.DeviceSwitch, "mode "+mode); strings.Contains(out, "Error") {
			t.Fatalf("set mode %q on Eth-Trunk %d failed: %q", mode, trunkID, out)
		}
	}
}

// qaAddMembers 在聚合口视图批量纳管成员口。
func qaAddMembers(t *testing.T, s *CLIState, members ...string) {
	t.Helper()
	for _, m := range members {
		if out := runOn(s, topology.DeviceSwitch, "trunkport "+m); strings.Contains(out, "Error") {
			t.Fatalf("add member %q failed: %q", m, out)
		}
	}
}

// TestLAGQAAC4LACPCompleteState 验证 LACP 静态模式完整态：Local 块字段、Selected/Unselect 判定、
// 端口优先级（因子③）与系统优先级（因子①）选举体现。
func TestLAGQAAC4LACPCompleteState(t *testing.T) {
	s := qaNewSwitch()
	runOn(s, topology.DeviceSwitch, "system-view")

	// 系统级 LACP 优先级（因子①，本地视图占位级，列必须反映配置值）。
	if out := runOn(s, topology.DeviceSwitch, "lacp priority 100"); strings.Contains(out, "Error") {
		t.Fatalf("lacp priority 100 failed: %q", out)
	}

	// 成员端口优先级（因子③）：GE0/0/2 优于 GE0/0/1。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "lacp priority 100")
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/2")
	runOn(s, topology.DeviceSwitch, "lacp priority 50")
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/3")
	runOn(s, topology.DeviceSwitch, "lacp priority 200")
	runOn(s, topology.DeviceSwitch, "quit")

	qaSetupTrunk(t, s, 1, "lacp-static")
	qaAddMembers(t, s, "GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3")

	res := EvaluateLAG(s, 1)
	if res.Mode != LAGModeLACP {
		t.Fatalf("mode want lacp-static, got %q", res.Mode)
	}
	// 活动口 = 物理 up 的 2 个成员（GE0/0/3 默认 Down）。
	if len(res.ActiveMembers) != 2 {
		t.Fatalf("active members want 2, got %d (%v)", len(res.ActiveMembers), namesOf(res.ActiveMembers))
	}
	// 端口优先级（因子③）小者优先 → GE0/0/2(pri50) 排在 GE0/0/1(pri100) 之前。
	if res.ActiveMembers[0].Name != "GigabitEthernet0/0/2" {
		t.Errorf("active[0] want GE0/0/2 (pri50), got %q (%v)", res.ActiveMembers[0].Name, namesOf(res.ActiveMembers))
	}
	// Selected/Unselect 角色回填：up 成员 Selected，down 成员 Unselect。
	roleByName := map[string]string{}
	for _, m := range res.Members {
		roleByName[m.Name] = m.Role
		if m.PhyDown && m.Role != lagRoleUnselect {
			t.Errorf("%q (down) Role want Unselect, got %q", m.Name, m.Role)
		}
		if !m.PhyDown && m.Role != lagRoleSelected {
			t.Errorf("%q (up) Role want Selected, got %q", m.Name, m.Role)
		}
	}
	// 系统优先级（因子①）列反映配置值 100（本地视图占位，但必须真实落地）。
	if res.SysPriority != 100 {
		t.Errorf("SysPriority want 100 (configured), got %d", res.SysPriority)
	}

	// display 渲染：Local 块 + actor 字段 + 系统优先级 + Selected/Unselect + Partner 诚实占位。
	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1")
	for _, want := range []string{
		"WorkingMode: LACP",
		"Local:",
		"System Priority: 100",
		"System ID:",
		"ActorPortName",
		"PortPri",
		"PortNo",
		lagRoleSelected,
		lagRoleUnselect,
		lagPartnerPlaceholder,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("display eth-trunk 1 missing %q, got: %q", want, out)
		}
	}
	// Partner 块不得编造对端 MAC / 伪造行。
	if strings.Contains(out, "0000-0000-0000") {
		t.Errorf("must not fabricate Partner MAC, got: %q", out)
	}
}

// TestLAGQAAC5ManualThreshold 验证手工模式 up/down 阈值：全部物理 up 成员即活动；
// 活动口数 < least active-linknumber → down；display 成员顺序确定性。
func TestLAGQAAC5ManualThreshold(t *testing.T) {
	s := qaNewSwitch()
	qaSetupTrunk(t, s, 1, "manual load-balance")
	qaAddMembers(t, s, "GigabitEthernet0/0/1", "GigabitEthernet0/0/2") // 默认均 Up
	runOn(s, topology.DeviceSwitch, "least active-linknumber 2")

	// 两个 up 成员 ≥ least(2) → up。
	res := EvaluateLAG(s, 1)
	if len(res.Members) != 2 {
		t.Fatalf("manual members want 2, got %d", len(res.Members))
	}
	if res.UpPortCount != 2 {
		t.Errorf("UpPortCount want 2, got %d", res.UpPortCount)
	}
	if res.OperateStatus != "up" {
		t.Fatalf("two up members ≥ least(2) → up, got %q", res.OperateStatus)
	}
	if out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1"); !strings.Contains(out, "Operate status: up") {
		t.Errorf("display should show Operate status: up, got: %q", out)
	}

	// 移出一个成员 → 仅 1 个 up 成员 < least(2) → down。
	if out := runOn(s, topology.DeviceSwitch, "undo trunkport GigabitEthernet0/0/2"); strings.Contains(out, "Error") {
		t.Fatalf("undo trunkport failed: %q", out)
	}
	res2 := EvaluateLAG(s, 1)
	if res2.UpPortCount != 1 {
		t.Errorf("after removing a member UpPortCount want 1, got %d", res2.UpPortCount)
	}
	if res2.OperateStatus != "down" {
		t.Fatalf("1 up member < least(2) → down, got %q", res2.OperateStatus)
	}
	if out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1"); !strings.Contains(out, "Operate status: down") {
		t.Errorf("display should show Operate status: down, got: %q", out)
	}

	// 确定性：连续两次评估成员顺序一致（AC5 字节级可复现前提）。
	resA := EvaluateLAG(s, 1)
	resB := EvaluateLAG(s, 1)
	if strings.Join(namesOf(resA.Members), ",") != strings.Join(namesOf(resB.Members), ",") {
		t.Errorf("member order not deterministic: %v vs %v", namesOf(resA.Members), namesOf(resB.Members))
	}
}

// TestLAGQAAC5LACPLessThanLeastDown 验证 LACP 模式按 least active-linknumber 判定：
// 活动口数 < 下限 → down；调低下限后恢复 up。
func TestLAGQAAC5LACPLessThanLeastDown(t *testing.T) {
	s := qaNewSwitch()
	qaSetupTrunk(t, s, 1, "lacp-static")
	qaAddMembers(t, s, "GigabitEthernet0/0/1", "GigabitEthernet0/0/2") // 默认均 Up
	runOn(s, topology.DeviceSwitch, "least active-linknumber 3")        // 2 个 up 成员 < 3

	res := EvaluateLAG(s, 1)
	if res.UpPortCount != 2 {
		t.Errorf("up members want 2, got %d", res.UpPortCount)
	}
	if res.OperateStatus != "down" {
		t.Fatalf("2 active < least(3) → down, got %q", res.OperateStatus)
	}
	if out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1"); !strings.Contains(out, "Operate status: down") {
		t.Errorf("LACP display should show Operate status: down, got: %q", out)
	}

	// 调低下限至 1 → 2 个 up 成员 ≥ 1 → up。
	if out := runOn(s, topology.DeviceSwitch, "least active-linknumber 1"); strings.Contains(out, "Error") {
		t.Fatalf("set least 1 failed: %q", out)
	}
	res2 := EvaluateLAG(s, 1)
	if res2.OperateStatus != "up" {
		t.Fatalf("2 active ≥ least(1) → up, got %q", res2.OperateStatus)
	}
}

// TestLAGQAAC6LoadBalanceEnumAndHash 验证 load-balance 六值枚举、display Hash arithmetic
// 映射、缺省 src-dst-ip、非法值报错。
func TestLAGQAAC6LoadBalanceEnumAndHash(t *testing.T) {
	cases := map[string]string{
		"dst-ip":     "DA 目的 IP",
		"dst-mac":    "DMAC 目的 MAC",
		"src-ip":     "SA 源 IP",
		"src-mac":    "SMAC 源 MAC",
		"src-dst-ip": "SA 源 IP 与目的 IP",
		"src-dst-mac": "SMAC 源 MAC 与目的 MAC",
	}

	for v, wantHash := range cases {
		s := qaNewSwitch()
		qaSetupTrunk(t, s, 1, "") // 仅建组，不写 mode（load-balance 与 mode 无关）
		if out := runOn(s, topology.DeviceSwitch, "load-balance "+v); strings.Contains(out, "Error") {
			t.Fatalf("load-balance %q failed: %q", v, out)
		}
		// 配置落键。
		if s.DeviceConfig[lagTrunkKey(1, "load-balance")] != v {
			t.Errorf("config key want %q, got %q", v, s.DeviceConfig[lagTrunkKey(1, "load-balance")])
		}
		// 评估器合并缺省值后反映取值与 Hash 映射。
		res := EvaluateLAG(s, 1)
		if res.LoadBalance != v {
			t.Errorf("LoadBalance want %q, got %q", v, res.LoadBalance)
		}
		if res.HashArithmetic != wantHash {
			t.Errorf("HashArithmetic(%q) want %q, got %q", v, wantHash, res.HashArithmetic)
		}
		// display eth-trunk <id> load-balance 展示映射串。
		out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1 load-balance")
		if !strings.Contains(out, "Load-Balance Profile: "+v) {
			t.Errorf("display should show Profile %q, got: %q", v, out)
		}
		if !strings.Contains(out, "Hash arithmetic: "+wantHash) {
			t.Errorf("display Hash arithmetic want %q, got: %q", wantHash, out)
		}
	}

	// 缺省值：新建组未配置 load-balance → src-dst-ip + 对应 Hash 串。
	s := qaNewSwitch()
	qaSetupTrunk(t, s, 1, "")
	res := EvaluateLAG(s, 1)
	if res.LoadBalance != DefaultLoadBalance {
		t.Errorf("default LoadBalance want %q, got %q", DefaultLoadBalance, res.LoadBalance)
	}
	if res.HashArithmetic != "SA 源 IP 与目的 IP" {
		t.Errorf("default HashArithmetic want 'SA 源 IP 与目的 IP', got %q", res.HashArithmetic)
	}

	// 非法取值报错。
	s2 := qaNewSwitch()
	qaSetupTrunk(t, s2, 1, "")
	if out := runOn(s2, topology.DeviceSwitch, "load-balance bogus"); !strings.Contains(out, "Error") {
		t.Errorf("invalid load-balance should error, got: %q", out)
	}
}

// TestLAGQAAC8UndoPaths 验证 undo 全路径：undo eth-trunk / undo trunkport /
// undo interface Eth-Trunk <id>（有成员拒绝、无成员删除），状态回滚与归属键清除。
func TestLAGQAAC8UndoPaths(t *testing.T) {
	s := qaNewSwitch()
	qaSetupTrunk(t, s, 1, "manual load-balance")
	qaAddMembers(t, s, "GigabitEthernet0/0/1", "GigabitEthernet0/0/2")

	// ① undo eth-trunk（成员口视图）：移出 GE0/0/1。需先退出聚合口视图回到系统视图。
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "undo eth-trunk"); strings.Contains(out, "Error") {
		t.Fatalf("undo eth-trunk failed: %q", out)
	}
	if _, ok := lagMemberTrunkID(s, "GigabitEthernet0/0/1"); ok {
		t.Errorf("GE0/0/1 should be removed from trunk after undo eth-trunk")
	}
	res := EvaluateLAG(s, 1)
	if len(res.Members) != 1 || res.Members[0].Name != "GigabitEthernet0/0/2" {
		t.Errorf("after undo eth-trunk only GE0/0/2 remains, got %v", namesOf(res.Members))
	}

	// ② undo trunkport（聚合口视图）：移出 GE0/0/2。需先退出成员视图回到聚合口视图。
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1")
	if out := runOn(s, topology.DeviceSwitch, "undo trunkport GigabitEthernet0/0/2"); strings.Contains(out, "Error") {
		t.Fatalf("undo trunkport failed: %q", out)
	}
	if _, ok := lagMemberTrunkID(s, "GigabitEthernet0/0/2"); ok {
		t.Errorf("GE0/0/2 should be removed after undo trunkport")
	}
	res2 := EvaluateLAG(s, 1)
	if len(res2.Members) != 0 {
		t.Errorf("trunk should have no members after both undos, got %v", namesOf(res2.Members))
	}

	// ③ undo interface Eth-Trunk <id> 有成员时拒绝。
	qaAddMembers(t, s, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "quit") // 回到系统视图
	if out := runOn(s, topology.DeviceSwitch, "undo interface Eth-Trunk 1"); !strings.Contains(out, "has member ports") {
		t.Fatalf("undo interface Eth-Trunk with members should be rejected, got: %q", out)
	}
	// 拒绝后成员仍在。
	if id, ok := lagMemberTrunkID(s, "GigabitEthernet0/0/1"); !ok || id != 1 {
		t.Errorf("member must remain after rejected undo, got id=%d ok=%v", id, ok)
	}

	// ④ undo interface Eth-Trunk <id> 无成员时删除。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "undo eth-trunk")
	runOn(s, topology.DeviceSwitch, "quit")
	if out := runOn(s, topology.DeviceSwitch, "undo interface Eth-Trunk 1"); strings.Contains(out, "Error") {
		t.Fatalf("undo interface Eth-Trunk (no members) failed: %q", out)
	}
	if lagTrunkExists(s, 1) {
		t.Errorf("Eth-Trunk 1 should be deleted after undo (no members)")
	}
	if out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1"); !strings.Contains(out, "does not exist") {
		t.Errorf("display eth-trunk 1 after delete should report not exist, got: %q", out)
	}

	// ⑤ undo load-balance 回滚到缺省（额外回滚验证）。
	s2 := qaNewSwitch()
	qaSetupTrunk(t, s2, 1, "")
	runOn(s2, topology.DeviceSwitch, "load-balance src-dst-mac")
	if s2.DeviceConfig[lagTrunkKey(1, "load-balance")] != "src-dst-mac" {
		t.Fatalf("precondition: load-balance should be src-dst-mac")
	}
	if out := runOn(s2, topology.DeviceSwitch, "undo load-balance"); strings.Contains(out, "Error") {
		t.Fatalf("undo load-balance failed: %q", out)
	}
	if _, ok := s2.DeviceConfig[lagTrunkKey(1, "load-balance")]; ok {
		t.Errorf("load-balance key should be cleared after undo")
	}
	if EvaluateLAG(s2, 1).LoadBalance != DefaultLoadBalance {
		t.Errorf("after undo load-balance should revert to default %q", DefaultLoadBalance)
	}
}

// TestLAGQAAC9LACPExtension 验证 lacp 扩展命令在系统视图/成员口/聚合口三处的落键与非法值报错。
func TestLAGQAAC9LACPExtension(t *testing.T) {
	s := qaNewSwitch()
	runOn(s, topology.DeviceSwitch, "system-view")

	// —— 系统视图 ——
	if out := runOn(s, topology.DeviceSwitch, "lacp priority 100"); strings.Contains(out, "Error") {
		t.Fatalf("lacp priority 100 failed: %q", out)
	}
	if s.DeviceConfig[lagSysKey("priority")] != "100" {
		t.Errorf("sys lacp:priority want 100, got %q", s.DeviceConfig[lagSysKey("priority")])
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp preempt enable"); strings.Contains(out, "Error") {
		t.Fatalf("lacp preempt enable failed: %q", out)
	}
	if s.DeviceConfig[lagSysKey("preempt")] != "enable" {
		t.Errorf("sys lacp:preempt want enable, got %q", s.DeviceConfig[lagSysKey("preempt")])
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp timeout fast"); strings.Contains(out, "Error") {
		t.Fatalf("lacp timeout fast failed: %q", out)
	}
	if s.DeviceConfig[lagSysKey("timeout")] != "fast" {
		t.Errorf("sys lacp:timeout want fast, got %q", s.DeviceConfig[lagSysKey("timeout")])
	}

	// 系统视图非法值。
	if out := runOn(s, topology.DeviceSwitch, "lacp priority 99999"); !strings.Contains(out, errLAGLACPPriRange) {
		t.Errorf("lacp priority 99999 should error %q, got: %q", errLAGLACPPriRange, out)
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp timeout bogus"); !strings.Contains(out, errLAGUnrecognized) {
		t.Errorf("lacp timeout bogus should error %q, got: %q", errLAGUnrecognized, out)
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp preempt maybe"); !strings.Contains(out, errLAGUnrecognized) {
		t.Errorf("lacp preempt maybe should error %q, got: %q", errLAGUnrecognized, out)
	}

	// —— 成员口视图：lacp priority ——
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "lacp priority 200"); strings.Contains(out, "Error") {
		t.Fatalf("member lacp priority 200 failed: %q", out)
	}
	if s.DeviceConfig[lagMemberKey("GigabitEthernet0/0/1", "lacp:priority")] != "200" {
		t.Errorf("member lacp:priority want 200, got %q", s.DeviceConfig[lagMemberKey("GigabitEthernet0/0/1", "lacp:priority")])
	}
	// 成员口 undo lacp priority 清除。
	if out := runOn(s, topology.DeviceSwitch, "undo lacp priority"); strings.Contains(out, "Error") {
		t.Fatalf("member undo lacp priority failed: %q", out)
	}
	if _, ok := s.DeviceConfig[lagMemberKey("GigabitEthernet0/0/1", "lacp:priority")]; ok {
		t.Errorf("member lacp:priority should be cleared after undo")
	}
	runOn(s, topology.DeviceSwitch, "quit")

	// —— 聚合口视图：lacp preempt / preempt delay / timeout ——
	qaSetupTrunk(t, s, 1, "lacp-static")
	if out := runOn(s, topology.DeviceSwitch, "lacp preempt enable"); strings.Contains(out, "Error") {
		t.Fatalf("trunk lacp preempt enable failed: %q", out)
	}
	if s.DeviceConfig[lagTrunkKey(1, "preempt")] != "enable" {
		t.Errorf("trunk :lag:preempt want enable, got %q", s.DeviceConfig[lagTrunkKey(1, "preempt")])
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp timeout fast"); strings.Contains(out, "Error") {
		t.Fatalf("trunk lacp timeout fast failed: %q", out)
	}
	if s.DeviceConfig[lagTrunkKey(1, "lacp-timeout")] != "fast" {
		t.Errorf("trunk :lag:lacp-timeout want fast, got %q", s.DeviceConfig[lagTrunkKey(1, "lacp-timeout")])
	}
	if out := runOn(s, topology.DeviceSwitch, "lacp preempt delay 60"); strings.Contains(out, "Error") {
		t.Fatalf("trunk lacp preempt delay 60 failed: %q", out)
	}
	if s.DeviceConfig[lagTrunkKey(1, "preempt-delay")] != "60" {
		t.Errorf("trunk :lag:preempt-delay want 60, got %q", s.DeviceConfig[lagTrunkKey(1, "preempt-delay")])
	}
	// 聚合口非法 preempt delay（>180）。
	if out := runOn(s, topology.DeviceSwitch, "lacp preempt delay 200"); !strings.Contains(out, errLAGPreemptDelay) {
		t.Errorf("trunk lacp preempt delay 200 should error %q, got: %q", errLAGPreemptDelay, out)
	}
	// 配置在 display 中可见（verbose 块）。
	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1 verbose")
	for _, want := range []string{"LACP Preempt: enable", "LACP Timeout: fast", "Preempt Delay: 60s"} {
		if !strings.Contains(out, want) {
			t.Errorf("display verbose missing %q, got: %q", want, out)
		}
	}
}

// TestLAGQAAC10H3CVariant 验证 H3C 变体：Bridge-Aggregation<N> 经 port link-aggregation group /
// link-aggregation mode 写 agg-family="h3c"，display link-aggregation summary 按 agg-family 归类，
// 未配置的华为侧不编造 Eth-Trunk<N>。
func TestLAGQAAC10H3CVariant(t *testing.T) {
	s := qaNewSwitch()
	runOn(s, topology.DeviceSwitch, "system-view")

	// 创建 H3C 逻辑口 Bridge-Aggregation 1。
	runOn(s, topology.DeviceSwitch, "interface Bridge-Aggregation 1")
	// 退出逻辑口视图回到系统视图，再进入成员物理口（避免嵌套接口视图被拒）。
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "port link-aggregation group 1"); strings.Contains(out, "Error") {
		t.Fatalf("port link-aggregation group 1 failed: %q", out)
	}
	if s.DeviceConfig[lagMemberKey("GigabitEthernet0/0/1", "agg-family")] != aggFamilyH3C {
		t.Errorf("member agg-family want %q, got %q", aggFamilyH3C, s.DeviceConfig[lagMemberKey("GigabitEthernet0/0/1", "agg-family")])
	}
	if id, ok := lagMemberTrunkID(s, "GigabitEthernet0/0/1"); !ok || id != 1 {
		t.Errorf("member should join group 1, got id=%d ok=%v", id, ok)
	}
	runOn(s, topology.DeviceSwitch, "quit")

	// 设 H3C 模式（写 :lag:mode）。已在系统视图，直接进入 Bridge-Aggregation。
	runOn(s, topology.DeviceSwitch, "interface Bridge-Aggregation 1")
	if out := runOn(s, topology.DeviceSwitch, "link-aggregation mode static"); strings.Contains(out, "Error") {
		t.Fatalf("link-aggregation mode static failed: %q", out)
	}
	runOn(s, topology.DeviceSwitch, "quit")

	// summary 按 agg-family 归类为 Bridge-Aggregation1；不得编造 Eth-Trunk1 组名。
	sum := runOn(s, topology.DeviceSwitch, "display link-aggregation summary")
	if !strings.Contains(sum, "Bridge-Aggregation1") {
		t.Errorf("summary should group h3c group as Bridge-Aggregation1, got: %q", sum)
	}
	if strings.Contains(sum, "Eth-Trunk1") {
		t.Errorf("summary must NOT invent a phantom Eth-Trunk1 for unconfigured Huawei group, got: %q", sum)
	}
	if !strings.Contains(sum, "GigabitEthernet0/0/1") {
		t.Errorf("summary should list member GE0/0/1, got: %q", sum)
	}

	// display eth-trunk 1（同组 id）也应以 agg-family 决定展示名 Bridge-Aggregation1。
	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1")
	if !strings.Contains(out, "Bridge-Aggregation1's state information is:") {
		t.Errorf("display eth-trunk 1 should use Bridge-Aggregation1 name (by agg-family), got: %q", out)
	}

	// 对照：同时配置一个华为 Eth-Trunk，summary 应各自归类、不串。
	qaSetupTrunk(t, s, 2, "manual load-balance")
	qaAddMembers(t, s, "GigabitEthernet0/0/3") // GE0/0/3 默认 Down，仅验证归类
	sum2 := runOn(s, topology.DeviceSwitch, "display link-aggregation summary")
	if !strings.Contains(sum2, "Bridge-Aggregation1") || !strings.Contains(sum2, "Eth-Trunk2") {
		t.Errorf("mixed summary should show both Bridge-Aggregation1 and Eth-Trunk2, got: %q", sum2)
	}
}
