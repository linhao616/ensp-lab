package cli

// p2_vrrp_qa_test.go —— VRRP 增量「独立端到端验收（T06，QA 严过关 Yan）」。
//
// 本文件独立于工程师的 vrrp_eval_test.go / p2_vrrp_test.go，由 QA 独立编写，
// 目的是证明「真的能用」而非「存在即过」。覆盖：
//   AC4（选举 + tie-break，独立场景）            TestQA_EvaluateVRRPThreeRoles / TestQA_CompareVRRPPriorityTieBreak
//   AC5（lite 诚实占位 + 角色诚实文案）          TestQA_VRRPSimNoteLite / TestQA_DisplayVRRPHonestNotes / TestQA_MultiInterfaceVRRPRoles
//   AC6（纯函数无副作用契约）                    TestQA_EvaluateVRRPNoSideEffects
//   P1 收口端到端（track / auth / undo）          TestQA_P1TrackEffectivePriority / TestQA_P1TrackReducedDefaultVsExplicit /
//                                                 TestQA_P1AuthModeDisplayVrrpHidesKey / TestQA_P1AuthModeCurrentConfigHidesKey / TestQA_P1UndoRemovesGroup
//   健壮性 / 边界（独立补充）                     TestQA_BoundaryRejections / TestQA_NonInterfaceViewGuard / TestQA_NonL3DeviceCapabilityRejection
//   save→reload 复现（独立复现 AC2）              TestQA_SaveReloadReproducesConfig
//
// 智能路由：源码有 Bug → 反馈工程师；测试代码 Bug → 自行修复；全部通过 → 报告成功（NoOne）。
// 严禁本文件代写实现代码或 PRD/设计：仅验收与判定。

import (
	"sort"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// qaSnapshotInterfaces 把 Interfaces map 序列化为可比较的排序字符串（用于 AC6 无副作用断言）。
func qaSnapshotInterfaces(s *CLIState) string {
	if s == nil || s.Interfaces == nil {
		return ""
	}
	keys := make([]string, 0, len(s.Interfaces))
	for k := range s.Interfaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		ic := s.Interfaces[k]
		if ic == nil {
			b.WriteString(k)
			b.WriteString("=<nil>\n")
			continue
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(ic.Name)
		b.WriteString(",")
		b.WriteString(ic.IP)
		b.WriteString(",")
		b.WriteString(ic.Mask)
		b.WriteString(",")
		b.WriteString(ic.Status)
		b.WriteString(",")
		b.WriteString(ic.Protocol)
		b.WriteString(",")
		b.WriteString(ic.Description)
		b.WriteString("\n")
	}
	return b.String()
}

// TestQA_EvaluateVRRPThreeRoles AC4：独立验证三种角色路径（未配齐 / 255 拥有者 / 普通优先级）。
func TestQA_EvaluateVRRPThreeRoles(t *testing.T) {
	// 路径 A：未配齐（无 virtual-ip 键）→ Initialize，Reason "VRRP group not configured"。
	sA := NewCLIStateWithType(topology.DeviceRouter)
	rA := EvaluateVRRP(sA, "GigabitEthernet0/0/5", 7)
	if rA.Configured {
		t.Errorf("path A: expected Configured=false, got true")
	}
	if rA.Role != "Initialize" {
		t.Errorf("path A: expected Role=Initialize, got %q", rA.Role)
	}
	if rA.Reason != "VRRP group not configured" {
		t.Errorf("path A: unexpected Reason %q", rA.Reason)
	}

	// 路径 B：Priority==255（虚拟 IP 拥有者）→ Master, IsOwner=true。
	sB := NewCLIStateWithType(topology.DeviceRouter)
	sB.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "virtual-ip")] = "172.16.5.254"
	sB.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "priority")] = "255"
	rB := EvaluateVRRP(sB, "GigabitEthernet0/0/5", 7)
	if !rB.Configured || rB.Role != "Master" {
		t.Errorf("path B: expected Master, got Configured=%v Role=%q", rB.Configured, rB.Role)
	}
	if !rB.IsOwner {
		t.Errorf("path B: expected IsOwner=true for priority 255")
	}
	if rB.Reason != "Virtual IP owner (priority 255)" {
		t.Errorf("path B: unexpected Reason %q", rB.Reason)
	}
	if rB.Priority != 255 {
		t.Errorf("path B: expected Priority=255, got %d", rB.Priority)
	}

	// 路径 C：普通优先级 → 本地静态假设 Master（Reason 含 "Local static assumption"）。
	sC := NewCLIStateWithType(topology.DeviceRouter)
	sC.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "virtual-ip")] = "172.16.5.253"
	sC.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "priority")] = "90"
	sC.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "preempt")] = "disable"
	sC.DeviceConfig[vrrpKey("GigabitEthernet0/0/5", 7, "advertise")] = "3"
	rC := EvaluateVRRP(sC, "GigabitEthernet0/0/5", 7)
	if rC.Role != "Master" {
		t.Errorf("path C: expected Master, got %q", rC.Role)
	}
	if rC.IsOwner {
		t.Errorf("path C: expected IsOwner=false for priority 90")
	}
	if !strings.Contains(rC.Reason, "Local static assumption") {
		t.Errorf("path C: expected honest reason, got %q", rC.Reason)
	}
	if rC.Priority != 90 || rC.EffectivePriority != 90 {
		t.Errorf("path C: expected Priority/EffectivePriority=90, got %d/%d", rC.Priority, rC.EffectivePriority)
	}
	if rC.Preempt {
		t.Errorf("path C: expected Preempt=false (preempt-mode disable)")
	}
	if rC.Advertise != 3 {
		t.Errorf("path C: expected Advertise=3, got %d", rC.Advertise)
	}
}

