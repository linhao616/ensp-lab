package cli

// p2_dhcp_test.go —— P2 第六项（DHCP 中继，华为 VRP 课程 27）验收测试（T5）。
//
// 覆盖 PRD 验收标准 AC1–AC6 / AC9 / AC12：
//   AC1  dhcp select 迁移到接口视图；系统视图旧用法报错引导；死字段已删除
//   AC2  relay 前置守卫：未 dhcp select relay 配任何 relay 参数 → 报错且**不写任何键**
//   AC3  server-ip 保序 / 去重 / 上限 8 / 非法地址拒绝
//   AC4  option82 enable / strategy 枚举校验与缺省值
//   AC5  usage 校验（缺参给 usage 而非语义错）
//   AC6  display dhcp relay interface <if> 单接口详情块
//   AC9  undo 全分支（select 级联 / server-ip 无参清空与精确摘除 / information / source-ip）
//   AC12 三态互斥级联清理（切 global/interface 清除全部中继键，杜绝幽灵配置）
//
// 全部经 runOn(state, dt, raw) = ExecuteCommandOn(state, ParseCommand(raw), dt) 驱动，
// 不依赖网络与真实引擎。

import (
	"fmt"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

const dhcpTestIface = "GigabitEthernet0/0/1"

// newRelayRouter 构造一台已进入 dhcpTestIface 接口视图的路由器。
func newRelayRouter(t *testing.T) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	if out := runOn(st, topology.DeviceRouter, "interface "+dhcpTestIface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view failed: %s", out)
	}
	if st.CurrentView != ViewInterface || st.CurrentSub != dhcpTestIface {
		t.Fatalf("unexpected view state: view=%v sub=%q", st.CurrentView, st.CurrentSub)
	}
	return st
}

