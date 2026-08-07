package cli

// p2_stp_qa_test.go —— STP/RSTP/MSTP 增量「独立端到端验收（T06，QA 严过关 Yan）」。
//
// 本文件独立于工程师的 stp_eval_test.go / p2_stp_test.go，由 QA 独立编写，
// 目的是证明「真的能用」而非「存在即过」。覆盖设计文档 §5 T06：
//   AC4（MSTP region 配置持久化 + active 前预配置不生效 / 激活后生效，官方语义保真）
//        TestP2STPQA_AC4RegionPersistenceAndActiveSemantics
//   AC5（lite 引擎下 display stp / interface / brief 均含 stpSimNote 诚实注记，
//        不臆造 Backup/Master、不伪造 TC 计数）
//        TestP2STPQA_AC5LiteHonestNotes
//   AC6（纯函数无副作用契约：EvaluateSTP/CompareBridgeID 连续两次一致、不写 DeviceConfig/Interfaces；
//        undo stp 清理全部 stp 键后 display stp 复现 Disabled；非 switchDevices 设备被能力拒绝）
//        TestP2STPQA_AC6PureFunctionContract / TestP2STPQA_AC6UndoSTPClearsKeys /
//        TestP2STPQA_AC6CapabilityRejection
//   P1 端到端（bpdu/root/loop/tc-protection(含 interval) / bridge-address /
//        stp instance <id> root primary|secondary / undo stp root /
//        undo stp instance <id> root 的配置持久化与 display/current-configuration 展示）
//        TestP2STPQA_P1ProtectionsAndBridgeAddress / TestP2STPQA_P1InstanceRootAndUndo
//
// 智能路由：源码有 Bug → 反馈工程师；测试代码 Bug → 自行修复；全部通过 → 报告成功（NoOne）。
// 严禁本文件代写实现代码或 PRD/设计：仅验收与判定。运行：go test ./internal/cli/ -run TestP2STPQA

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// qaSTPEnterRegion 进入 MST region 视图并配置完整 region（name/revision/instance vlan），返回状态。
func qaSTPEnterRegion(s *CLIState, dt topology.DeviceType) {
	runOn(s, dt, "system-view")
	runOn(s, dt, "stp region-configuration") // 进入 ViewMSTRegion
	runOn(s, dt, "stp region-name HUAWEI")
	runOn(s, dt, "stp revision-level 1")
	runOn(s, dt, "stp instance 1 vlan 2 to 10")
}

// TestP2STPQA_AC4RegionPersistenceAndActiveSemantics AC4：
// MSTP region 配置持久化；active region-configuration 之前预配置不生效、激活后生效（官方语义保真）。
func TestP2STPQA_AC4RegionPersistenceAndActiveSemantics(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)

	// 未配置 → not configured。
	if out := runOn(s, topology.DeviceSwitch, "display stp region-configuration"); !strings.Contains(out, "MSTP Region: not configured") {
		t.Errorf("未配置应显示 'MSTP Region: not configured', got: %q", out)
	}

	// 进入 region 视图并预配置（尚未 active）。
	qaSTPEnterRegion(s, topology.DeviceSwitch)
	pre := runOn(s, topology.DeviceSwitch, "display stp region-configuration")
	if !strings.Contains(pre, "Region name") || !strings.Contains(pre, "HUAWEI") {
		t.Errorf("预配置后应显示 Region name/HUAWEI, got: %q", pre)
	}
	if !strings.Contains(pre, "Configuration Status: Inactive") {
		t.Errorf("active 前 region 状态应为 Inactive, got: %q", pre)
	}
	// 预配置不生效：display stp brief 仅含 CIST(0)，不应出现实例 1 行。
	preBrief := runOn(s, topology.DeviceSwitch, "display stp brief")
	if strings.Contains(preBrief, "\n1     ") {
		t.Errorf("active 前实例 1 不应参与计算（brief 不应出现 MSTID=1 行）, got: %q", preBrief)
	}
	// 实例集合（驱动 brief/display）在 active 前仅 [0]。
	if ids := collectSTPInstances(s); len(ids) != 1 || ids[0] != 0 {
		t.Errorf("active 前 collectSTPInstances 应仅 [0], got %v", ids)
	}

	// active region-configuration。
	if out := runOn(s, topology.DeviceSwitch, "stp active region-configuration"); strings.Contains(out, "Error") {
		t.Errorf("active region-configuration 应成功, got: %q", out)
	}
	post := runOn(s, topology.DeviceSwitch, "display stp region-configuration")
	if !strings.Contains(post, "Configuration Status: Active") {
		t.Errorf("active 后 region 状态应为 Active, got: %q", post)
	}
	// active 后实例 1 生效：brief 出现 MSTID=1 行，且 collectSTPInstances 含 1。
	postBrief := runOn(s, topology.DeviceSwitch, "display stp brief")
	if !strings.Contains(postBrief, "\n1     ") {
		t.Errorf("active 后实例 1 应参与计算（brief 应出现 MSTID=1 行）, got: %q", postBrief)
	}
	if ids := collectSTPInstances(s); len(ids) != 2 || ids[0] != 0 || ids[1] != 1 {
		t.Errorf("active 后 collectSTPInstances 应为 [0,1], got %v", ids)
	}

	// 持久化：SerializeToDeviceConfigData（save 写入的内容）经 reload 后 region 仍 Active + 配置保留。
	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW1")
	reOut := runOn(reloaded, topology.DeviceSwitch, "display stp region-configuration")
	if !strings.Contains(reOut, "Configuration Status: Active") {
		t.Errorf("reload 后 region 应仍为 Active, got: %q", reOut)
	}
	if !strings.Contains(reOut, "HUAWEI") || !strings.Contains(reOut, "2 to 10") {
		t.Errorf("reload 后 region 名称/实例 VLAN 应保留, got: %q", reOut)
	}
}

