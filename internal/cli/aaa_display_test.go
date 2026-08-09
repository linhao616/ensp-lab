// aaa_display_test.go 是 AAA 展示层与架构合规的验收测试（P2 第八项，T8）。
//
// 覆盖 PRD §5 的 AC8–AC13：display 忠实展示与输出确定性、口令脱敏双红线、
// 诚实占位红线、undo 语义完整、能力守卫、纯函数合规与**键碰撞专项**。
// AC1–AC7（视图层级 / 事实源 / 持久化 / 旧形态下线 / case 分派 / 引用完整性 / 校验矩阵）
// 见 aaa_test.go。
//
// 🔴 本文件的全部 helper 一律使用 `aaaVT` 独占前缀并**自包含**，
// 不依赖 aaa_test.go 中的任何符号 —— 两个文件由不同工程师并行维护，
// 共享 helper 会在同包内产生 duplicate symbol 编译错误。
package cli

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— 独占测试脚手架（aaaVT 前缀）——

// aaaVTDevice 构造指定类型的设备态，设备名固定 R1（与 PRD §4 样例一致）。
func aaaVTDevice(dt topology.DeviceType) *CLIState {
	st := NewCLIStateWithType(dt)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	st.DeviceConfig["sysname"] = "R1"
	st.DeviceName = "R1"
	return st
}

// aaaVTExec 执行一条命令并返回回显。
func aaaVTExec(st *CLIState, line string) string {
	return ExecuteCommandOn(st, ParseCommand(line), st.DeviceType)
}

// aaaVTExecAll 顺序执行多条命令，任一条返回 Error: 即失败。
func aaaVTExecAll(t *testing.T, st *CLIState, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if out := aaaVTExec(st, line); strings.HasPrefix(out, "Error:") {
			t.Fatalf("命令 %q 意外失败：%s", line, out)
		}
	}
}

// aaaVTSave 执行 save 并完成 VRP 的 Y/N 二次确认。
//
// 🔴 既有 save 是两段式（PendingSave → 下一条输入被当作 Y/N 答复）：
// 直接连发 `save` 与 `display ...` 会让 display 被当成答复吞掉，
// 返回 "Error: invalid input, please enter Y or N."。
func aaaVTSave(t *testing.T, st *CLIState) {
	t.Helper()
	aaaVTExec(st, "save")
	if out := aaaVTExec(st, "y"); !strings.Contains(out, "Save the configuration successfully.") {
		t.Fatalf("save 未成功落盘：%q", out)
	}
}

// aaaVTMainline 走完 PRD §4.1 课程 71 主线，停在 ViewAAA。
func aaaVTMainline(t *testing.T) *CLIState {
	t.Helper()
	st := aaaVTDevice(topology.DeviceRouter)
	aaaVTExecAll(t, st,
		"system-view",
		"aaa",
		"local-user admin password cipher Huawei@123",
		"local-user admin privilege level 15",
		"local-user admin service-type telnet ssh",
		"local-user guest password cipher Guest@2026",
		"local-user guest privilege level 1",
		"local-user guest state block",
		"authentication-scheme default",
		"quit",
		"authentication-scheme sch1",
		"authentication-mode local",
		"quit",
		"domain huawei",
		"authentication-scheme sch1",
		"quit",
	)
	return st
}

// aaaVTThreeUsers 构造 PRD §4.2 样例的三用户状态（admin / guest / operator）。
//
// operator 刻意只配 service-type：用于断言 Privilege 列为 "-"（而非假 0）
// 且 Password 列为 "-"（与 "****" 可区分）。
func aaaVTThreeUsers(t *testing.T) *CLIState {
	t.Helper()
	st := aaaVTDevice(topology.DeviceRouter)
	aaaVTExecAll(t, st, "system-view", "aaa",
		"local-user admin password cipher Huawei@123",
		"local-user admin privilege level 15",
		"local-user admin service-type telnet ssh",
		"local-user guest password cipher Guest@2026",
		"local-user guest privilege level 1",
		"local-user guest state block",
		"local-user operator service-type terminal",
	)
	return st
}