// relayKeysOf 返回该接口全部 dhcp-relay:* 键（用于「不写任何键」断言）。
func relayKeysOf(st *CLIState, iface string) []string {
	prefix := dhcpRelayKeyPrefix(iface)
	out := make([]string, 0)
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// —— AC1：dhcp select 迁移 ——

func TestAC1DHCPSelectMovedToInterfaceView(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")

	// ① 系统视图旧用法 → 报错引导（不再返回 "DHCP global selected"）
	out := runOn(st, topology.DeviceRouter, "dhcp select global")
	if out != errDHCPSelectInterfaceView {
		t.Errorf("system-view 'dhcp select global' = %q, want %q", out, errDHCPSelectInterfaceView)
	}
	if strings.Contains(out, "selected") {
		t.Errorf("system-view dhcp select must not report success, got %q", out)
	}
	// ② 系统视图 undo dhcp select → 报错引导（设计 A8），不得静默 "DHCP disabled"
	out = runOn(st, topology.DeviceRouter, "undo dhcp select")
	if out != errUndoDHCPSelectInterfaceView {
		t.Errorf("system-view 'undo dhcp select' = %q, want %q", out, errUndoDHCPSelectInterfaceView)
	}
	// ③ 系统视图 undo dhcp（无 select）行为不变
	if out = runOn(st, topology.DeviceRouter, "undo dhcp"); out != "DHCP disabled" {
		t.Errorf("system-view 'undo dhcp' = %q, want %q", out, "DHCP disabled")
	}

	// ④ 接口视图三态均可写入 dhcp-select 键（单一事实源）
	for _, mode := range []string{relayModeGlobal, relayModeInterface, relayModeRelay} {
		st2 := newRelayRouter(t)
		if out := runOn(st2, topology.DeviceRouter, "dhcp select "+mode); strings.HasPrefix(out, "Error") {
			t.Fatalf("interface-view 'dhcp select %s' = %q", mode, out)
		}
		if got := st2.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; got != mode {
			t.Errorf("dhcp-select key = %q, want %q", got, mode)
		}
	}

	// ⑤ 非法模式 → usage
	st3 := newRelayRouter(t)
	if out := runOn(st3, topology.DeviceRouter, "dhcp select bogus"); out != errDHCPSelectUsage {
		t.Errorf("invalid mode = %q, want %q", out, errDHCPSelectUsage)
	}
	if _, ok := st3.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; ok {
		t.Error("invalid mode must not write dhcp-select key")
	}
}

func TestAC1DHCPSelectIdempotent(t *testing.T) {
	st := newRelayRouter(t)
	for i := 0; i < 3; i++ {
		if out := runOn(st, topology.DeviceRouter, "dhcp select relay"); strings.HasPrefix(out, "Error") {
			t.Fatalf("repeat %d: %s", i, out)
		}
	}
	if got := st.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; got != relayModeRelay {
		t.Errorf("dhcp-select = %q after repeats, want relay", got)
	}
}

// —— AC2：relay 前置守卫（拍板 #1）——

func TestAC2RelayPreconditionGuard(t *testing.T) {
	cmds := []string{
		"dhcp relay server-ip 10.1.1.1",
		"dhcp relay source-ip 10.2.2.254",
		"dhcp relay information enable",
		"dhcp relay information strategy keep",
	}
	// ① 完全未配 dhcp select
	for _, raw := range cmds {
		st := newRelayRouter(t)
		out := runOn(st, topology.DeviceRouter, raw)
		if out != errDHCPSelectRelayFirst {
			t.Errorf("%q without select relay = %q, want %q", raw, out, errDHCPSelectRelayFirst)
		}
		if keys := relayKeysOf(st, dhcpTestIface); len(keys) != 0 {
			t.Errorf("%q must not write any key, got %v", raw, keys)
		}
	}
	// ② 已配 select global / interface（非 relay）同样拒绝
	for _, mode := range []string{relayModeGlobal, relayModeInterface} {
		for _, raw := range cmds {
			st := newRelayRouter(t)
			runOn(st, topology.DeviceRouter, "dhcp select "+mode)
			out := runOn(st, topology.DeviceRouter, raw)
			if out != errDHCPSelectRelayFirst {
				t.Errorf("mode=%s %q = %q, want %q", mode, raw, out, errDHCPSelectRelayFirst)
			}
			if keys := relayKeysOf(st, dhcpTestIface); len(keys) != 0 {
				t.Errorf("mode=%s %q must not write any key, got %v", mode, raw, keys)
			}
		}
	}
	// ③ 配了 select relay 之后全部放行
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	for _, raw := range cmds {
		if out := runOn(st, topology.DeviceRouter, raw); strings.HasPrefix(out, "Error") {
			t.Errorf("%q after select relay = %q, want success", raw, out)
		}
	}
}

// —— AC3：server-ip 保序 / 去重 / 上限 / 校验 ——

func TestAC3ServerIPOrderDedupLimit(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")

	// 保序：先配先列（非排序）
	for _, ip := range []string{"10.1.1.3", "10.1.1.1", "10.1.1.2"} {
		if out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip "+ip); strings.HasPrefix(out, "Error") {
			t.Fatalf("add %s failed: %s", ip, out)
		}
	}
	key := dhcpRelayKey(dhcpTestIface, dhcpRelayFieldServerIPs)
	if got := st.DeviceConfig[key]; got != "10.1.1.3,10.1.1.1,10.1.1.2" {
		t.Errorf("server-ips = %q, want insertion order preserved", got)
	}

	// 去重：重复地址幂等成功，不追加
	if out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1"); strings.HasPrefix(out, "Error") {
		t.Errorf("duplicate add should be idempotent success, got %q", out)
	}
	if got := st.DeviceConfig[key]; got != "10.1.1.3,10.1.1.1,10.1.1.2" {
		t.Errorf("duplicate changed value: %q", got)
	}

	// 上限 8：补到 8 个后第 9 个必须拒绝
	for i := 4; i <= MaxRelayServerIPs; i++ {
		ip := fmt.Sprintf("10.1.1.%d", i)
		if out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip "+ip); strings.HasPrefix(out, "Error") {
			t.Fatalf("add %s (#%d) failed: %s", ip, i, out)
		}
	}
	if n := len(parseRelayServerIPs(st.DeviceConfig[key])); n != MaxRelayServerIPs {
		t.Fatalf("server count = %d, want %d", n, MaxRelayServerIPs)
	}
	out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.9.9.9")
	if !strings.HasPrefix(out, "Error:") || !strings.Contains(out, "upper limit") {
		t.Errorf("over-limit add = %q, want upper limit error", out)
	}
	if n := len(parseRelayServerIPs(st.DeviceConfig[key])); n != MaxRelayServerIPs {
		t.Errorf("over-limit add mutated list to %d entries", n)
	}
}

