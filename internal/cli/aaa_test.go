// aaa_test.go 是 AAA 本地认证的集成验收测试（P2 第八项，华为 VRP 课程 71，T8）。
//
// 覆盖 PRD §5 的 AC1–AC7：视图层级与 quit 弹回、DeviceConfig 单一事实源写入、
// save → reload 持久化贯通、旧形态下线且无残留写入路径、
// `authentication-mode` 顶层 case 视图分派（本期最高危代码冲突）、
// 引用完整性守卫、参数校验与守卫矩阵。
//
// AC8–AC13（display 忠实展示与确定性、口令脱敏、诚实占位、undo 语义、
// 能力守卫、键碰撞专项与纯函数合规）见 aaa_display_test.go。
// 纯函数层单测（键 helper / 收集器 / EvaluateAAA）见 aaa_eval_test.go。
//
// 🔴 本文件的全部 helper 一律使用 `aaaIT` 独占前缀并**自包含**，
// 不依赖 aaa_display_test.go 中的任何符号 —— 两个文件由不同工程师并行维护，
// 共享 helper 会在同包内产生 duplicate symbol 编译错误。
//
// 断言口径：一律使用 PRD §4 权威样例的**具体子串或逐字全等**，
// 禁止「返回非空」这类恒真断言。
package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— 独占测试脚手架（aaaIT 前缀）——

// aaaITDeviceName 是全部提示符断言使用的设备名（与 PRD §4 样例一致）。
const aaaITDeviceName = "R1"

// aaaITDevice 构造指定类型的裸设备态（停留在用户视图）。
func aaaITDevice(dt topology.DeviceType) *CLIState {
	st := NewCLIStateWithType(dt)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	st.DeviceName = aaaITDeviceName
	return st
}

// aaaITExec 在 state 自带的设备类型上执行一条原始命令并返回回显。
func aaaITExec(st *CLIState, line string) string {
	return ExecuteCommandOn(st, ParseCommand(line), st.DeviceType)
}

// aaaITExecAll 顺序执行多条命令，任一条返回 Error: 即判定用例失败。
//
// 用于「前置配置铺垫」：铺垫步骤本身出错必须立刻暴露，
// 而不是让后续断言以误导性的方式失败。
func aaaITExecAll(t *testing.T, st *CLIState, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if out := aaaITExec(st, line); strings.HasPrefix(out, "Error:") {
			t.Fatalf("前置命令 %q 意外失败：%s", line, out)
		}
	}
}

// aaaITSystemView 构造一台已进入系统视图的指定类型设备。
func aaaITSystemView(t *testing.T, dt topology.DeviceType) *CLIState {
	t.Helper()
	st := aaaITDevice(dt)
	aaaITExecAll(t, st, "system-view")
	if st.CurrentView != ViewSystem {
		t.Fatalf("system-view 后视图为 %v，期望 ViewSystem", st.CurrentView)
	}
	return st
}

// aaaITRouter 构造一台已进入系统视图的路由器（最常用起点）。
func aaaITRouter(t *testing.T) *CLIState {
	t.Helper()
	return aaaITSystemView(t, topology.DeviceRouter)
}

// aaaITMainlineSeq 是 PRD §4.1 课程 71 主线操作流（自系统视图起，结束回系统视图）。
var aaaITMainlineSeq = []string{
	"aaa",
	"local-user admin password cipher Huawei@123",
	"local-user admin privilege level 15",
	"local-user admin service-type telnet ssh",
	"local-user guest password cipher Guest@2026",
	"local-user guest privilege level 1",
	"local-user guest state block",
	"authentication-scheme sch1",
	"authentication-mode local",
	"quit",
	"domain huawei",
	"authentication-scheme sch1",
	"quit",
	"quit",
}

// aaaITRunMainline 在给定设备上走完 PRD §4.1 主线，任一步 Error: 即失败。
func aaaITRunMainline(t *testing.T, st *CLIState) {
	t.Helper()
	aaaITExecAll(t, st, aaaITMainlineSeq...)
	if st.CurrentView != ViewSystem {
		t.Fatalf("主线结束后视图为 %v，期望 ViewSystem", st.CurrentView)
	}
}

// aaaITKeys 返回 DeviceConfig 中**精确 "aaa:" 前缀**的键值对拷贝。
func aaaITKeys(st *CLIState) map[string]string {
	out := make(map[string]string)
	for k, v := range st.DeviceConfig {
		if strings.HasPrefix(k, aaaKeyPrefix()) {
			out[k] = v
		}
	}
	return out
}

// aaaITAssertNoKeys 断言不存在任何 "aaa:" 前缀键（拒错路径「失败必须不写键」）。
func aaaITAssertNoKeys(t *testing.T, st *CLIState, ctx string) {
	t.Helper()
	if got := aaaITKeys(st); len(got) != 0 {
		t.Errorf("%s：拒错路径不得写入任何 aaa: 键，实际写入 %v", ctx, got)
	}
}

