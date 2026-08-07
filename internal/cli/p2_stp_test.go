package cli

// p2_stp_test.go —— STP/RSTP/MSTP 增量集成测试（T06 之外，对齐 AC1/AC2/AC3）。
//
// 通过 ExecuteCommandOn + ParseCommand 驱动命令分发，覆盖：
//   AC1 命令接受与拒错（范围校验 / 视图守卫 / 能力拒绝 / region 视图前置）；
//   AC2 配置持久化往返（save → reload → display 复现，修掉 P0-1 丢配置缺陷）；
//   AC3 display stp 各形态渲染（CIST/brief/interface/region-configuration）+ 诚实注记 + undo 后 Disabled。
//
// 注：MST region 视图命令在该模拟器中经 `stp` 前缀路由（applySTP → applySTPRegion），
// 故 region 视图内使用 `stp region-name` / `stp instance ...` / `stp active region-configuration`。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestAC1STPCommandsAccepted STP 系统/接口命令在交换机上被接受并写入正确 DeviceConfig 键。
func TestAC1STPCommandsAccepted(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	cases := []struct {
		cmd string
		key string
		val string
	}{
		{"stp mode rstp", "stp:mode", "rstp"},
		{"stp priority 4096", "stp:priority", "4096"},
		{"stp root primary", "stp:priority", "0"},
		{"stp root secondary", "stp:priority", "4096"},
		{"stp pathcost-standard dot1d-1998", "stp:pathcost-standard", "dot1d-1998"},
		{"stp bpdu-protection", "stp:bpdu-protection", "enable"},
		{"stp tc-protection interval 5", "stp:tc-protection", "enable"},
		{"stp bridge-address 4c1f-cc00-1111", "stp:bridge-address", "4c1f-cc00-1111"},
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
	if s.DeviceConfig["stp:tc-protection-interval"] != "5" {
		t.Errorf("stp tc-protection interval 应写 5, got %q", s.DeviceConfig["stp:tc-protection-interval"])
	}

	// 接口级命令（需接口视图）。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "stp edged-port enable"); strings.Contains(out, "Error") {
		t.Errorf("stp edged-port enable should succeed, got: %q", out)
	}
	if s.DeviceConfig["interface:GigabitEthernet0/0/1:stp:edged-port"] != "enable" {
		t.Errorf("edged-port 应写 enable")
	}
	if out := runOn(s, topology.DeviceSwitch, "stp cost 10000"); strings.Contains(out, "Error") {
		t.Errorf("stp cost 10000 should succeed, got: %q", out)
	}
	if s.DeviceConfig["interface:GigabitEthernet0/0/1:stp:cost"] != "10000" {
		t.Errorf("cost 应写 10000")
	}
}

// TestAC1STPCommandsRejected STP 非法命令 / 视图守卫 / 能力拒绝 / region 前置。
func TestAC1STPCommandsRejected(t *testing.T) {
	// 范围非法：priority 非 4096 倍数。
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	if out := runOn(s, topology.DeviceSwitch, "stp priority 4097"); !strings.Contains(out, "Error") {
		t.Errorf("stp priority 4097 应被拒, got: %q", out)
	}

	// 接口视图命令误用在系统视图。
	if out := runOn(s, topology.DeviceSwitch, "stp cost 10000"); !strings.Contains(out, "Error") || !strings.Contains(out, "interface view") {
		t.Errorf("系统视图 stp cost 应报接口视图错误, got: %q", out)
	}

	// 系统视图命令误用在接口视图。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	if out := runOn(s, topology.DeviceSwitch, "stp mode rstp"); !strings.Contains(out, "Error") || !strings.Contains(out, "system view") {
		t.Errorf("接口视图 stp mode 应报系统视图错误, got: %q", out)
	}

	// region 子命令未进 region 视图（stp region-name 在系统视图应被拒）。
	s2 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s2, topology.DeviceSwitch, "system-view")
	if out := runOn(s2, topology.DeviceSwitch, "stp region-name foo"); !strings.Contains(out, "Error") || !strings.Contains(out, "MST region") {
		t.Errorf("未进 region 视图的 stp region-name 应被拒, got: %q", out)
	}

	// 能力拒绝：路由器不支持 stp（switchDevices 之外）。
	r := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(r, topology.DeviceRouter, "stp mode mstp"); !strings.Contains(out, "not supported") {
		t.Errorf("路由器应拒绝 stp, got: %q", out)
	}
}

