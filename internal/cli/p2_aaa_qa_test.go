// p2_aaa_qa_test.go 是 P2 第八项「AAA 本地认证」（华为 VRP 课程 71）的
// **QA 独立验收测试**（设计 T9），由 QA 工程师 严过关 独立于工程师自测编写。
//
// 立场：不信任工程师自测结论，全部断言基于 PRD §4（输出权威源）/ §5（AC1–AC13）
// / §6（铁律红线）与设计 §0（拍板 C1–C10）/ §0.1（裁决 A1–A12）独立推导。
//
// 覆盖重点（按风险从高到低）：
//   - AC13 键碰撞专项：端口安全粘滞 MAC（含 0aaa / aaaa-bbbb-cccc）与 LAG 键
//     必须零误伤；collectAAALocalUsers 不得产生 MAC 派生的幽灵用户。
//   - AC10 诚实占位：7 个运行态字段恒 "-"，无伪造数字 / 时间 / 会话 / 密文。
//   - AC9  口令脱敏：四路 display 明文零泄漏，恒 "****"。
//   - AC5  authentication-mode 视图分派（本期最高危代码冲突）+ VTY 逐字零回归。
//   - AC1  quit 层级（子视图不得越级弹回）。
//   - AC3  save→reload 贯通 + PRD §4.5 逐字一致。
//   - AC8  确定性 + 死字段假 0 修复。
//   - AC12 能力守卫 + capabilities.go 零改动。
//   - AC7  参数守卫（含缺参 usage 口径）。
//   - 跨特性零回归：undo aaa 后既有 GRE / LAG / 端口安全 / VRRP / STP 配置逐字不变。
//
// 🔴 本文件全部 helper 使用 `qaAAA` 独占前缀并自包含，
// 不复用 aaa_test.go（aaaIT*）/ aaa_display_test.go（aaaVT*）/ aaa_eval_test.go 的任何符号。
package cli

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— QA 独占脚手架 ——

const qaAAADev = "R1"

// qaAAANew 构造指定类型的裸设备（用户视图）。
func qaAAANew(dt topology.DeviceType) *CLIState {
	st := NewCLIStateWithType(dt)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	st.DeviceName = qaAAADev
	return st
}

// qaAAARun 执行一条命令并返回回显。
func qaAAARun(st *CLIState, line string) string {
	return ExecuteCommandOn(st, ParseCommand(line), st.DeviceType)
}

// qaAAARunOK 顺序执行铺垫命令，任一条返回 Error: 立即失败。
func qaAAARunOK(t *testing.T, st *CLIState, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if out := qaAAARun(st, l); strings.HasPrefix(out, "Error:") {
			t.Fatalf("铺垫命令 %q 意外失败：%s", l, out)
		}
	}
}

// qaAAARouter 构造一台已进系统视图的路由器。
func qaAAARouter(t *testing.T) *CLIState {
	t.Helper()
	st := qaAAANew(topology.DeviceRouter)
	qaAAARunOK(t, st, "system-view")
	return st
}

// qaAAAMainline 是 PRD §4.1 课程 71 主线（自系统视图起，结束停在系统视图）。
var qaAAAMainline = []string{
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

// qaAAAScenario 构造 PRD §4.2 的三用户场景（admin / guest / operator）+ 方案 + 域。
//
// operator 刻意**只配 service-type**：用于 AC8②③ 断言
// privilege 列必须是 "-"（不是死字段假 0）、password 列必须是 "-"（不是 ****）。
func qaAAAScenario(t *testing.T) *CLIState {
	t.Helper()
	st := qaAAARouter(t)
	qaAAARunOK(t, st,
		"aaa",
		"local-user admin password cipher Huawei@123",
		"local-user admin privilege level 15",
		"local-user admin service-type telnet ssh",
		"local-user guest password cipher Guest@2026",
		"local-user guest privilege level 1",
		"local-user guest state block",
		"local-user operator service-type terminal",
		"authentication-scheme default",
		"quit",
		"authentication-scheme sch1",
		"authentication-mode local",
		"quit",
		"domain huawei",
		"authentication-scheme sch1",
		"quit",
		"quit",
	)
	return st
}

// qaAAAKeySet 返回 DeviceConfig 中精确 "aaa:" 前缀的键值快照。
func qaAAAKeySet(st *CLIState) map[string]string {
	out := make(map[string]string)
	for k, v := range st.DeviceConfig {
		if strings.HasPrefix(k, "aaa:") {
			out[k] = v
		}
	}
	return out
}

// qaAAASnapshotConfig 返回 DeviceConfig 的完整深拷贝（纯函数副作用检测用）。
func qaAAASnapshotConfig(st *CLIState) map[string]string {
	out := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		out[k] = v
	}
	return out
}

// qaAAASource 读取被测源文件（测试 CWD 即包目录）。
func qaAAASource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取源文件 %s 失败：%v", name, err)
	}
	return string(b)
}

// qaAAACodeLines 返回源码中**剔除整行注释与空行**后的代码行（静态红线断言用）。
//
// 只剔除以 // 开头的整行注释：行内尾随注释仍保留，
// 以免把 `x := f() // strings.Contains(k,"aaa") 是禁止的` 这类写法漏判成通过。
func qaAAACodeLines(src string) []string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// qaAAAStatValue 抽取形如 "  Label   : value" 的运行态字段值；未出现返回 ok=false。
func qaAAAStatValue(out, label string) (string, bool) {
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, label) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, label))
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(rest, ":")), true
	}
	return "", false
}

// qaAAAUserRow 返回 display local-user 中指定用户的数据行。
func qaAAAUserRow(out, user string) (string, bool) {
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), user+" ") || strings.TrimSpace(ln) == user {
			return ln, true
		}
	}
	return "", false
}

// ============================================================================
// AC13 · 键碰撞专项（本期最高危红线，PRD §6-4 / 设计 A1）
// ============================================================================

// qaAAAAlienKeys 是必须与 AAA 完全隔离的**异族键**集合。
//
// 三条键都刻意含 "aaa" 或 "domain" 语义碎片：
//   - 00e0-fc12-0aaa   仓库实存端口安全测试 MAC（p2_portsec_qa_t07_test.go:275）
//   - aaaa-bbbb-cccc   网络实验最常用示教 MAC，含 4 个连续 a
//   - Bridge-Aggregation1  链路聚合口名（历史上 Ag-gre-gation 已坑过 GRE 那轮）
var qaAAAAlienKeys = map[string]string{
	"interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa": "1",
	"interface:GigabitEthernet0/0/2:port-security-sticky-learned:aaaa-bbbb-cccc": "1",
	"interface:Bridge-Aggregation1:lag:mode":                                     "lacp-static",
}

