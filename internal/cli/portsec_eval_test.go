package cli

// portsec_eval_test.go —— EvaluatePortSecurity 纯函数行为矩阵（T06，对齐 AC5）。
//
// 直接构造 CLIState 并写入 DeviceConfig 键，驱动纯函数评估；不依赖命令分发/引擎。
// 重点验证：未启用→admit、授权/粘滞→admit、超 max-mac 三种 protect-action、
// 合法新 MAC 返回 Learned、以及纯函数「无副作用」（连续两次一致且不改写 state）。

import (
	"strconv"
	"testing"

	"ensp-lab/internal/topology"
)

// psSetup 构造一台交换机并在指定接口启用端口安全、设 max-mac。
func psSetup(iface string, maxMAC int) *CLIState {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	s.DeviceConfig[psKey(iface, psKeyEnabled)] = "enable"
	if maxMAC > 0 {
		s.DeviceConfig[psKey(iface, psKeyMaxMAC)] = strconv.Itoa(maxMAC)
	}
	return s
}

// resultEqual 比较两次 PortSecurityResult 是否等价（含 Learned MAC）。
func resultEqual(a, b PortSecurityResult) bool {
	if a.Admit != b.Admit {
		return false
	}
	if (a.Violation == nil) != (b.Violation == nil) {
		return false
	}
	if a.Violation != nil && b.Violation != nil {
		if a.Violation.Action != b.Violation.Action || a.Violation.ErrorDown != b.Violation.ErrorDown {
			return false
		}
	}
	if (a.Learned == nil) != (b.Learned == nil) {
		return false
	}
	if a.Learned != nil && b.Learned != nil {
		if a.Learned.MAC != b.Learned.MAC || a.Learned.Type != b.Learned.Type || a.Learned.Interface != b.Learned.Interface {
			return false
		}
	}
	return true
}

// TestEvaluatePortSecurityNotEnabled 未启用端口安全应直接准入（不介入 L2）。
func TestEvaluatePortSecurityNotEnabled(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	res := EvaluatePortSecurity(s, "GigabitEthernet0/0/1", Frame{SrcMAC: "00e0-fc12-3456"})
	if !res.Admit {
		t.Errorf("disabled port should admit, got %+v", res)
	}
	if res.Violation != nil || res.Learned != nil {
		t.Errorf("disabled port should have no violation/learned, got %+v", res)
	}
}

// TestEvaluatePortSecurityAuthorizedManualBinding 手动绑定 sticky-mac 视为授权，准入不学习。
func TestEvaluatePortSecurityAuthorizedManualBinding(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.DeviceConfig[psKey(iface, psKeyStickyMACPre)+"00e0-fc12-3456"] = "10"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00e0-fc12-3456"})
	if !res.Admit {
		t.Errorf("manual bound MAC should be admitted, got %+v", res)
	}
	if res.Learned != nil {
		t.Errorf("authorized MAC should not be learned, got %+v", res)
	}
}

// TestEvaluatePortSecurityAuthorizedLearned 已学 sticky/security 视为授权。
func TestEvaluatePortSecurityAuthorizedLearned(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00e0-fc12-3456", VLAN: 10, Interface: iface, Type: "sticky"})
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00e0-fc12-3456"})
	if !res.Admit || res.Learned != nil {
		t.Errorf("learned sticky should be admitted without learning, got %+v", res)
	}
}

// TestEvaluatePortSecurityLearnSecurity 未达上限且粘滞关闭 → 学习为 security 类型。
func TestEvaluatePortSecurityLearnSecurity(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 2)
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00e0-fc12-3456"})
	if !res.Admit || res.Learned == nil {
		t.Fatalf("below limit should admit+learn, got %+v", res)
	}
	if res.Learned.Type != "security" {
		t.Errorf("non-sticky learn should be 'security', got %q", res.Learned.Type)
	}
	if res.Learned.Interface != iface {
		t.Errorf("learned interface should be %s, got %q", iface, res.Learned.Interface)
	}
}

// TestEvaluatePortSecurityLearnSticky 粘滞开启 → 学习为 sticky 类型。
func TestEvaluatePortSecurityLearnSticky(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 2)
	s.DeviceConfig[psKey(iface, psKeySticky)] = "enable"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00e0-fc12-3456"})
	if !res.Admit || res.Learned == nil || res.Learned.Type != "sticky" {
		t.Errorf("sticky enabled should learn 'sticky', got %+v", res)
	}
}