// aaaITAssertKeyAbsent 断言指定键**不存在**（而非留空串）。
func aaaITAssertKeyAbsent(t *testing.T, st *CLIState, key, ctx string) {
	t.Helper()
	if v, ok := st.DeviceConfig[key]; ok {
		t.Errorf("%s：键 %q 应当不存在，实际值 %q", ctx, key, v)
	}
}

// aaaITAssertKey 断言指定键的值逐字相等。
func aaaITAssertKey(t *testing.T, st *CLIState, key, want, ctx string) {
	t.Helper()
	got, ok := st.DeviceConfig[key]
	if !ok {
		t.Errorf("%s：键 %q 缺失（期望 %q）", ctx, key, want)
		return
	}
	if got != want {
		t.Errorf("%s：键 %q = %q，期望 %q", ctx, key, got, want)
	}
}

// aaaITContains 断言输出含指定子串。
func aaaITContains(t *testing.T, out, want, ctx string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("%s：输出缺少子串 %q\n---实际输出---\n%s", ctx, want, out)
	}
}

// aaaITNotContains 断言输出不含指定子串。
func aaaITNotContains(t *testing.T, out, bad, ctx string) {
	t.Helper()
	if strings.Contains(out, bad) {
		t.Errorf("%s：输出不得含子串 %q\n---实际输出---\n%s", ctx, bad, out)
	}
}

// aaaITSource 读取 internal/cli 下源文件的**剔除注释后**代码文本（静态断言用）。
//
// 必须剔除注释：实现文件的红线注释里逐字写了被禁的反例
// （如 `strings.Contains(k, "aaa")`、`state.LocalUsers`），
// 裸做子串扫描会被注释自我命中（假阳性）。
// 同时剔除行尾注释，覆盖 `code // 说明` 形态。
func aaaITSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取源文件 %s 失败：%v", name, err)
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if idx := strings.Index(ln, "//"); idx >= 0 {
			// 仅当 "//" 之前的引号成对时才视为注释起点，避免误伤字符串字面量。
			if strings.Count(ln[:idx], `"`)%2 == 0 {
				ln = ln[:idx]
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// —— AC1：AAA 视图与视图层级正确（P0-1）——

// TestAAAITViewHierarchyAndQuit 逐字校验 PRD §4.1 的提示符演进与 quit 弹回层级。
//
// 🔴 ③ 是本条核心：子视图 quit 必须回 [R1-aaa]，**不是** [R1]
// （直击 parser.go quit if-else 链末尾 else 越级弹回的隐患）。
func TestAAAITViewHierarchyAndQuit(t *testing.T) {
	st := aaaITRouter(t)

	// ① 系统视图 aaa → ViewAAA + [R1-aaa]
	aaaITExecAll(t, st, "aaa")
	if st.CurrentView != ViewAAA {
		t.Fatalf("① 期望 ViewAAA，实际 %v", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1-aaa]" {
		t.Errorf("① 提示符 = %q，期望 %q", got, "[R1-aaa]")
	}

	// ② authentication-scheme sch1 → [R1-aaa-authen-sch1]
	aaaITExecAll(t, st, "authentication-scheme sch1")
	if st.CurrentView != ViewAAAAuthen {
		t.Fatalf("② 期望 ViewAAAAuthen，实际 %v", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1-aaa-authen-sch1]" {
		t.Errorf("② 提示符 = %q，期望 %q", got, "[R1-aaa-authen-sch1]")
	}

	// ③ 🔴 方案子视图 quit → 必须回 [R1-aaa]（不是 [R1]）
	aaaITExec(st, "quit")
	if st.CurrentView != ViewAAA {
		t.Fatalf("③ 方案子视图 quit 必须落在 ViewAAA，实际 %v（越级弹回缺陷）", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1-aaa]" {
		t.Errorf("③ quit 后提示符 = %q，期望 %q（越级弹回缺陷）", got, "[R1-aaa]")
	}

	// ④ domain huawei → [R1-aaa-domain-huawei]；quit → [R1-aaa]
	aaaITExecAll(t, st, "domain huawei")
	if st.CurrentView != ViewAAADomain {
		t.Fatalf("④ 期望 ViewAAADomain，实际 %v", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1-aaa-domain-huawei]" {
		t.Errorf("④ 提示符 = %q，期望 %q", got, "[R1-aaa-domain-huawei]")
	}
	aaaITExec(st, "quit")
	if st.CurrentView != ViewAAA {
		t.Fatalf("④ 域子视图 quit 必须落在 ViewAAA，实际 %v", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1-aaa]" {
		t.Errorf("④ 域 quit 后提示符 = %q，期望 %q", got, "[R1-aaa]")
	}

	// ⑤ [R1-aaa] quit → [R1]
	aaaITExec(st, "quit")
	if st.CurrentView != ViewSystem {
		t.Fatalf("⑤ ViewAAA quit 必须落在 ViewSystem，实际 %v", st.CurrentView)
	}
	if got := GetPrompt(st, aaaITDeviceName); got != "[R1]" {
		t.Errorf("⑤ 提示符 = %q，期望 %q", got, "[R1]")
	}
}

// TestAAAITReturnFromSubViews 验证 ⑥ 任意 AAA 子视图 `return` 均回 ViewUser（既有行为不变）。
func TestAAAITReturnFromSubViews(t *testing.T) {
	cases := []struct {
		name string
		seq  []string
	}{
		{"AAA 视图", []string{"aaa"}},
		{"方案子视图", []string{"aaa", "authentication-scheme sch1"}},
		{"域子视图", []string{"aaa", "domain huawei"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := aaaITRouter(t)
			aaaITExecAll(t, st, tc.seq...)
			aaaITExec(st, "return")
			if st.CurrentView != ViewUser {
				t.Errorf("%s return 后期望 ViewUser，实际 %v", tc.name, st.CurrentView)
			}
		})
	}
}

// —— AC2：事实源写入（P0-2 / P0-3 / P0-4 / P0-5 / P0-6 / P0-7）——

// TestAAAITFactSourceKeys 走完主线后逐键断言 DeviceConfig 单一事实源。
func TestAAAITFactSourceKeys(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DeviceRouter, topology.DeviceL3Switch} {
		t.Run(string(dt), func(t *testing.T) {
			st := aaaITSystemView(t, dt)
			aaaITRunMainline(t, st)

			aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldPassword), "Huawei@123", "AC2")
			aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldPrivilege), "15", "AC2")
			aaaITAssertKey(t, st, aaaLocalUserKey("guest", aaaFieldPassword), "Guest@2026", "AC2")
			aaaITAssertKey(t, st, aaaLocalUserKey("guest", aaaFieldPrivilege), "1", "AC2")
			aaaITAssertKey(t, st, aaaLocalUserKey("guest", aaaFieldState), AAAUserStateBlock, "AC2")
			aaaITAssertKey(t, st, aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "local", "AC2")
			aaaITAssertKey(t, st, aaaDomainKey("huawei", aaaFieldAuthenScheme), "sch1", "AC2")

			svc := st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldServiceType)]
			if !strings.Contains(svc, "telnet") || !strings.Contains(svc, "ssh") {
				t.Errorf("AC2：admin service-type = %q，期望同时含 telnet 与 ssh", svc)
			}

			// 生效缺省 active 不落盘（拍板 C8）：admin 未配 state，键必须不存在。
			aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldState), "AC2 缺省 active 不落盘")
		})
	}
}

