package cli

// stp_eval_test.go —— STP/RSTP/MSTP 纯函数评估器单测（无副作用、不依赖引擎/网络）。
//
// 覆盖 docs/p2-stp-prd.md / design.md 的评估器契约：
//   - CompareBridgeID「小者胜」（拍板 #2 更正：同优先级比 MAC 小者胜）；
//   - EvaluateSTP 本地静态根桥假设（IsRoot=true、Root=本桥、PathCost=0、无副作用）；
//   - 缺省值（mode=mstp / pathcost-standard=dot1t / priority=32768 / STP 默认开启）；
//   - collectSTPInstances（恒含 0，region-active 后才纳入 id>0）；
//   - defaultPortCost 三档；validPriority/validCost/validPortPriority/validInstanceID 边界；
//   - collectSTPPorts 端口角色启发式（Down/edge/candidate）；
//   - stpSimNote 诚实占位含「模拟生成树」。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// newSTPTestState 构造干净交换机 CLIState（STP 默认开启）。
func newSTPTestState() *CLIState {
	return NewCLIStateWithType(topology.DeviceSwitch)
}

// TestCompareBridgeIDSmallerWins 验证桥 ID 比较「小者胜」：
// Priority 小者胜；同 Priority 比 Address 小者胜（VRP MAC 去短横后字典序）；
// 完全相等返回 0；Priority 大者返回负值。
func TestCompareBridgeIDSmallerWins(t *testing.T) {
	// Priority 小者胜（a 的优先级 4096 < b 的 32768）。
	a := BridgeID{Priority: 4096, Address: "4c1f-cc00-0001"}
	b := BridgeID{Priority: 32768, Address: "4c1f-cc00-0002"}
	if cmp := CompareBridgeID(a, b); cmp <= 0 {
		t.Errorf("priority 小者应胜 (cmp>0), got %d", cmp)
	}
	// 同 Priority 比 Address 小者胜（0001 < 0002）。
	c := BridgeID{Priority: 32768, Address: "4c1f-cc00-0001"}
	d := BridgeID{Priority: 32768, Address: "4c1f-cc00-0002"}
	if cmp := CompareBridgeID(c, d); cmp <= 0 {
		t.Errorf("同优先级 MAC 小者应胜 (cmp>0), got %d", cmp)
	}
	// 完全相等 → 0。
	e := BridgeID{Priority: 32768, Address: "4c1f-cc00-0002"}
	if cmp := CompareBridgeID(d, e); cmp != 0 {
		t.Errorf("完全相等应返回 0, got %d", cmp)
	}
	// Priority 大者应为负（b vs a，b 优先级更大 → a 当 first 时 cmp<0）。
	if cmp := CompareBridgeID(b, a); cmp >= 0 {
		t.Errorf("priority 大者应负 (cmp<0), got %d", cmp)
	}
}

// TestEvaluateSTPRootIsSelf 验证 lite 引擎下本桥恒为根桥（本地静态假设）。
func TestEvaluateSTPRootIsSelf(t *testing.T) {
	s := newSTPTestState()
	s.DeviceName = "SW1"
	inst := EvaluateSTP(s, 0)
	if !inst.IsRoot {
		t.Errorf("lite 引擎下本桥应恒为根桥 IsRoot=true")
	}
	if inst.RootPriority != inst.BridgePriority {
		t.Errorf("RootPriority 应=本桥 BridgePriority, got %d vs %d", inst.RootPriority, inst.BridgePriority)
	}
	if inst.RootAddress != inst.BridgeAddress {
		t.Errorf("RootAddress 应=本桥 BridgeAddress")
	}
	if inst.RootPathCost != 0 {
		t.Errorf("RootPathCost 应=0, got %d", inst.RootPathCost)
	}
	if inst.BridgePriority != stpPriDefault {
		t.Errorf("默认桥优先级应=%d, got %d", stpPriDefault, inst.BridgePriority)
	}
	// 未配 bridge-address 时，默认桥 MAC 由设备名派生。
	if inst.BridgeAddress != deriveMACFromName("SW1") {
		t.Errorf("默认桥 MAC 应由设备名派生, got %s", inst.BridgeAddress)
	}
}