// TestP2STPQA_AC5LiteHonestNotes AC5：
// lite 引擎下 display stp / display stp interface / display stp brief 均含 stpSimNote 诚实注记，
// 不臆造 Backup/Master、不伪造 TC 计数（interface 详情如实报告 TC or TCN received: 0）。
func TestP2STPQA_AC5LiteHonestNotes(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	// 制造若干端口状态差异，确保端口 Role/State 段被渲染。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "stp edged-port enable")
	runOn(s, topology.DeviceSwitch, "quit")

	// 纯函数口径：lite 注记含「非内核级真实 BPDU 选举 / 无真实拓扑收敛」。
	liteNote := stpSimNote()
	for _, want := range []string{"模拟生成树", "lite 引擎", "非内核级真实 BPDU 选举", "无真实拓扑收敛"} {
		if !strings.Contains(liteNote, want) {
			t.Errorf("stpSimNote() lite 注记应含 %q, got: %q", want, liteNote)
		}
	}

	cases := []struct {
		name string
		cmd  string
	}{
		{"default", "display stp"},
		{"brief", "display stp brief"},
		{"interface", "display stp interface GigabitEthernet0/0/1"},
	}
	for _, c := range cases {
		out := runOn(s, topology.DeviceSwitch, c.cmd)
		// 均含 lite 诚实注记。
		if !strings.Contains(out, "模拟生成树") || !strings.Contains(out, "lite 引擎") ||
			!strings.Contains(out, "非内核级真实 BPDU 选举") || !strings.Contains(out, "无真实拓扑收敛") {
			t.Errorf("[%s] %q 应含 lite 诚实注记, got: %q", c.name, c.cmd, out)
		}
		// 不臆造 Backup/Master（STP 角色仅 DESI/ROOT/ALTE/BACK/--）。
		if strings.Contains(out, "Backup") {
			t.Errorf("[%s] %q 不得臆造 Backup 角色, got: %q", c.name, c.cmd, out)
		}
		if strings.Contains(out, "Master") {
			t.Errorf("[%s] %q 不得臆造 Master 角色, got: %q", c.name, c.cmd, out)
		}
	}

	// interface 详情：TC 计数诚实（固定 0，不伪造非零计数）。
	ifc := runOn(s, topology.DeviceSwitch, "display stp interface GigabitEthernet0/0/1")
	if !strings.Contains(ifc, "TC or TCN received: 0") {
		t.Errorf("display stp interface 应诚实报告 'TC or TCN received: 0', got: %q", ifc)
	}
}

