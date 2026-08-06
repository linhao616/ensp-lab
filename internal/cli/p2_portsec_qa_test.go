package cli

// p2_portsec_qa_test.go —— 端口安全端到端 QA 验收（T07，对齐 AC4/AC6 + 粘滞持久化）。
//
// 通过 simulate frame 触发端口安全准入判定，验证三种 protect-action 效果、粘滞学习、
// lite 诚实占位注记，以及粘滞 MAC 随 save/reload 回填（运行态 error-down/violations 归零）。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestAC4ProtectActionProtect protect-action=protect：第 2 个非授权 MAC 丢弃且无告警（violations=0）。
func TestAC4ProtectActionProtect(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action protect")

	o1 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	if !strings.Contains(o1, "ADMITTED") {
		t.Fatalf("first frame should be admitted, got: %q", o1)
	}
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	if !strings.Contains(o2, "DROPPED") {
		t.Fatalf("second frame should be dropped, got: %q", o2)
	}
	if strings.Contains(o2, "violation") {
		t.Errorf("protect must not log violation, got: %q", o2)
	}
	detail := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(detail, "Violations              : 0") {
		t.Errorf("protect should leave violations at 0, got: %q", detail)
	}
}

// TestAC4ProtectActionRestrict protect-action=restrict：丢弃 + violation 计数 +1。
func TestAC4ProtectActionRestrict(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")

	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	if !strings.Contains(o2, "DROPPED") || !strings.Contains(o2, "restrict") {
		t.Fatalf("restrict should drop+restrict, got: %q", o2)
	}
	if !strings.Contains(o2, "violation logged") {
		t.Errorf("restrict should log violation, got: %q", o2)
	}
	detail := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(detail, "Violations              : 1") {
		t.Errorf("restrict should increment violations to 1, got: %q", detail)
	}
}

// TestAC4ProtectActionShutdown protect-action=shutdown：error-down 置位且后续帧被拒。
func TestAC4ProtectActionShutdown(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action shutdown")

	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	if !strings.Contains(o2, "PORT ERROR-DOWN") || !strings.Contains(o2, "shutdown") {
		t.Fatalf("shutdown should error-down, got: %q", o2)
	}
	detail := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(detail, "Error-Down              : yes") {
		t.Errorf("error-down should show yes, got: %q", detail)
	}
	// 后续帧在 error-down 端口被拒。
	o3 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0003")
	if strings.Contains(o3, "ADMITTED") {
		t.Errorf("frames after error-down must be dropped, got: %q", o3)
	}
}

// TestAC4StickyLearning 粘滞开启时合法/粘滞 MAC 注入 → 准入且进入 MACTable(Type=sticky)。
func TestAC4StickyLearning(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 5")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky") // 自动粘滞标志

	o := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	if !strings.Contains(o, "ADMITTED") || !strings.Contains(o, "sticky MAC learned") {
		t.Fatalf("sticky-enabled learn should admit+sticky, got: %q", o)
	}
	macOut := runOn(s, topology.DeviceSwitch, "display mac-address")
	if !strings.Contains(macOut, "00e0-fc12-0001") || !strings.Contains(macOut, "sticky") {
		t.Errorf("learned sticky MAC should appear in display mac-address, got: %q", macOut)
	}
}

// TestAC6LiteSimNote lite 引擎下 simulate frame 输出带诚实占位注记。
func TestAC6LiteSimNote(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	o := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	if !strings.Contains(o, "模拟帧注入（lite 引擎），非内核级真实 MAC 学习") {
		t.Errorf("lite sim note missing, got: %q", o)
	}
}

// TestStickyPersistenceReload 粘滞 MAC 经 save/reload 回填，运行态 error-down/violations 归零。
func TestStickyPersistenceReload(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 5")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky") // 自动粘滞
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")
	// 触发一次违规（制造运行态），再学习一条粘滞 MAC。
	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-9999") // 学习第1条(占1槽)
	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-8888") // 第2条，但 max=5，仍学习
	// 制造违规：先把槽位填满再注入新 MAC
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-7777") // 超 1 槽 → restrict violation
	before := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(before, "Violations              : 1") {
		t.Fatalf("pre-reload should have 1 violation, got: %q", before)
	}

	// save + reload
	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")
	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")

	// 粘滞学习 MAC 仍可见
	macOut := runOn(reloaded, topology.DeviceSwitch, "display mac-address")
	if !strings.Contains(macOut, "sticky") {
		t.Errorf("reloaded display mac-address should still show sticky entries, got: %q", macOut)
	}
	// 运行态归零
	detail := runOn(reloaded, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(detail, "Violations              : 0") {
		t.Errorf("reloaded violations should be reset to 0, got: %q", detail)
	}
	if !strings.Contains(detail, "Error-Down              : no") {
		t.Errorf("reloaded error-down should be reset to no, got: %q", detail)
	}
	// 粘滞学习键保留
	if _, ok := reloaded.DeviceConfig["interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-9999"]; !ok {
		t.Errorf("reloaded should preserve sticky-learned key, got %v", reloaded.DeviceConfig)
	}
}
