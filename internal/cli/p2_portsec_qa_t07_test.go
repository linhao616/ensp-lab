package cli

// p2_portsec_qa_t07_test.go —— 端口安全端到端 QA 验收（T07，严过关，独立证明 AC1–AC6）。
//
// 本文件是 QA 角色的独立验收用例，刻意不复用工程师（寇豆码）既有用例，而是通过
// 不同 MAC / 不同断言角度 / 直接访问纯函数与 DeviceConfig 来独立证明每条 AC。
//
// 覆盖：
//   AC1 命令接受与拒错（含边界值 1/4096 与 Router/PC 能力拒绝）
//   AC2 配置持久化（save→reload 复现 + 粘滞学习 MAC 回填 MACTable + 运行态归零）
//   AC3 display port-security 列头/详情 + display mac-address 的 Type 标签渲染
//   AC4 违规动作触发（protect/restrict/shutdown）经真实 CLI 命令路径端到端验证
//   AC5 EvaluatePortSecurity 纯函数无副作用 + 幂等 + 行为矩阵
//   AC6 lite 引擎诚实占位注记
//
// 仅依赖本包既有测试 helper：enterIface / runOn / NewCLIStateWithType /
// SerializeToDeviceConfigData / NewCLIStateFromDeviceConfig。

import (
	"strconv"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— QA 断言辅助（前缀 t07 避免与既有测试 helper 命名冲突） ——

func t07Contains(t *testing.T, got, sub string) {
	t.Helper()
	if !strings.Contains(got, sub) {
		t.Errorf("expected output to contain %q, got: %q", sub, got)
	}
}

func t07NotContains(t *testing.T, got, sub string) {
	t.Helper()
	if strings.Contains(got, sub) {
		t.Errorf("expected output NOT to contain %q, got: %q", sub, got)
	}
}

// deviceConfigSnapshot 返回 DeviceConfig 的浅拷贝，用于 AC5 副作用比对。
func deviceConfigSnapshot(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// macTableFingerprint 返回 MACTable 的内容指纹（指针+逐字段），用于 AC5 无副作用比对。
func macTableFingerprint(tab []*MACEntry) []string {
	fp := make([]string, 0, len(tab))
	for _, e := range tab {
		if e == nil {
			fp = append(fp, "<nil>")
			continue
		}
		fp = append(fp, e.MAC+"|"+e.Interface+"|"+strconv.Itoa(e.VLAN)+"|"+e.Type)
	}
	return fp
}

// ============================================================================
// AC1 命令接受与拒错
// ============================================================================

// TestT07AC1CommandsAcceptedAndKeys 交换机接口视图逐条命令成功且 DeviceConfig 键正确写入。
func TestT07AC1CommandsAcceptedAndKeys(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	iface := "GigabitEthernet0/0/1"
	cases := []struct {
		cmd string
		key string
		want string
	}{
		{"port-security enable", "interface:" + iface + ":port-security", "enable"},
		{"port-security max-mac-num 2", "interface:" + iface + ":port-security-max-mac", "2"},
		{"port-security protect-action restrict", "interface:" + iface + ":port-security-protect-action", "restrict"},
		{"port-security aging-time 15", "interface:" + iface + ":port-security-aging-time", "15"},
		// 手动粘滞绑定：MAC 应被规范化为小写连字符格式，value 为 vlan
		{"port-security mac-address sticky 00E0-FC12-3456 vlan 10", "interface:" + iface + ":port-security-sticky-mac:00e0-fc12-3456", "10"},
	}
	for _, c := range cases {
		out := runOn(s, topology.DeviceSwitch, c.cmd)
		if strings.Contains(out, "Error") {
			t.Errorf("cmd %q should succeed, got: %q", c.cmd, out)
		}
		if got := s.DeviceConfig[c.key]; got != c.want {
			t.Errorf("cmd %q: key %q = %q, want %q", c.cmd, c.key, got, c.want)
		}
	}
	// 自动粘滞标志（无参）应写 port-security-sticky=enable
	if out := runOn(s, topology.DeviceSwitch, "port-security mac-address sticky"); !strings.Contains(out, "sticky MAC enabled") {
		t.Errorf("sticky flag should enable, got: %q", out)
	}
	if s.DeviceConfig["interface:"+iface+":port-security-sticky"] != "enable" {
		t.Errorf("sticky flag key not enabled, got %q", s.DeviceConfig["interface:"+iface+":port-security-sticky"])
	}
}

// TestT07AC1BoundaryRanges 校验 max-mac-num / aging-time 的合法边界 1 与 4096/1440，
// 以及非法值（0/超界/非数字/负数）的 Error 拒错。
func TestT07AC1BoundaryRanges(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	iface := "GigabitEthernet0/0/1"

	// 合法边界：最小 1 / 最大 4096（max-mac），最小 1 / 最大 1440（aging）。
	valid := []struct {
		cmd  string
		key  string
		want string
	}{
		{"port-security max-mac-num 1", "interface:" + iface + ":port-security-max-mac", "1"},
		{"port-security max-mac-num 4096", "interface:" + iface + ":port-security-max-mac", "4096"},
		{"port-security aging-time 1", "interface:" + iface + ":port-security-aging-time", "1"},
		{"port-security aging-time 1440", "interface:" + iface + ":port-security-aging-time", "1440"},
	}
	for _, c := range valid {
		out := runOn(s, topology.DeviceSwitch, c.cmd)
		if strings.Contains(out, "Error") {
			t.Errorf("valid boundary cmd %q wrongly rejected: %q", c.cmd, out)
		}
		if s.DeviceConfig[c.key] != c.want {
			t.Errorf("valid boundary cmd %q: key %q = %q, want %q", c.cmd, c.key, s.DeviceConfig[c.key], c.want)
		}
	}

	// 非法值必须明确 Error
	invalid := []string{
		"port-security max-mac-num 0",     // 0 非法（至少 1）
		"port-security max-mac-num 4097",  // 超上界
		"port-security max-mac-num -5",    // 负数
		"port-security max-mac-num abc",   // 非数字
		"port-security aging-time 0",      // 0 非法
		"port-security aging-time 1441",   // 超上界
		"port-security aging-time -1",     // 负数
		"port-security aging-time xyz",    // 非数字
		"port-security protect-action foo", // 非法取值
		"port-security mac-address sticky gggg vlan 10",   // 非法 MAC（带 vlan）
		"port-security mac-address sticky 00e0-fc12-345 vlan 10", // 短 MAC
	}
	for _, c := range invalid {
		out := runOn(s, topology.DeviceSwitch, c)
		if !strings.Contains(out, "Error") {
			t.Errorf("invalid cmd %q should be rejected with Error, got: %q", c, out)
		}
	}
}

// TestT07AC1NonInterfaceView 非接口视图执行 port-security / simulate 应被接口视图守卫拒绝。
func TestT07AC1NonInterfaceView(t *testing.T) {
	// port-security
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	out := runOn(s, topology.DeviceSwitch, "port-security enable")
	t07Contains(t, out, "interface view")

	// simulate（即便在接口视图之外）
	s2 := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s2, topology.DeviceSwitch, "system-view")
	out2 := runOn(s2, topology.DeviceSwitch, "simulate frame 00e0-fc12-3456")
	t07Contains(t, out2, "interface view")
}

// TestT07AC1RouterRejected 路由器执行 port-security / simulate 应被能力矩阵拒绝（not supported）。
func TestT07AC1RouterRejected(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(s, topology.DeviceRouter, "port-security enable")
	if !strings.Contains(out, "not supported") {
		t.Errorf("router port-security should be 'not supported', got: %q", out)
	}
	s2 := NewCLIStateWithType(topology.DeviceRouter)
	out2 := runOn(s2, topology.DeviceRouter, "simulate frame 00e0-fc12-3456")
	if !strings.Contains(out2, "not supported") {
		t.Errorf("router simulate should be 'not supported', got: %q", out2)
	}
}

// TestT07AC1PCRejected 主理人拍板要求 Router/PC 均拒绝；独立验证 PC 设备能力拒绝。
func TestT07AC1PCRejected(t *testing.T) {
	s := NewCLIStateWithType(topology.DevicePC)
	out := runOn(s, topology.DevicePC, "port-security enable")
	if !strings.Contains(out, "not supported") {
		t.Errorf("PC port-security should be 'not supported', got: %q", out)
	}
	s2 := NewCLIStateWithType(topology.DevicePC)
	out2 := runOn(s2, topology.DevicePC, "simulate frame 00e0-fc12-3456")
	if !strings.Contains(out2, "not supported") {
		t.Errorf("PC simulate should be 'not supported', got: %q", out2)
	}
}

// ============================================================================
// AC2 配置持久化
// ============================================================================

// TestT07AC2ConfigPersistenceRoundTrip 全程配置经 save→reload 后 DeviceConfig 键与
// display port-security 均复现（含粘滞标志）。
func TestT07AC2ConfigPersistenceRoundTrip(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 2")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")
	runOn(s, topology.DeviceSwitch, "port-security aging-time 15")
	// 自动粘滞标志，用于复现 "Sticky: yes"
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky")
	// 手动粘滞绑定
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky 00e0-fc12-3456 vlan 10")

	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")
	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")

	// 逐键复现
	wantKeys := []string{
		"interface:GigabitEthernet0/0/1:port-security",
		"interface:GigabitEthernet0/0/1:port-security-max-mac",
		"interface:GigabitEthernet0/0/1:port-security-protect-action",
		"interface:GigabitEthernet0/0/1:port-security-aging-time",
		"interface:GigabitEthernet0/0/1:port-security-sticky",
		"interface:GigabitEthernet0/0/1:port-security-sticky-mac:00e0-fc12-3456",
	}
	for _, k := range wantKeys {
		if _, ok := reloaded.DeviceConfig[k]; !ok {
			t.Errorf("reload lost key %s; have %v", k, reloaded.DeviceConfig)
		}
	}

	// display port-security 单端口详情复现
	detail := runOn(reloaded, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail, "enable")
	t07Contains(t, detail, "restrict")
	t07Contains(t, detail, "Max MAC                 : 2")
	t07Contains(t, detail, "Aging(min)              : 15")
	t07Contains(t, detail, "Sticky                  : yes")

	// 全接口表也应列出该端口
	full := runOn(reloaded, topology.DeviceSwitch, "display port-security")
	t07Contains(t, full, "GigabitEthernet0/0/1")
	t07Contains(t, full, "restrict")
}