// TestQA_CompareVRRPPriorityTieBreak AC4：独立验证 CompareVRRPPriority 的胜负与确定性 tie-break。
func TestQA_CompareVRRPPriorityTieBreak(t *testing.T) {
	// 高优先级胜（即便对端接口 IP 更大）。
	if c := CompareVRRPPriority(
		VRRPGroup{Priority: 200, InterfaceIP: "10.0.0.1"},
		VRRPGroup{Priority: 100, InterfaceIP: "10.0.0.9"},
	); c <= 0 {
		t.Errorf("higher priority should win, got %d", c)
	}
	// 255 拥有者胜（即便对端普通优先级更高）。
	if c := CompareVRRPPriority(
		VRRPGroup{Priority: 255, InterfaceIP: "10.0.0.1"},
		VRRPGroup{Priority: 254, InterfaceIP: "10.0.0.2"},
	); c <= 0 {
		t.Errorf("owner 255 should win, got %d", c)
	}
	if c := CompareVRRPPriority(
		VRRPGroup{Priority: 254, InterfaceIP: "10.0.0.2"},
		VRRPGroup{Priority: 255, InterfaceIP: "10.0.0.1"},
	); c >= 0 {
		t.Errorf("non-owner should lose to 255, got %d", c)
	}
	// 同优先级比接口 IP 大者胜（确定性）。
	a := VRRPGroup{Priority: 100, InterfaceIP: "10.0.0.9"}
	b := VRRPGroup{Priority: 100, InterfaceIP: "10.0.0.3"}
	if c := CompareVRRPPriority(a, b); c <= 0 {
		t.Errorf("larger interface IP should win on tie, got %d", c)
	}
	if c := CompareVRRPPriority(b, a); c >= 0 {
		t.Errorf("smaller interface IP should lose on tie, got %d", c)
	}
	// 完全相等 → 0（确定性 tie-break）。
	same := VRRPGroup{Priority: 100, InterfaceIP: "10.0.0.5"}
	if c := CompareVRRPPriority(same, same); c != 0 {
		t.Errorf("identical groups should tie (0), got %d", c)
	}
}

// TestQA_VRRPSimNoteLite AC5：lite 引擎下 vrrpSimNote() 含「非内核级真实 VRRP 故障切换」诚实注记。
func TestQA_VRRPSimNoteLite(t *testing.T) {
	note := vrrpSimNote()
	if !strings.Contains(note, "非内核级真实 VRRP 故障切换") {
		t.Errorf("lite sim note should mention honest failover placeholder, got %q", note)
	}
	if !strings.Contains(note, "lite") {
		t.Errorf("lite sim note should mention lite engine, got %q", note)
	}
}

