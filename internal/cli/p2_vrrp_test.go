package cli

// p2_vrrp_test.go —— VRRP 增量集成测试（T05，对齐 AC1 / AC2 / AC3）。
//
// 通过 ExecuteCommandOn + ParseCommand 驱动命令分发，覆盖：
//   AC1 命令接受与拒错（vrid/priority/advertise 越界、非接口视图、能力拒绝、同网段校验）；
//   AC2 配置持久化往返（save → reload → display vrrp / display current-configuration 复现，
//       验证残桩 save/reload 丢配置缺陷已修复）；
//   AC3 display vrrp 列头/单组详情/brief/interface/vrid 渲染、current-configuration 输出 virtual-ip。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// setIfaceIP 在接口视图为指定接口配置 IP（供同网段校验使用）。
func setIfaceIP(s *CLIState, dt topology.DeviceType, iface, ip, mask string) {
	runOn(s, dt, "system-view")
	runOn(s, dt, "interface "+iface)
	runOn(s, dt, "ip address "+ip+" "+mask)
}

// TestAC1VRRPCommandsAccepted 在 Router 接口视图执行 VRRP 子命令均成功并写入 DeviceConfig 键。
func TestAC1VRRPCommandsAccepted(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")

	cases := []struct {
		cmd string
		key string
		val string
	}{
		{"vrrp vrid 1 virtual-ip 192.168.1.254", "interface:GigabitEthernet0/0/1:vrrp:1:virtual-ip", "192.168.1.254"},
		{"vrrp vrid 1 priority 120", "interface:GigabitEthernet0/0/1:vrrp:1:priority", "120"},
		{"vrrp vrid 1 preempt-mode disable", "interface:GigabitEthernet0/0/1:vrrp:1:preempt", "disable"},
		{"vrrp vrid 1 timer advertise 2", "interface:GigabitEthernet0/0/1:vrrp:1:advertise", "2"},
	}
	for _, c := range cases {
		out := runOn(s, topology.DeviceRouter, c.cmd)
		if strings.Contains(out, "Error") {
			t.Errorf("%q should succeed, got: %q", c.cmd, out)
		}
		if s.DeviceConfig[c.key] != c.val {
			t.Errorf("%q should write %s=%q, got %q", c.cmd, c.key, c.val, s.DeviceConfig[c.key])
		}
	}
}

// TestAC1VRRPRangeRejection 校验越界 / 非法取值返回 Error。
func TestAC1VRRPRangeRejection(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	rejects := []string{
		"vrrp vrid 0 virtual-ip 192.168.1.254",  // vrid 越界（最小 1）
		"vrrp vrid 256 virtual-ip 192.168.1.254", // vrid 越界（最大 255）
		"vrrp vrid 1 priority 0",                 // priority 越界（最小 1）
		"vrrp vrid 1 priority 255",               // priority 越界（最大 254，255 保留给 owner）
		"vrrp vrid 1 priority 300",               // priority 越界
		"vrrp vrid 1 timer advertise 0",         // advertise 越界（最小 1）
		"vrrp vrid 1 timer advertise 256",       // advertise 越界
		"vrrp vrid 1 virtual-ip not-an-ip",       // 非法 virtual-ip
	}
	for _, c := range rejects {
		out := runOn(s, topology.DeviceRouter, c)
		if !strings.Contains(out, "Error") {
			t.Errorf("%q should be rejected with Error, got: %q", c, out)
		}
	}
}

// TestAC1VRRPInterfaceViewGuard 非接口视图执行 vrrp 应被守卫拒绝。
func TestAC1VRRPInterfaceViewGuard(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	runOn(s, topology.DeviceRouter, "system-view")
	out := runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	if !strings.Contains(out, "interface view") {
		t.Errorf("non-interface view should be rejected, got: %q", out)
	}
}

// TestAC1VRRPCapabilityRejection Switch / PC 应能力拒绝（非 l3Devices）。
func TestAC1VRRPCapabilityRejection(t *testing.T) {
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

// TestAC1VRRPDifferentSubnet 虚拟 IP 与接口 IP 不同网段应被拒绝。
func TestAC1VRRPDifferentSubnet(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "10.0.0.1", "255.255.255.0")
	out := runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	if !strings.Contains(out, "Error") || !strings.Contains(out, "same subnet") {
		t.Errorf("different subnet should be rejected, got: %q", out)
	}
	// 同网段应成功。
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	out2 := runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	if strings.Contains(out2, "Error") {
		t.Errorf("same subnet should succeed, got: %q", out2)
	}
}