// TestEvaluateSTPNoSideEffects 验证 EvaluateSTP 纯函数不修改 DeviceConfig。
func TestEvaluateSTPNoSideEffects(t *testing.T) {
	s := newSTPTestState()
	s.DeviceConfig["stp:mode"] = "rstp"
	s.DeviceConfig["stp:priority"] = "4096"
	before := len(s.DeviceConfig)
	_ = EvaluateSTP(s, 0)
	_ = EvaluateSTP(s, 1)
	if len(s.DeviceConfig) != before {
		t.Errorf("EvaluateSTP 不应修改 DeviceConfig (before=%d after=%d)", before, len(s.DeviceConfig))
	}
	if s.DeviceConfig["stp:mode"] != "rstp" || s.DeviceConfig["stp:priority"] != "4096" {
		t.Errorf("EvaluateSTP 不应改写既有 DeviceConfig 键")
	}
}

// TestSTPDefaults 验证缺省值（mode=mstp / pathcost-standard=dot1t / STP 默认开启）。
func TestSTPDefaults(t *testing.T) {
	s := newSTPTestState()
	if stpMode(s) != stpModeDefault {
		t.Errorf("默认模式应=%s, got %s", stpModeDefault, stpMode(s))
	}
	if stpPathCostStd(s) != stpPCStdDefault {
		t.Errorf("默认 pathcost-standard 应=%s, got %s", stpPCStdDefault, stpPathCostStd(s))
	}
	if !isSTPEnabled(s) {
		t.Errorf("STP 默认应开启")
	}
	s.DeviceConfig["stp:enabled"] = "false"
	if isSTPEnabled(s) {
		t.Errorf("stp:enabled=false 时应判定为关闭")
	}
}

// TestCollectSTPInstances 验证实例集合：恒含 CIST(0)，region-active 后才纳入 id>0。
func TestCollectSTPInstances(t *testing.T) {
	s := newSTPTestState()
	// 无 region-active 时仅含 CIST(0)。
	if ids := collectSTPInstances(s); len(ids) != 1 || ids[0] != 0 {
		t.Errorf("无 region 时实例应仅 [0], got %v", ids)
	}
	// 配置实例 VLAN 但未 active，仍不应出现 id>0。
	s.DeviceConfig["stp:instance:1:vlans"] = "2 to 10"
	if ids := collectSTPInstances(s); len(ids) != 1 || ids[0] != 0 {
		t.Errorf("未 active 时实例不应含 1, got %v", ids)
	}
	// active 后出现 1。
	s.DeviceConfig["stp:region-active"] = "true"
	ids := collectSTPInstances(s)
	if len(ids) != 2 || ids[0] != 0 || ids[1] != 1 {
		t.Errorf("active 后实例应 [0,1], got %v", ids)
	}
}

// TestDefaultPortCost 验证三种 pathcost-standard 下的缺省端口开销。
func TestDefaultPortCost(t *testing.T) {
	if defaultPortCost("dot1t") != stpDefCostDot1t {
		t.Errorf("dot1t 默认开销应=%d", stpDefCostDot1t)
	}
	if defaultPortCost("dot1d-1998") != stpDefCostDot1d1998 {
		t.Errorf("dot1d-1998 默认开销应=%d", stpDefCostDot1d1998)
	}
	if defaultPortCost("legacy") != stpDefCostLegacy {
		t.Errorf("legacy 默认开销应=%d", stpDefCostLegacy)
	}
}