// TestQAAAAKeyCollisionNoGhostUser 断言 collect* 系列不会把异族键派生成幽灵实体。
//
// 🔴 若实现退化为 strings.Contains(k, "aaa")，MAC 键会被解析成用户
// "GigabitEthernet0/0/1" 之类的幽灵用户，本用例即刻炸掉。
func TestQAAAAKeyCollisionNoGhostUser(t *testing.T) {
	st := qaAAANew(topology.DeviceRouter)
	for k, v := range qaAAAAlienKeys {
		st.DeviceConfig[k] = v
	}
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)] = "Huawei@123"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = "local"
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldState)] = "active"

	if got := collectAAALocalUsers(st); !reflect.DeepEqual(got, []string{"admin"}) {
		t.Fatalf("AC13①：collectAAALocalUsers = %v，期望恰为 [admin]（异族 MAC 键不得派生幽灵用户）", got)
	}
	if got := collectAAASchemes(st, AAASchemeKindAuthen); !reflect.DeepEqual(got, []string{"sch1"}) {
		t.Errorf("AC13①：collectAAASchemes(authen) = %v，期望恰为 [sch1]", got)
	}
	if got := collectAAADomains(st); !reflect.DeepEqual(got, []string{"huawei"}) {
		t.Errorf("AC13①：collectAAADomains = %v，期望恰为 [huawei]", got)
	}

	// 反向：EvaluateAAA 派生视图同样不得含幽灵用户。
	res := EvaluateAAA(st)
	if len(res.Users) != 1 || res.Users[0].Name != "admin" {
		t.Errorf("AC13①：EvaluateAAA.Users = %+v，期望仅 admin", res.Users)
	}
	// display 层同样不得泄漏 MAC 派生名。
	out := buildAAALocalUserDisplay(st)
	for _, ghost := range []string{"00e0-fc12-0aaa", "aaaa-bbbb-cccc", "Bridge-Aggregation1", "GigabitEthernet"} {
		if strings.Contains(out, ghost) {
			t.Errorf("AC13①：display local-user 输出泄漏异族键片段 %q：\n%s", ghost, out)
		}
	}
}

// TestQAAAAUndoAAADoesNotTouchAlienKeys 断言 `undo aaa` 级联清理零误伤。
//
// 🔴 这是 PRD §6-4 与 P1-3 的最高危触发点：若用 Contains("aaa") 清理，
// 端口安全粘滞 MAC 键会被连带删除（学员的端口安全配置凭空消失）。
func TestQAAAAUndoAAADoesNotTouchAlienKeys(t *testing.T) {
	st := qaAAARouter(t)
	for k, v := range qaAAAAlienKeys {
		st.DeviceConfig[k] = v
	}
	qaAAARunOK(t, st, qaAAAMainline...)

	if len(qaAAAKeySet(st)) == 0 {
		t.Fatalf("前置失败：主线执行后没有任何 aaa: 键")
	}
	if out := qaAAARun(st, "undo aaa"); strings.HasPrefix(out, "Error:") {
		t.Fatalf("AC13②：系统视图 undo aaa 失败：%s", out)
	}

	if left := qaAAAKeySet(st); len(left) != 0 {
		t.Errorf("AC13②：undo aaa 后仍残留 aaa: 键 %v", left)
	}
	for k, want := range qaAAAAlienKeys {
		got, ok := st.DeviceConfig[k]
		if !ok {
			t.Errorf("🔴 AC13② 键碰撞误删：undo aaa 删掉了异族键 %q（端口安全 / LAG 配置被误伤）", k)
			continue
		}
		if got != want {
			t.Errorf("AC13②：异族键 %q 值被改写为 %q，期望 %q", k, got, want)
		}
	}
	// display aaa 回到空态（证明确实清理干净，而不是没执行）。
	if out := buildAAADisplay(st); !strings.Contains(out, "Info: No AAA configuration.") {
		t.Errorf("AC13②：undo aaa 后 display aaa 未回空态：\n%s", out)
	}
}

// TestQAAAANoFuzzyKeyMatchingInSource 静态断言：AAA 三件套源码零模糊匹配。
//
// 只扫描**代码行**（剔除整行注释），因此文档注释里出现的反例说明不算命中。
func TestQAAAANoFuzzyKeyMatchingInSource(t *testing.T) {
	// 命中形如 strings.Contains(<任意变量>, "aaa") / (..., "domain")，
	// 也拦截 HasSuffix / Index 这类等价的子串扫描写法。
	banned := regexp.MustCompile(`strings\.(Contains|ContainsAny|Index|LastIndex|HasSuffix)\s*\([^,)]+,\s*"(aaa|domain)"\s*\)`)
	for _, f := range []string{"aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		for i, ln := range qaAAACodeLines(qaAAASource(t, f)) {
			if banned.MatchString(ln) {
				t.Errorf("🔴 AC13③ %s 代码行 #%d 使用了模糊子串匹配：%s", f, i+1, strings.TrimSpace(ln))
			}
		}
	}
	// 正向：命名空间前缀必须自带尾冒号，否则 "aaaa-bbbb-cccc" 会被 HasPrefix 命中。
	if !strings.HasSuffix(aaaKeyPrefix(), ":") {
		t.Fatalf("AC13③：aaaKeyPrefix() = %q，必须以 ':' 结尾", aaaKeyPrefix())
	}
	for _, mac := range []string{"aaaa-bbbb-cccc", "00e0-fc12-0aaa", "aaa", "aaa-domain"} {
		if strings.HasPrefix(mac, aaaKeyPrefix()) {
			t.Errorf("AC13③：裸串 %q 竟被 aaaKeyPrefix() 前缀命中", mac)
		}
	}
}

// TestQAAAAStateGoHasNoAAAStruct 静态断言：CLIState 未新增任何 AAA 内嵌结构体（PRD §6-3）。
func TestQAAAAStateGoHasNoAAAStruct(t *testing.T) {
	code := strings.Join(qaAAACodeLines(qaAAASource(t, "state.go")), "\n")
	for _, bad := range []string{
		"LocalUsers", "LocalUser struct", "AAAConfig", "DomainConfig", "SchemeConfig",
	} {
		if strings.Contains(code, bad) {
			t.Errorf("PRD §6-3：state.go 代码中仍含 %q（结构体事实源必须彻底删除）", bad)
		}
	}
	// 反向：三档视图枚举必须存在（P0-1）。
	for _, want := range []string{"ViewAAA ", "ViewAAAAuthen ", "ViewAAADomain "} {
		if !strings.Contains(code, want) {
			t.Errorf("P0-1：state.go 缺少视图枚举 %q", want)
		}
	}
	// 全仓不得再有 state.LocalUsers 引用（AC4②）。
	for _, f := range []string{"parser.go", "aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		if strings.Contains(qaAAASource(t, f), "state.LocalUsers") {
			t.Errorf("AC4②：%s 仍引用 state.LocalUsers", f)
		}
	}
}

// ============================================================================
// AC10 · 诚实占位（CRITICAL 红线，PRD §6-1）
// ============================================================================

// qaAAARuntimeLabels 是 PRD §4.2 / §4.4 标注表列出的**全部 7 个运行态字段**。
// 它们没有任何真实数据源，值必须恒为 "-"。
var qaAAARuntimeLabels = []string{
	"Successful authentications",
	"Failed authentications",
	"Online sessions",
	"Last login time",
	"Online users",
	"Access accepts",
	"Access rejects",
}