// TestP2STPQA_AC6PureFunctionContract AC6：
// EvaluateSTP / CompareBridgeID 纯函数无副作用契约——连续两次结果一致，且不写 DeviceConfig / Interfaces。
// （AC6 另含「不 import internal/protocol、零新依赖」为静态设计保障，由代码审查保证，本测试覆盖可观测运行期契约。）
func TestP2STPQA_AC6PureFunctionContract(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	s.DeviceName = "SW1"
	// 预置一些 stp 配置，作为纯函数读取输入。
	s.DeviceConfig[stpKey("mode")] = "rstp"
	s.DeviceConfig[stpKey("priority")] = "4096"
	s.DeviceConfig[stpKey("bridge-address")] = "4c1f-cc00-1234"
	s.Interfaces = map[string]*InterfaceConfig{
		"GigabitEthernet0/0/1": {Name: "GigabitEthernet0/0/1", Status: "Up"},
		"GigabitEthernet0/0/2": {Name: "GigabitEthernet0/0/2", Status: "Up"},
		"GigabitEthernet0/0/3": {Name: "GigabitEthernet0/0/3", Status: "Down"},
	}

	dcBefore := snapshotDeviceConfig(s)
	ifBefore := qaSnapshotInterfaces(s)

	// EvaluateSTP 连续两次调用。
	r1 := EvaluateSTP(s, 0)
	r2 := EvaluateSTP(s, 0)
	if r1.BridgePriority != r2.BridgePriority || r1.RootAddress != r2.RootAddress ||
		r1.IsRoot != r2.IsRoot || len(r1.Ports) != len(r2.Ports) {
		t.Errorf("两次 EvaluateSTP 结果不一致: %+v vs %+v", r1, r2)
	}
	// 也验证实例视图（id>0）一致性。
	ri1 := EvaluateSTP(s, 1)
	ri2 := EvaluateSTP(s, 1)
	if ri1.InstanceID != ri2.InstanceID || len(ri1.Ports) != len(ri2.Ports) {
		t.Errorf("两次 EvaluateSTP(instance) 结果不一致: %+v vs %+v", ri1, ri2)
	}

	// CompareBridgeID 连续两次一致（值类型入参，天然无副作用，这里固化行为）。
	a := BridgeID{Priority: 4096, Address: "4c1f-cc00-0001"}
	b := BridgeID{Priority: 32768, Address: "4c1f-cc00-0002"}
	if c1, c2 := CompareBridgeID(a, b), CompareBridgeID(a, b); c1 != c2 {
		t.Errorf("两次 CompareBridgeID 结果不一致: %d vs %d", c1, c2)
	}

	dcAfter := snapshotDeviceConfig(s)
	ifAfter := qaSnapshotInterfaces(s)
	// 不得改写 DeviceConfig / Interfaces（不得写入任何键）。
	if dcAfter != dcBefore {
		t.Errorf("EvaluateSTP/CompareBridgeID 不应改写 DeviceConfig:\nbefore=%q\nafter =%q", dcBefore, dcAfter)
	}
	if ifAfter != ifBefore {
		t.Errorf("EvaluateSTP/CompareBridgeID 不应改写 Interfaces:\nbefore=%q\nafter =%q", ifBefore, ifAfter)
	}
	// 不应新增 STP 运行态键（如 stp:enabled 等由命令处理器维护的键不得被纯函数写入）。
	if _, ok := s.DeviceConfig[stpKey("enabled")]; ok {
		t.Errorf("纯函数不应写入 stp:enabled 等运行态键")
	}
}

// TestP2STPQA_AC6UndoSTPClearsKeys AC6：
// undo stp 清理全部 stp:* / interface:*:stp:* 键后，display stp 复现 STP: Disabled。
func TestP2STPQA_AC6UndoSTPClearsKeys(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	// 制造系统级 + 接口级 stp 配置。
	runOn(s, topology.DeviceSwitch, "stp mode rstp")
	runOn(s, topology.DeviceSwitch, "stp priority 4096")
	runOn(s, topology.DeviceSwitch, "stp bpdu-protection")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "stp edged-port enable")
	runOn(s, topology.DeviceSwitch, "quit")

	// 确认配置已写入。
	if s.DeviceConfig[stpKey("mode")] != "rstp" {
		t.Fatalf("前置：stp:mode 应为 rstp, got %q", s.DeviceConfig[stpKey("mode")])
	}
	if s.DeviceConfig[stpIfaceKey("GigabitEthernet0/0/1", "edged-port")] != "enable" {
		t.Fatalf("前置：接口级 stp 键应已写入")
	}

	// 系统视图执行 undo stp。
	if out := runOn(s, topology.DeviceSwitch, "undo stp"); strings.Contains(out, "Error") {
		t.Fatalf("undo stp 应成功, got: %q", out)
	}
	// 验证清理：除保活禁用键 stp:enabled=false 外，不应残留任何 stp:* / interface:*:stp:* 键。
	for k := range s.DeviceConfig {
		if k == stpKey("enabled") {
			if s.DeviceConfig[k] != "false" {
				t.Errorf("undo stp 应置 stp:enabled=false, got %q", s.DeviceConfig[k])
			}
			continue
		}
		if strings.HasPrefix(k, "stp:") {
			t.Errorf("undo stp 后不应残留 stp:* 键: %s=%q", k, s.DeviceConfig[k])
		}
		if strings.HasPrefix(k, "interface:") && strings.Contains(k, ":stp:") {
			t.Errorf("undo stp 后不应残留接口级 stp 键: %s=%q", k, s.DeviceConfig[k])
		}
	}
	// display stp 复现 STP: Disabled。
	disabled := runOn(s, topology.DeviceSwitch, "display stp")
	if !strings.Contains(disabled, "STP: Disabled") {
		t.Errorf("undo stp 后 display stp 应显示 'STP: Disabled', got: %q", disabled)
	}
	// 禁用态下 display current-configuration 不应再出现系统级 stp 配置块。
	cur := runOn(s, topology.DeviceSwitch, "display current-configuration")
	if strings.Contains(cur, "stp mode") || strings.Contains(cur, "stp priority") ||
		strings.Contains(cur, "stp bpdu-protection") {
		t.Errorf("undo stp 后 current-configuration 不应出现 stp 配置块, got: %q", cur)
	}
}