// TestT07AC2StickyLearnedReload 粘滞学习 MAC（simulate frame 触发）reload 后回填 MACTable(Type=sticky)，
// 且运行态 error-down/violations 归零（主理人拍板 #3）。
func TestT07AC2StickyLearnedReload(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 5")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky") // 粘滞标志，学习 Type=sticky
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")

	// 学习两条粘滞 MAC
	o1 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0aaa")
	t07Contains(t, o1, "ADMITTED")
	t07Contains(t, o1, "sticky MAC learned")
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0bbb")
	t07Contains(t, o2, "sticky MAC learned")

	// 制造运行态违规：把 max-mac 收紧到 1，再注入新 MAC → restrict violation
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0ccc")
	before := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, before, "Violations              : 1")

	// save + reload
	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")
	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")

	// 粘滞学习键应保留（持久化）
	if _, ok := reloaded.DeviceConfig["interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa"]; !ok {
		t.Errorf("reload lost sticky-learned key for 0aaa; dc=%v", reloaded.DeviceConfig)
	}

	// 回填 MACTable：两条粘滞 MAC 必须存在且 Type=sticky
	found := map[string]bool{}
	for _, e := range reloaded.MACTable {
		if e == nil {
			continue
		}
		if e.Interface == "GigabitEthernet0/0/1" && (e.MAC == "00e0-fc12-0aaa" || e.MAC == "00e0-fc12-0bbb") {
			if e.Type != "sticky" {
				t.Errorf("reloaded learned MAC %s has Type=%q, want sticky", e.MAC, e.Type)
			}
			found[e.MAC] = true
		}
	}
	if !found["00e0-fc12-0aaa"] || !found["00e0-fc12-0bbb"] {
		t.Errorf("reload did not repopulate both sticky MACs into MACTable; found=%v", found)
	}

	// 运行态归零
	detail := runOn(reloaded, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail, "Violations              : 0")
	t07Contains(t, detail, "Error-Down              : no")

	// display mac-address 仍可见粘滞条目
	macOut := runOn(reloaded, topology.DeviceSwitch, "display mac-address")
	t07Contains(t, macOut, "00e0-fc12-0aaa")
	t07Contains(t, macOut, "sticky")
}