// TestQAAAAHonestRuntimePlaceholders 断言四路 display 的运行态字段恒 "-" 且无伪造痕迹。
func TestQAAAAHonestRuntimePlaceholders(t *testing.T) {
	st := qaAAAScenario(t)
	// 伪造运行态的典型形态：数字计数 / time.Now() 派生日期时间 / 假会话词 / 假密文。
	forged := []*regexp.Regexp{
		regexp.MustCompile(`\d{4}-\d{2}-\d{2}`),        // 2026-08-09 之类的假时间
		regexp.MustCompile(`\d{2}:\d{2}:\d{2}`),        // 12:34:56 之类的假时刻
		regexp.MustCompile(`(?i)\bnever\b`),            // "Never" 伪状态词
		regexp.MustCompile(`\d+\s*(?i)online`),         // "0 online" / "1 online"
		regexp.MustCompile(`(?i)online\s*:\s*\d`),      // "Online: 3"
		regexp.MustCompile(`(?i)\d+\s*user\(s\)\s*on`), // "1 user(s) online"
		regexp.MustCompile(`%\^%#`),                    // 伪造 VRP 密文标记
	}

	cases := map[string]string{
		"display local-user":    qaAAARun(st, "display local-user"),
		"display aaa":           qaAAARun(st, "display aaa"),
		"display domain":        qaAAARun(st, "display domain"),
		"display domain huawei": qaAAARun(st, "display domain huawei"),
	}
	for name, out := range cases {
		// ① 出现过的运行态字段，值必须逐字为 "-"。
		hit := 0
		for _, label := range qaAAARuntimeLabels {
			v, ok := qaAAAStatValue(out, label)
			if !ok {
				continue
			}
			hit++
			if v != AAAStatPlaceholder {
				t.Errorf("🔴 AC10：%s 的运行态字段 %q = %q，必须恒为 %q", name, label, v, AAAStatPlaceholder)
			}
		}
		// ② 全部 display 必须附诚实注记。
		if !strings.Contains(out, aaaSimNote()) {
			t.Errorf("AC10：%s 输出缺少 aaaSimNote() 诚实注记：\n%s", name, out)
		}
		// ③ 输出中不得出现任何伪造运行态痕迹。
		for _, re := range forged {
			if m := re.FindString(out); m != "" {
				t.Errorf("🔴 AC10：%s 输出含伪造运行态痕迹 %q（正则 %s）：\n%s", name, m, re, out)
			}
		}
		_ = hit
	}

	// ④ display local-user / display domain <name> 必须**保留**运行态分组（拍板 C10）
	//    且四 / 三个字段一个不少 —— 防止靠「整块删掉」来通过 ③ 的假性达标。
	lu := cases["display local-user"]
	if !strings.Contains(lu, "--- Authentication runtime statistics ---") {
		t.Errorf("C10：display local-user 缺少 Authentication runtime statistics 分组")
	}
	for _, label := range []string{"Successful authentications", "Failed authentications", "Online sessions", "Last login time"} {
		if _, ok := qaAAAStatValue(lu, label); !ok {
			t.Errorf("C10：display local-user 缺少运行态字段 %q", label)
		}
	}
	dd := cases["display domain huawei"]
	if !strings.Contains(dd, "--- Domain runtime statistics ---") {
		t.Errorf("C10：display domain <name> 缺少 Domain runtime statistics 分组")
	}
	for _, label := range []string{"Online users", "Access accepts", "Access rejects"} {
		if _, ok := qaAAAStatValue(dd, label); !ok {
			t.Errorf("C10：display domain <name> 缺少运行态字段 %q", label)
		}
	}

	// ⑤ AAAStats 类型层面恒 "-"（从类型上杜绝日后填数字，设计 A4）。
	stats := EvaluateAAA(st).Stats
	rv := reflect.ValueOf(stats)
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		if rv.Field(i).Kind() != reflect.String {
			t.Errorf("🔴 A4：AAAStats.%s 类型为 %s，必须为 string", f.Name, rv.Field(i).Kind())
			continue
		}
		if got := rv.Field(i).String(); got != AAAStatPlaceholder {
			t.Errorf("🔴 A4：AAAStats.%s = %q，必须恒为 %q", f.Name, got, AAAStatPlaceholder)
		}
	}
}

// TestQAAAANoClockOrRandomInSource 静态断言：AAA 源码不得引入时钟 / 随机数。
//
// 一旦出现 time.Now() 或 math/rand，就具备了「编造运行态」的能力，
// 属于诚实占位红线的**结构性**风险，必须在源码层面掐死。
func TestQAAAANoClockOrRandomInSource(t *testing.T) {
	banned := []string{"time.Now(", "time.Since(", "rand.", "math/rand"}
	for _, f := range []string{"aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		code := strings.Join(qaAAACodeLines(qaAAASource(t, f)), "\n")
		for _, b := range banned {
			if strings.Contains(code, b) {
				t.Errorf("🔴 PRD §6-1：%s 代码中出现 %q，具备编造运行态的能力", f, b)
			}
		}
		// 纯函数层不得 import internal/protocol（AC13 / 设计 §6）。
		if strings.Contains(code, "internal/protocol") {
			t.Errorf("AC13：%s 不得 import internal/protocol", f)
		}
	}
}

// TestQAAAASimNoteTwoModes 断言诚实注记内容真实反映仿真边界。
func TestQAAAASimNoteTwoModes(t *testing.T) {
	note := aaaSimNote()
	for _, want := range []string{"配置态模拟", "无真实登录握手", "RADIUS", "计费"} {
		if !strings.Contains(note, want) {
			t.Errorf("P0-14：aaaSimNote() = %q，缺少关键声明 %q", note, want)
		}
	}
	if strings.Contains(note, "支持") || strings.Contains(note, "已实现") {
		t.Errorf("P0-14：aaaSimNote() 出现夸大表述：%q", note)
	}
}

// ============================================================================
// AC9 · 口令脱敏（诚实 + 安全双红线）
// ============================================================================

// TestQAAAAPasswordMaskedInAllFourDisplays 断言四路 display 明文口令零泄漏。
func TestQAAAAPasswordMaskedInAllFourDisplays(t *testing.T) {
	st := qaAAAScenario(t)
	qaAAARunOK(t, st, "save")
	if out := qaAAARun(st, "Y"); strings.HasPrefix(out, "Error:") {
		t.Fatalf("save 确认失败：%s", out)
	}

	outs := map[string]string{
		"display local-user":            qaAAARun(st, "display local-user"),
		"display aaa":                   qaAAARun(st, "display aaa"),
		"display current-configuration": qaAAARun(st, "display current-configuration"),
		"display saved-configuration":   qaAAARun(st, "display saved-configuration"),
	}
	plaintexts := []string{"Huawei@123", "Guest@2026"}
	fakeCipher := regexp.MustCompile(`%\^%#`)

	for name, out := range outs {
		for _, p := range plaintexts {
			if strings.Contains(out, p) {
				t.Errorf("🔴 AC9①：%s 泄漏明文口令 %q：\n%s", name, p, out)
			}
		}
		if m := fakeCipher.FindString(out); m != "" {
			t.Errorf("🔴 AC9④：%s 输出伪造 VRP 密文标记 %q", name, m)
		}
	}

	// ②③ Password 列：已配恒 ****，未配恒 -（两者必须可区分）。
	lu := outs["display local-user"]
	for _, c := range []struct {
		user string
		want string
	}{
		{"admin", "****"},
		{"guest", "****"},
		{"operator", "-"},
	} {
		row, ok := qaAAAUserRow(lu, c.user)
		if !ok {
			t.Fatalf("AC9②③：display local-user 缺少用户 %s 的数据行：\n%s", c.user, lu)
		}
		fields := strings.Fields(row)
		got := fields[len(fields)-1]
		if got != c.want {
			t.Errorf("AC9②③：用户 %s 的 Password 列 = %q，期望 %q（行：%q）", c.user, got, c.want, row)
		}
	}

	// ⑤ display local-user 必须附「未实现不可逆加密」的诚实说明。
	for _, want := range []string{"未实现", "明文", "脱敏"} {
		if !strings.Contains(lu, want) {
			t.Errorf("AC9⑤：display local-user 缺少诚实说明关键词 %q：\n%s", want, lu)
		}
	}

	// ⑥ maskAAAPassword 恒 ****（对任何入参，含空串与超长串）。
	for _, in := range []string{"", "anything", "Huawei@123", strings.Repeat("x", 200), "%^%#abc%^%#"} {
		if got := maskAAAPassword(in); got != "****" {
			t.Errorf("AC9⑥：maskAAAPassword(%q) = %q，期望恒 \"****\"", in, got)
		}
	}

	// 诚实边界反向断言：DeviceConfig 中确实存的是明文（工具如实声明"以明文存于本地配置文件"），
	// 若这里变成伪密文，说明实现在造假。
	if got := st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)]; got != "Huawei@123" {
		t.Errorf("AC9：事实源应如实存明文，实得 %q", got)
	}
}