// TestAC2STPPersistenceRoundTrip STP 配置经 save→reload 后 DeviceConfig 键完整保留 + display 复现。
func TestAC2STPPersistenceRoundTrip(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	runOn(s, topology.DeviceSwitch, "stp mode rstp")
	runOn(s, topology.DeviceSwitch, "stp root primary") // priority=0
	runOn(s, topology.DeviceSwitch, "stp pathcost-standard dot1d-1998")
	runOn(s, topology.DeviceSwitch, "stp tc-protection")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "stp edged-port enable")
	runOn(s, topology.DeviceSwitch, "stp cost 10000")
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")

	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW1")

	// 方案 A：全部 stp:* / interface:*:stp:* 键随 DeviceConfig 序列化往返，不丢。
	wantKeys := []string{
		"stp:mode", "rstp",
		"stp:priority", "0",
		"stp:pathcost-standard", "dot1d-1998",
		"stp:tc-protection", "enable",
		"interface:GigabitEthernet0/0/1:stp:edged-port", "enable",
		"interface:GigabitEthernet0/0/1:stp:cost", "10000",
	}
	for i := 0; i < len(wantKeys); i += 2 {
		if reloaded.DeviceConfig[wantKeys[i]] != wantKeys[i+1] {
			t.Errorf("reload 应保留 %s=%q, got %q", wantKeys[i], wantKeys[i+1], reloaded.DeviceConfig[wantKeys[i]])
		}
	}

	// display stp 复现模式与 CIST 根桥假设。
	out := runOn(reloaded, topology.DeviceSwitch, "display stp")
	if !strings.Contains(out, "rstp") {
		t.Errorf("reloaded display stp 应显示 rstp 模式, got: %q", out)
	}
	if !strings.Contains(out, "CIST Global Info") {
		t.Errorf("reloaded display stp 应含 CIST Global Info, got: %q", out)
	}

	// display current-configuration 复现系统级 STP 配置行（修掉旧直排 DeviceConfig 键）。
	cur := runOn(reloaded, topology.DeviceSwitch, "display current-configuration")
	if !strings.Contains(cur, "stp mode rstp") {
		t.Errorf("reloaded current-config 应含 'stp mode rstp', got: %q", cur)
	}
	if !strings.Contains(cur, "stp root primary") {
		t.Errorf("reloaded current-config 应含 'stp root primary', got: %q", cur)
	}
	if !strings.Contains(cur, "stp pathcost-standard dot1d-1998") {
		t.Errorf("reloaded current-config 应含 'stp pathcost-standard dot1d-1998', got: %q", cur)
	}
}

// TestAC3DisplaySTPRenders display stp 各形态渲染 + 诚实注记 + undo stp 后 Disabled。
func TestAC3DisplaySTPRenders(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "stp edged-port enable")
	runOn(s, topology.DeviceSwitch, "quit")

	// 默认（CIST Global Info + 端口 Role/State + 诚实注记）。
	out := runOn(s, topology.DeviceSwitch, "display stp")
	if !strings.Contains(out, "CIST Global Info") {
		t.Errorf("display stp 应含 CIST Global Info, got: %q", out)
	}
	if !strings.Contains(out, "Port Role/State") {
		t.Errorf("display stp 应含 Port Role/State, got: %q", out)
	}
	if !strings.Contains(out, "模拟生成树") {
		t.Errorf("display stp 应附诚实占位注记, got: %q", out)
	}

	// brief 摘要表（MSTID / Role）。
	brief := runOn(s, topology.DeviceSwitch, "display stp brief")
	if !strings.Contains(brief, "MSTID") || !strings.Contains(brief, "Role") {
		t.Errorf("display stp brief 应含 MSTID/Role 表头, got: %q", brief)
	}

	// interface 单端口详情（含接口名 + 诚实注记）。
	ifc := runOn(s, topology.DeviceSwitch, "display stp interface GigabitEthernet0/0/1")
	if !strings.Contains(ifc, "GigabitEthernet0/0/1") {
		t.Errorf("display stp interface 应含接口名, got: %q", ifc)
	}
	if !strings.Contains(ifc, "CIST Global Information") {
		t.Errorf("display stp interface 应含 CIST Global Information, got: %q", ifc)
	}
	if !strings.Contains(ifc, "模拟生成树") {
		t.Errorf("display stp interface 应附诚实注记, got: %q", ifc)
	}

	// region-configuration（未配置）。
	region := runOn(s, topology.DeviceSwitch, "display stp region-configuration")
	if !strings.Contains(region, "MSTP Region") {
		t.Errorf("display stp region-configuration 应含 MSTP Region, got: %q", region)
	}

	// 进入 region 视图并配置后，display 应显示 Region name / Active。
	runOn(s, topology.DeviceSwitch, "stp region-configuration")
	runOn(s, topology.DeviceSwitch, "stp region-name HUAWEI")
	runOn(s, topology.DeviceSwitch, "stp revision-level 1")
	runOn(s, topology.DeviceSwitch, "stp instance 1 vlan 2 to 10")
	runOn(s, topology.DeviceSwitch, "stp active region-configuration")
	region2 := runOn(s, topology.DeviceSwitch, "display stp region-configuration")
	if !strings.Contains(region2, "Region name") || !strings.Contains(region2, "HUAWEI") {
		t.Errorf("region 应显示 Region name/HUAWEI, got: %q", region2)
	}
	if !strings.Contains(region2, "Active") {
		t.Errorf("active region 应显示 Active, got: %q", region2)
	}

	// undo stp（系统视图）应清理全部 stp 键、display stp 显示 Disabled。
	// 注意：undo 按视图分发，且本模拟器 quit 不从 MST region 视图退出，
	// 故独立用系统视图状态验证 undo stp（与 p1f_qa_test 一致）。
	u := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(u, topology.DeviceSwitch, "system-view")
	runOn(u, topology.DeviceSwitch, "stp mode rstp")
	runOn(u, topology.DeviceSwitch, "undo stp")
	if u.DeviceConfig["stp:enabled"] != "false" {
		t.Errorf("undo stp 应置 stp:enabled=false")
	}
	disabled := runOn(u, topology.DeviceSwitch, "display stp")
	if !strings.Contains(disabled, "Disabled") {
		t.Errorf("undo stp 后 display stp 应显示 Disabled, got: %q", disabled)
	}
}