// TestAAAITServiceTypeIsDeterministicAndOverriding 验证 service-type 的
// 覆盖语义与固定枚举排序（C6）：与输入顺序无关，且后一次配置整体覆盖前一次。
func TestAAAITServiceTypeIsDeterministicAndOverriding(t *testing.T) {
	key := aaaLocalUserKey("u1", aaaFieldServiceType)

	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa", "local-user u1 service-type ssh telnet")
	first := st.DeviceConfig[key]

	st2 := aaaITRouter(t)
	aaaITExecAll(t, st2, "aaa", "local-user u1 service-type telnet ssh")
	if second := st2.DeviceConfig[key]; second != first {
		t.Errorf("service-type 落盘与输入顺序有关：%q vs %q（确定性红线）", first, second)
	}
	if first != "telnet ssh" {
		t.Errorf("service-type 落盘 = %q，期望按 AAAServiceTypeOrder 规范化为 %q", first, "telnet ssh")
	}

	// 覆盖语义：再配一次仅 ftp，旧值必须整体被替换而非追加。
	aaaITExecAll(t, st, "local-user u1 service-type ftp")
	if got := st.DeviceConfig[key]; got != "ftp" {
		t.Errorf("service-type 覆盖语义失效：= %q，期望 %q", got, "ftp")
	}
}

// —— AC3：save → reload 持久化贯通（现状 100% 丢失，本期最大价值点）——

// aaaITPRDSnapshotBlock 是 PRD §4.5 权威样例的 AAA 快照块（不含首尾 "#" 分隔行）。
const aaaITPRDSnapshotBlock = "" +
	"aaa\n" +
	" authentication-scheme default\n" +
	" authentication-scheme sch1\n" +
	"  authentication-mode local\n" +
	" local-user admin password cipher ****\n" +
	" local-user admin privilege level 15\n" +
	" local-user admin service-type telnet ssh\n" +
	" local-user guest password cipher ****\n" +
	" local-user guest privilege level 1\n" +
	" local-user guest state block\n" +
	" domain huawei\n" +
	"  authentication-scheme sch1\n"