// ============================================================================
// AC3 display 输出
// ============================================================================

// TestT07AC3DisplayTableColumns display port-security 全接口表含全部列头与目标接口。
func TestT07AC3DisplayTableColumns(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 2")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")
	out := runOn(s, topology.DeviceSwitch, "display port-security")
	for _, col := range []string{"Interface", "Status", "Max MAC", "Protect-Action", "Sticky", "Aging(min)", "Violations"} {
		t07Contains(t, out, col)
	}
	t07Contains(t, out, "GigabitEthernet0/0/1")
	t07Contains(t, out, "restrict")
}

// TestT07AC3DisplayDetailSections 单端口详情含运行态区块与已学 MAC 区块；缺省动作标注 (default)。
func TestT07AC3DisplayDetailSections(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	// 不显式配 protect-action，应保持缺省 restrict 并标注 (default)
	out := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	for _, field := range []string{"Status", "Max MAC", "Protect-Action", "Sticky", "Aging(min)", "Violations", "Error-Down", "Learned Secure MACs"} {
		t07Contains(t, out, field)
	}
	t07Contains(t, out, "restrict (default)")
}

// TestT07AC3MacAddressTypeLabels display mac-address 的 Type 列对 sticky/security/static/dynamic 渲染正确。
func TestT07AC3MacAddressTypeLabels(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	// 手工插入运行时安全的两种类型（种子已含 static/dynamic）
	s.MACTable = append(s.MACTable,
		&MACEntry{MAC: "00e0-fc12-aaaa", VLAN: 10, Interface: "GigabitEthernet0/0/1", Type: "sticky"},
		&MACEntry{MAC: "00e0-fc12-bbbb", VLAN: 20, Interface: "GigabitEthernet0/0/2", Type: "security"},
	)
	out := runOn(s, topology.DeviceSwitch, "display mac-address")
	for _, label := range []string{"sticky", "security", "static", "dynamic"} {
		t07Contains(t, out, label)
	}
}

