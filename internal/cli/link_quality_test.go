package cli

// link_quality_test.go —— v0.12 链路质量模拟：CLI 三件套验收测试。
//
// 覆盖：
//   - eval：入参校验边界、丢包格式化确定性、🔴 键碰撞红线（精确三段解析）；
//   - cmd ：接口视图写入 / undo 清除 / 视图守卫 / 参数个数与词法错误；
//   - display：表格与单接口详情、诚实占位（Measured/Jitter 恒为 "-"）、
//     仿真扩展声明、saved-configuration 差异值行与 reload 补齐通道；
//   - 接线：parser 顶层 case、display 派发、undo 分支、补全表一致性。
//
// 🔴 红线断言：本文件显式验证 parseInterfaceQualityKey 不会误吞形如
// port-security MAC（含 "aaa"/"loss" 子串的十六进制串）或四段键，
// 防止历史上多次踩过的 strings.Contains 模糊匹配回归。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// lqEnterInterface 进入 system view + 指定接口视图。
func lqEnterInterface(t *testing.T, state *CLIState, iface string) {
	t.Helper()
	if out := runOn(state, topology.DeviceRouter, "system-view"); !strings.Contains(out, "Enter system view") {
		t.Fatalf("enter system view failed: %q", out)
	}
	if out := runOn(state, topology.DeviceRouter, "interface "+iface); !strings.Contains(out, "Enter interface view") {
		t.Fatalf("enter interface %s failed: %q", iface, out)
	}
}

// —— eval ——

func TestValidateLinkDelay(t *testing.T) {
	cases := []struct {
		arg     string
		wantMs  int
		wantErr string // 期望错误文案关键字，空=应通过
	}{
		{"0", 0, ""},
		{"20", 20, ""},
		{"10000", 10000, ""},
		{"", 0, "incomplete command"},
		{"abc", 0, "invalid delay value"},
		{"-1", 0, "invalid delay value"},
		{"1.5", 0, "invalid delay value"},
		{"10001", 0, "out of range"},
		{"99999", 0, "out of range"},
		{"123456", 0, "invalid delay value"}, // 6 位超词法上限
	}
	for _, tc := range cases {
		ms, errMsg := ValidateLinkDelay(tc.arg)
		if tc.wantErr == "" {
			if errMsg != "" {
				t.Errorf("ValidateLinkDelay(%q) unexpected error %q", tc.arg, errMsg)
				continue
			}
			if ms != tc.wantMs {
				t.Errorf("ValidateLinkDelay(%q) = %d, want %d", tc.arg, ms, tc.wantMs)
			}
			continue
		}
		if !strings.Contains(errMsg, tc.wantErr) {
			t.Errorf("ValidateLinkDelay(%q) error = %q, want contains %q", tc.arg, errMsg, tc.wantErr)
		}
	}
}

func TestValidateLinkLoss(t *testing.T) {
	cases := []struct {
		arg     string
		wantPct float64
		wantErr string
	}{
		{"0", 0, ""},
		{"0.5", 0.5, ""},
		{"10", 10, ""},
		{"100", 100, ""},
		{"", 0, "incomplete command"},
		{"abc", 0, "invalid loss value"},
		{"-1", 0, "invalid loss value"},
		{"0.55", 0, "invalid loss value"}, // 仅允许一位小数
		{"100.1", 0, "out of range"},
		{"999", 0, "out of range"},
	}
	for _, tc := range cases {
		pct, errMsg := ValidateLinkLoss(tc.arg)
		if tc.wantErr == "" {
			if errMsg != "" {
				t.Errorf("ValidateLinkLoss(%q) unexpected error %q", tc.arg, errMsg)
				continue
			}
			if pct != tc.wantPct {
				t.Errorf("ValidateLinkLoss(%q) = %v, want %v", tc.arg, pct, tc.wantPct)
			}
			continue
		}
		if !strings.Contains(errMsg, tc.wantErr) {
			t.Errorf("ValidateLinkLoss(%q) error = %q, want contains %q", tc.arg, errMsg, tc.wantErr)
		}
	}
}