func TestAC3ServerIPValidation(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	key := dhcpRelayKey(dhcpTestIface, dhcpRelayFieldServerIPs)
	for _, bad := range []string{"abc", "10.1.1.256", "0.0.0.0", "255.255.255.255", "127.0.0.1", "224.0.0.5"} {
		out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip "+bad)
		if !strings.HasPrefix(out, "Error:") {
			t.Errorf("server-ip %s = %q, want Error", bad, out)
		}
		if _, ok := st.DeviceConfig[key]; ok {
			t.Fatalf("invalid ip %s wrote the key", bad)
		}
	}
	// source-ip 走同一校验
	for _, bad := range []string{"0.0.0.0", "not-an-ip"} {
		out := runOn(st, topology.DeviceRouter, "dhcp relay source-ip "+bad)
		if !strings.HasPrefix(out, "Error:") {
			t.Errorf("source-ip %s = %q, want Error", bad, out)
		}
	}
	// 合法 source-ip 单值覆盖
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.253")
	if got := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldSourceIP)]; got != "10.2.2.253" {
		t.Errorf("source-ip = %q, want last-write-wins 10.2.2.253", got)
	}
}

// —— AC4：option82 ——

func TestAC4Option82EnableAndStrategy(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")

	// 缺省：未配即 Disabled + replace
	cfg := readRelayConfig(st, dhcpTestIface)
	if cfg.Option82 != false || cfg.Option82Strategy != DefaultOption82Strategy {
		t.Errorf("defaults = (%v,%q)", cfg.Option82, cfg.Option82Strategy)
	}

	// enable
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")
	if got := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldOption82)]; got != "true" {
		t.Errorf("option82 key = %q, want true", got)
	}

	// strategy 合法枚举
	for _, s := range []string{"drop", "keep", "replace"} {
		if out := runOn(st, topology.DeviceRouter, "dhcp relay information strategy "+s); strings.HasPrefix(out, "Error") {
			t.Errorf("strategy %s = %q", s, out)
		}
		if got := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldStrategy)]; got != s {
			t.Errorf("strategy key = %q, want %q", got, s)
		}
	}
	// strategy 非法枚举 → unrecognized command，且不覆盖既有值
	prev := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldStrategy)]
	if out := runOn(st, topology.DeviceRouter, "dhcp relay information strategy bogus"); out != errUnrecognizedCommand {
		t.Errorf("invalid strategy = %q, want %q", out, errUnrecognizedCommand)
	}
	if got := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldStrategy)]; got != prev {
		t.Errorf("invalid strategy mutated key to %q", got)
	}
}

func TestAC4StrategyBeforeEnableSoftHint(t *testing.T) {
	// 拍板 #6：未 information enable 就配 strategy → 允许 + Info 软提示（不阻断）。
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	out := runOn(st, topology.DeviceRouter, "dhcp relay information strategy keep")
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("strategy before enable must not be rejected, got %q", out)
	}
	if !strings.Contains(out, "Info:") {
		t.Errorf("expected Info hint, got %q", out)
	}
	if got := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldStrategy)]; got != "keep" {
		t.Errorf("strategy key = %q, want keep (config must still be written)", got)
	}
}

func TestAC4DHCPNotEnabledSoftHint(t *testing.T) {
	// 拍板 #6：全局未 dhcp enable → Info 软提示，键照写、不阻断。
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "interface "+dhcpTestIface)

	out := runOn(st, topology.DeviceRouter, "dhcp select relay")
	if !strings.Contains(out, "Info:") || strings.HasPrefix(out, "Error") {
		t.Errorf("expected Info hint without Error, got %q", out)
	}
	if got := st.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; got != relayModeRelay {
		t.Errorf("key must still be written, got %q", got)
	}
	// 启用 DHCP 后不再提示
	runOn(st, topology.DeviceRouter, "quit")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+dhcpTestIface)
	if out := runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1"); out != "" {
		t.Errorf("expected silent success after dhcp enable, got %q", out)
	}
}

// —— AC5：usage 校验 ——