// ============================================================================
// AC4 违规动作触发（经真实 simulate frame CLI 路径）
// ============================================================================

// TestT07AC4ProtectDropNoViolation protect：第 2 个非授权 MAC 丢弃、无告警、不计数、不进 MACTable。
func TestT07AC4ProtectDropNoViolation(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action protect")

	o1 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	t07Contains(t, o1, "ADMITTED")
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	t07Contains(t, o2, "DROPPED")
	t07NotContains(t, o2, "violation")
	t07NotContains(t, o2, "error-down")

	// 被丢弃的第 2 个 MAC 不应进入 MACTable
	for _, e := range s.MACTable {
		if e != nil && e.MAC == "00e0-fc12-0002" {
			t.Errorf("protect should NOT learn dropped MAC 00e0-fc12-0002")
		}
	}
	detail := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail, "Violations              : 0")
	t07Contains(t, detail, "Error-Down              : no")
}

// TestT07AC4RestrictViolationIncrement restrict：丢弃 + violation 计数递增（连续 2 次）。
func TestT07AC4RestrictViolationIncrement(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action restrict")

	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001") // 占用 1 槽
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	t07Contains(t, o2, "DROPPED")
	t07Contains(t, o2, "restrict")
	t07Contains(t, o2, "violation logged")
	detail1 := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail1, "Violations              : 1")

	// 第 3 个 MAC 仍超槽 → 计数到 2
	o3 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0003")
	t07Contains(t, o3, "violation logged")
	detail2 := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail2, "Violations              : 2")
}