// aaaITPRDScenario 构造 PRD §4.5 权威样例的完整场景：主线 + default 方案。
//
// default 方案只创建、不配 mode —— 用于验证「存在性标记（空串）不输出 mode 子行、
// 显式配置过的 sch1 才输出 authentication-mode local 子行」这一保真判据。
func aaaITPRDScenario(t *testing.T) *CLIState {
	t.Helper()
	st := aaaITRouter(t)
	aaaITRunMainline(t, st)
	aaaITExecAll(t, st, "aaa", "authentication-scheme default", "quit", "quit")
	return st
}

// TestAAAITPersistenceRoundTrip 验证 AAA 配置经 SerializeToDeviceConfigData →
// NewCLIStateFromDeviceConfig 往返后**逐键完全一致**，且三类 display 完整复现。
func TestAAAITPersistenceRoundTrip(t *testing.T) {
	st := aaaITPRDScenario(t)
	dt := st.DeviceType

	before := aaaITKeys(st)
	if len(before) == 0 {
		t.Fatalf("AC3 前置失效：没有任何 aaa: 键可供持久化")
	}
	snapBefore := aaaITExec(st, "display current-configuration")

	cfg := st.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(dt, cfg, aaaITDeviceName)

	// ① aaa: 精确前缀键集逐键完全一致
	after := aaaITKeys(reloaded)
	if len(before) != len(after) {
		t.Errorf("AC3①：aaa: 键数量 %d → %d\nbefore=%v\nafter=%v",
			len(before), len(after), before, after)
	}
	for k, v := range before {
		got, ok := after[k]
		if !ok {
			t.Errorf("AC3①：键 %q 在 reload 后丢失", k)
			continue
		}
		if got != v {
			t.Errorf("AC3①：键 %q reload 后 = %q，期望 %q", k, got, v)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("AC3①：reload 后凭空多出键 %q", k)
		}
	}

	// ② display local-user 完整复现两个用户及其 privilege / service-type / state
	users := aaaITExec(reloaded, "display local-user")
	aaaITContains(t, users, "admin", "AC3②")
	aaaITContains(t, users, "guest", "AC3②")
	aaaITContains(t, users, "15", "AC3② admin privilege")
	aaaITContains(t, users, "telnet ssh", "AC3② admin service-type")
	aaaITContains(t, users, "Block", "AC3② guest state")
	aaaITContains(t, users, "Total 2 user(s)", "AC3②")

	// ③ display domain huawei 复现绑定（含跨对象解引用）
	dom := aaaITExec(reloaded, "display domain huawei")
	aaaITContains(t, dom, "huawei", "AC3③")
	aaaITContains(t, dom, "sch1  (mode: local)", "AC3③ 跨对象解引用")

	// ④ display current-configuration 复现 §4.5 全部行，且两次快照字节级一致
	snapAfter := aaaITExec(reloaded, "display current-configuration")
	if !strings.Contains(snapAfter, aaaITPRDSnapshotBlock) {
		t.Errorf("AC3④：reload 后快照缺少 PRD §4.5 AAA 块\n---期望---\n%s\n---实际---\n%s",
			aaaITPRDSnapshotBlock, snapAfter)
	}
	if snapBefore != snapAfter {
		t.Errorf("AC3④：save/reload 前后快照不一致\n---before---\n%s\n---after---\n%s",
			snapBefore, snapAfter)
	}
}

// TestAAAITSnapshotSchemeSubLineFidelity 单独钉死方案 mode 子行的保真判据。
//
// 🔴 判据是「mode 是否被**显式配置过**」（原始键值非空），
// **不是**「mode 是否等于缺省值」—— 后者会把显式配置的 `authentication-mode local`
// 静默吞掉，与 PRD §4.5（default 无子行、sch1 有子行）冲突。
func TestAAAITSnapshotSchemeSubLineFidelity(t *testing.T) {
	st := aaaITPRDScenario(t)
	snap := aaaITExec(st, "display current-configuration")

	// default：只创建未配 mode → 无子行
	aaaITContains(t, snap,
		" authentication-scheme default\n authentication-scheme sch1\n",
		"AC3 default 未显式配 mode 时不得输出子行")
	// sch1：显式配置 local（恰等于缺省值）→ 必须有子行
	aaaITContains(t, snap,
		" authentication-scheme sch1\n  authentication-mode local\n",
		"AC3 sch1 显式配 mode 必须输出子行")

	// 对照断言：空配置设备的快照不含任何 AAA 块（证明缺陷确被修复）。
	fresh := aaaITRouter(t)
	if out := aaaITExec(fresh, "display current-configuration"); strings.Contains(out, "\naaa\n") {
		t.Errorf("AC3 对照：空配置设备的快照不应含 AAA 块，实际：\n%s", out)
	}
	if out := buildAAALocalUserDisplay(fresh); !strings.Contains(out, "No local user configured") {
		t.Errorf("AC3 对照：空配置设备应无本地用户，实际：\n%s", out)
	}
}