// aaaVTKeys 返回全部 aaa: 精确前缀键（升序）。
func aaaVTKeys(st *CLIState) []string {
	keys := make([]string, 0, 8)
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, aaaKeyPrefix()) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// aaaVTReadSource 读取 internal/cli 下的源文件（测试 cwd 即包目录）。
func aaaVTReadSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(b)
}

// aaaVTNonCommentSource 返回源文件中**剔除整行注释后**的代码文本。
//
// 🔴 必须剔除注释：aaa_eval.go / aaa_cmd.go 的红线注释里逐字写了被禁的
// `strings.Contains(k, "aaa")` 反例，裸做子串扫描会被注释自我命中（假阳性）。
func aaaVTNonCommentSource(t *testing.T, name string) string {
	t.Helper()
	var b strings.Builder
	for _, ln := range strings.Split(aaaVTReadSource(t, name), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}

// aaaVTStatValue 提取形如 "  <label> : <value>" 行的值部分。
func aaaVTStatValue(out, label string) (string, bool) {
	for _, ln := range strings.Split(out, "\n") {
		if !strings.Contains(ln, label) {
			continue
		}
		idx := strings.Index(ln, ":")
		if idx < 0 {
			continue
		}
		return strings.TrimSpace(ln[idx+1:]), true
	}
	return "", false
}

// —— AC8：display 忠实展示 + 输出确定性 ——

func TestAAAVTDisplayLocalUserColumns(t *testing.T) {
	st := aaaVTThreeUsers(t)
	out := aaaVTExec(st, "display local-user")

	// ① 三行数据行，且按用户名升序。
	order := []string{"admin", "guest", "operator"}
	var dataLines []string
	for _, ln := range strings.Split(out, "\n") {
		for _, name := range order {
			if strings.HasPrefix(ln, "  "+name+" ") {
				dataLines = append(dataLines, ln)
			}
		}
	}
	if len(dataLines) != 3 {
		t.Fatalf("数据行数 = %d, want 3，实际输出：\n%s", len(dataLines), out)
	}
	for i, ln := range dataLines {
		if !strings.HasPrefix(strings.TrimSpace(ln), order[i]) {
			t.Fatalf("第 %d 行应为用户 %s，实际 %q（必须按名称升序）", i+1, order[i], ln)
		}
	}
	if !strings.Contains(out, "Total 3 user(s)") {
		t.Error("缺少 Total 3 user(s)")
	}

	// 各列取值正确（整行断言，同时校验 PRD §4.2 列宽）。
	// ② 未配 privilege 的 operator 该列为 "-"；③ 未配 service-type 的 guest 该列为 "-"。
	wantRows := []string{
		"  admin                    Active   15          telnet ssh            ****",
		"  guest                    Block    1           -                     ****",
		"  operator                 Active   -           terminal              -",
	}
	for _, want := range wantRows {
		if !strings.Contains(out, want) {
			t.Errorf("缺少数据行（列宽/取值不符 PRD §4.2）：\n%q\n实际输出：\n%s", want, out)
		}
	}

	// 🔴 P0-5 关键断言：operator 的 Privilege 列不得出现死字段假 0。
	for _, ln := range dataLines {
		if strings.HasPrefix(strings.TrimSpace(ln), "operator") && strings.Contains(ln, " 0 ") {
			t.Errorf("operator 的 Privilege 列出现假 0：%q", ln)
		}
	}
}

// TestAAAVTDisplayAAAMatchesPRD 断言 display aaa 的头部计数与方案/域小表逐字符合 PRD §4.3。
func TestAAAVTDisplayAAAMatchesPRD(t *testing.T) {
	st := aaaVTThreeUsers(t)
	aaaVTExecAll(t, st,
		"authentication-scheme default", "quit",
		"authentication-scheme sch1", "authentication-mode local", "quit",
		"domain huawei", "authentication-scheme sch1", "quit",
	)
	// VTY 引用关系（P1-8 只读引用）。
	aaaVTExecAll(t, st, "quit", "user-interface vty 0 4", "authentication-mode aaa")
	out := aaaVTExec(st, "display aaa")

	wantLines := []string{
		"AAA configuration information",
		"Local user count            : 3",
		"Authentication scheme count : 2",
		"Authorization scheme count  : 0",
		"Accounting scheme count     : 0",
		"Domain count                : 1",
		"VTY authentication-mode     : aaa        (user-interface vty, referenced)",
		"Authentication schemes:",
		"  Name                     Mode",
		"  default                  local",
		"  sch1                     local",
		"Domains:",
		"  Name                     Authen-scheme   Author-scheme   Acct-scheme   State",
		"  huawei                   sch1            -               -             Active",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Errorf("display aaa 缺少 PRD §4.3 行：\n%q\n实际输出：\n%s", want, out)
		}
	}
	// 未创建任何授权/计费方案时，对应小表整段不输出。
	if strings.Contains(out, "Authorization schemes:") {
		t.Error("授权方案为空时不应输出该小表")
	}
}