// TestAC2VRRPPersistenceRoundTrip 配置经 save→reload 后 display vrrp / current-configuration 复现。
func TestAC2VRRPPersistenceRoundTrip(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 120")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 preempt-mode disable")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 timer advertise 2")

	// save（含 Y/N 确认）并序列化。
	runOn(s, topology.DeviceRouter, "save")
	runOn(s, topology.DeviceRouter, "y")
	cfg := s.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	wantKeys := []string{
		"interface:GigabitEthernet0/0/1:vrrp:1:virtual-ip",
		"interface:GigabitEthernet0/0/1:vrrp:1:priority",
		"interface:GigabitEthernet0/0/1:vrrp:1:preempt",
		"interface:GigabitEthernet0/0/1:vrrp:1:advertise",
	}
	for _, k := range wantKeys {
		if _, ok := reloaded.DeviceConfig[k]; !ok {
			t.Errorf("reload should preserve key %s, got %v", k, reloaded.DeviceConfig)
		}
	}

	// display vrrp 复现 virtual-ip / priority / Master。
	out := runOn(reloaded, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(out, "192.168.1.254") {
		t.Errorf("reloaded display vrrp should show virtual-ip, got: %q", out)
	}
	if !strings.Contains(out, "120") {
		t.Errorf("reloaded display vrrp should show priority 120, got: %q", out)
	}
	if !strings.Contains(out, "Master") {
		t.Errorf("reloaded display vrrp should show Master role, got: %q", out)
	}

	// display current-configuration 复现 vrrp vrid X virtual-ip（修掉旧 ip %s 格式）。
	cur := runOn(reloaded, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(cur, "vrrp vrid 1 virtual-ip 192.168.1.254") {
		t.Errorf("reloaded current-configuration should show 'vrrp vrid 1 virtual-ip 192.168.1.254', got: %q", cur)
	}
	if !strings.Contains(cur, "vrrp vrid 1 priority 120") {
		t.Errorf("reloaded current-configuration should show priority 120, got: %q", cur)
	}
	if !strings.Contains(cur, "vrrp vrid 1 preempt-mode disable") {
		t.Errorf("reloaded current-configuration should show preempt-mode disable, got: %q", cur)
	}
	if !strings.Contains(cur, "vrrp vrid 1 timer advertise 2") {
		t.Errorf("reloaded current-configuration should show timer advertise 2, got: %q", cur)
	}
}

// TestAC3DisplayVRRPRenders display vrrp 全接口详情渲染（含角色诚实注记）。
func TestAC3DisplayVRRPRenders(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 120")

	out := runOn(s, topology.DeviceRouter, "display vrrp")
	for _, want := range []string{"Virtual Router 1", "State", "Virtual IP", "192.168.1.254", "Priority", "120", "Master"} {
		if !strings.Contains(out, want) {
			t.Errorf("display vrrp should contain %q, got: %q", want, out)
		}
	}
	// AC5 诚实占位：本地假设选举 + 模拟选举注记（lite 必含"非内核级真实 VRRP 故障切换"）。
	if !strings.Contains(out, "本地假设选举") {
		t.Errorf("display vrrp should contain honest local-assumption note, got: %q", out)
	}
	if !strings.Contains(out, "模拟选举") {
		t.Errorf("display vrrp should contain honest simulation note, got: %q", out)
	}
}

// TestAC3DisplayVRRPBrief display vrrp brief 摘要表含列头与角色。
func TestAC3DisplayVRRPBrief(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 priority 120")

	out := runOn(s, topology.DeviceRouter, "display vrrp brief")
	for _, want := range []string{"VRID", "Interface", "Virtual IP", "Priority", "Role", "1", "192.168.1.254", "120", "Master"} {
		if !strings.Contains(out, want) {
			t.Errorf("display vrrp brief should contain %q, got: %q", want, out)
		}
	}
}

// TestAC3DisplayVRRPInterfaceAndVRID display vrrp interface <if> / vrid <id> 单组过滤。
func TestAC3DisplayVRRPInterfaceAndVRID(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")

	outIface := runOn(s, topology.DeviceRouter, "display vrrp interface GigabitEthernet0/0/1")
	if !strings.Contains(outIface, "192.168.1.254") {
		t.Errorf("display vrrp interface should show virtual-ip, got: %q", outIface)
	}
	outVRID := runOn(s, topology.DeviceRouter, "display vrrp vrid 1")
	if !strings.Contains(outVRID, "192.168.1.254") {
		t.Errorf("display vrrp vrid 1 should show virtual-ip, got: %q", outVRID)
	}
}

// TestAC3DisplayVRRPNotConfigured 未配置时显示 Not configured。
func TestAC3DisplayVRRPNotConfigured(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(s, topology.DeviceRouter, "display vrrp")
	if !strings.Contains(out, "Not configured") {
		t.Errorf("display vrrp with no config should show Not configured, got: %q", out)
	}
}

// TestAC4CurrentConfigurationVirtualIPFormat display current-configuration 输出 VRP 合规 virtual-ip（非旧 ip）。
func TestAC4CurrentConfigurationVirtualIPFormat(t *testing.T) {
	s := enterIface(topology.DeviceRouter, "GigabitEthernet0/0/1")
	setIfaceIP(s, topology.DeviceRouter, "GigabitEthernet0/0/1", "192.168.1.1", "255.255.255.0")
	runOn(s, topology.DeviceRouter, "vrrp vrid 1 virtual-ip 192.168.1.254")

	cur := runOn(s, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(cur, "vrrp vrid 1 virtual-ip 192.168.1.254") {
		t.Errorf("current-configuration should contain 'vrrp vrid 1 virtual-ip 192.168.1.254', got: %q", cur)
	}
	// 旧的非合规格式不应出现。
	if strings.Contains(cur, "vrrp vrid 1 ip ") {
		t.Errorf("current-configuration must not use legacy 'vrrp vrid 1 ip' format, got: %q", cur)
	}
}