// —— AC4：旧形态已下线，且无残留写入路径（P0-2 / P0-11）——

// TestAAAITLegacySystemViewLocalUserRejected 验证系统视图下 local-user 被引导到 AAA 视图，
// 且**不写任何 aaa: 键**；文案不得再是与真机相反的 "must be in system view"。
func TestAAAITLegacySystemViewLocalUserRejected(t *testing.T) {
	st := aaaITRouter(t)
	out := aaaITExec(st, "local-user admin password cipher Huawei@123")

	if out != ErrAAAViewFirst {
		t.Errorf("AC4①：回显 = %q，期望逐字 %q", out, ErrAAAViewFirst)
	}
	aaaITContains(t, out, "AAA view", "AC4①")
	aaaITNotContains(t, out, "must be in system view", "AC4① 与真机相反的教错文案必须下线")
	aaaITAssertNoKeys(t, st, "AC4① 拒错路径")
}

// TestAAAITNoLegacyFactSourceResidue 静态断言 state.LocalUsers 结构体事实源已彻底废弃，
// 且不存在自造欢快回显 "Local user ... created"。
func TestAAAITNoLegacyFactSourceResidue(t *testing.T) {
	files := []string{"state.go", "parser.go", "aaa_eval.go", "aaa_cmd.go", "aaa_display.go"}
	reCreated := regexp.MustCompile(`"Local user [^"]*created`)
	for _, f := range files {
		src := aaaITSource(t, f)
		// ② state.LocalUsers 零命中
		if strings.Contains(src, "state.LocalUsers") || strings.Contains(src, "s.LocalUsers") {
			t.Errorf("AC4②：%s 仍引用 LocalUsers 结构体事实源", f)
		}
		// ③ 自造回显零命中
		if hit := reCreated.FindString(src); hit != "" {
			t.Errorf("AC4③：%s 含自造欢快回显 %q", f, hit)
		}
	}
	// state.go 不得声明任何 AAA / LocalUser / Domain / Scheme 内嵌结构体（架构铁律）
	stateSrc := aaaITSource(t, "state.go")
	for _, bad := range []string{"LocalUser", "AAAConfig", "DomainConfig", "SchemeConfig"} {
		if strings.Contains(stateSrc, bad) {
			t.Errorf("AC4/AC13：state.go 不得声明 %q（事实源单一铁律）", bad)
		}
	}
}

// —— AC5：authentication-mode 顶层 case 视图分派（P0-8，本期最高危代码冲突）——

// TestAAAITAuthModeAAAPath 验证 AC5a：AAA 路径写入方案 mode 键。
func TestAAAITAuthModeAAAPath(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa", "authentication-scheme sch1", "authentication-mode local")
	aaaITAssertKey(t, st, aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "local", "AC5a")
}

// TestAAAITAuthModeVTYZeroRegression 验证 AC5b：VTY 既有行为**逐字不变**（零回归红线）。
func TestAAAITAuthModeVTYZeroRegression(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "user-interface vty 0 4")
	if st.CurrentView != ViewVTY {
		t.Fatalf("AC5b：期望 ViewVTY，实际 %v", st.CurrentView)
	}
	for _, mode := range []string{"aaa", "password", "none"} {
		out := aaaITExec(st, "authentication-mode "+mode)
		want := fmt.Sprintf("Authentication-mode set to %s", mode)
		if out != want {
			t.Errorf("AC5b：authentication-mode %s → %q，期望逐字 %q", mode, out, want)
		}
		if st.VTY.AuthenticationMode != mode {
			t.Errorf("AC5b：state.VTY.AuthenticationMode = %q，期望 %q", st.VTY.AuthenticationMode, mode)
		}
	}
	// 非法值仍返回原 usage 文案（逐字不变）
	wantUsage := "Error: usage: authentication-mode aaa|password|none"
	if out := aaaITExec(st, "authentication-mode bogus"); out != wantUsage {
		t.Errorf("AC5b：非法值 → %q，期望逐字 %q", out, wantUsage)
	}
	// VTY 路径不得写入任何 aaa: 键
	aaaITAssertNoKeys(t, st, "AC5b VTY 路径")
}