// TestAAAVTVTYNotReferenced 断言 VTY 未引用 AAA 时的标注文案。
func TestAAAVTVTYNotReferenced(t *testing.T) {
	st := aaaVTMainline(t)
	out := aaaVTExec(st, "display aaa")
	if !strings.Contains(out, "(AAA not referenced by VTY)") {
		t.Errorf("VTY 未引用 AAA 时应标注 (AAA not referenced by VTY)，实际：\n%s", out)
	}
}

// TestAAAVTDisplayDeterminism 断言同状态连续 10 次调用输出**字节级一致**。
func TestAAAVTDisplayDeterminism(t *testing.T) {
	st := aaaVTThreeUsers(t)
	aaaVTExecAll(t, st,
		"authentication-scheme sch1", "authentication-mode local", "quit",
		"authorization-scheme aut1", "quit",
		"accounting-scheme acc1", "quit",
		"domain huawei", "authentication-scheme sch1", "quit",
		"domain abc", "quit",
	)
	for _, cmd := range []string{
		"display local-user", "display aaa", "display domain", "display domain huawei",
	} {
		first := aaaVTExec(st, cmd)
		for i := 2; i <= 10; i++ {
			if got := aaaVTExec(st, cmd); got != first {
				t.Fatalf("%s 第 %d 次输出与首次不一致（存在 map 随机遍历）", cmd, i)
			}
		}
	}
}

func TestAAAVTEmptyStateAndUnknownDomain(t *testing.T) {
	st := aaaVTDevice(topology.DeviceRouter)
	empties := map[string]string{
		"display local-user": "No local user configured",
		"display aaa":        "No AAA configuration",
		"display domain":     "No domain configured",
	}
	for cmd, want := range empties {
		if out := aaaVTExec(st, cmd); !strings.Contains(out, want) {
			t.Errorf("空态 %s = %q, want 含 %q", cmd, out, want)
		}
	}
	// ⑥ 指定不存在的域。
	if out := aaaVTExec(aaaVTMainline(t), "display domain nosuch"); !strings.Contains(out, "does not exist") {
		t.Errorf("display domain nosuch = %q, want 含 does not exist", out)
	}
}

// TestAAAVTDomainDetailDereference 断言域详情做跨对象解引用（P1-7）。
func TestAAAVTDomainDetailDereference(t *testing.T) {
	st := aaaVTMainline(t)
	out := aaaVTExec(st, "display domain huawei")
	for _, want := range []string{
		"Domain-name                 : huawei",
		"State                       : Active",
		"Authentication-scheme       : sch1  (mode: local)",
		"Authorization-scheme        : -",
		"Accounting-scheme           : -",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("域详情缺少 PRD §4.4 行 %q，实际：\n%s", want, out)
		}
	}
	// 人为制造悬空引用 → 必须如实标注 (not found)，不得静默显示成正常绑定。
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldAuthenScheme)] = "ghost"
	if out := aaaVTExec(st, "display domain huawei"); !strings.Contains(out, "ghost  (mode: - (not found))") {
		t.Errorf("悬空引用未如实标注，实际：\n%s", out)
	}
}

// —— AC9：口令脱敏（诚实 + 安全双红线）——