func TestAC5UsageMessages(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"dhcp select", errDHCPSelectUsage},
		{"dhcp relay", errDHCPRelayUsage},
		{"dhcp relay server-ip", errDHCPRelayServerIPUsage},
		{"dhcp relay source-ip", errDHCPRelaySourceIPUsage},
		{"dhcp relay information", errDHCPRelayInformationUsage},
		{"dhcp relay information strategy", errDHCPRelayInformationUsage},
		{"dhcp relay bogus", errUnrecognizedCommand},
	}
	for _, c := range cases {
		// 缺参属语法错误：即便未 select relay 也应给 usage，而不是语义前置错。
		st := newRelayRouter(t)
		if out := runOn(st, topology.DeviceRouter, c.raw); out != c.want {
			t.Errorf("%q = %q, want %q", c.raw, out, c.want)
		}
	}
}

func TestAC5DeviceGuardOnL2Switch(t *testing.T) {
	// 二层交换机不支持 DHCP 中继（设备守卫 ②，复用 l3Devices()）。
	st := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(st, topology.DeviceSwitch, "system-view")
	runOn(st, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	want := errDHCPRelayNotSupported(string(topology.DeviceSwitch))
	for _, raw := range []string{"dhcp select relay", "dhcp relay server-ip 10.1.1.1"} {
		if out := runOn(st, topology.DeviceSwitch, raw); out != want {
			t.Errorf("%q on L2 switch = %q, want %q", raw, out, want)
		}
	}
	if len(relayKeysOf(st, "GigabitEthernet0/0/1")) != 0 {
		t.Error("L2 switch must not write relay keys")
	}
	// 三层交换机放行
	l3 := NewCLIStateWithType(topology.DeviceL3Switch)
	runOn(l3, topology.DeviceL3Switch, "system-view")
	runOn(l3, topology.DeviceL3Switch, "vlan 10")
	runOn(l3, topology.DeviceL3Switch, "quit")
	runOn(l3, topology.DeviceL3Switch, "interface Vlanif10")
	if out := runOn(l3, topology.DeviceL3Switch, "dhcp select relay"); strings.Contains(out, "not supported") {
		t.Errorf("L3 switch must support relay, got %q", out)
	}
}

// —— AC6：display dhcp relay interface <if> ——

func TestAC6DisplayInterfaceDetail(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.2")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")

	out := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+dhcpTestIface)
	for _, want := range []string{
		"DHCP relay information of interface " + dhcpTestIface,
		"Relay mode", "relay",
		"Server IP address(es)", "10.1.1.1", "10.1.1.2",
		"Option82 (information)", "Enabled",
		"Option82 strategy", DefaultOption82Strategy,
		"Source IP address", "10.2.2.254",
		"Interface status",
		"Forwarding statistics",
		"DHCP packets forwarded", "DISCOVER forwarded", "OFFER received",
		"REQUEST forwarded", "ACK received", "Server reachability",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("display detail missing %q\n---\n%s", want, out)
		}
	}
	// 保序：10.1.1.1 必须出现在 10.1.1.2 之前
	if strings.Index(out, "10.1.1.1") > strings.Index(out, "10.1.1.2") {
		t.Errorf("server ip order not preserved\n%s", out)
	}
	// 诚实注记
	if !strings.Contains(out, dhcpRelaySimNote()) {
		t.Errorf("display detail missing sim note\n%s", out)
	}
}

func TestAC6DisplayInterfaceEdgeCases(t *testing.T) {
	st := newRelayRouter(t)
	// ① 接口存在但未配中继 → Info（非 Error、非空串）
	out := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+dhcpTestIface)
	if !strings.Contains(out, "Info:") || strings.Contains(out, "Error:") {
		t.Errorf("unconfigured interface = %q, want Info", out)
	}
	// ② 接口不存在 → Error，且不附诚实注记（命令被拒绝，不构成 display 输出）
	out = runOn(st, topology.DeviceRouter, "display dhcp relay interface GigabitEthernet9/9/9")
	if out != errDHCPRelayIfaceNotExist {
		t.Errorf("missing interface = %q, want %q", out, errDHCPRelayIfaceNotExist)
	}
	// ③ 缺接口名 → usage
	if out = runOn(st, topology.DeviceRouter, "display dhcp relay interface"); out != errDHCPRelayDisplayUsage {
		t.Errorf("missing name = %q, want %q", out, errDHCPRelayDisplayUsage)
	}
	// ④ 未知子命令 → usage
	if out = runOn(st, topology.DeviceRouter, "display dhcp relay bogus"); out != errDHCPRelayDisplayUsage {
		t.Errorf("bogus subcmd = %q, want %q", out, errDHCPRelayDisplayUsage)
	}
	// ⑤ display dhcp <其它> → unrecognized command
	if out = runOn(st, topology.DeviceRouter, "display dhcp pool"); out != errUnrecognizedCommand {
		t.Errorf("display dhcp pool = %q, want %q", out, errUnrecognizedCommand)
	}
}