// TestAAAITAuthModeInAAAViewWithoutScheme 验证 AC5c：AAA 视图直接敲 authentication-mode
// 被引导到 authentication-scheme，且不写任何键。
func TestAAAITAuthModeInAAAViewWithoutScheme(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa")
	out := aaaITExec(st, "authentication-mode local")
	if out != ErrAuthSchemeFirst {
		t.Errorf("AC5c：回显 = %q，期望逐字 %q", out, ErrAuthSchemeFirst)
	}
	aaaITContains(t, out, "authentication-scheme", "AC5c")
	aaaITAssertNoKeys(t, st, "AC5c 拒错路径")
}

// TestAAAITAuthModeOutsideAnyView 验证既有 VTY 守卫文案在非 AAA / 非 VTY 视图下保持。
func TestAAAITAuthModeOutsideAnyView(t *testing.T) {
	st := aaaITRouter(t)
	if out := aaaITExec(st, "authentication-mode aaa"); out != ErrMustBeInVTY {
		t.Errorf("AC5：系统视图 authentication-mode → %q，期望逐字 %q", out, ErrMustBeInVTY)
	}
	aaaITAssertNoKeys(t, st, "AC5 系统视图拒错路径")
}

// TestAAAITAuthModeCaseUniqueness 验证 AC5d：顶层 switch 中
// `case "authentication-mode"` **有且仅有 1 处**（编译期 duplicate case 防线），
// 且 VRRP 内层 case 仍然存在（逐字未改，零回归）。
func TestAAAITAuthModeCaseUniqueness(t *testing.T) {
	src := aaaITSource(t, "parser.go")

	// 🔴 不能用缩进区分：两处 case 都位于各自函数体内的一级 switch，缩进同为 1 个 tab。
	// 唯一可靠的判据是**外层函数**：顶层分派在 ExecuteCommandOn，VRRP 在 applyVRRP。
	reFunc := regexp.MustCompile(`^func\s+(\w+)`)
	byFunc := map[string]int{}
	current := ""
	for _, ln := range strings.Split(src, "\n") {
		if m := reFunc.FindStringSubmatch(ln); m != nil {
			current = m[1]
		}
		if strings.Contains(ln, `case "authentication-mode"`) {
			byFunc[current]++
		}
	}

	if got := byFunc["ExecuteCommandOn"]; got != 1 {
		t.Errorf("AC5d：ExecuteCommandOn 顶层 switch 中 case \"authentication-mode\" 出现 %d 次，必须恰为 1 次"+
			"（新增同名 case 会触发 Go 编译期 duplicate case 错误）", got)
	}
	if got := byFunc["applyVRRP"]; got != 1 {
		t.Errorf("AC5d：applyVRRP 内层 case \"authentication-mode\" 出现 %d 次，期望 1 次（零回归被破坏）", got)
	}
	total := 0
	for _, n := range byFunc {
		total += n
	}
	if total != 2 {
		t.Errorf("AC5d：parser.go 中 case \"authentication-mode\" 共出现 %d 次，期望恰为 2 次"+
			"（ExecuteCommandOn 顶层分派 + applyVRRP 内层），实际分布 %v", total, byFunc)
	}
}

// —— AC6：引用完整性守卫（P0-10 / P1-2，最高教学价值）——

// TestAAAITBindNonexistentSchemeRejected 验证 ①：域子视图绑定不存在的方案必须硬拒绝，
// **且不写绑定键、不隐式创建方案**（证明未静默成功）。
func TestAAAITBindNonexistentSchemeRejected(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa", "domain huawei")

	out := aaaITExec(st, "authentication-scheme nosuch")
	want := fmt.Sprintf(ErrSchemeNotExist, "nosuch")
	if out != want {
		t.Errorf("AC6①：回显 = %q，期望逐字 %q", out, want)
	}
	aaaITContains(t, out, "does not exist", "AC6①")

	// 绑定键未写入
	aaaITAssertKeyAbsent(t, st, aaaDomainKey("huawei", aaaFieldAuthenScheme), "AC6① 绑定键不得写入")
	// 未隐式创建方案
	aaaITAssertKeyAbsent(t, st,
		aaaSchemeKey(AAASchemeKindAuthen, "nosuch", aaaFieldMode), "AC6① 不得隐式创建方案")
	for _, s := range EvaluateAAA(st).AuthenSchemes {
		if s.Name == "nosuch" {
			t.Errorf("AC6①：方案 nosuch 被隐式创建（幽灵对象）")
		}
	}
}

// TestAAAITBindExistingSchemeSucceeds 验证 ②：先建方案再绑定 → 成功且键正确。
func TestAAAITBindExistingSchemeSucceeds(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st,
		"aaa",
		"authentication-scheme sch1", "quit",
		"domain huawei", "authentication-scheme sch1",
	)
	aaaITAssertKey(t, st, aaaDomainKey("huawei", aaaFieldAuthenScheme), "sch1", "AC6②")
}