func TestAAAVTPasswordMasking(t *testing.T) {
	st := aaaVTThreeUsers(t)
	aaaVTExec(st, "quit") // 回系统视图
	aaaVTSave(t, st)

	outputs := map[string]string{
		"display local-user":            aaaVTExec(st, "display local-user"),
		"display aaa":                   aaaVTExec(st, "display aaa"),
		"display current-configuration": aaaVTExec(st, "display current-configuration"),
		"display saved-configuration":   aaaVTExec(st, "display saved-configuration"),
	}
	cipherRe := regexp.MustCompile(`%\^%#`)
	for name, out := range outputs {
		if out == "" {
			t.Fatalf("%s 输出为空，用例失效", name)
		}
		// ① 明文零泄漏。
		for _, plain := range []string{"Huawei@123", "Guest@2026"} {
			if strings.Contains(out, plain) {
				t.Errorf("🔴 %s 泄漏明文口令 %q", name, plain)
			}
		}
		// ④ 无伪造 VRP 密文标记。
		if cipherRe.MatchString(out) {
			t.Errorf("🔴 %s 出现伪造密文标记 %%^%%#", name)
		}
	}

	// 快照确实落到了 saved-configuration（证明上面的零泄漏不是「压根没输出」造成的假阳性）。
	if !strings.Contains(outputs["display saved-configuration"], "local-user admin password cipher ****") {
		t.Errorf("display saved-configuration 未包含脱敏口令行，实际：\n%s",
			outputs["display saved-configuration"])
	}

	lu := outputs["display local-user"]
	// ② 已配口令恒 "****"；③ 未配口令为 "-"（两者可区分）。
	if !strings.Contains(lu, "telnet ssh            ****") {
		t.Errorf("admin 的 Password 列未脱敏为 ****，实际：\n%s", lu)
	}
	if !strings.Contains(lu, "terminal              -") {
		t.Errorf("operator 未配口令时 Password 列应为 -，实际：\n%s", lu)
	}
	// ⑤ 诚实说明子串。
	for _, sub := range []string{"未实现 VRP 密文算法", "明文存于本地配置文件"} {
		if !strings.Contains(lu, sub) {
			t.Errorf("display local-user 缺少诚实说明 %q", sub)
		}
	}
}

// —— AC10：诚实占位（CRITICAL 红线）——

func TestAAAVTHonestPlaceholders(t *testing.T) {
	st := aaaVTMainline(t)
	digitRe := regexp.MustCompile(`\d`)
	fakeRe := regexp.MustCompile(`(?i)online|never|\d{4}-\d{2}-\d{2}`)

	checks := []struct {
		cmd    string
		labels []string
	}{
		{"display local-user", []string{
			"Successful authentications", "Failed authentications", "Online sessions", "Last login time"}},
		{"display domain huawei", []string{
			"Online users", "Access accepts", "Access rejects"}},
	}
	for _, c := range checks {
		out := aaaVTExec(st, c.cmd)
		for _, sub := range []string{"无真实登录握手", "无 RADIUS 协议交互"} {
			if !strings.Contains(out, sub) {
				t.Errorf("%s 缺少诚实注记 %q", c.cmd, sub)
			}
		}
		for _, label := range c.labels {
			val, ok := aaaVTStatValue(out, label)
			if !ok {
				t.Errorf("%s 缺少运行态字段 %q", c.cmd, label)
				continue
			}
			if val != AAAStatPlaceholder {
				t.Errorf("🔴 %s 的 %q = %q, want %q（严禁编造运行态数据）",
					c.cmd, label, val, AAAStatPlaceholder)
			}
			if digitRe.MatchString(val) {
				t.Errorf("🔴 %s 的 %q 出现数字 %q", c.cmd, label, val)
			}
			if fakeRe.MatchString(val) {
				t.Errorf("🔴 %s 的 %q 出现伪造运行态派生值 %q", c.cmd, label, val)
			}
		}
	}
	if out := aaaVTExec(st, "display aaa"); !strings.Contains(out, "无真实登录握手") {
		t.Error("display aaa 缺少诚实注记")
	}
}