// TestFormatLinkLossDeterministic 锁死落盘串的确定性（saved-config 字节级稳定）。
func TestFormatLinkLossDeterministic(t *testing.T) {
	cases := map[float64]string{0: "0", 1: "1", 10: "10", 100: "100", 0.5: "0.5", 12.3: "12.3"}
	for in, want := range cases {
		if got := FormatLinkLoss(in); got != want {
			t.Errorf("FormatLinkLoss(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestParseInterfaceQualityKeyNoCollision 是键碰撞红线的静态守卫。
func TestParseInterfaceQualityKeyNoCollision(t *testing.T) {
	accept := map[string][2]string{
		"interface:GigabitEthernet0/0/1:delay": {"GigabitEthernet0/0/1", "delay"},
		"interface:GigabitEthernet0/0/1:loss":  {"GigabitEthernet0/0/1", "loss"},
		"interface:Eth-Trunk1:delay":           {"Eth-Trunk1", "delay"},
	}
	for key, want := range accept {
		iface, field, ok := parseInterfaceQualityKey(key)
		if !ok {
			t.Errorf("parseInterfaceQualityKey(%q) should accept", key)
			continue
		}
		if iface != want[0] || field != want[1] {
			t.Errorf("parseInterfaceQualityKey(%q) = (%q,%q), want (%q,%q)", key, iface, field, want[0], want[1])
		}
	}

	reject := []string{
		"",
		"interface",
		"interface:GE0/0/1",                      // 两段
		"interface::delay",                       // 空接口名
		"interface:GE0/0/1:delay-extra",          // 字段非精确匹配
		"interface:GE0/0/1:lossy",                // 字段非精确匹配
		"interface:GE0/0/1:lag:delay",            // 四段
		"interface:GE0/0/1:ipv6-enable",          // 其它特性键
		"interface:GE0/0/1:gre-keepalive-period", // GRE 键
		"port-security:mac:00e0-fc12-0aaa",       // MAC 串（历史 Contains 误伤源）
		"aaa:local-user:loss",                    // 非 interface 命名空间
		"delay",
		"loss",
	}
	for _, key := range reject {
		if _, _, ok := parseInterfaceQualityKey(key); ok {
			t.Errorf("parseInterfaceQualityKey(%q) must reject", key)
		}
	}
}

// TestLinkQualityKeysDoNotLeakOtherFeatures 验证 linkQualityInterfaces 只挑出
// 链路质量键，不把同接口的其它特性键当成配置项（否则 display 会凭空多出接口）。
func TestLinkQualityKeysDoNotLeakOtherFeatures(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig = map[string]string{
		"interface:GE0/0/1:delay":              "20",
		"interface:GE0/0/2:ipv6-enable":        "true",
		"interface:GE0/0/3:gre-source":         "1.1.1.1",
		"interface:GE0/0/4:lag:delay":          "5",
		"port-security:mac:00e0-fc12-0aaa":     "GE0/0/9",
		"interface:Eth-Trunk1:lacp-timeout":    "fast",
		"aaa:domain:default:authentication":    "local",
		"interface:GigabitEthernet0/0/5:loss":  "0.5",
		"interface:GigabitEthernet0/0/5:delay": "5",
	}
	got := linkQualityInterfaces(st)
	want := []string{"GE0/0/1", "GigabitEthernet0/0/5"}
	if len(got) != len(want) {
		t.Fatalf("linkQualityInterfaces = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("linkQualityInterfaces = %v, want %v (升序确定性)", got, want)
		}
	}
}

// TestLinkQualityUnsetVsExplicitZero 验证「未配置」与「显式配 0」可区分。
func TestLinkQualityUnsetVsExplicitZero(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	unset := interfaceLinkQuality(st, "GE0/0/1")
	if unset.Configured() {
		t.Fatalf("未配置接口不应 Configured")
	}
	if unset.DelayText() != "-" || unset.LossText() != "-" {
		t.Fatalf("未配置应渲染为 '-'，got delay=%q loss=%q", unset.DelayText(), unset.LossText())
	}

	st.DeviceConfig[linkDelayKey("GE0/0/1")] = "0"
	st.DeviceConfig[linkLossKey("GE0/0/1")] = "0"
	zero := interfaceLinkQuality(st, "GE0/0/1")
	if !zero.Configured() || !zero.HasDelay || !zero.HasLoss {
		t.Fatalf("显式配 0 应视为已配置，got %+v", zero)
	}
	if zero.DelayText() != "0" || zero.LossText() != "0" {
		t.Fatalf("显式配 0 应渲染为 '0'，got delay=%q loss=%q", zero.DelayText(), zero.LossText())
	}
}

func TestIsLinkQualityCommand(t *testing.T) {
	yes := []string{"delay 20", "loss 0.5", "DELAY 20", "undo delay", "undo loss", "delay"}
	for _, raw := range yes {
		if !IsLinkQualityCommand(ParseCommand(raw)) {
			t.Errorf("IsLinkQualityCommand(%q) = false, want true", raw)
		}
	}
	// 反向：无关命令绝不可触发链路同步（否则 REST 设置的 delay 会被清零）。
	no := []string{"display version", "undo shutdown", "vrrp vrid 1 preempt timer delay 60",
		"interface GigabitEthernet0/0/1", "system-view", "quit", ""}
	for _, raw := range no {
		if IsLinkQualityCommand(ParseCommand(raw)) {
			t.Errorf("IsLinkQualityCommand(%q) = true, want false", raw)
		}
	}
	if IsLinkQualityCommand(nil) {
		t.Errorf("IsLinkQualityCommand(nil) must be false")
	}
}

// —— cmd ——

func TestLinkQualityCmdWriteAndUndo(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	lqEnterInterface(t, st, "GigabitEthernet0/0/1")
	iface := st.CurrentSub

	if out := runOn(st, topology.DeviceRouter, "delay 20"); !strings.Contains(out, "set to 20 ms") {
		t.Fatalf("delay 20 echo = %q", out)
	}
	if v := st.DeviceConfig[linkDelayKey(iface)]; v != "20" {
		t.Fatalf("delay key = %q, want \"20\"", v)
	}
	if out := runOn(st, topology.DeviceRouter, "loss 0.5"); !strings.Contains(out, "set to 0.5%") {
		t.Fatalf("loss 0.5 echo = %q", out)
	}
	if v := st.DeviceConfig[linkLossKey(iface)]; v != "0.5" {
		t.Fatalf("loss key = %q, want \"0.5\"", v)
	}

	// 幂等覆盖：再配一次应覆盖而非追加。
	runOn(st, topology.DeviceRouter, "delay 35")
	if v := st.DeviceConfig[linkDelayKey(iface)]; v != "35" {
		t.Fatalf("delay overwrite = %q, want \"35\"", v)
	}

	// undo delay 只清 delay，不得连带清 loss。
	if out := runOn(st, topology.DeviceRouter, "undo delay"); !strings.Contains(out, "restored to default") {
		t.Fatalf("undo delay echo = %q", out)
	}
	if _, ok := st.DeviceConfig[linkDelayKey(iface)]; ok {
		t.Fatalf("undo delay 后 delay 键应删除")
	}
	if _, ok := st.DeviceConfig[linkLossKey(iface)]; !ok {
		t.Fatalf("undo delay 不得清除 loss 键")
	}
	if out := runOn(st, topology.DeviceRouter, "undo loss"); !strings.Contains(out, "restored to default") {
		t.Fatalf("undo loss echo = %q", out)
	}
	if _, ok := st.DeviceConfig[linkLossKey(iface)]; ok {
		t.Fatalf("undo loss 后 loss 键应删除")
	}
}

func TestLinkQualityCmdViewGuard(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	// 配置命令：顶层 case 可达，由 linkQualityViewGuard 给出引导文案。
	for _, raw := range []string{"delay 20", "loss 1"} {
		out := runOn(st, topology.DeviceRouter, raw)
		if !strings.Contains(out, "must be in interface view") {
			t.Errorf("system view %q = %q, want 'must be in interface view'", raw, out)
		}
	}
	// undo 命令：系统视图走 applyUndoSystemFeature，落到平台统一的
	// "undo 'x' is not supported" 文案 —— 与 undo shutdown / undo description
	// 等接口专属特性完全一致（已实测确认）。此处锁死该口径，避免后续被误
	// "修正"成 must be in interface view，与平台其它特性产生分叉。
	for _, raw := range []string{"undo delay", "undo loss"} {
		out := runOn(st, topology.DeviceRouter, raw)
		if !strings.Contains(out, "is not supported") {
			t.Errorf("system view %q = %q, want 'is not supported'（与 undo shutdown 同口径）", raw, out)
		}
	}
	if len(st.DeviceConfig) > 0 {
		for k := range st.DeviceConfig {
			if _, _, ok := parseInterfaceQualityKey(k); ok {
				t.Fatalf("视图守卫失败后不得写入链路质量键: %s", k)
			}
		}
	}
}

func TestLinkQualityCmdArgErrors(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	lqEnterInterface(t, st, "GigabitEthernet0/0/1")
	iface := st.CurrentSub

	cases := map[string]string{
		"delay":       "incomplete command",
		"delay 20 30": "too many parameters",
		"delay abc":   "invalid delay value",
		"delay 10001": "out of range",
		"loss":        "incomplete command",
		"loss 1 2":    "too many parameters",
		"loss 0.55":   "invalid loss value",
		"loss 101":    "out of range",
	}
	for raw, want := range cases {
		out := runOn(st, topology.DeviceRouter, raw)
		if !strings.Contains(out, want) {
			t.Errorf("%q = %q, want contains %q", raw, out, want)
		}
	}
	// 任何失败命令都不得留下键。
	if _, ok := st.DeviceConfig[linkDelayKey(iface)]; ok {
		t.Errorf("非法 delay 不应写键")
	}
	if _, ok := st.DeviceConfig[linkLossKey(iface)]; ok {
		t.Errorf("非法 loss 不应写键")
	}
}

// —— display ——

// lqTopoState 构造带拓扑上下文的状态：r1 GE0/0/1 <-> r2 GE0/0/2。
func lqTopoState(t *testing.T) *CLIState {
	t.Helper()
	topo := topology.NewTopology("lq", "link-quality")
	r1 := &topology.Device{ID: "r1", Name: "R1", Type: topology.DeviceRouter, Status: topology.StatusRunning}
	r1.InitializeDefaults()
	r2 := &topology.Device{ID: "r2", Name: "R2", Type: topology.DeviceRouter, Status: topology.StatusRunning}
	r2.InitializeDefaults()
	topo.AddDevice(r1)
	topo.AddDevice(r2)
	topo.AddLink(&topology.Link{
		ID:           "l1",
		SourceDevice: "r1", SourcePort: "GigabitEthernet0/0/1",
		TargetDevice: "r2", TargetPort: "GigabitEthernet0/0/2",
		LinkType: topology.LinkTypeBusiness,
		Delay:    20, Loss: 0.5,
	})
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceID = "r1"
	st.Topology = topo
	return st
}

func TestLinkQualityDisplayEmpty(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	out := runOn(st, topology.DeviceRouter, "display link-quality")
	if strings.Contains(out, "unrecognized command") || strings.Contains(out, "unknown command") {
		t.Fatalf("display link-quality 未接线: %q", out)
	}
	if !strings.Contains(out, "No link quality configured") {
		t.Fatalf("零配置输出 = %q", out)
	}
	// 仿真扩展声明必须出现（诚实原则：VRP 真机无此命令）。
	if !strings.Contains(out, "VRP 真机接口视图无此命令") {
		t.Fatalf("缺少仿真扩展声明: %q", out)
	}
}

func TestLinkQualityDisplayTable(t *testing.T) {
	st := lqTopoState(t)
	lqEnterInterface(t, st, "GigabitEthernet0/0/1")
	runOn(st, topology.DeviceRouter, "delay 20")
	runOn(st, topology.DeviceRouter, "loss 0.5")
	runOn(st, topology.DeviceRouter, "quit")

	out := runOn(st, topology.DeviceRouter, "display link-quality")
	for _, want := range []string{
		"Interface", "Delay(ms)", "Loss(%)", "Peer", "Measured", "Jitter",
		"GigabitEthernet0/0/1", "20", "0.5",
		"R2 GigabitEthernet0/0/2",                 // 对端解析
		"Total: 1 interface(s) with link quality", // 汇总
	} {
		if !strings.Contains(out, want) {
			t.Errorf("display link-quality 缺少 %q，实际:\n%s", want, out)
		}
	}
}

// TestLinkQualityDisplayHonestPlaceholder 诚实占位红线：lite 引擎不采集
// 逐链路实测丢包与抖动，单接口详情的 Measured / Jitter 必须恒为 "-"。
func TestLinkQualityDisplayHonestPlaceholder(t *testing.T) {
	st := lqTopoState(t)
	lqEnterInterface(t, st, "GigabitEthernet0/0/1")
	runOn(st, topology.DeviceRouter, "delay 20")
	runOn(st, topology.DeviceRouter, "loss 0.5")

	out := runOn(st, topology.DeviceRouter, "display link-quality interface GigabitEthernet0/0/1")
	for _, want := range []string{
		"One-way delay (ms)  : 20",
		"One-way loss (%)    : 0.5",
		"Measured loss (%)   : -",
		"Jitter (ms)         : -",
		"Applied to link     : l1 (delay 20 ms, loss 0.5%)",
		"Peer                : R2 GigabitEthernet0/0/2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("单接口详情缺少 %q，实际:\n%s", want, out)
		}
	}
}

func TestLinkQualityDisplayUnconfiguredInterface(t *testing.T) {
	st := lqTopoState(t)
	out := runOn(st, topology.DeviceRouter, "display link-quality interface GigabitEthernet0/0/1")
	if !strings.Contains(out, "No link quality configured on this interface") {
		t.Fatalf("未配置接口详情 = %q", out)
	}
	if !strings.Contains(out, "One-way delay (ms)  : -") {
		t.Fatalf("未配置应显示 '-'，实际:\n%s", out)
	}
}

// —— saved / current configuration ——

func TestLinkQualitySavedConfigInterfaceBlock(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	lqEnterInterface(t, st, "GigabitEthernet0/0/1")
	runOn(st, topology.DeviceRouter, "delay 20")
	runOn(st, topology.DeviceRouter, "loss 0.5")
	runOn(st, topology.DeviceRouter, "quit")

	out := runOn(st, topology.DeviceRouter, "display current-configuration")
	if !strings.Contains(out, " delay 20\n") {
		t.Errorf("current-configuration 缺少 ' delay 20'，实际:\n%s", out)
	}
	if !strings.Contains(out, " loss 0.5\n") {
		t.Errorf("current-configuration 缺少 ' loss 0.5'，实际:\n%s", out)
	}
	// 未配置的接口不得出现 delay/loss 行（VRP 只落差异值）。
	if strings.Count(out, " delay 20\n") != 1 {
		t.Errorf("delay 行应恰好出现一次，实际:\n%s", out)
	}
}

// TestLinkQualitySavedConfigReloadChannel 验证 reload 后（state.Interfaces 未重建）
// 独立输出通道能补齐 interface 块，配置不丢。
func TestLinkQualitySavedConfigReloadChannel(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	// 模拟 save -> reload：只有 DeviceConfig 键，state.Interfaces 中无该接口。
	st.DeviceConfig[linkDelayKey("GigabitEthernet9/9/9")] = "40"
	st.DeviceConfig[linkLossKey("GigabitEthernet9/9/9")] = "1.5"

	got := buildSavedLinkQualityConfig(st)
	for _, want := range []string{"interface GigabitEthernet9/9/9\n", " delay 40\n", " loss 1.5\n", "#\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("reload 补齐通道缺少 %q，实际:\n%s", want, got)
		}
	}
	// 已在 state.Interfaces 中的接口不得重复输出（避免与接口块双写）。
	st.Interfaces["GigabitEthernet9/9/9"] = &InterfaceConfig{Name: "GigabitEthernet9/9/9"}
	if again := buildSavedLinkQualityConfig(st); again != "" {
		t.Errorf("接口已在 state.Interfaces 时不应重复输出，实际:\n%s", again)
	}
}

// TestLinkQualitySavedConfigOnlyDiff 未配置时不得输出任何行。
func TestLinkQualitySavedConfigOnlyDiff(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	if got := buildSavedLinkQualityInterfaceConfig(st, "GigabitEthernet0/0/1"); got != "" {
		t.Fatalf("未配置应返回空串，got %q", got)
	}
	if got := buildSavedLinkQualityConfig(st); got != "" {
		t.Fatalf("未配置应返回空串，got %q", got)
	}
}

// —— 接线一致性 ——

// TestLinkQualityRegistryAndCompletion 锁死 display 注册与补全表的一致性：
// 历史上 displayRegistry 曾成为孤儿（只被补全引用、执行走 parser switch），
// 导致 dis evpn / dis ndp 无法执行。
func TestLinkQualityRegistryAndCompletion(t *testing.T) {
	if _, ok := displayRegistry["link-quality"]; !ok {
		t.Fatalf("displayRegistry 缺少 link-quality 注册")
	}
	// 补全表必须含 delay / loss，且与 parser 首别名一致。
	found := map[string]bool{}
	for _, c := range interfaceViewCommands {
		if isLinkQualityCommandName(c) {
			found[c] = true
		}
	}
	for _, want := range []string{"delay", "loss"} {
		if !found[want] {
			t.Errorf("interfaceViewCommands 缺少 %q（补全漂移）", want)
		}
	}
}

// TestLinkQualityDisplayDispatchedViaRegistry 验证 dis link-quality 能被执行
// （而非仅出现在补全里）。
func TestLinkQualityDisplayDispatchedViaRegistry(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	for _, raw := range []string{"display link-quality", "dis link-quality"} {
		out := runOn(st, topology.DeviceRouter, raw)
		if strings.Contains(out, "unrecognized command") || strings.Contains(out, "unknown command") {
			t.Errorf("%q 未被派发: %q", raw, out)
		}
	}
}