// TestQA_DisplayVRRPHonestNotes AC5：display vrrp 每行 State 后含本地假设注记、末尾含 lite 注记、绝不臆造 Backup。
func TestQA_DisplayVRRPHonestNotes(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.88.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.88.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 110")

	out := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(out, "（本地假设选举，非跨设备真实通告）") {
		t.Errorf("display vrrp should contain honest local-assumption note per State line, got: %q", out)
	}
	if !strings.Contains(out, "非内核级真实 VRRP 故障切换") {
		t.Errorf("display vrrp should contain lite failover honest note, got: %q", out)
	}
	// 本期不得臆造 Backup（除非未来 P2 跨设备选举）。
	if strings.Contains(out, "Backup") {
		t.Errorf("display vrrp must NOT fabricate Backup role, got: %q", out)
	}
}

// TestQA_MultiInterfaceVRRPRoles AC4/AC5：多接口多组均诚实标 Master、brief/详情均不出现 Backup。
func TestQA_MultiInterfaceVRRPRoles(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 120")
	// 第二个接口（setIfaceIP 内部已切到该接口视图）。
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/2", "10.0.0.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 2 virtual-ip 10.0.0.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 2 priority 200")

	brief := runOn(s, topology.DeviceRouter, "display vrrp brief")
	if !strings.Contains(brief, "Master") {
		t.Errorf("brief should show Master role, got: %q", brief)
	}
	if strings.Contains(brief, "Backup") {
		t.Errorf("brief must NOT show Backup, got: %q", brief)
	}

	detail := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(detail, "192.168.1.254") || !strings.Contains(detail, "10.0.0.254") {
		t.Errorf("detail should show both virtual-ip, got: %q", detail)
	}
	if strings.Contains(detail, "Backup") {
		t.Errorf("detail must NOT show Backup, got: %q", detail)
	}
}

// TestQA_EvaluateVRRPNoSideEffects AC6：连续两次 EvaluateVRRP 结果一致，且不改写 DeviceConfig / Interfaces。
func TestQA_EvaluateVRRPNoSideEffects(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "virtual-ip")] = "10.0.0.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "priority")] = "150"
	s.DeviceConfig["interface:GigabitEthernet0/0/1:ip"] = "10.0.0.1 255.255.255.0"

	dcBefore := snapshotDeviceConfig(s)
	ifBefore := qaSnapshotInterfaces(s)

	res1 := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	dcMid := snapshotDeviceConfig(s)
	ifMid := qaSnapshotInterfaces(s)

	res2 := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	dcAfter := snapshotDeviceConfig(s)
	ifAfter := qaSnapshotInterfaces(s)

	if res1 != res2 {
		t.Errorf("two consecutive EvaluateVRRP returned different results: %+v vs %+v", res1, res2)
	}
	if dcMid != dcBefore {
		t.Errorf("EvaluateVRRP mutated DeviceConfig on 1st call:\nbefore=%q\nafter=%q", dcBefore, dcMid)
	}
	if dcAfter != dcMid {
		t.Errorf("EvaluateVRRP mutated DeviceConfig on 2nd call:\nafter1=%q\nafter2=%q", dcMid, dcAfter)
	}
	if ifMid != ifBefore {
		t.Errorf("EvaluateVRRP mutated Interfaces on 1st call:\nbefore=%q\nafter=%q", ifBefore, ifMid)
	}
	if ifAfter != ifMid {
		t.Errorf("EvaluateVRRP mutated Interfaces on 2nd call:\nafter1=%q\nafter2=%q", ifMid, ifAfter)
	}
}

// TestQA_P1TrackEffectivePriority P1：被跟踪接口 Down 时有效优先级 = 配置优先级 − reduced（端到端纯函数层验证）。
func TestQA_P1TrackEffectivePriority(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 2, "virtual-ip")] = "10.1.2.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 2, "priority")] = "120"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 2, "track-iface")] = "GigabitEthernet0/0/3"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 2, "track-reduced")] = "30"

	// 被跟踪口未 Down → 有效优先级 = 配置优先级。
	rUp := EvaluateVRRP(s, "GigabitEthernet0/0/1", 2)
	if rUp.EffectivePriority != 120 {
		t.Errorf("track iface up: expected EffectivePriority=120, got %d", rUp.EffectivePriority)
	}
	// 被跟踪口 Down → 有效优先级 = 120 - 30 = 90。
	s.DeviceConfig["interface:GigabitEthernet0/0/3:status"] = "Down"
	rDown := EvaluateVRRP(s, "GigabitEthernet0/0/1", 2)
	if rDown.EffectivePriority != 90 {
		t.Errorf("track iface down: expected EffectivePriority=90, got %d", rDown.EffectivePriority)
	}
}