// TestAAAVTNoTimeNowInSource 静态断言 AAA 源码中不出现 time.Now()（防造假登录时刻）。
func TestAAAVTNoTimeNowInSource(t *testing.T) {
	for _, f := range []string{"aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		if strings.Contains(aaaVTNonCommentSource(t, f), "time.Now()") {
			t.Errorf("🔴 %s 出现 time.Now()（诚实占位红线）", f)
		}
	}
}

// —— AC11：undo 语义完整 ——

func TestAAAVTUndoSemantics(t *testing.T) {
	// ① 属性级 undo：键被清除而非留空串，其余属性完好。
	st := aaaVTMainline(t)
	if out := aaaVTExec(st, "undo local-user admin privilege"); out != "" {
		t.Fatalf("undo local-user admin privilege 应静默成功，实际 %q", out)
	}
	if _, ok := st.DeviceConfig["aaa:local-user:admin:privilege"]; ok {
		t.Error("privilege 键未被清除（不得留空串）")
	}
	if st.DeviceConfig["aaa:local-user:admin:password"] != "Huawei@123" {
		t.Error("其余属性键被误删")
	}

	// ② 整用户 undo：精确前缀全部清理，其他用户完好。
	if out := aaaVTExec(st, "undo local-user admin"); out != "" {
		t.Fatalf("undo local-user admin 应静默成功，实际 %q", out)
	}
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, "aaa:local-user:admin:") {
			t.Errorf("残留键 %q", k)
		}
	}
	if st.DeviceConfig["aaa:local-user:guest:password"] != "Guest@2026" {
		t.Error("undo admin 误伤了 guest")
	}
	if out := aaaVTExec(st, "display local-user"); strings.Contains(out, "admin") {
		t.Error("display local-user 仍显示已删除的 admin")
	}

	// ③ undo domain 级联清理。
	if out := aaaVTExec(st, "undo domain huawei"); out != "" {
		t.Fatalf("undo domain huawei 应静默成功，实际 %q", out)
	}
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, "aaa:domain:huawei:") {
			t.Errorf("域残留键 %q", k)
		}
	}

	// ④ 系统视图 undo aaa 级联清理，display aaa 回空态。
	st2 := aaaVTMainline(t)
	aaaVTExec(st2, "quit")
	if out := aaaVTExec(st2, "undo aaa"); out != "" {
		t.Fatalf("undo aaa 应静默成功，实际 %q", out)
	}
	if keys := aaaVTKeys(st2); len(keys) != 0 {
		t.Fatalf("undo aaa 后残留 %v", keys)
	}
	if out := aaaVTExec(st2, "display aaa"); !strings.Contains(out, "No AAA configuration") {
		t.Errorf("undo aaa 后 display aaa = %q, want 空态", out)
	}
}

// TestAAAVTUndoNotSupportedFallback 断言 AAA 视图内未识别的 undo 落到既有统一文案（零回归）。
func TestAAAVTUndoNotSupportedFallback(t *testing.T) {
	if out := aaaVTExec(aaaVTMainline(t), "undo bogus-feature"); !strings.Contains(out, "is not supported") {
		t.Errorf("未识别的 undo 回显 = %q, want 含 is not supported", out)
	}
}

// TestAAAVTUndoDomainState 断言域 state 的 undo 回到生效缺省而非删除存在性键。
func TestAAAVTUndoDomainState(t *testing.T) {
	st := aaaVTDevice(topology.DeviceRouter)
	aaaVTExecAll(t, st, "system-view", "aaa", "domain huawei", "state block")
	if got := st.DeviceConfig["aaa:domain:huawei:state"]; got != "block" {
		t.Fatalf("域 state = %q, want block", got)
	}
	if out := aaaVTExec(st, "undo state"); out != "" {
		t.Fatalf("undo state 应静默成功，实际 %q", out)
	}
	if got := st.DeviceConfig["aaa:domain:huawei:state"]; got != AAADefaultUserState {
		t.Fatalf("undo state 后 = %q, want %q（该键承载域存在性，须改写而非删除）", got, AAADefaultUserState)
	}
	if !aaaDomainExists(st, "huawei") {
		t.Fatal("undo state 误删了域")
	}
}