// ============================================================================
// AC5 · authentication-mode 视图分派（本期最高危代码冲突）
// ============================================================================

// TestQAAAAAuthModeVTYZeroRegression 断言 VTY 既有行为逐字零回归（AC5b）。
func TestQAAAAAuthModeVTYZeroRegression(t *testing.T) {
	st := qaAAARouter(t)
	qaAAARunOK(t, st, "user-interface vty 0 4")

	for _, c := range []struct{ in, want, mode string }{
		{"authentication-mode aaa", "Authentication-mode set to aaa", "aaa"},
		{"authentication-mode password", "Authentication-mode set to password", "password"},
		{"authentication-mode none", "Authentication-mode set to none", "none"},
	} {
		if got := qaAAARun(st, c.in); got != c.want {
			t.Errorf("AC5b：%q → %q，期望逐字 %q", c.in, got, c.want)
		}
		if st.VTY.AuthenticationMode != c.mode {
			t.Errorf("AC5b：%q 后 VTY.AuthenticationMode = %q，期望 %q", c.in, st.VTY.AuthenticationMode, c.mode)
		}
	}
	const usage = "Error: usage: authentication-mode aaa|password|none"
	if got := qaAAARun(st, "authentication-mode bogus"); got != usage {
		t.Errorf("AC5b：VTY 非法值 → %q，期望逐字 %q", got, usage)
	}
	if got := qaAAARun(st, "authentication-mode"); got != usage {
		t.Errorf("AC5b：VTY 缺参 → %q，期望逐字 %q", got, usage)
	}
	// VTY 路径**绝不**写任何 aaa: 键（认证模式属 VTY 侧状态）。
	if left := qaAAAKeySet(st); len(left) != 0 {
		t.Errorf("AC5b：VTY authentication-mode 竟写入 aaa: 键 %v", left)
	}

	// 非 VTY / 非 AAA 视图逐字保持原文案。
	const notVTY = "Error: must be in VTY user interface view"
	qaAAARunOK(t, st, "quit")
	if got := qaAAARun(st, "authentication-mode aaa"); got != notVTY {
		t.Errorf("AC5b：系统视图 → %q，期望逐字 %q", got, notVTY)
	}
	qaAAARunOK(t, st, "interface GigabitEthernet0/0/1")
	if got := qaAAARun(st, "authentication-mode aaa"); got != notVTY {
		t.Errorf("AC5b：接口视图 → %q，期望逐字 %q", got, notVTY)
	}
}

// TestQAAAAAuthModeAAAPathAndGuard 断言 AAA 路径写键、AAA 视图直敲报错且不写键（AC5a / AC5c）。
func TestQAAAAAuthModeAAAPathAndGuard(t *testing.T) {
	// AC5c：AAA 视图直接敲 authentication-mode。
	st := qaAAARouter(t)
	qaAAARunOK(t, st, "aaa")
	got := qaAAARun(st, "authentication-mode local")
	if !strings.Contains(got, "authentication-scheme") || !strings.HasPrefix(got, "Error:") {
		t.Errorf("AC5c：AAA 视图直敲 → %q，期望含 authentication-scheme 引导的 Error:", got)
	}
	if left := qaAAAKeySet(st); len(left) != 0 {
		t.Errorf("AC5c：报错路径竟写入键 %v（必须一个键都不写）", left)
	}

	// AC5a：方案子视图写键，三种模式（含 radius 配置态接受，拍板 C3）。
	for _, mode := range []string{"local", "radius", "none"} {
		st2 := qaAAARouter(t)
		qaAAARunOK(t, st2, "aaa", "authentication-scheme sch1")
		if out := qaAAARun(st2, "authentication-mode "+mode); strings.HasPrefix(out, "Error:") {
			t.Fatalf("AC5a：authentication-mode %s 失败：%s", mode, out)
		}
		key := aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)
		if got := st2.DeviceConfig[key]; got != mode {
			t.Errorf("AC5a：%s = %q，期望 %q", key, got, mode)
		}
		// C3：radius 绝不联动既有 state.RADIUS 死代码。
		if mode == "radius" && st2.RADIUS != nil && st2.RADIUS.Enabled {
			t.Errorf("🔴 C3：authentication-mode radius 竟联动了 state.RADIUS")
		}
	}
}