func TestAC6DisplaySummaryAndEmpty(t *testing.T) {
	// 空态
	st := newRelayRouter(t)
	for _, raw := range []string{"display dhcp relay", "display dhcp relay all"} {
		out := runOn(st, topology.DeviceRouter, raw)
		if !strings.Contains(out, infoNoDHCPRelayInterface) {
			t.Errorf("%q empty state = %q", raw, out)
		}
		if !strings.Contains(out, dhcpRelaySimNote()) {
			t.Errorf("%q must carry sim note", raw)
		}
	}
	// 多接口汇总：升序 + 表头 + 合计
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "quit")
	runOn(st, topology.DeviceRouter, "interface GigabitEthernet0/0/0")
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.9.9.9")

	out := runOn(st, topology.DeviceRouter, "display dhcp relay all")
	for _, want := range []string{"Interface", "Mode", "Servers", "Primary Server", "Option82", "Source IP", "Fwd",
		"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "Total: 2 relay interface(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n---\n%s", want, out)
		}
	}
	// 升序：0/0/0 行在 0/0/1 行之前
	if strings.Index(out, "GigabitEthernet0/0/0") > strings.Index(out, "GigabitEthernet0/0/1") {
		t.Errorf("summary rows not sorted ascending\n%s", out)
	}
}

// —— AC9：undo 全分支 ——

func TestAC9UndoServerIP(t *testing.T) {
	key := dhcpRelayKey(dhcpTestIface, dhcpRelayFieldServerIPs)

	// ① 带参精确摘除，其余保序
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	for _, ip := range []string{"10.1.1.1", "10.1.1.2", "10.1.1.3"} {
		runOn(st, topology.DeviceRouter, "dhcp relay server-ip "+ip)
	}
	if out := runOn(st, topology.DeviceRouter, "undo dhcp relay server-ip 10.1.1.2"); out != "" {
		t.Errorf("undo server-ip = %q, want silent", out)
	}
	if got := st.DeviceConfig[key]; got != "10.1.1.1,10.1.1.3" {
		t.Errorf("after removal = %q, want 10.1.1.1,10.1.1.3", got)
	}
	// ② 不存在的地址 → Error
	if out := runOn(st, topology.DeviceRouter, "undo dhcp relay server-ip 10.8.8.8"); out != errDHCPRelayServerIPNotExist {
		t.Errorf("undo non-existent = %q, want %q", out, errDHCPRelayServerIPNotExist)
	}
	// ③ 删至空 → delete(map,key)，而非留空串键
	runOn(st, topology.DeviceRouter, "undo dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "undo dhcp relay server-ip 10.1.1.3")
	if _, ok := st.DeviceConfig[key]; ok {
		t.Errorf("empty list must delete key, got %q", st.DeviceConfig[key])
	}
	// ④ 无参清空全部
	st2 := newRelayRouter(t)
	runOn(st2, topology.DeviceRouter, "dhcp select relay")
	runOn(st2, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st2, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.2")
	if out := runOn(st2, topology.DeviceRouter, "undo dhcp relay server-ip"); out != "" {
		t.Errorf("undo all = %q, want silent", out)
	}
	if _, ok := st2.DeviceConfig[key]; ok {
		t.Error("undo without arg must clear the key entirely")
	}
}

func TestAC9UndoInformationAndSourceIP(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")
	runOn(st, topology.DeviceRouter, "dhcp relay information strategy keep")
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")

	runOn(st, topology.DeviceRouter, "undo dhcp relay information enable")
	if _, ok := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldOption82)]; ok {
		t.Error("undo information enable must delete option82 key")
	}
	runOn(st, topology.DeviceRouter, "undo dhcp relay information strategy")
	if _, ok := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldStrategy)]; ok {
		t.Error("undo information strategy must delete strategy key")
	}
	runOn(st, topology.DeviceRouter, "undo dhcp relay source-ip")
	if _, ok := st.DeviceConfig[dhcpRelayKey(dhcpTestIface, dhcpRelayFieldSourceIP)]; ok {
		t.Error("undo source-ip must delete source-ip key")
	}
	// 回落缺省值
	cfg := readRelayConfig(st, dhcpTestIface)
	if cfg.Option82 != DefaultOption82Enabled || cfg.Option82Strategy != DefaultOption82Strategy || cfg.SourceIP != "" {
		t.Errorf("defaults not restored: %+v", cfg)
	}
	// 未知子命令
	if out := runOn(st, topology.DeviceRouter, "undo dhcp relay bogus"); out != errUnrecognizedCommand {
		t.Errorf("undo bogus = %q, want %q", out, errUnrecognizedCommand)
	}
	if out := runOn(st, topology.DeviceRouter, "undo dhcp bogus"); out != errUnrecognizedCommand {
		t.Errorf("undo dhcp bogus = %q, want %q", out, errUnrecognizedCommand)
	}
}