// TestP2STPQA_AC6CapabilityRejection AC6：
// 非 switchDevices() 设备（如 Router）执行系统级 stp 被能力拒绝。
func TestP2STPQA_AC6CapabilityRejection(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DeviceRouter, topology.DevicePC} {
		s := NewCLIStateWithType(dt)
		runOn(s, dt, "system-view")
		// 系统视图 stp 命令应被能力拒绝。
		if out := runOn(s, dt, "stp mode mstp"); !strings.Contains(out, "not supported") {
			t.Errorf("%s 应拒绝 stp（能力门禁）, got: %q", dt, out)
		}
		// region 视图进入同样拒绝。
		if out := runOn(s, dt, "stp region-configuration"); !strings.Contains(out, "not supported") {
			t.Errorf("%s 应拒绝 stp region-configuration（能力门禁）, got: %q", dt, out)
		}
	}
}

// TestP2STPQA_P1ProtectionsAndBridgeAddress P1：
// bpdu/root/loop/tc-protection(含 interval) 与 bridge-address 配置持久化 + display/current-configuration 展示。
func TestP2STPQA_P1ProtectionsAndBridgeAddress(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")

	// 四项 protections + tc interval。
	runOn(s, topology.DeviceSwitch, "stp bpdu-protection")
	runOn(s, topology.DeviceSwitch, "stp root-protection")
	runOn(s, topology.DeviceSwitch, "stp loop-protection")
	runOn(s, topology.DeviceSwitch, "stp tc-protection interval 7")

	// bridge-address。
	runOn(s, topology.DeviceSwitch, "stp bridge-address 4c1f-cc00-1111")

	// 持久化键校验。
	wantKeys := map[string]string{
		stpKey("bpdu-protection"):       "enable",
		stpKey("root-protection"):       "enable",
		stpKey("loop-protection"):       "enable",
		stpKey("tc-protection"):         "enable",
		stpKey("tc-protection-interval"): "7",
		stpKey("bridge-address"):        "4c1f-cc00-1111",
	}
	for k, v := range wantKeys {
		if s.DeviceConfig[k] != v {
			t.Errorf("%s 应=%q, got %q", k, v, s.DeviceConfig[k])
		}
	}

	// display stp 展示 bridge-address 与根桥 MAC；BPDU-Protection 在 interface 详情中展示。
	def := runOn(s, topology.DeviceSwitch, "display stp")
	if !strings.Contains(def, "4c1f-cc00-1111") {
		t.Errorf("display stp 应展示配置的 bridge-address, got: %q", def)
	}
	ifc := runOn(s, topology.DeviceSwitch, "display stp interface GigabitEthernet0/0/1")
	if !strings.Contains(ifc, "BPDU-Protection") || !strings.Contains(ifc, "Enabled") {
		t.Errorf("display stp interface 应展示 BPDU-Protection: Enabled, got: %q", ifc)
	}
	// 默认（未配 bpdu-protection）应显示 Disabled——独立干净状态验证。
	clean := NewCLIStateWithType(topology.DeviceSwitch)
	cleanIfc := runOn(clean, topology.DeviceSwitch, "display stp interface GigabitEthernet0/0/1")
	if !strings.Contains(cleanIfc, "BPDU-Protection") || !strings.Contains(cleanIfc, "Disabled") {
		t.Errorf("未配 bpdu-protection 时 display stp interface 应显示 BPDU-Protection: Disabled, got: %q", cleanIfc)
	}

	// display current-configuration 复现各项差异行（含 tc interval 显式值）。
	cur := runOn(s, topology.DeviceSwitch, "display current-configuration")
	for _, want := range []string{
		"stp bpdu-protection",
		"stp root-protection",
		"stp loop-protection",
		"stp tc-protection",
		"stp tc-protection interval 7",
		"stp bridge-address 4c1f-cc00-1111",
	} {
		if !strings.Contains(cur, want) {
			t.Errorf("current-configuration 应含 %q, got: %q", want, cur)
		}
	}

	// 持久化往返：save 内容经 reload 后 current-configuration 仍完整复现。
	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW1")
	reCur := runOn(reloaded, topology.DeviceSwitch, "display current-configuration")
	if !strings.Contains(reCur, "stp bridge-address 4c1f-cc00-1111") ||
		!strings.Contains(reCur, "stp tc-protection interval 7") {
		t.Errorf("reload 后 current-configuration 应完整复现 protections/bridge-address, got: %q", reCur)
	}
}