// TestQA_P1TrackReducedDefaultVsExplicit P1：命令层 track 缺省 reduced=10、显式 reduced=n 的端到端有效优先级。
func TestQA_P1TrackReducedDefaultVsExplicit(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 100")
	// track 不指定 reduced（缺省 10）。
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 track interface GigabitEthernet0/0/2")

	// 将被跟踪口显式 shutdown（诚实触发，无自动链路事件）。
	runOn(s, topology.DeviceRouter, "system-view")
	runOn(s, topology.DeviceRouter, "interface GigabitEthernet0/0/2")
	runOn(s, topology.DeviceRouter, "shutdown")
	// 回到 GE0/0/1 视图核对。
	runOn(s, topology.DeviceRouter, "system-view")
	runOn(s, topology.DeviceRouter, "interface GigabitEthernet0/0/1")

	out := runOn(s, topology.DeviceRouter, "display vrrp")
	// 有效优先级 = 100 - 10 = 90。
	if !strings.Contains(out, "Effective Priority 90") {
		t.Errorf("default reduced=10 should yield Effective Priority 90, got: %q", out)
	}

	// 显式 reduced 40 → 有效优先级 = 100 - 40 = 60。
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 track interface GigabitEthernet0/0/2 reduced 40")
	out2 := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(out2, "Effective Priority 60") {
		t.Errorf("explicit reduced=40 should yield Effective Priority 60, got: %q", out2)
	}
}

// TestQA_P1AuthModeDisplayVrrpHidesKey P1：display vrrp 展示认证模式但绝不显示明文 key（诚实边界 O3）。
func TestQA_P1AuthModeDisplayVrrpHidesKey(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	outCmd := runOn(s, topology.DeviceRouter, "vrrp vrid 1 authentication-mode simple QaSecKey2026")
	if strings.Contains(outCmd, "Error") {
		t.Fatalf("authentication-mode should configure, got %q", outCmd)
	}
	out := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(out, "Authentication") {
		t.Errorf("display vrrp should show Authentication field, got: %q", out)
	}
	if !strings.Contains(out, "simple") {
		t.Errorf("display vrrp should show auth mode simple, got: %q", out)
	}
	if strings.Contains(out, "QaSecKey2026") {
		t.Errorf("display vrrp must NOT show plaintext auth key, got: %q", out)
	}
}