// TestT07AC4ShutdownErrorDownAndReject shutdown：error-down 置位且后续帧被拒。
func TestT07AC4ShutdownErrorDownAndReject(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	runOn(s, topology.DeviceSwitch, "port-security protect-action shutdown")

	runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001") // 占 1 槽
	o2 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0002")
	t07Contains(t, o2, "PORT ERROR-DOWN")
	t07Contains(t, o2, "shutdown")
	detail := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail, "Error-Down              : yes")
	t07Contains(t, detail, "Violations              : 1")

	// 后续帧在 error-down 端口一律被拒（不得 ADMITTED）
	o3 := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0003")
	t07NotContains(t, o3, "ADMITTED")
	// error-down 不计入新违规（计数保持 1）
	detail2 := runOn(s, topology.DeviceSwitch, "display port-security interface GigabitEthernet0/0/1")
	t07Contains(t, detail2, "Violations              : 1")
}

// TestT07AC4StickyLearnIntoMACTable 粘滞开启时合法 MAC 注入 → 准入且进 MACTable(Type=sticky)。
func TestT07AC4StickyLearnIntoMACTable(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 5")
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky") // 粘滞标志

	o := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	t07Contains(t, o, "ADMITTED")
	t07Contains(t, o, "sticky MAC learned")

	// 直接比对 MACTable：应含该条目且 Type=sticky，接口/VLAN 正确
	var found *MACEntry
	for _, e := range s.MACTable {
		if e != nil && e.MAC == "00e0-fc12-0001" {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("learned sticky MAC 00e0-fc12-0001 not found in MACTable: %v", s.MACTable)
	}
	if found.Type != "sticky" {
		t.Errorf("learned MAC Type=%q, want sticky", found.Type)
	}
	if found.Interface != "GigabitEthernet0/0/1" {
		t.Errorf("learned MAC Interface=%q, want GigabitEthernet0/0/1", found.Interface)
	}
	// display mac-address 可见
	macOut := runOn(s, topology.DeviceSwitch, "display mac-address")
	t07Contains(t, macOut, "00e0-fc12-0001")
	t07Contains(t, macOut, "sticky")
}

// TestT07AC4AuthorizedMACAdmitted 手动绑定的授权 MAC 注入 → 准入但不重复学习（不占新槽）。
func TestT07AC4AuthorizedMACAdmitted(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	runOn(s, topology.DeviceSwitch, "port-security max-mac-num 1")
	// 手动绑定 00e0-fc12-9999 vlan 10 → 授权 MAC，占 1 槽
	runOn(s, topology.DeviceSwitch, "port-security mac-address sticky 00e0-fc12-9999 vlan 10")

	o := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-9999")
	t07Contains(t, o, "ADMITTED")
	// 授权 MAC 不应被再次学习（MACTable 中不应出现重复授权条目）
	count := 0
	for _, e := range s.MACTable {
		if e != nil && e.MAC == "00e0-fc12-9999" {
			count++
		}
	}
	if count > 0 {
		t.Errorf("authorized MAC should NOT be re-learned into MACTable, found %d entries", count)
	}
}

// ============================================================================
// AC5 纯函数 / 无副作用
// ============================================================================

// TestT07AC5PureFunctionNoSideEffect 直接调用 EvaluatePortSecurity，验证 state 逐字段不变。
func TestT07AC5PureFunctionNoSideEffect(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	iface := "GigabitEthernet0/0/1"
	s.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s.DeviceConfig["interface:"+iface+":port-security-max-mac"] = "1"
	s.DeviceConfig["interface:"+iface+":port-security-sticky"] = "enable"
	// 预置一条已学安全 MAC（占 1 槽）
	s.MACTable = append(s.MACTable, &MACEntry{MAC: "00e0-fc12-1111", VLAN: 10, Interface: iface, Type: "security"})

	beforeDC := deviceConfigSnapshot(s.DeviceConfig)
	beforeMAC := macTableFingerprint(s.MACTable)
	beforeIfaces := len(s.Interfaces)

	res := EvaluatePortSecurity(s, iface, Frame{SrcMAC: "00e0-fc12-2222", VLAN: 20})

	// 无副作用断言
	afterDC := deviceConfigSnapshot(s.DeviceConfig)
	if len(beforeDC) != len(afterDC) {
		t.Errorf("DeviceConfig mutated: size %d -> %d", len(beforeDC), len(afterDC))
	}
	for k, v := range beforeDC {
		if afterDC[k] != v {
			t.Errorf("DeviceConfig key %q mutated: %q -> %q", k, v, afterDC[k])
		}
	}
	afterMAC := macTableFingerprint(s.MACTable)
	if len(beforeMAC) != len(afterMAC) {
		t.Errorf("MACTable mutated: %v -> %v", beforeMAC, afterMAC)
	}
	for i := range beforeMAC {
		if beforeMAC[i] != afterMAC[i] {
			t.Errorf("MACTable entry %d mutated: %q -> %q", i, beforeMAC[i], afterMAC[i])
		}
	}
	if len(s.Interfaces) != beforeIfaces {
		t.Errorf("Interfaces mutated: %d -> %d", beforeIfaces, len(s.Interfaces))
	}
	// 因已占 1 槽且 max-mac=1，新 MAC 应触发违规
	if res.Admit {
		t.Errorf("expected violation (admit=false) for over-limit MAC, got Admit=true")
	}
	if res.Violation == nil {
		t.Errorf("expected Violation non-nil for over-limit MAC")
	}
}

// TestT07AC5Idempotent 连续两次调用结果一致（纯函数可重入）。
func TestT07AC5Idempotent(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	iface := "GigabitEthernet0/0/1"
	s.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s.DeviceConfig["interface:"+iface+":port-security-max-mac"] = "2"
	s.DeviceConfig["interface:"+iface+":port-security-protect-action"] = "restrict"

	frame := Frame{SrcMAC: "00e0-fc12-2222", VLAN: 20}
	r1 := EvaluatePortSecurity(s, iface, frame)
	r2 := EvaluatePortSecurity(s, iface, frame)
	if r1.Admit != r2.Admit {
		t.Errorf("idempotency broken: Admit %v != %v", r1.Admit, r2.Admit)
	}
	if (r1.Violation == nil) != (r2.Violation == nil) {
		t.Errorf("idempotency broken: Violation presence differs")
	}
	if (r1.Learned == nil) != (r2.Learned == nil) {
		t.Errorf("idempotency broken: Learned presence differs")
	}
	if r1.Violation != nil && r2.Violation != nil && r1.Violation.Action != r2.Violation.Action {
		t.Errorf("idempotency broken: Violation.Action %q != %q", r1.Violation.Action, r2.Violation.Action)
	}
}

// TestT07AC5BehaviorMatrix 行为矩阵：未启用→admit；授权→admit；超上限→按 protect-action 触发。
func TestT07AC5BehaviorMatrix(t *testing.T) {
	iface := "GigabitEthernet0/0/1"

	// 1) 未启用 → admit，无 Violation/Learned
	s0 := NewCLIStateWithType(topology.DeviceSwitch)
	r0 := EvaluatePortSecurity(s0, iface, Frame{SrcMAC: "00e0-fc12-2222"})
	if !r0.Admit || r0.Violation != nil || r0.Learned != nil {
		t.Errorf("disabled port should admit with no side-effects, got %+v", r0)
	}

	// 2) 授权 MAC（手动绑定）→ admit，不学习
	s1 := NewCLIStateWithType(topology.DeviceSwitch)
	s1.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s1.DeviceConfig["interface:"+iface+":port-security-sticky-mac:00e0-fc12-7777"] = "10"
	r1 := EvaluatePortSecurity(s1, iface, Frame{SrcMAC: "00e0-fc12-7777"})
	if !r1.Admit || r1.Learned != nil {
		t.Errorf("authorized MAC should admit without learning, got %+v", r1)
	}

	// 3) 未达上限 → admit + Learned（粘滞关 → Type=security）
	s2 := NewCLIStateWithType(topology.DeviceSwitch)
	s2.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s2.DeviceConfig["interface:"+iface+":port-security-max-mac"] = "5"
	r2 := EvaluatePortSecurity(s2, iface, Frame{SrcMAC: "00e0-fc12-3333", VLAN: 30})
	if !r2.Admit || r2.Learned == nil {
		t.Errorf("under-limit MAC should admit and be learned, got %+v", r2)
	}
	if r2.Learned.Type != "security" {
		t.Errorf("learned Type=%q, want security (sticky off)", r2.Learned.Type)
	}
	if r2.Learned.Interface != iface || r2.Learned.VLAN != 30 {
		t.Errorf("learned entry mismatch: %+v", r2.Learned)
	}

	// 4) 超上限且 protect-action=protect → Admit=false, Violation.Action=protect
	s3 := NewCLIStateWithType(topology.DeviceSwitch)
	s3.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s3.DeviceConfig["interface:"+iface+":port-security-max-mac"] = "1"
	s3.DeviceConfig["interface:"+iface+":port-security-protect-action"] = "protect"
	s3.MACTable = append(s3.MACTable, &MACEntry{MAC: "00e0-fc12-1111", VLAN: 10, Interface: iface, Type: "security"})
	r3 := EvaluatePortSecurity(s3, iface, Frame{SrcMAC: "00e0-fc12-4444"})
	if r3.Admit || r3.Violation == nil || r3.Violation.Action != "protect" {
		t.Errorf("over-limit protect should drop w/ Action=protect, got %+v", r3)
	}

	// 5) 超上限且 protect-action=shutdown → Violation.ErrorDown=true
	s4 := NewCLIStateWithType(topology.DeviceSwitch)
	s4.DeviceConfig["interface:"+iface+":port-security"] = "enable"
	s4.DeviceConfig["interface:"+iface+":port-security-max-mac"] = "1"
	s4.DeviceConfig["interface:"+iface+":port-security-protect-action"] = "shutdown"
	s4.MACTable = append(s4.MACTable, &MACEntry{MAC: "00e0-fc12-1111", VLAN: 10, Interface: iface, Type: "security"})
	r4 := EvaluatePortSecurity(s4, iface, Frame{SrcMAC: "00e0-fc12-5555"})
	if r4.Admit || r4.Violation == nil || r4.Violation.Action != "shutdown" || !r4.Violation.ErrorDown {
		t.Errorf("over-limit shutdown should drop w/ ErrorDown=true, got %+v", r4)
	}

	// 6) nil state → 安全返回 admit
	rn := EvaluatePortSecurity(nil, iface, Frame{SrcMAC: "00e0-fc12-6666"})
	if !rn.Admit {
		t.Errorf("nil state should return Admit=true, got %+v", rn)
	}
}

// ============================================================================
// AC6 诚实占位
// ============================================================================

// TestT07AC6LiteSimNote lite 引擎下 simulate frame 输出带诚实占位注记。
func TestT07AC6LiteSimNote(t *testing.T) {
	s := enterIface(topology.DeviceSwitch, "GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "port-security enable")
	o := runOn(s, topology.DeviceSwitch, "simulate frame 00e0-fc12-0001")
	t07Contains(t, o, "模拟帧注入（lite 引擎），非内核级真实 MAC 学习")
	// 诚实注记不应是 full 版的"仅模拟帧注入"
	t07NotContains(t, o, "（模拟帧注入）")
}
