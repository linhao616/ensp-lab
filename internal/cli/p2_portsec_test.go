package cli

// p2_portsec_test.go —— 端口安全增量集成测试（T06，对齐 AC1/AC2/AC3）。
//
// 通过 ExecuteCommandOn + ParseCommand 驱动命令分发，覆盖：
//   AC1 命令接受与拒错（范围校验 / 能力拒绝 / 接口视图守卫 / 手动粘滞绑定）；
//   AC2 配置持久化往返（save → reload → display 复现）；
//   AC3 display port-security 列头/单端口详情 与 display mac-address 的 Type 标签渲染。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// enterIface 进入指定设备类型的系统视图并切入接口视图，返回已就绪的 CLIState。
func enterIface(dt topology.DeviceType, iface string) *CLIState {
	s := NewCLIStateWithType(dt)
	runOn(s, dt, "system-view")
	runOn(s, dt, "interface "+iface)
	return s
}

// TestAC1PortSecurityCommandsAccepted 在交换机接口视图执行全部 port-security 子命令返回成功。
func TestAC1PortSecurityCommandsAccepted(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	cases := []struct {
		cmd string
		key string
		val string
	}{
		{"port-security enable", "interface:GigabitEthernet0/0/1:port-security", "enable"},
		{"port-security max-mac-num 2", "interface:GigabitEthernet0/0/1:port-security-max-mac", "2"},
		{"port-security protect-action restrict", "interface:GigabitEthernet0/0/1:port-security-protect-action", "restrict"},
		{"port-security aging-time 15", "interface:GigabitEthernet0/0/1:port-security-aging-time", "15"},
		{"port-security mac-address sticky 00e0-fc12-3456 vlan 10", "interface:GigabitEthernet0/0/1:port-security-sticky-mac:00e0-fc12-3456", "10"},
	}
	for _, c := range cases {
		out := runOn(s, topology.DeviceSwitch, c.cmd)
		if strings.Contains(out, "Error") {
			t.Errorf("%q should succeed, got: %q", c.cmd, out)
		}
		if s.DeviceConfig[c.key] != c.val {
			t.Errorf("%q should write %s=%q, got %q", c.cmd, c.key, c.val, s.DeviceConfig[c.key])
		}
	}
}

// TestAC1ProtectActionStickyFlag 验证粘滞开启（无参）与三种 protect-action 合法值。
func TestAC1ProtectActionStickyFlag(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "port-security mac-address sticky"); !strings.Contains(out, "sticky MAC enabled") {
		t.Errorf("sticky flag should enable, got: %q", out)
	}
	for _, act := range []string{"protect", "restrict", "shutdown"} {
		out := runOn(s, topology.DeviceSwitch, "port-security protect-action "+act)
		if strings.Contains(out, "Error") {
			t.Errorf("protect-action %s should be valid, got: %q", act, out)
		}
	}
}

// TestAC1RangeRejection 校验非法范围 / 非法取值返回 Error。
func TestAC1RangeRejection(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	rejects := []string{
		"port-security max-mac-num 0",     // 0 非法
		"port-security max-mac-num 4097",  // 超界
		"port-security max-mac-num abc",   // 非数字
		"port-security protect-action foo", // 非法取值
		"port-security aging-time 0",      // 0 非法
		"port-security aging-time 1441",   // 超界
		"port-security aging-time xyz",    // 非数字
		"port-security mac-address sticky zzz vlan 10", // 非法 MAC
	}
	for _, c := range rejects {
		out := runOn(s, topology.DeviceSwitch, c)
		if !strings.Contains(out, "Error") {
			t.Errorf("%q should be rejected with Error, got: %q", c, out)
		}
	}
}

// TestAC1InterfaceViewGuard 顶层 port-security 不在接口视图应被守卫拒绝。
func TestAC1InterfaceViewGuard(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	out := runOn(s, topology.DeviceSwitch, "port-security enable")
	if !strings.Contains(out, "interface view") {
		t.Errorf("port-security outside interface view should be rejected, got: %q", out)
	}
}

// TestAC1RouterNotSupported 路由器执行 port-security 应被能力矩阵拒绝。
func TestAC1RouterNotSupported(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	out := runOn(s, topology.DeviceRouter, "port-security enable")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("port-security on router should be rejected, got: %q", out)
	}
}