// TestAAAITDeleteReferencedSchemeRejected 验证 ③：删除仍被域引用的方案必须拒绝，
// 且方案键保留（拍板 C7）。
func TestAAAITDeleteReferencedSchemeRejected(t *testing.T) {
	st := aaaITRouter(t)
	aaaITRunMainline(t, st) // 主线已建立 huawei → sch1 的引用
	aaaITExecAll(t, st, "aaa")

	out := aaaITExec(st, "undo authentication-scheme sch1")
	want := fmt.Sprintf(ErrSchemeReferenced, "sch1", "huawei")
	if out != want {
		t.Errorf("AC6③：回显 = %q，期望逐字 %q", out, want)
	}
	aaaITContains(t, out, "is referenced by domain", "AC6③")

	// 方案键必须保留
	aaaITAssertKey(t, st, aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "local", "AC6③ 方案键保留")

	// 解除引用后方可删除
	aaaITExecAll(t, st, "domain huawei", "undo authentication-scheme", "quit")
	if out := aaaITExec(st, "undo authentication-scheme sch1"); strings.HasPrefix(out, "Error:") {
		t.Errorf("AC6③：解除引用后删除方案仍失败：%s", out)
	}
	aaaITAssertKeyAbsent(t, st,
		aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "AC6③ 解除引用后方案应被删除")
}

// —— AC7：参数校验与守卫矩阵（P0-4 / P0-5 / P0-6 / P0-7 / P0-11）——

// TestAAAITPrivilegeValidation 验证 ①：privilege level 越界 / 非数字一律硬拒绝且不写键。
func TestAAAITPrivilegeValidation(t *testing.T) {
	for _, bad := range []string{"16", "-1", "abc", "999"} {
		t.Run(bad, func(t *testing.T) {
			st := aaaITRouter(t)
			aaaITExecAll(t, st, "aaa")
			out := aaaITExec(st, "local-user admin privilege level "+bad)
			if out != ErrPrivilegeRange {
				t.Errorf("AC7①：privilege level %s → %q，期望逐字 %q", bad, out, ErrPrivilegeRange)
			}
			aaaITContains(t, out, "between 0 and 15", "AC7①")
			aaaITAssertKeyAbsent(t, st,
				aaaLocalUserKey("admin", aaaFieldPrivilege), "AC7① 越界值不得写入")
		})
	}
	// 边界内合法值必须放行
	for _, ok := range []string{"0", "15"} {
		st := aaaITRouter(t)
		aaaITExecAll(t, st, "aaa", "local-user admin privilege level "+ok)
		aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldPrivilege), ok, "AC7① 合法边界值")
	}
}

// TestAAAITServiceTypeValidation 验证 ②：非法 service-type 硬拒绝且不写键。
func TestAAAITServiceTypeValidation(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa")

	out := aaaITExec(st, "local-user admin service-type vnc")
	want := fmt.Sprintf(ErrServiceType, "vnc")
	if out != want {
		t.Errorf("AC7②：回显 = %q，期望逐字 %q", out, want)
	}
	aaaITContains(t, out, "Invalid service-type", "AC7②")
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldServiceType), "AC7② 非法值不得写入")

	// 合法列表中混入一个非法 token → 整体拒绝，不得部分写入
	st2 := aaaITRouter(t)
	aaaITExecAll(t, st2, "aaa")
	if out := aaaITExec(st2, "local-user admin service-type telnet vnc"); !strings.Contains(out, "Invalid service-type") {
		t.Errorf("AC7②：混入非法 token 未整体拒绝，回显 %q", out)
	}
	aaaITAssertKeyAbsent(t, st2, aaaLocalUserKey("admin", aaaFieldServiceType), "AC7② 不得部分写入")
}

// TestAAAITStateValidation 验证 ③：非法 state 返回 usage 且列出 active / block。
func TestAAAITStateValidation(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa")

	out := aaaITExec(st, "local-user admin state enabled")
	if out != ErrStateUsage {
		t.Errorf("AC7③：回显 = %q，期望逐字 %q", out, ErrStateUsage)
	}
	for _, want := range []string{"usage:", "active", "block"} {
		aaaITContains(t, out, want, "AC7③")
	}
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldState), "AC7③ 非法状态不得写入")

	// active 是生效缺省 → 显式配置也不落盘（拍板 C8）
	aaaITExecAll(t, st, "local-user admin state active")
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldState), "AC7③ 缺省 active 不落盘")
	// block 落盘
	aaaITExecAll(t, st, "local-user admin state block")
	aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldState), AAAUserStateBlock, "AC7③ block 落盘")
	// 再改回 active → 键被清除而非留空串
	aaaITExecAll(t, st, "local-user admin state active")
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldState), "AC7③ 回到缺省应清键")
}