// TestAAAVTUndoSchemeUnbindInDomain 断言域子视图内 undo 方案 = 解绑（而非删方案）。
func TestAAAVTUndoSchemeUnbindInDomain(t *testing.T) {
	st := aaaVTMainline(t)
	aaaVTExecAll(t, st, "domain huawei")
	if out := aaaVTExec(st, "undo authentication-scheme sch1"); out != "" {
		t.Fatalf("域内解绑应静默成功，实际 %q", out)
	}
	if _, ok := st.DeviceConfig["aaa:domain:huawei:authen-scheme"]; ok {
		t.Error("解绑后域侧绑定键仍存在")
	}
	if !aaaSchemeExists(st, AAASchemeKindAuthen, "sch1") {
		t.Error("域内 undo 误删了方案本身（应只解绑）")
	}
	// 解绑后方案不再被引用，可以正常删除。
	aaaVTExecAll(t, st, "quit")
	if out := aaaVTExec(st, "undo authentication-scheme sch1"); out != "" {
		t.Fatalf("解绑后删除方案应静默成功，实际 %q", out)
	}
	if aaaSchemeExists(st, AAASchemeKindAuthen, "sch1") {
		t.Error("方案未被删除")
	}
}

// —— AC12：能力守卫 ——

func TestAAAVTConfigGuardByDeviceType(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer, topology.DeviceSwitch} {
		st := aaaVTDevice(dt)
		aaaVTExec(st, "system-view")
		if out := aaaVTExec(st, "aaa"); !strings.Contains(out, "Error:") {
			t.Errorf("[%s] aaa 应被拒绝，实际 %q", dt, out)
		}
		if st.CurrentView == ViewAAA {
			t.Errorf("[%s] 拒绝后仍进入了 ViewAAA", dt)
		}
		if len(aaaVTKeys(st)) != 0 {
			t.Errorf("[%s] 拒绝后仍写入了 aaa: 键", dt)
		}
	}
	for _, dt := range []topology.DeviceType{
		topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceFirewall, topology.DeviceVTEP,
	} {
		st := aaaVTDevice(dt)
		aaaVTExec(st, "system-view")
		if out := aaaVTExec(st, "aaa"); out != "" {
			t.Errorf("[%s] aaa 应放行，实际 %q", dt, out)
		}
		if st.CurrentView != ViewAAA {
			t.Errorf("[%s] 未进入 ViewAAA", dt)
		}
		aaaVTExecAll(t, st, "local-user u1 password cipher Huawei@123",
			"authentication-scheme s1", "quit", "domain d1")
	}
}

// TestAAAVTDisplayReadableOnAnyDevice 断言 display 只读命令在任意设备均放行为空态。
func TestAAAVTDisplayReadableOnAnyDevice(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer} {
		for _, cmd := range []string{"display local-user", "display aaa", "display domain"} {
			out := aaaVTExec(aaaVTDevice(dt), cmd)
			if strings.Contains(out, "is not supported on") {
				t.Errorf("[%s] %s 被能力拒绝，应放行输出空态：%q", dt, cmd, out)
			}
			if !strings.Contains(out, "Info:") {
				t.Errorf("[%s] %s 应输出空态 Info:，实际 %q", dt, cmd, out)
			}
		}
	}
}

// TestAAAVTCapabilitiesUntouched 断言能力矩阵零改动。
func TestAAAVTCapabilitiesUntouched(t *testing.T) {
	src := aaaVTReadSource(t, "capabilities.go")
	if !strings.Contains(src, `"local-user":     l3Devices(),`) {
		t.Error("capabilities.go 的 local-user 行被改动（AC12c 要求零改动）")
	}
	if strings.Contains(src, "authentication-mode") {
		t.Error("capabilities.go 新增了 authentication-mode 矩阵行（AC12c 禁止）")
	}
}

// —— AC13：架构合规 + 键碰撞专项 ——