// TestSTPValidators 验证各校验器边界（合法/非法）。
func TestSTPValidators(t *testing.T) {
	// validPriority：0/4096/32768/61440 合法；-1/1/4097/61441 非法。
	for _, v := range []int{0, 4096, 32768, 61440} {
		if ok, _ := validPriority(v); !ok {
			t.Errorf("validPriority(%d) 应合法", v)
		}
	}
	for _, v := range []int{-1, 1, 4097, 61441} {
		if ok, _ := validPriority(v); ok {
			t.Errorf("validPriority(%d) 应非法", v)
		}
	}
	// validPortPriority：0/16/240 合法；1/241 非法。
	for _, v := range []int{0, 16, 240} {
		if ok, _ := validPortPriority(v); !ok {
			t.Errorf("validPortPriority(%d) 应合法", v)
		}
	}
	for _, v := range []int{1, 241} {
		if ok, _ := validPortPriority(v); ok {
			t.Errorf("validPortPriority(%d) 应非法", v)
		}
	}
	// validCost：cost 0 非法；dot1t 上界 200000000 合法、超界非法；legacy 上界 200000；dot1d-1998 上界 65535。
	if ok, _ := validCost(0, "dot1t"); ok {
		t.Errorf("cost 0 应非法")
	}
	if ok, _ := validCost(200000000, "dot1t"); !ok {
		t.Errorf("dot1t cost 上界 200000000 应合法")
	}
	if ok, _ := validCost(200000001, "dot1t"); ok {
		t.Errorf("dot1t cost 超上界应非法")
	}
	if ok, _ := validCost(200000, "legacy"); !ok {
		t.Errorf("legacy cost 上界 200000 应合法")
	}
	if ok, _ := validCost(65535, "dot1d-1998"); !ok {
		t.Errorf("dot1d-1998 cost 上界 65535 应合法")
	}
	// validInstanceID：0/4094 合法；-1/4095 非法。
	for _, v := range []int{0, 4094} {
		if ok, _ := validInstanceID(v); !ok {
			t.Errorf("validInstanceID(%d) 应合法", v)
		}
	}
	for _, v := range []int{-1, 4095} {
		if ok, _ := validInstanceID(v); ok {
			t.Errorf("validInstanceID(%d) 应非法", v)
		}
	}
}

// TestCollectSTPPortsHeuristic 验证端口角色启发式（Down/edge/candidate）。
func TestCollectSTPPortsHeuristic(t *testing.T) {
	s := newSTPTestState()
	s.Interfaces = map[string]*InterfaceConfig{
		"GigabitEthernet0/0/1": {Name: "GigabitEthernet0/0/1", Status: "Up"},
		"GigabitEthernet0/0/2": {Name: "GigabitEthernet0/0/2", Status: "Up"},
		"GigabitEthernet0/0/3": {Name: "GigabitEthernet0/0/3", Status: "Down"},
	}
	// 标记 0/0/2 为边缘端口。
	s.DeviceConfig["interface:GigabitEthernet0/0/2:stp:edged-port"] = "enable"
	ports := collectSTPPorts(s, 0)
	if len(ports) != 3 {
		t.Fatalf("应派生 3 个端口, got %d", len(ports))
	}
	byName := map[string]STPPortResult{}
	for _, p := range ports {
		byName[p.Interface] = p
	}
	// Down 端口 → -- / DOWN。
	if byName["GigabitEthernet0/0/3"].Role != "--" || byName["GigabitEthernet0/0/3"].State != "DOWN" {
		t.Errorf("Down 端口应为 --/DOWN, got %s/%s",
			byName["GigabitEthernet0/0/3"].Role, byName["GigabitEthernet0/0/3"].State)
	}
	// 边缘端口 → DESI / FORWARDING。
	if byName["GigabitEthernet0/0/2"].Role != "DESI" || byName["GigabitEthernet0/0/2"].State != "FORWARDING" {
		t.Errorf("边缘端口应为 DESI/FORWARDING, got %s/%s",
			byName["GigabitEthernet0/0/2"].Role, byName["GigabitEthernet0/0/2"].State)
	}
	// 唯一 active 非边缘端口（0/0/1）→ ROOT / FORWARDING。
	if byName["GigabitEthernet0/0/1"].Role != "ROOT" || byName["GigabitEthernet0/0/1"].State != "FORWARDING" {
		t.Errorf("唯一候选端口应为 ROOT/FORWARDING, got %s/%s",
			byName["GigabitEthernet0/0/1"].Role, byName["GigabitEthernet0/0/1"].State)
	}
}

// TestSTPSimNote 验证诚实占位注记含「模拟生成树」。
func TestSTPSimNote(t *testing.T) {
	note := stpSimNote()
	if !strings.Contains(note, "模拟生成树") {
		t.Errorf("stpSimNote 应含 '模拟生成树', got %q", note)
	}
}