// TestAAAITPasswordValidation 验证 ④：口令长度越界硬拒绝且不写键（拍板 C5）。
func TestAAAITPasswordValidation(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa")

	// 过短
	out := aaaITExec(st, "local-user admin password cipher 123")
	if out != ErrPasswordLength {
		t.Errorf("AC7④：短口令回显 = %q，期望逐字 %q", out, ErrPasswordLength)
	}
	aaaITContains(t, out, "length must be between 8 and 128", "AC7④")
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldPassword), "AC7④ 短口令不得写入")

	// 过长
	long := strings.Repeat("a", AAAPasswordMaxLen+1)
	if out := aaaITExec(st, "local-user admin password cipher "+long); out != ErrPasswordLength {
		t.Errorf("AC7④：超长口令回显 = %q，期望逐字 %q", out, ErrPasswordLength)
	}
	aaaITAssertKeyAbsent(t, st, aaaLocalUserKey("admin", aaaFieldPassword), "AC7④ 超长口令不得写入")

	// 边界内合法（恰好 8 位 / 恰好 128 位）
	edge := strings.Repeat("b", AAAPasswordMinLen)
	aaaITExecAll(t, st, "local-user admin password cipher "+edge)
	aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldPassword), edge, "AC7④ 下边界合法")
	maxPwd := strings.Repeat("c", AAAPasswordMaxLen)
	aaaITExecAll(t, st, "local-user admin password cipher "+maxPwd)
	aaaITAssertKey(t, st, aaaLocalUserKey("admin", aaaFieldPassword), maxPwd, "AC7④ 上边界合法")
}

// TestAAAITUnknownAttributeCreatesNoGhostUser 验证 ⑤：打错子属性必须
// `unrecognized command`，**且不得凭空产生幽灵用户**（一个 aaa:local-user:admin:* 键都不许有）。
func TestAAAITUnknownAttributeCreatesNoGhostUser(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa")

	out := aaaITExec(st, "local-user admin foobar x")
	if out != ErrUnrecognized {
		t.Errorf("AC7⑤：回显 = %q，期望逐字 %q", out, ErrUnrecognized)
	}
	aaaITContains(t, out, "unrecognized command", "AC7⑤")

	prefix := aaaEntityPrefix(aaaLocalUserPrefix, "admin")
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			t.Errorf("AC7⑤：打错字产生了幽灵用户键 %q", k)
		}
	}
	aaaITAssertNoKeys(t, st, "AC7⑤ 拒错路径")
	if out := aaaITExec(st, "display local-user"); strings.Contains(out, "admin") {
		t.Errorf("AC7⑤：display local-user 出现幽灵用户 admin：\n%s", out)
	}
}

// TestAAAITMissingArgsUsage 验证 ⑥：缺参一律返回可操作的 usage / unrecognized，且不写键。
func TestAAAITMissingArgsUsage(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"local-user", ErrLocalUserUsage},
		{"local-user admin", ErrLocalUserUsage},
		{"local-user admin password", ErrPasswordUsage},
		{"local-user admin password cipher", ErrPasswordUsage},
		// privilege 缺 level 关键字 / 缺取值 → 缺参，按 AC7⑥ 统一口径给 usage:。
		{"local-user admin privilege", ErrPrivilegeUsage},
		{"local-user admin privilege level", ErrPrivilegeUsage},
		{"local-user admin state", ErrStateUsage},
		// service-type 缺参走独立 usage 文案（不得复用 ErrServiceType 渲染出空类型名）。
		{"local-user admin service-type", ErrServiceTypeUsage},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			st := aaaITRouter(t)
			aaaITExecAll(t, st, "aaa")
			out := aaaITExec(st, c.line)
			if out != c.want {
				t.Errorf("AC7⑥：%q → %q，期望逐字 %q", c.line, out, c.want)
			}
			if !strings.HasPrefix(out, "Error:") {
				t.Errorf("AC7⑥：%q 未以 Error: 硬拒绝，回显 %q", c.line, out)
			}
			aaaITAssertNoKeys(t, st, "AC7⑥ "+c.line)
		})
	}
}

// TestAAAITSchemeModeValidation 验证方案 *-mode 的取值守卫：
// 非法模式返回 usage 且**不改写**已有 mode 值。
func TestAAAITSchemeModeValidation(t *testing.T) {
	st := aaaITRouter(t)
	aaaITExecAll(t, st, "aaa", "authentication-scheme sch1", "authentication-mode local")

	out := aaaITExec(st, "authentication-mode bogus")
	if !strings.HasPrefix(out, "Error: usage:") {
		t.Errorf("AC7：非法方案模式 → %q，期望以 %q 开头", out, "Error: usage:")
	}
	// 已有合法值不得被非法输入污染
	aaaITAssertKey(t, st,
		aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "local", "AC7 非法模式不得改写已有值")
}