// TestAC1SimulateNotSupportedOnRouter simulate frame 在非交换机（路由器）应回 not supported。
func TestAC1SimulateNotSupportedOnRouter(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	out := runOn(s, topology.DeviceRouter, "simulate frame 00e0-fc12-3456")
	if !strings.Contains(out, "not supported") && !strings.Contains(out, "unknown command") {
		t.Errorf("simulate on router should be rejected, got: %q", out)
	}
}

// TestAC1SimulateInterfaceViewGuard simulate frame 须处于接口视图。
func TestAC1SimulateInterfaceViewGuard(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	out := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-3456")
	if !strings.Contains(out, "interface view") {
		t.Errorf("simulate outside interface view should be rejected, got: %q", out)
	}
}

// TestAC2PersistenceRoundTrip 配置经 save→reload 后 display port-security 复现。
func TestAC2PersistenceRoundTrip(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 2")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")
	runOn(s, topology.DeviceSwitch, "port-security aging-time 15")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky 00e0-fc12-3456 vlan 10")

	// save（含 Y/N 确认）并序列化。
	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")
	cfg := s.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")
	wantKeys := []string{
		"interface:GigabitEthernet0/0/1:port-security",
		"interface:GigabitEthernet0/0/1:port-security-max-mac",
		"interface:GigabitEthernet0/0/1:port-security-protect-action",
		"interface:GigabitEthernet0/0/1:port-security-aging-time",
		"interface:GigabitEthernet0/0/1:port-security-sticky-mac:00e0-fc12-3456",
	}
	for _, k := range wantKeys {
		if _, ok := reloaded.DeviceConfig[k]; !ok {
			t.Errorf("reload should preserve key %s, got %v", k, reloaded.DeviceConfig)
		}
	}

	out := runOn(reloaded, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	if !strings.Contains(out, "enable") {
		t.Errorf("reloaded display should show enable, got: %q", out)
	}
	if !strings.Contains(out, "restrict") {
		t.Errorf("reloaded display should show restrict, got: %q", out)
	}
	if !strings.Contains(out, "15") {
		t.Errorf("reloaded display should show aging 15, got: %q", out)
	}
}

// TestAC3DisplayPortSecurityTable display port-security 全接口表含列头。
func TestAC3DisplayPortSecurityTable(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 2")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")
	out := runOn(s, topology.DeviceSwitch, "display port-security")
	for _, col := range []string{"Interface", "Status", "Max MAC", "Protect-Action", "Sticky", "Aging(min)", "Violations"} {
		if !strings.Contains(out, col) {
			t.Errorf("display port-security should contain column %q, got: %q", col, out)
		}
	}
	if !strings.Contains(out, "GigabitEthernet0/0/1") {
		t.Errorf("display port-security should list the interface, got: %q", out)
	}
}

// TestAC3DisplayPortSecurityDetail 单端口详情含运行态与已学 MAC 区块。
func TestAC3DisplayPortSecurityDetail(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky")
	out := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	for _, field := range []string{"Status", "Max MAC", "Protect-Action", "Sticky", "Aging(min)", "Violations", "Error-Down", "Learned Secure MACs"} {
		if !strings.Contains(out, field) {
			t.Errorf("detail should contain %q, got: %q", field, out)
		}
	}
	// 缺省 protect-action 应标注 (default)。
	if !strings.Contains(out, "restrict (default)") {
		t.Errorf("detail should show restrict (default), got: %q", out)
	}
}

// TestAC3MacAddressTypeLabels display mac-address 的 Type 列正确渲染各标签。
func TestAC3MacAddressTypeLabels(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceL3Switch)
	// 已学安全/粘滞条目由 simulate 写入，此处手工插入验证渲染。
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00e0-fc12-aaaa", VLAN: 10, Interface: "GigabitEthernet0/0/1", Type: "sticky"})
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00e0-fc12-bbbb", VLAN: 20, Interface: "GigabitEthernet0/0/2", Type: "security"})
	out := runOn(s, topology.DeviceL3Switch, "display mac-address")
	// 注：种子 Static 条目按主理人拍板 O2「保留 Static 不动」，故显示大写 "Static"；
	// 运行时学习的粘滞/安全/动态条目为小写 sticky/security/dynamic。
	for _, label := range []string{"sticky", "security", "Static", "dynamic"} {
		if !strings.Contains(out, label) {
			t.Errorf("display mac-address should render Type label %q, got: %q", label, out)
		}
	}
}