// TestQA_P1AuthModeCurrentConfigHidesKey P1：display current-configuration 出现 authentication-mode 行但 key 不显明文。
//
// 注：本例若失败，属源码偏离验收标准——详见团队回报（parser.go:5175 当前以明文回显 key）。
func TestQA_P1AuthModeCurrentConfigHidesKey(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 authentication-mode md5 QaSecKey2026")

	cur := runOn(s, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(cur, "authentication-mode") {
		t.Fatalf("current-configuration should contain authentication-mode line, got: %q", cur)
	}
	// 诚实边界：current-configuration 亦不得泄露明文 key。
	if strings.Contains(cur, "QaSecKey2026") {
		t.Errorf("current-configuration must NOT show plaintext auth key (honest placeholder), got: %q", cur)
	}
}

// TestQA_P1UndoRemovesGroup P1：undo vrrp vrid X 后该组从 display vrrp / display current-configuration 消失。
func TestQA_P1UndoRemovesGroup(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 120")

	before := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(before, "192.168.1.254") {
		t.Fatalf("group should exist before undo, got: %q", before)
	}

	out := runOn(s, topology.DeviceRouter, "undo vrrp vrid 1")
	if strings.Contains(out, "Error") {
		t.Fatalf("undo vrrp vrid 1 should succeed, got: %q", out)
	}

	after := runOn(s, topology.DeviceRouter, "display vrrp")
	if strings.Contains(after, "192.168.1.254") {
		t.Errorf("undo should remove group from display vrrp, got: %q", after)
	}
	if !strings.Contains(after, "Not configured") {
		t.Errorf("display vrrp should show Not configured after undo, got: %q", after)
	}

	cur := runOn(s, topology.DeviceRouter, "display current-configuration")
	if strings.Contains(cur, "vrrp vrid 1 virtual-ip") {
		t.Errorf("undo should remove group from current-configuration, got: %q", cur)
	}
}

// TestQA_BoundaryRejections 健壮性/边界：vrid/priority/advertise 越界、virtual-ip 非法/不同网段均明确 Error。
func TestQA_BoundaryRejections(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")

	rejects := []struct {
		cmd  string
		must string
	}{
		{"vrrp vrid 0 virtual-ip 192.168.1.254", "vrid"},
		{"vrrp vrid 256 virtual-ip 192.168.1.254", "vrid"},
		{"vrrp vrid 1 priority 0", "priority"},
		{"vrrp vrid 1 priority 255", "priority"},
		{"vrrp vrid 1 priority 300", "priority"},
		{"vrrp vrid 1 timer advertise 0", "advertise"},
		{"vrrp vrid 1 timer advertise 256", "advertise"},
		{"vrrp vrid 1 virtual-ip 999.1.1.1", "virtual-ip"},
		{"vrrp vrid 1 virtual-ip not-an-ip", "virtual-ip"},
	}
	for _, c := range rejects {
		out := runOn(s, topology.DeviceRouter, c.cmd)
		if !strings.Contains(out, "Error") {
			t.Errorf("%q should be rejected with Error, got: %q", c.cmd, out)
		}
		if !strings.Contains(out, c.must) {
			t.Errorf("%q should mention %q in error, got: %q", c.cmd, c.must, out)
		}
	}
	// 不同网段明确拒绝（同网段校验，拍板 #4）。
	out := runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 10.9.9.9")
	if !strings.Contains(out, "same subnet") {
		t.Errorf("different subnet should be rejected, got: %q", out)
	}
}

// TestQA_NonInterfaceViewGuard 健壮性：非接口视图执行 vrrp → "interface view" 守卫。
func TestQA_NonInterfaceViewGuard(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	runOn(s, topology.DeviceRouter, "system-view")
	out := runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	if !strings.Contains(out, "interface view") {
		t.Errorf("non-interface view should be rejected, got: %q", out)
	}
}

// TestQA_NonL3DeviceCapabilityRejection 健壮性：非 l3Devices 设备（Switch/PC）能力拒绝。
func TestQA_NonL3DeviceCapabilityRejection(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DeviceSwitch, topology.DevicePC} {
		s := NewCLIStateWithType(dt)
		runOn(s, dt, "system-view")
		runOn(s, dt, "interface GigabitEthernet0/0/1")
		out := runOn(s, dt, "vrrp vrid 1 virtual-ip 192.168.1.254")
		if !strings.Contains(out, "not supported") {
			t.Errorf("%s should reject vrrp by capability, got: %q", dt, out)
		}
	}
}

// TestQA_SaveReloadReproducesConfig 独立复现 AC2：save→reload 后 display vrrp / current-configuration 完整复现配置。
func TestQA_SaveReloadReproducesConfig(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 150")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 preempt-mode disable")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 timer advertise 3")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 track interface GigabitEthernet0/0/2 reduced 40")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 authentication-mode md5 QaSecKey2026")

	// save（含 Y/N 确认）并序列化。
	runOn(s, topology.DeviceRouter, "save")
	runOn(s, topology.DeviceRouter, "y")
	cfg := s.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")

	// display vrrp 复现 virtual-ip / priority / preempt / advertise / track / auth 模式。
	out := runOn(reloaded, topology.DeviceRouter, "display vrrp")
	for _, want := range []string{
		"192.168.1.254", "150", "Disabled", "3 s",
		"GigabitEthernet0/0/2", "40", "Authentication", "md5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reloaded display vrrp should contain %q, got: %q", want, out)
		}
	}
	// display vrrp 仍须隐藏明文 key。
	if strings.Contains(out, "QaSecKey2026") {
		t.Errorf("reloaded display vrrp must not show plaintext key, got: %q", out)
	}

	// display current-configuration 复现各差异行（证明残桩丢配置缺陷已根治）。
	cur := runOn(reloaded, topology.DeviceRouter, "display current-configuration")
	for _, want := range []string{
		"vrrp vrid 1 virtual-ip 192.168.1.254",
		"vrrp vrid 1 priority 150",
		"vrrp vrid 1 preempt-mode disable",
		"vrrp vrid 1 timer advertise 3",
		"vrrp vrid 1 track interface GigabitEthernet0/0/2 reduced 40",
		"vrrp vrid 1 authentication-mode md5",
	} {
		if !strings.Contains(cur, want) {
			t.Errorf("reloaded current-configuration should contain %q, got: %q", want, cur)
		}
	}
}