func TestAC9UndoSelectCascades(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")
	// 干扰项：地址池绑定键必须存活（§1.6）
	st.DeviceConfig["interface:"+dhcpTestIface+":dhcp-pool"] = "pool1"

	if out := runOn(st, topology.DeviceRouter, "undo dhcp select"); out != "" {
		t.Errorf("undo dhcp select = %q, want silent", out)
	}
	if _, ok := st.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; ok {
		t.Error("undo select must delete dhcp-select key")
	}
	if keys := relayKeysOf(st, dhcpTestIface); len(keys) != 0 {
		t.Errorf("undo select must cascade-clear relay keys, left %v", keys)
	}
	if _, ok := st.DeviceConfig["interface:"+dhcpTestIface+":dhcp-pool"]; !ok {
		t.Error("cascade must NOT delete dhcp-pool key (§1.6 key collision red line)")
	}
}

// —— AC12：三态互斥级联清理（拍板 #3）——

func TestAC12ModeSwitchCascadeCleanup(t *testing.T) {
	for _, target := range []string{relayModeGlobal, relayModeInterface} {
		st := newRelayRouter(t)
		runOn(st, topology.DeviceRouter, "dhcp select relay")
		runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
		runOn(st, topology.DeviceRouter, "dhcp relay information enable")
		runOn(st, topology.DeviceRouter, "dhcp relay information strategy keep")
		runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")
		st.DeviceConfig["interface:"+dhcpTestIface+":dhcp-pool"] = "pool1"
		if n := len(relayKeysOf(st, dhcpTestIface)); n != 4 {
			t.Fatalf("precondition: relay key count = %d, want 4", n)
		}

		if out := runOn(st, topology.DeviceRouter, "dhcp select "+target); strings.HasPrefix(out, "Error") {
			t.Fatalf("switch to %s failed: %s", target, out)
		}
		if got := st.DeviceConfig[dhcpSelectKey(dhcpTestIface)]; got != target {
			t.Errorf("mode = %q, want %q", got, target)
		}
		if keys := relayKeysOf(st, dhcpTestIface); len(keys) != 0 {
			t.Errorf("switch to %s left ghost relay keys: %v", target, keys)
		}
		if _, ok := st.DeviceConfig["interface:"+dhcpTestIface+":dhcp-pool"]; !ok {
			t.Errorf("switch to %s wrongly deleted dhcp-pool key", target)
		}
		// 级联后该接口不再出现在中继接口集合中（无幽灵接口）
		for _, name := range collectRelayInterfaces(st) {
			if name == dhcpTestIface {
				t.Errorf("ghost relay interface after switching to %s", target)
			}
		}
	}
}

func TestAC12SwitchBackToRelayStartsClean(t *testing.T) {
	st := newRelayRouter(t)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp select global")
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	cfg := readRelayConfig(st, dhcpTestIface)
	if len(cfg.ServerIPs) != 0 {
		t.Errorf("re-entering relay mode must start clean, got %v", cfg.ServerIPs)
	}
	if EvaluateDHCPRelay(st, dhcpTestIface).Active {
		t.Error("re-entered relay mode without servers must not be Active")
	}
}