// TestEvaluatePortSecurityViolationProtect 超上限且 protect-action=protect → 丢弃不记录。
func TestEvaluatePortSecurityViolationProtect(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00aa-aa00-0001", VLAN: 1, Interface: iface, Type: "security"})
	s.DeviceConfig[psKey(iface, psKeyProtect)] = "protect"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00bb-bb00-0002"})
	if res.Admit {
		t.Fatalf("over limit should drop, got %+v", res)
	}
	if res.Violation == nil || res.Violation.Action != "protect" || res.Violation.ErrorDown {
		t.Errorf("protect violation should be {protect,false}, got %+v", res.Violation)
	}
}

// TestEvaluatePortSecurityViolationRestrict 超上限且 restrict → 丢弃+violation。
func TestEvaluatePortSecurityViolationRestrict(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00aa-aa00-0001", VLAN: 1, Interface: iface, Type: "security"})
	s.DeviceConfig[psKey(iface, psKeyProtect)] = "restrict"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00bb-bb00-0002"})
	if res.Admit || res.Violation == nil || res.Violation.Action != "restrict" || res.Violation.ErrorDown {
		t.Errorf("restrict violation expected, got %+v", res)
	}
}

// TestEvaluatePortSecurityViolationShutdown 超上限且 shutdown → 丢弃+error-down。
func TestEvaluatePortSecurityViolationShutdown(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00aa-aa00-0001", VLAN: 1, Interface: iface, Type: "security"})
	s.DeviceConfig[psKey(iface, psKeyProtect)] = "shutdown"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00bb-bb00-0002"})
	if res.Admit || res.Violation == nil || res.Violation.Action != "shutdown" || !res.Violation.ErrorDown {
		t.Errorf("shutdown violation expected {shutdown,true}, got %+v", res.Violation)
	}
}

// TestEvaluatePortSecurityErrorDown 已 error-down → 直接丢弃，不再计数。
func TestEvaluatePortSecurityErrorDown(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 10)
	s.DeviceConfig[psKey(iface, psKeyErrorDown)] = "true"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00bb-bb00-0002"})
	if res.Admit {
		t.Errorf("error-down port should drop, got %+v", res)
	}
	if res.Violation != nil {
		t.Errorf("error-down drop should carry no violation (no counting), got %+v", res)
	}
}

// TestEvaluatePortSecurityManualBindingCounts O1：手动绑定计入 max-mac 占用。
// max=1 且仅 1 条手动绑定 → 新 MAC 触发违规。
func TestEvaluatePortSecurityManualBindingCounts(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 1)
	s.DeviceConfig[psKey(iface, psKeyStickyMACPre)+"00e0-fc12-3456"] = "10"
	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00bb-bb00-0002"})
	if res.Admit {
		t.Errorf("manual binding should occupy the only slot, new MAC must drop, got %+v", res)
	}
	if res.Violation == nil || res.Violation.Action != "restrict" {
		t.Errorf("default protect-action should be restrict, got %+v", res.Violation)
	}
}

// TestEvaluatePortSecurityPureNoSideEffect 验证纯函数无副作用：
// 连续两次调用结果一致，且不改写 MACTable / DeviceConfig。
func TestEvaluatePortSecurityPureNoSideEffect(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	s := psSetup(iface, 2)
	s.DeviceConfig[psKey(iface, psKeyStickyMACPre)+"00e0-fc12-3456"] = "10" // 1 条手动绑定，used=1<2 → 学习新 MAC

	macTableLenBefore := len(s.MACTable)
	dcKeysBefore := len(s.DeviceConfig)

	frame := Frame{SrcMAC: "00bb-bb00-0002", VLAN: 20}
	res1 := EvaluatePortSecurity(s, iface, frame)
	res2 := EvaluatePortSecurity(s, iface, frame)

	if !resultEqual(res1, res2) {
		t.Errorf("two consecutive calls should be identical: %+v vs %+v", res1, res2)
	}
	if len(s.MACTable) != macTableLenBefore {
		t.Errorf("pure function must not append MACTable: before=%d after=%d", macTableLenBefore, len(s.MACTable))
	}
	if len(s.DeviceConfig) != dcKeysBefore {
		t.Errorf("pure function must not write DeviceConfig: before=%d after=%d", dcKeysBefore, len(s.DeviceConfig))
	}
	if _, ok := s.DeviceConfig[psKey(iface, psKeyStickyLearnedPre)+"00bb-bb00-0002"]; ok {
		t.Errorf("pure function must not write sticky-learned key")
	}
	if !res1.Admit || res1.Learned == nil {
		t.Errorf("expected admit+learn, got %+v", res1)
	}
}