// TestQAAAAAuthModeSingleTopLevelCase 静态断言顶层 case 唯一（AC5d）。
func TestQAAAAAuthModeSingleTopLevelCase(t *testing.T) {
	src := qaAAASource(t, "parser.go")
	total := strings.Count(src, `case "authentication-mode":`)
	if total != 2 {
		t.Errorf("AC5d：parser.go 中 case \"authentication-mode\" 共 %d 处，期望 2 处"+
			"（ExecuteCommandOn 顶层 1 处 + applyVRRP 内层 1 处）", total)
	}
	// ExecuteCommandOn 函数体内必须**有且仅有** 1 处。
	start := strings.Index(src, "func ExecuteCommandOn(")
	if start < 0 {
		t.Fatalf("未找到 ExecuteCommandOn 定义")
	}
	rest := src[start+len("func ExecuteCommandOn("):]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		end = len(rest)
	}
	if n := strings.Count(rest[:end], `case "authentication-mode":`); n != 1 {
		t.Errorf("AC5d：ExecuteCommandOn 内 case \"authentication-mode\" 共 %d 处，期望恰 1 处（Go 不允许重复 case）", n)
	}
	// VRRP 内层分支逐字未改（PRD 明令不得改动）。
	for _, want := range []string{
		`return "Error: usage: vrrp vrid <id> authentication-mode {simple|md5} <key>"`,
		`return "Error: authentication-mode must be simple or md5"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("AC5d：VRRP 内层 authentication-mode 文案被改动，缺失 %q", want)
		}
	}
}

// ============================================================================
// AC1 · 视图层级与 quit 不越级（直击 quit if-else 链末尾兜底 else）
// ============================================================================

// TestQAAAAQuitHierarchyNoLevelSkip 断言各档提示符逐字正确、quit 逐级回退。
func TestQAAAAQuitHierarchyNoLevelSkip(t *testing.T) {
	st := qaAAARouter(t)
	if got := GetPrompt(st, qaAAADev); got != "[R1]" {
		t.Fatalf("AC1：系统视图提示符 = %q，期望 [R1]", got)
	}
	qaAAARunOK(t, st, "aaa")
	if st.CurrentView != ViewAAA {
		t.Fatalf("AC1①：aaa 后视图 = %v，期望 ViewAAA", st.CurrentView)
	}
	if got := GetPrompt(st, qaAAADev); got != "[R1-aaa]" {
		t.Fatalf("AC1①：提示符 = %q，期望 [R1-aaa]", got)
	}

	// ② + ③ 认证方案子视图与 quit 回退（不得越级弹回 [R1]）。
	subCases := []struct{ enter, prompt string }{
		{"authentication-scheme sch1", "[R1-aaa-authen-sch1]"},
		{"authorization-scheme aut1", "[R1-aaa-author-aut1]"},
		{"accounting-scheme acc1", "[R1-aaa-acct-acc1]"},
		{"domain huawei", "[R1-aaa-domain-huawei]"},
	}
	for _, c := range subCases {
		qaAAARunOK(t, st, c.enter)
		if got := GetPrompt(st, qaAAADev); got != c.prompt {
			t.Errorf("AC1②④：%q 后提示符 = %q，期望 %q", c.enter, got, c.prompt)
		}
		qaAAARun(st, "quit")
		if st.CurrentView != ViewAAA {
			t.Errorf("🔴 AC1③：%q 子视图 quit 后视图 = %v，期望回 ViewAAA（越级弹回是静默错误）", c.enter, st.CurrentView)
		}
		if got := GetPrompt(st, qaAAADev); got != "[R1-aaa]" {
			t.Errorf("🔴 AC1③：%q 子视图 quit 后提示符 = %q，期望 [R1-aaa]（不是 [R1]）", c.enter, got)
		}
	}

	// ⑤ ViewAAA quit → 系统视图。
	qaAAARun(st, "quit")
	if st.CurrentView != ViewSystem {
		t.Errorf("AC1⑤：ViewAAA quit 后视图 = %v，期望 ViewSystem", st.CurrentView)
	}
	if got := GetPrompt(st, qaAAADev); got != "[R1]" {
		t.Errorf("AC1⑤：ViewAAA quit 后提示符 = %q，期望 [R1]", got)
	}

	// ⑥ 任意 AAA 子视图 return → 用户视图（既有行为不变）。
	for _, enter := range []string{"authentication-scheme sch1", "domain huawei"} {
		st2 := qaAAARouter(t)
		qaAAARunOK(t, st2, "aaa", enter)
		qaAAARun(st2, "return")
		if st2.CurrentView != ViewUser {
			t.Errorf("AC1⑥：%q 后 return → %v，期望 ViewUser", enter, st2.CurrentView)
		}
	}
}

// ============================================================================
// AC3 · save → reload 持久化贯通（本期最大价值点）
// ============================================================================

// qaAAAPRDSection45 是 PRD §4.5 的权威 AAA 配置块（逐字，含首尾 # 行）。
const qaAAAPRDSection45 = "#\n" +
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
	"  authentication-scheme sch1\n" +
	"#\n"

// TestQAAAASaveReloadRoundTrip 断言 aaa: 键集逐键一致、快照逐字一致。
func TestQAAAASaveReloadRoundTrip(t *testing.T) {
	// 严格按 PRD §4.5 样例（admin + guest + default/sch1 + domain huawei）构造。
	st := qaAAARouter(t)
	qaAAARunOK(t, st,
		"aaa",
		"authentication-scheme default",
		"quit",
		"authentication-scheme sch1",
		"authentication-mode local",
		"quit",
		"local-user admin password cipher Huawei@123",
		"local-user admin privilege level 15",
		"local-user admin service-type telnet ssh",
		"local-user guest password cipher Guest@2026",
		"local-user guest privilege level 1",
		"local-user guest state block",
		"domain huawei",
		"authentication-scheme sch1",
		"quit",
		"quit",
	)

	// ④ display current-configuration 的 AAA 块必须与 PRD §4.5 逐字一致。
	if got := buildSavedAAAConfig(st); got != qaAAAPRDSection45 {
		t.Errorf("AC3④：AAA 配置块与 PRD §4.5 不一致：\n--- got ---\n%s\n--- want ---\n%s", got, qaAAAPRDSection45)
	}
	cur := qaAAARun(st, "display current-configuration")
	if !strings.Contains(cur, qaAAAPRDSection45) {
		t.Errorf("AC3④：display current-configuration 未包含 PRD §4.5 块：\n%s", cur)
	}

	before := qaAAAKeySet(st)
	beforeSnap := buildSavedAAAConfig(st)

	// save → reload 往返（复用既有序列化通道，A9 零新增持久化代码）。
	cfg := st.SerializeToDeviceConfigData()
	reloaded := qaAAANew(topology.DeviceRouter)
	reloaded.LoadFromDeviceConfigData(cfg)

	after := qaAAAKeySet(reloaded)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("🔴 AC3①：reload 后 aaa: 键集不一致\n--- before(%d) ---\n%v\n--- after(%d) ---\n%v",
			len(before), before, len(after), after)
	}
	if len(after) == 0 {
		t.Fatalf("🔴 AC3：reload 后 aaa: 键全部丢失（这正是改造前的缺陷形态）")
	}

	// ② display local-user 完整复现三项属性。
	lu := qaAAARun(reloaded, "display local-user")
	for _, want := range []string{"admin", "15", "telnet ssh", "guest", "Block"} {
		if !strings.Contains(lu, want) {
			t.Errorf("AC3②：reload 后 display local-user 缺少 %q：\n%s", want, lu)
		}
	}
	// ③ display domain huawei 复现绑定 + 跨对象解引用。
	dd := qaAAARun(reloaded, "display domain huawei")
	if !strings.Contains(dd, "sch1") || !strings.Contains(dd, "mode: local") {
		t.Errorf("AC3③：reload 后 display domain huawei 未复现绑定：\n%s", dd)
	}
	// ④ 快照逐字一致，且连续两次调用字节级一致。
	if got := buildSavedAAAConfig(reloaded); got != beforeSnap {
		t.Errorf("AC3④：reload 后快照与 reload 前不一致\n--- after ---\n%s\n--- before ---\n%s", got, beforeSnap)
	}
	if a, b := buildSavedAAAConfig(reloaded), buildSavedAAAConfig(reloaded); a != b {
		t.Errorf("AC3④：连续两次快照不一致")
	}
}

// ============================================================================
// AC8 · display 确定性 + 死字段假 0 修复
// ============================================================================

// TestQAAAADisplayDeterministic10x 断言同状态连续 10 次输出字节级一致。
func TestQAAAADisplayDeterministic10x(t *testing.T) {
	st := qaAAAScenario(t)
	for _, cmdLine := range []string{
		"display local-user", "display aaa", "display domain", "display domain huawei",
	} {
		first := qaAAARun(st, cmdLine)
		for i := 2; i <= 10; i++ {
			if got := qaAAARun(st, cmdLine); got != first {
				t.Fatalf("AC8④：%q 第 %d 次输出与首次不一致（map 随机遍历未消除）\n--- first ---\n%s\n--- #%d ---\n%s",
					cmdLine, i, first, i, got)
			}
		}
	}
	// 快照通道同样确定性。
	first := buildSavedAAAConfig(st)
	for i := 2; i <= 10; i++ {
		if got := buildSavedAAAConfig(st); got != first {
			t.Fatalf("AC8④：buildSavedAAAConfig 第 %d 次不一致", i)
		}
	}
	// 用户必须按名称升序（admin < guest < operator）。
	lu := qaAAARun(st, "display local-user")
	ia, ig, io := strings.Index(lu, "admin"), strings.Index(lu, "guest"), strings.Index(lu, "operator")
	if !(ia >= 0 && ia < ig && ig < io) {
		t.Errorf("AC8①：用户未按名称升序排列（admin=%d guest=%d operator=%d）：\n%s", ia, ig, io, lu)
	}
}

// TestQAAAADeadFieldFakeZeroFixed 断言未配 privilege 显示 "-" 而非死字段假 0。
func TestQAAAADeadFieldFakeZeroFixed(t *testing.T) {
	st := qaAAAScenario(t)
	lu := qaAAARun(st, "display local-user")

	row, ok := qaAAAUserRow(lu, "operator")
	if !ok {
		t.Fatalf("AC8②：display local-user 缺少 operator 行：\n%s", lu)
	}
	f := strings.Fields(row) // operator | Active | <priv> | <svc> | <pwd>
	if len(f) < 5 {
		t.Fatalf("AC8②：operator 行列数异常：%q", row)
	}
	if f[2] != "-" {
		t.Errorf("🔴 AC8②：未配 privilege 的用户该列 = %q，必须为 \"-\"（\"0\" 是合法最低权限级，与未配置语义不同）", f[2])
	}
	if f[2] == "0" {
		t.Errorf("🔴 AC8②：死字段假 0 缺陷复发")
	}

	// ③ 未配 service-type 的用户该列为 "-"。
	grow, ok := qaAAAUserRow(lu, "guest")
	if !ok {
		t.Fatalf("AC8③：缺少 guest 行")
	}
	gf := strings.Fields(grow)
	if gf[3] != "-" {
		t.Errorf("AC8③：guest 未配 service-type，该列 = %q，期望 \"-\"", gf[3])
	}
	// 反向：真实配置值必须如实展示，不得一律 "-"（防止靠"全填 -"蒙混过关）。
	arow, _ := qaAAAUserRow(lu, "admin")
	if !strings.Contains(arow, "15") || !strings.Contains(arow, "telnet ssh") {
		t.Errorf("AC8①：admin 行未如实展示 privilege 15 / service-type telnet ssh：%q", arow)
	}

	// ⑤ 空态文案。
	empty := qaAAARouter(t)
	if got := qaAAARun(empty, "display local-user"); !strings.Contains(got, "No local user configured") {
		t.Errorf("AC8⑤：空态 display local-user = %q", got)
	}
	if got := qaAAARun(empty, "display domain"); !strings.Contains(got, "No domain configured") {
		t.Errorf("AC8⑤：空态 display domain = %q", got)
	}
	// ⑥ 不存在的域。
	if got := qaAAARun(st, "display domain nosuch"); !strings.Contains(got, "does not exist") || !strings.HasPrefix(got, "Error:") {
		t.Errorf("AC8⑥：display domain nosuch = %q，期望含 does not exist 的 Error:", got)
	}
}

// ============================================================================
// AC12 · 能力守卫（配置命令按设备类型拒绝 / display 任意设备可读）
// ============================================================================

// TestQAAAACapabilityGuardMatrix 断言 PC / Server / 二层 Switch 的守卫矩阵。
func TestQAAAACapabilityGuardMatrix(t *testing.T) {
	denied := []topology.DeviceType{topology.DevicePC, topology.DeviceServer, topology.DeviceSwitch}
	for _, dt := range denied {
		t.Run(string(dt), func(t *testing.T) {
			st := qaAAANew(dt)
			qaAAARun(st, "system-view")
			for _, line := range []string{
				"aaa",
				"local-user u1 password cipher Huawei@123",
				"authentication-scheme s1",
				"domain d1",
			} {
				out := qaAAARun(st, line)
				if !strings.HasPrefix(out, "Error:") {
					t.Errorf("AC12a：%s 上 %q 未被拒绝，回显 %q", dt, line, out)
				}
			}
			// 拒绝路径不得写任何键。
			if left := qaAAAKeySet(st); len(left) != 0 {
				t.Errorf("AC12a：%s 上被拒命令竟写入键 %v", dt, left)
			}
			// AC12b：display 只读命令任意设备可读，输出空态 Info:，且不含能力拒绝串。
			for _, line := range []string{"display local-user", "display aaa", "display domain"} {
				out := qaAAARun(st, line)
				if strings.Contains(out, "is not supported on") {
					t.Errorf("🔴 AC12b：%s 上 %q 被能力拒绝：%q", dt, line, out)
				}
				if !strings.Contains(out, "Info:") {
					t.Errorf("AC12b：%s 上 %q 未输出空态 Info:：%q", dt, line, out)
				}
			}
		})
	}

	// 三层设备正常放行。
	for _, dt := range []topology.DeviceType{topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceFirewall} {
		st := qaAAANew(dt)
		qaAAARun(st, "system-view")
		if out := qaAAARun(st, "aaa"); strings.HasPrefix(out, "Error:") {
			t.Errorf("AC12a：%s 上 aaa 应放行，实得 %q", dt, out)
		}
		if st.CurrentView != ViewAAA {
			t.Errorf("AC12a：%s 上 aaa 未进入 ViewAAA", dt)
		}
	}
}

// TestQAAAACapabilitiesFileUntouched 断言 capabilities.go 零改动（AC12c / 设计 A5）。
func TestQAAAACapabilitiesFileUntouched(t *testing.T) {
	src := qaAAASource(t, "capabilities.go")
	// gofmt 会把 map 字面量整体对齐（"local-user":<多空格>l3Devices()），故用正则容忍空白差异。
	if !regexp.MustCompile(`"local-user":\s+l3Devices\(\),`).MatchString(src) {
		t.Errorf("AC12c：capabilities.go 丢失既有行 \"local-user\": l3Devices(),")
	}
	// 本期严禁给这些新 token 增加矩阵行（会连带影响既有 VTY 用例）。
	for _, tok := range []string{
		`"aaa":`, `"authentication-mode":`, `"authentication-scheme":`,
		`"authorization-scheme":`, `"accounting-scheme":`, `"domain":`,
	} {
		if strings.Contains(src, tok) {
			t.Errorf("🔴 AC12c：capabilities.go 新增了矩阵行 %s（本期要求零改动）", tok)
		}
	}
	// 设备守卫必须复用 l3Devices()，不得在 AAA 侧重定义设备集合。
	cmdSrc := qaAAASource(t, "aaa_cmd.go")
	if !strings.Contains(cmdSrc, "l3Devices()") {
		t.Errorf("A5：aaa_cmd.go 未复用 l3Devices() 做分支内设备守卫")
	}
	if strings.Contains(cmdSrc, "func l3Devices") {
		t.Errorf("🔴 A5：aaa_cmd.go 重定义了 l3Devices")
	}
}

// ============================================================================
// AC6 / AC7 · 引用完整性与参数守卫
// ============================================================================

// TestQAAAAReferenceIntegrity 断言「先建后绑」教学点与 C7 硬拒绝。
func TestQAAAAReferenceIntegrity(t *testing.T) {
	st := qaAAARouter(t)
	qaAAARunOK(t, st, "aaa", "domain huawei")

	// ① 绑定不存在的方案 → 硬拒绝且不写键。
	out := qaAAARun(st, "authentication-scheme nosuch")
	if got, want := out, fmt.Sprintf(ErrSchemeNotExist, "nosuch"); got != want {
		t.Errorf("AC6①：%q，期望逐字 %q", got, want)
	}
	if _, ok := st.DeviceConfig[aaaDomainKey("huawei", aaaFieldAuthenScheme)]; ok {
		t.Errorf("🔴 AC6①：绑定失败却写入了 aaa:domain:huawei:authen-scheme 键")
	}
	if aaaSchemeExists(st, AAASchemeKindAuthen, "nosuch") {
		t.Errorf("🔴 AC6①/A12：域内绑定竟隐式创建了方案 nosuch")
	}

	// ② 先建方案再绑定 → 成功。
	qaAAARunOK(t, st, "quit", "authentication-scheme sch1", "quit", "domain huawei")
	if o := qaAAARun(st, "authentication-scheme sch1"); o != "" {
		t.Errorf("AC6②：合法绑定应静默成功，实得 %q", o)
	}
	if got := st.DeviceConfig[aaaDomainKey("huawei", aaaFieldAuthenScheme)]; got != "sch1" {
		t.Errorf("AC6②：绑定键 = %q，期望 sch1", got)
	}

	// ③ 删除仍被域引用的方案 → C7 硬拒绝，方案键保留。
	qaAAARunOK(t, st, "quit")
	del := qaAAARun(st, "undo authentication-scheme sch1")
	if !strings.Contains(del, "referenced by domain") || !strings.HasPrefix(del, "Error:") {
		t.Errorf("AC6③/C7：%q，期望含 referenced by domain 的 Error:", del)
	}
	if !aaaSchemeExists(st, AAASchemeKindAuthen, "sch1") {
		t.Errorf("🔴 AC6③：被拒绝的删除竟然把方案键删掉了")
	}
	// 解除引用后可正常删除。
	qaAAARunOK(t, st, "undo domain huawei")
	if o := qaAAARun(st, "undo authentication-scheme sch1"); o != "" {
		t.Errorf("AC6③：解除引用后删除应成功，实得 %q", o)
	}
	if aaaSchemeExists(st, AAASchemeKindAuthen, "sch1") {
		t.Errorf("AC6③：方案 sch1 未被删除")
	}
}

// TestQAAAAParamGuardsWriteNoKey 断言全部参数校验失败路径「报错且零写键」。
func TestQAAAAParamGuardsWriteNoKey(t *testing.T) {
	cases := []struct {
		line    string
		wantSub string
		desc    string
	}{
		{"local-user admin privilege level 16", "between 0 and 15", "AC7① 上越界"},
		{"local-user admin privilege level -1", "between 0 and 15", "AC7① 下越界"},
		{"local-user admin privilege level abc", "between 0 and 15", "AC7① 非数字"},
		{"local-user admin service-type vnc", "Invalid service-type", "AC7②"},
		{"local-user admin state enabled", "usage:", "AC7③"},
		{"local-user admin password cipher 123", "length must be between 8 and 128", "AC7④"},
		{"local-user admin foobar x", "unrecognized command", "AC7⑤ 幽灵用户"},
		{"local-user admin password simple Huawei@123", "unrecognized command", "P0-4 simple 已废弃"},
		{"local-user admin password irreversible-cipher Huawei@123", "unrecognized command", "P2-3 不实现"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			st := qaAAARouter(t)
			qaAAARunOK(t, st, "aaa")
			out := qaAAARun(st, c.line)
			if !strings.HasPrefix(out, "Error:") {
				t.Fatalf("%s：%q 未硬拒绝，回显 %q", c.desc, c.line, out)
			}
			if !strings.Contains(out, c.wantSub) {
				t.Errorf("%s：%q → %q，期望含 %q", c.desc, c.line, out, c.wantSub)
			}
			if left := qaAAAKeySet(st); len(left) != 0 {
				t.Errorf("🔴 %s：%q 被拒却写入键 %v（打错字不得凭空产生配置）", c.desc, c.line, left)
			}
		})
	}
	// AC7③ 的 usage 必须同时点出两个合法取值。
	st := qaAAARouter(t)
	qaAAARunOK(t, st, "aaa")
	out := qaAAARun(st, "local-user admin state enabled")
	for _, w := range []string{"active", "block"} {
		if !strings.Contains(out, w) {
			t.Errorf("AC7③：state usage 未列出合法值 %q：%q", w, out)
		}
	}
}

// TestQAAAAMissingArgsMustGiveUsage 断言「缺参 → 含 usage:」（PRD AC7⑥）。
//
// PRD §5 AC7⑥ 原文：「`local-user`（缺参）→ 含 `usage:`」。
// 与之配套，设计 §4.4 之外工程侧还自行补齐了 ErrLocalUserUsage / ErrPrivilegeUsage
// 两条常量并写明「缺参 → usage:，值非法 → 具体约束文案」是全命令族统一口径。
// 本用例独立验证该口径是否真的落地。
func TestQAAAAMissingArgsMustGiveUsage(t *testing.T) {
	for _, line := range []string{
		"local-user",
		"local-user admin",
		"local-user admin privilege",
		"local-user admin privilege level",
	} {
		t.Run(line, func(t *testing.T) {
			st := qaAAARouter(t)
			qaAAARunOK(t, st, "aaa")
			out := qaAAARun(st, line)
			if !strings.HasPrefix(out, "Error:") {
				t.Fatalf("AC7⑥：%q 未硬拒绝，回显 %q", line, out)
			}
			if !strings.Contains(out, "usage:") {
				t.Errorf("AC7⑥：%q → %q，PRD 要求缺参回显必须含 \"usage:\"（当前为 unrecognized command，学员看不出少打了什么）", line, out)
			}
			if left := qaAAAKeySet(st); len(left) != 0 {
				t.Errorf("AC7⑥：%q 被拒却写入键 %v", line, left)
			}
		})
	}
	// 已声明的 usage 常量必须真正被使用，否则即为死常量。
	cmdSrc := qaAAASource(t, "aaa_cmd.go")
	for _, name := range []string{"ErrLocalUserUsage", "ErrPrivilegeUsage"} {
		if !strings.Contains(cmdSrc, name) {
			t.Errorf("aaa_eval.go 声明了 %s 但 aaa_cmd.go 从未使用，属死常量（口径未落地）", name)
		}
	}
}

// ============================================================================
// AC13 · 纯函数无副作用 + 跨特性零回归
// ============================================================================

// TestQAAAAEvaluateAndDisplayHaveNoSideEffect 断言评估 / 渲染链路零副作用。
func TestQAAAAEvaluateAndDisplayHaveNoSideEffect(t *testing.T) {
	st := qaAAAScenario(t)
	before := qaAAASnapshotConfig(st)
	view, sub := st.CurrentView, st.CurrentSub

	for i := 0; i < 3; i++ {
		_ = EvaluateAAA(st)
		_ = collectAAALocalUsers(st)
		_ = collectAAASchemes(st, AAASchemeKindAuthen)
		_ = collectAAADomains(st)
		_ = maskAAAPassword("Huawei@123")
		_ = aaaSimNote()
		_ = buildAAADisplay(st)
		_ = buildAAALocalUserDisplay(st)
		_ = buildAAADomainDisplay(st, "huawei")
		_ = buildAAADomainDisplay(st, "")
		_ = buildSavedAAAConfig(st)
		_ = fixSSHLocalUsersDisplay(st)
	}

	if after := qaAAASnapshotConfig(st); !reflect.DeepEqual(before, after) {
		t.Errorf("🔴 AC13：纯函数 / 渲染层改写了 DeviceConfig\n--- before ---\n%v\n--- after ---\n%v", before, after)
	}
	if st.CurrentView != view || st.CurrentSub != sub {
		t.Errorf("AC13：渲染层改写了视图状态：%v/%q → %v/%q", view, sub, st.CurrentView, st.CurrentSub)
	}
	// 连续两次评估结果一致。
	if !reflect.DeepEqual(EvaluateAAA(st), EvaluateAAA(st)) {
		t.Errorf("AC13：EvaluateAAA 连续两次结果不一致")
	}
	// nil / 空态安全。
	if got := EvaluateAAA(nil); !got.IsEmpty() {
		t.Errorf("AC13：EvaluateAAA(nil) 应为空结果")
	}
}

// TestQAAAACrossFeatureZeroRegression 断言 AAA 全流程 + undo aaa 对既有特性零影响。
//
// 手法：先把 GRE / LAG / 端口安全 / VRRP / STP / DHCP 等既有配置做完并取
// `display current-configuration` 基线快照，再叠加完整 AAA 主线并 `undo aaa`，
// 断言快照**逐字回到基线**（PRD §6-6 零回归底线）。
func TestQAAAACrossFeatureZeroRegression(t *testing.T) {
	// 用三层交换机：既在 l3Devices()（可配 AAA / GRE），又在 switchDevices()
	// （可配 VLAN / STP / 端口安全），一台设备即可覆盖全部相邻特性。
	st := qaAAANew(topology.DeviceL3Switch)
	qaAAARunOK(t, st, "system-view")
	// 既有特性配置（覆盖历史上踩过键碰撞坑的 GRE / LAG / 端口安全）。
	qaAAARunOK(t, st,
		"stp mode rstp",
		"interface GigabitEthernet0/0/1",
		"port-security enable",
		"quit",
		"interface Tunnel0/0/1",
		"tunnel-protocol gre",
		"source 1.1.1.1",
		"destination 2.2.2.2",
		"quit",
	)
	// 端口安全粘滞 MAC（含 aaa 十六进制片段）与 LAG 键直接落 DeviceConfig，
	// 模拟真实学习结果与聚合口配置。
	for k, v := range qaAAAAlienKeys {
		st.DeviceConfig[k] = v
	}

	baseline := qaAAARun(st, "display current-configuration")
	baseCfg := qaAAASnapshotConfig(st)

	// 叠加完整 AAA 主线。
	qaAAARunOK(t, st, qaAAAMainline...)
	withAAA := qaAAARun(st, "display current-configuration")
	if withAAA == baseline {
		t.Fatalf("前置失败：AAA 主线未对 current-configuration 产生任何影响")
	}
	// 既有块必须仍在（AAA 只做增量，不得挤掉既有内容）。
	for _, want := range []string{"stp mode", "Tunnel0/0/1", "tunnel-protocol gre"} {
		if !strings.Contains(withAAA, want) {
			t.Errorf("PRD §6-6：叠加 AAA 后既有配置块 %q 消失：\n%s", want, withAAA)
		}
	}

	// 级联清理后必须逐字回到基线。
	if out := qaAAARun(st, "undo aaa"); strings.HasPrefix(out, "Error:") {
		t.Fatalf("undo aaa 失败：%s", out)
	}
	if got := qaAAARun(st, "display current-configuration"); got != baseline {
		t.Errorf("🔴 PRD §6-6：undo aaa 后 current-configuration 未逐字回到基线\n--- got ---\n%s\n--- baseline ---\n%s", got, baseline)
	}
	// DeviceConfig 也必须逐键回到基线。
	if got := qaAAASnapshotConfig(st); !reflect.DeepEqual(got, baseCfg) {
		t.Errorf("🔴 PRD §6-6：undo aaa 后 DeviceConfig 与基线不一致\n--- got ---\n%v\n--- baseline ---\n%v", got, baseCfg)
	}
}

// TestQAAAAAdjacentFeaturesUntouched 断言相邻技术债（radius / dot1x / ssh）行为未被联动。
func TestQAAAAAdjacentFeaturesUntouched(t *testing.T) {
	st := qaAAARouter(t)
	qaAAARunOK(t, st, qaAAAMainline...)

	// C3：AAA 配置绝不联动既有自造 radius 命令与 state.RADIUS。
	if st.RADIUS != nil && st.RADIUS.Enabled {
		t.Errorf("🔴 C3：仅配置 AAA 竟使 state.RADIUS.Enabled = true")
	}
	// SSH 独立体系（state.SSH.Users）不得被 AAA 用户污染。
	if len(st.SSH.Users) != 0 {
		t.Errorf("§7：AAA 本地用户竟写入了 state.SSH.Users：%+v", st.SSH.Users)
	}
	// display ssh server 的 Local Users 段读新事实源：确定性 + 脱敏 + 无假 0。
	sshOut := qaAAARun(st, "display ssh server")
	if strings.Contains(sshOut, "Huawei@123") {
		t.Errorf("🔴 AC9：display ssh server 泄漏明文口令：\n%s", sshOut)
	}
	if strings.Contains(sshOut, "Local Users:") {
		if !strings.Contains(sshOut, "****") {
			t.Errorf("A11：display ssh server 的 Local Users 段未脱敏：\n%s", sshOut)
		}
		ia, ig := strings.Index(sshOut, "User: admin"), strings.Index(sshOut, "User: guest")
		if ia < 0 || ig < 0 || ia > ig {
			t.Errorf("A11：display ssh server 的 Local Users 段未按名称升序：\n%s", sshOut)
		}
		for i := 0; i < 5; i++ {
			if got := qaAAARun(st, "display ssh server"); got != sshOut {
				t.Errorf("A11：display ssh server 输出不确定（map 随机遍历残留）")
				break
			}
		}
	}
}

// TestQAAAAUndoSemantics 断言属性级 / 实体级 undo 精确且互不牵连（AC11）。
func TestQAAAAUndoSemantics(t *testing.T) {
	st := qaAAARouter(t)
	qaAAARunOK(t, st, qaAAAMainline...)
	qaAAARunOK(t, st, "aaa")

	// ① 属性级 undo：键必须被**删除**而非留空串。
	if o := qaAAARun(st, "undo local-user admin privilege"); strings.HasPrefix(o, "Error:") {
		t.Fatalf("AC11①：undo local-user admin privilege 失败：%s", o)
	}
	if _, ok := st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPrivilege)]; ok {
		t.Errorf("🔴 AC11①：privilege 键未被删除（留空串会让 display 显示空白而非 -）")
	}
	for _, k := range []string{
		aaaLocalUserKey("admin", aaaFieldPassword),
		aaaLocalUserKey("admin", aaaFieldServiceType),
	} {
		if _, ok := st.DeviceConfig[k]; !ok {
			t.Errorf("AC11①：属性级 undo 误伤了兄弟键 %s", k)
		}
	}

	// ② 整用户 undo：本用户键全清，其他用户完好。
	if o := qaAAARun(st, "undo local-user admin"); strings.HasPrefix(o, "Error:") {
		t.Fatalf("AC11②：undo local-user admin 失败：%s", o)
	}
	for k := range qaAAAKeySet(st) {
		if strings.HasPrefix(k, aaaLocalUserPrefix+"admin:") {
			t.Errorf("AC11②：undo local-user admin 后仍残留 %s", k)
		}
	}
	if !aaaLocalUserExists(st, "guest") {
		t.Errorf("🔴 AC11②：undo local-user admin 误删了 guest")
	}
	lu := qaAAARun(st, "display local-user")
	if strings.Contains(lu, "admin") || !strings.Contains(lu, "guest") {
		t.Errorf("AC11②：display local-user 未正确反映删除结果：\n%s", lu)
	}

	// 前缀精确性：admin 不得误删 administrator。
	st2 := qaAAARouter(t)
	qaAAARunOK(t, st2, "aaa",
		"local-user admin password cipher Huawei@123",
		"local-user administrator password cipher Huawei@123",
		"undo local-user admin")
	if !aaaLocalUserExists(st2, "administrator") {
		t.Errorf("🔴 AC11②：undo local-user admin 误删了 administrator（前缀缺尾冒号）")
	}
	if aaaLocalUserExists(st2, "admin") {
		t.Errorf("AC11②：admin 未被删除")
	}

	// ③ undo domain。
	qaAAARunOK(t, st, "undo domain huawei")
	if aaaDomainExists(st, "huawei") {
		t.Errorf("AC11③：undo domain huawei 后域仍存在")
	}
	// ⑤ 未命中的 undo 交回既有分支，文案与其它视图一致（零回归）。
	if got := qaAAARun(st, "undo nosuchthing"); !strings.Contains(got, "is not supported") {
		t.Errorf("AC11⑤：AAA 视图未命中 undo → %q，期望沿用既有 not supported 文案", got)
	}
}