// TestAAAVTKeyCollisionUndoAAA 是本期最高危项的端到端断言。
func TestAAAVTKeyCollisionUndoAAA(t *testing.T) {
	st := aaaVTMainline(t)
	// 注入含 "aaa" 子串的异族键（仓库实存测试数据 + 最常用示教 MAC + 聚合键）。
	foreign := map[string]string{
		"interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa": "1",
		"interface:GigabitEthernet0/0/2:port-security-sticky-learned:aaaa-bbbb-cccc": "1",
		"interface:Bridge-Aggregation1:lag:mode":                                     "lacp-static",
	}
	for k, v := range foreign {
		st.DeviceConfig[k] = v
	}

	// ① 幽灵用户零产生。
	users := collectAAALocalUsers(st)
	if len(users) != 2 || users[0] != "admin" || users[1] != "guest" {
		t.Fatalf("collectAAALocalUsers = %v, want [admin guest]（MAC 键不得派生幽灵用户）", users)
	}

	// ② undo aaa 后异族键完好无损。
	aaaVTExec(st, "quit")
	if out := aaaVTExec(st, "undo aaa"); out != "" {
		t.Fatalf("undo aaa 应静默成功，实际 %q", out)
	}
	for k, v := range foreign {
		got, ok := st.DeviceConfig[k]
		if !ok {
			t.Errorf("🔴 undo aaa 误删非 AAA 键 %q", k)
			continue
		}
		if got != v {
			t.Errorf("🔴 undo aaa 篡改了非 AAA 键 %q：%q → %q", k, v, got)
		}
	}
	if keys := aaaVTKeys(st); len(keys) != 0 {
		t.Errorf("undo aaa 后仍残留 AAA 键 %v", keys)
	}
}

// TestAAAVTNoFuzzyKeyMatching 静态断言 AAA 源码零模糊匹配（A1 红线）。
func TestAAAVTNoFuzzyKeyMatching(t *testing.T) {
	banned := []string{
		`strings.Contains(k, "aaa")`,
		`strings.Contains(k, "domain")`,
		`strings.Contains(key, "aaa")`,
		`strings.Contains(key, "domain")`,
	}
	for _, f := range []string{"aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		code := aaaVTNonCommentSource(t, f)
		for _, pat := range banned {
			if strings.Contains(code, pat) {
				t.Errorf("🔴 %s 出现模糊匹配 %s（A1 键碰撞红线）", f, pat)
			}
		}
	}
}

// TestAAAVTNoProtocolImport 断言 AAA 源码不 import internal/protocol（C3 红线）。
func TestAAAVTNoProtocolImport(t *testing.T) {
	for _, f := range []string{"aaa_eval.go", "aaa_cmd.go", "aaa_display.go"} {
		if strings.Contains(aaaVTReadSource(t, f), `"ensp-lab/internal/protocol"`) {
			t.Errorf("%s 违规 import internal/protocol", f)
		}
	}
}

// TestAAAVTDisplayHasNoSideEffect 断言 display 系列命令不改写任何键。
func TestAAAVTDisplayHasNoSideEffect(t *testing.T) {
	st := aaaVTMainline(t)
	before := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		before[k] = v
	}
	for _, cmd := range []string{
		"display local-user", "display aaa", "display domain", "display domain huawei",
	} {
		aaaVTExec(st, cmd)
	}
	if len(st.DeviceConfig) != len(before) {
		t.Fatalf("display 改变了键数量：%d → %d", len(before), len(st.DeviceConfig))
	}
	for k, v := range before {
		if st.DeviceConfig[k] != v {
			t.Errorf("display 改写了键 %q：%q → %q", k, v, st.DeviceConfig[k])
		}
	}
}

// TestAAAVTStateGoHasNoLegacyStruct 静态断言 state.go 无任何 AAA 内嵌结构体（架构铁律 §7.1）。
//
// 被禁标识符动态拼接，避免本断言自身的字符串在扫描其它文件时自我命中。
func TestAAAVTStateGoHasNoLegacyStruct(t *testing.T) {
	src := aaaVTReadSource(t, "state.go")
	banned := []string{"Local" + "User", "AAA" + "Config", "Domain" + "Config", "Scheme" + "Config"}
	for _, pat := range banned {
		if strings.Contains(src, pat) {
			t.Errorf("state.go 仍含 %q —— 结构体事实源未彻底废弃（P0-2 / §7.1）", pat)
		}
	}
}