// TestP2STPQA_P1InstanceRootAndUndo P1：
// stp instance <id> root primary|secondary 配置 + undo stp root / undo stp instance <id> root 清理与展示。
func TestP2STPQA_P1InstanceRootAndUndo(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")

	// 系统级 root primary（stp:priority=0）。
	runOn(s, topology.DeviceSwitch, "stp root primary")
	if s.DeviceConfig[stpKey("priority")] != "0" {
		t.Errorf("stp root primary 应写 stp:priority=0, got %q", s.DeviceConfig[stpKey("priority")])
	}

	// 实例级 root primary/secondary。
	runOn(s, topology.DeviceSwitch, "stp instance 1 root primary")
	runOn(s, topology.DeviceSwitch, "stp instance 2 root secondary")
	want := map[string]string{
		stpKey("instance:1:priority"): "0",
		stpKey("instance:1:root"):     "primary",
		stpKey("instance:2:priority"): "4096",
		stpKey("instance:2:root"):     "secondary",
	}
	for k, v := range want {
		if s.DeviceConfig[k] != v {
			t.Errorf("%s 应=%q, got %q", k, v, s.DeviceConfig[k])
		}
	}

	// current-configuration 展示 root 差异行。
	cur := runOn(s, topology.DeviceSwitch, "display current-configuration")
	for _, want := range []string{
		"stp root primary",
		"stp instance 1 root primary",
		"stp instance 2 root secondary",
	} {
		if !strings.Contains(cur, want) {
			t.Errorf("current-configuration 应含 %q, got: %q", want, cur)
		}
	}

	// undo stp root：系统级 priority 清除（回到默认 32768），root primary 行消失。
	if out := runOn(s, topology.DeviceSwitch, "undo stp root"); strings.Contains(out, "Error") {
		t.Fatalf("undo stp root 应成功, got: %q", out)
	}
	if _, ok := s.DeviceConfig[stpKey("priority")]; ok {
		t.Errorf("undo stp root 后 stp:priority 应被清除, got %q", s.DeviceConfig[stpKey("priority")])
	}
	curAfterRoot := runOn(s, topology.DeviceSwitch, "display current-configuration")
	if strings.Contains(curAfterRoot, "stp root primary") {
		t.Errorf("undo stp root 后不应再有 'stp root primary' 行, got: %q", curAfterRoot)
	}
	// 实例级 root 仍保留（undo stp root 只清系统级）。
	if !strings.Contains(curAfterRoot, "stp instance 1 root primary") {
		t.Errorf("undo stp root 不应影响实例级 root, got: %q", curAfterRoot)
	}

	// undo stp instance 1 root：清除实例 1 的 priority/root 键。
	if out := runOn(s, topology.DeviceSwitch, "undo stp instance 1 root"); strings.Contains(out, "Error") {
		t.Fatalf("undo stp instance 1 root 应成功, got: %q", out)
	}
	if _, ok := s.DeviceConfig[stpKey("instance:1:priority")]; ok {
		t.Errorf("undo stp instance 1 root 后 stp:instance:1:priority 应被清除")
	}
	if _, ok := s.DeviceConfig[stpKey("instance:1:root")]; ok {
		t.Errorf("undo stp instance 1 root 后 stp:instance:1:root 应被清除")
	}
	curAfterInst := runOn(s, topology.DeviceSwitch, "display current-configuration")
	if strings.Contains(curAfterInst, "stp instance 1 root") {
		t.Errorf("undo stp instance 1 root 后不应再有实例 1 root 行, got: %q", curAfterInst)
	}
	if !strings.Contains(curAfterInst, "stp instance 2 root secondary") {
		t.Errorf("undo stp instance 1 root 不应影响实例 2 root, got: %q", curAfterInst)
	}
}
