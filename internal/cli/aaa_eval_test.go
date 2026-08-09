// aaa_eval_test.go 是 AAA 纯函数层（aaa_eval.go）的单元测试（P2 第八项，T8）。
//
// 覆盖重点：
//   - 键 helper 的**精确形态**（含尾冒号），这是 A1 键碰撞红线的第一道防线；
//   - collect* 的精确分段扫描：异形键、深层键、同前缀异族键一律不得混入；
//   - 🔴 键碰撞专项（AC13）：`aaa` 是合法十六进制串，端口安全粘滞 MAC 键
//     `...:00e0-fc12-0aaa` 与 MAC 值 `aaaa-bbbb-cccc` **绝不能**派生出幽灵用户；
//   - 纯函数契约（AC13）：EvaluateAAA 前后对 DeviceConfig 做 deep-equal，且两次调用结果一致；
//   - 诚实占位（AC10）与口令脱敏（AC9⑥）的类型级/取值级断言。
//
// 本文件只测纯函数，命令链路与 display 的端到端断言见 aaa_test.go。
package cli

import (
	"reflect"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// aaaUnitState 构造一个带空 DeviceConfig 的 Router 态，供纯函数测试注入键。
func aaaUnitState() *CLIState {
	st := NewCLIStateWithType(topology.DeviceRouter)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	return st
}

// —— 键 helper 精确形态 ——

func TestAAAKeyHelpersExactForm(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"namespace prefix", aaaKeyPrefix(), "aaa:"},
		{"local user key", aaaLocalUserKey("admin", aaaFieldPassword), "aaa:local-user:admin:password"},
		{"authen scheme key", aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode), "aaa:authen-scheme:sch1:mode"},
		{"author scheme key", aaaSchemeKey(AAASchemeKindAuthor, "sch1", aaaFieldMode), "aaa:author-scheme:sch1:mode"},
		{"acct scheme key", aaaSchemeKey(AAASchemeKindAcct, "sch1", aaaFieldMode), "aaa:acct-scheme:sch1:mode"},
		{"domain key", aaaDomainKey("huawei", aaaFieldAuthenScheme), "aaa:domain:huawei:authen-scheme"},
		{"scheme prefix", aaaSchemePrefix(AAASchemeKindAuthen), "aaa:authen-scheme:"},
		{"entity prefix", aaaEntityPrefix(aaaLocalUserPrefix, "admin"), "aaa:local-user:admin:"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestAAAPrefixesCarryTrailingColon 断言全部前缀常量都带尾冒号。
//
// 🔴 A1 红线：省略尾冒号后 "aaa" 会命中 "aaaa-bbbb-cccc"；
// 实体前缀省略尾冒号后 "admin" 会误删 "administrator"。
func TestAAAPrefixesCarryTrailingColon(t *testing.T) {
	prefixes := map[string]string{
		"aaaNamespacePrefix": aaaNamespacePrefix,
		"aaaLocalUserPrefix": aaaLocalUserPrefix,
		"aaaDomainPrefix":    aaaDomainPrefix,
		"authen scheme":      aaaSchemePrefix(AAASchemeKindAuthen),
		"author scheme":      aaaSchemePrefix(AAASchemeKindAuthor),
		"acct scheme":        aaaSchemePrefix(AAASchemeKindAcct),
		"entity(admin)":      aaaEntityPrefix(aaaLocalUserPrefix, "admin"),
	}
	for name, p := range prefixes {
		if !strings.HasSuffix(p, ":") {
			t.Errorf("%s = %q 缺少尾冒号（A1 键碰撞红线）", name, p)
		}
	}
	// 反例实证：裸 MAC 串与端口安全键均不得命中 "aaa:" 前缀。
	for _, k := range []string{
		"aaaa-bbbb-cccc",
		"interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa",
	} {
		if strings.HasPrefix(k, aaaKeyPrefix()) {
			t.Errorf("异族键 %q 竟命中 AAA 前缀", k)
		}
	}
	// "admin" 的实体前缀不得命中 "administrator" 的键。
	if strings.HasPrefix(aaaLocalUserKey("administrator", aaaFieldPassword),
		aaaEntityPrefix(aaaLocalUserPrefix, "admin")) {
		t.Error("aaa:local-user:administrator:password 竟命中 admin 的实体前缀")
	}
}

// —— collect* 精确分段扫描 ——

func TestCollectAAALocalUsersSortedAndDedup(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaLocalUserKey("zoe", aaaFieldPassword)] = "Zoe@12345"
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)] = "Huawei@123"
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPrivilege)] = "15"
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldServiceType)] = "telnet ssh"
	st.DeviceConfig[aaaLocalUserKey("guest", aaaFieldState)] = AAAUserStateBlock

	got := collectAAALocalUsers(st)
	want := []string{"admin", "guest", "zoe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectAAALocalUsers = %v, want %v（必须升序且去重）", got, want)
	}
}

// TestCollectAAAIgnoresMalformedKeys 断言异形键（缺 field 段 / 多一层 / 空 name）被跳过。
func TestCollectAAAIgnoresMalformedKeys(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)] = "Huawei@123"
	st.DeviceConfig["aaa:local-user:nofield"] = "x"           // 缺 field 段
	st.DeviceConfig["aaa:local-user::password"] = "x"         // name 段为空
	st.DeviceConfig["aaa:local-user:deep:sub:password"] = "x" // 多一层
	st.DeviceConfig["aaa:local-user:"] = "x"                  // 只有前缀

	got := collectAAALocalUsers(st)
	if !reflect.DeepEqual(got, []string{"admin"}) {
		t.Fatalf("collectAAALocalUsers = %v, want [admin]（异形键必须被跳过）", got)
	}
}

func TestCollectAAASchemesAndDomains(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = "local"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "default", aaaFieldMode)] = ""
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthor, "aut1", aaaFieldMode)] = "none"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAcct, "acc1", aaaFieldMode)] = "none"
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldState)] = AAADefaultUserState
	st.DeviceConfig[aaaDomainKey("abc", aaaFieldState)] = AAADefaultUserState

	if got, want := collectAAASchemes(st, AAASchemeKindAuthen), []string{"default", "sch1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("authen schemes = %v, want %v", got, want)
	}
	// 🔴 种类隔离：authen 扫描不得混入 author / acct 的方案。
	if got, want := collectAAASchemes(st, AAASchemeKindAuthor), []string{"aut1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("author schemes = %v, want %v", got, want)
	}
	if got, want := collectAAASchemes(st, AAASchemeKindAcct), []string{"acc1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("acct schemes = %v, want %v", got, want)
	}
	if got, want := collectAAADomains(st), []string{"abc", "huawei"}; !reflect.DeepEqual(got, want) {
		t.Errorf("domains = %v, want %v", got, want)
	}
	// 域扫描不得把 local-user / scheme 键当成域。
	for _, d := range collectAAADomains(st) {
		if d == "admin" || d == "sch1" {
			t.Errorf("域列表混入了非域实体 %q", d)
		}
	}
}

// TestCollectAAALocalUsersKeyCollision 是 AC13 键碰撞专项（本期最高危项）。
//
// 构造仓库实存的端口安全粘滞 MAC 键（含 `0aaa`）、最常用示教 MAC（`aaaa-bbbb-cccc`）
// 与链路聚合键，断言 collect* **只**返回真正的 AAA 实体。
func TestCollectAAALocalUsersKeyCollision(t *testing.T) {
	st := aaaUnitState()
	foreign := map[string]string{
		"interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa": "1",
		"interface:GigabitEthernet0/0/2:port-security-sticky-learned:aaaa-bbbb-cccc": "1",
		"interface:Bridge-Aggregation1:lag:mode":                                     "lacp-static",
		"aaaa-bbbb-cccc":                                                             "ghost",
		"mstp:region:domain":                                                         "R1",
	}
	for k, v := range foreign {
		st.DeviceConfig[k] = v
	}
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)] = "Huawei@123"

	if got := collectAAALocalUsers(st); !reflect.DeepEqual(got, []string{"admin"}) {
		t.Fatalf("collectAAALocalUsers = %v, want [admin]（MAC 键不得派生幽灵用户）", got)
	}
	if got := collectAAADomains(st); len(got) != 0 {
		t.Fatalf("collectAAADomains = %v, want 空（mstp:region:domain 不得被误判为 AAA 域）", got)
	}
	for _, kind := range []string{AAASchemeKindAuthen, AAASchemeKindAuthor, AAASchemeKindAcct} {
		if got := collectAAASchemes(st, kind); len(got) != 0 {
			t.Fatalf("collectAAASchemes(%s) = %v, want 空", kind, got)
		}
	}
	// 评估器整体同样只看到 admin。
	res := EvaluateAAA(st)
	if len(res.Users) != 1 || res.Users[0].Name != "admin" {
		t.Fatalf("EvaluateAAA.Users = %+v, want 仅 admin", res.Users)
	}
}

// —— 存在性判定 ——

func TestAAAExistenceHelpers(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaLocalUserKey("administrator", aaaFieldPassword)] = "Huawei@123"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = ""
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldState)] = AAADefaultUserState

	if aaaLocalUserExists(st, "admin") {
		t.Error("admin 不存在，却因 administrator 的前缀被误判为存在（A1 红线）")
	}
	if !aaaLocalUserExists(st, "administrator") {
		t.Error("administrator 应存在")
	}
	// 空值 mode 键同样构成「方案存在」（存在性标记语义）。
	if !aaaSchemeExists(st, AAASchemeKindAuthen, "sch1") {
		t.Error("空值 mode 键应视为方案已创建")
	}
	if aaaSchemeExists(st, AAASchemeKindAuthor, "sch1") {
		t.Error("author 种类下不存在 sch1，不得跨种类误判")
	}
	if !aaaDomainExists(st, "huawei") || aaaDomainExists(st, "hua") {
		t.Error("域存在性判定错误（前缀必须精确到尾冒号）")
	}
}

// —— 校验 helper ——

func TestAAAValidPrivilege(t *testing.T) {
	valid := []string{"0", "1", "15"}
	invalid := []string{"-1", "16", "abc", "", " 5", "1.0"}
	for _, v := range valid {
		if !aaaValidPrivilege(v) {
			t.Errorf("aaaValidPrivilege(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if aaaValidPrivilege(v) {
			t.Errorf("aaaValidPrivilege(%q) = true, want false", v)
		}
	}
}

func TestAAAValidPasswordLen(t *testing.T) {
	if aaaValidPasswordLen(strings.Repeat("a", AAAPasswordMinLen-1)) {
		t.Error("7 位口令应判为非法")
	}
	if !aaaValidPasswordLen(strings.Repeat("a", AAAPasswordMinLen)) {
		t.Error("8 位口令应判为合法")
	}
	if !aaaValidPasswordLen(strings.Repeat("a", AAAPasswordMaxLen)) {
		t.Error("128 位口令应判为合法")
	}
	if aaaValidPasswordLen(strings.Repeat("a", AAAPasswordMaxLen+1)) {
		t.Error("129 位口令应判为非法")
	}
}

// TestNormalizeAAAServiceTypesOrderIndependent 断言输入顺序无关的字节级确定性（拍板 C6）。
func TestNormalizeAAAServiceTypesOrderIndependent(t *testing.T) {
	a, bad := normalizeAAAServiceTypes([]string{"ssh", "telnet"})
	if bad != "" {
		t.Fatalf("意外非法 token %q", bad)
	}
	b, _ := normalizeAAAServiceTypes([]string{"telnet", "ssh"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("规范化结果与输入顺序相关：%v vs %v", a, b)
	}
	if !reflect.DeepEqual(a, []string{"telnet", "ssh"}) {
		t.Fatalf("规范化结果 = %v, want [telnet ssh]（按 AAAServiceTypeOrder 下标升序）", a)
	}
	// 去重。
	dedup, _ := normalizeAAAServiceTypes([]string{"ssh", "ssh", "ssh"})
	if !reflect.DeepEqual(dedup, []string{"ssh"}) {
		t.Fatalf("去重失败：%v", dedup)
	}
	// 非法 token 原样回传，且不产生部分结果。
	out, bad := normalizeAAAServiceTypes([]string{"telnet", "vnc"})
	if bad != "vnc" {
		t.Fatalf("非法 token = %q, want vnc", bad)
	}
	if out != nil {
		t.Fatalf("校验失败时不得返回部分结果，got %v", out)
	}
}

// —— 评估器：缺省回退 ——

func TestEvaluateAAADefaultFallback(t *testing.T) {
	st := aaaUnitState()
	// 只配口令，privilege / service-type / state 全部缺省。
	st.DeviceConfig[aaaLocalUserKey("u1", aaaFieldPassword)] = "Huawei@123"
	// 方案只有存在性标记（空值 mode）。
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "default", aaaFieldMode)] = ""
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAcct, "acc1", aaaFieldMode)] = ""

	res := EvaluateAAA(st)
	u := res.Users[0]
	// 🔴 P0-5 关键断言：未配 privilege 显示 "-"，**不得**回退成 "0"。
	if u.Privilege != AAANotConfiguredPlaceholder {
		t.Errorf("未配 privilege = %q, want %q（严禁死字段假 0）", u.Privilege, AAANotConfiguredPlaceholder)
	}
	if len(u.ServiceType) != 0 {
		t.Errorf("未配 service-type = %v, want 空", u.ServiceType)
	}
	if u.State != AAADefaultUserState {
		t.Errorf("未配 state = %q, want %q", u.State, AAADefaultUserState)
	}
	if !u.HasPassword {
		t.Error("已配口令的用户 HasPassword 应为 true")
	}
	// 认证方案缺省 local、计费方案缺省 none。
	if res.AuthenSchemes[0].Mode != "local" {
		t.Errorf("认证方案缺省 mode = %q, want local", res.AuthenSchemes[0].Mode)
	}
	if res.AcctSchemes[0].Mode != "none" {
		t.Errorf("计费方案缺省 mode = %q, want none", res.AcctSchemes[0].Mode)
	}
}

// TestAAASchemeExistenceMarkerSemantics 断言「存在性 + 是否显式配置」的双语义编码。
//
// 这是 display aaa（回退渲染缺省）与 display current-configuration（缺省不冗余输出）
// 能用**同一个键**精确复现 PRD §4.3 / §4.5 的关键：空串 = 已创建未显式配置。
func TestAAASchemeExistenceMarkerSemantics(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "default", aaaFieldMode)] = "" // 未显式配置
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = "local"

	res := EvaluateAAA(st)
	if len(res.AuthenSchemes) != 2 {
		t.Fatalf("方案数 = %d, want 2（空值键必须计入存在性）", len(res.AuthenSchemes))
	}
	// 视图层：两者 Mode 都渲染成 local（生效值）。
	for _, s := range res.AuthenSchemes {
		if s.Mode != "local" {
			t.Errorf("方案 %s 的生效 Mode = %q, want local", s.Name, s.Mode)
		}
	}
	// 键层：仅 sch1 是「显式配置过」，快照据此决定是否输出子行。
	if raw := st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "default", aaaFieldMode)]; raw != "" {
		t.Errorf("default 的原始 mode 键 = %q, want 空串", raw)
	}
	if raw := st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)]; raw != "local" {
		t.Errorf("sch1 的原始 mode 键 = %q, want local", raw)
	}
}

func TestAAAFindSchemeModeAndReferences(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = "radius"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch2", aaaFieldMode)] = ""
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldAuthenScheme)] = "sch1"
	st.DeviceConfig[aaaDomainKey("abc", aaaFieldAuthenScheme)] = "sch1"
	st.DeviceConfig[aaaDomainKey("other", aaaFieldAuthenScheme)] = "sch2"

	if mode, ok := aaaFindSchemeMode(st, AAASchemeKindAuthen, "sch1"); !ok || mode != "radius" {
		t.Errorf("aaaFindSchemeMode(sch1) = (%q,%v), want (radius,true)", mode, ok)
	}
	// 空值 mode 键 → 存在且回退缺省。
	if mode, ok := aaaFindSchemeMode(st, AAASchemeKindAuthen, "sch2"); !ok || mode != "local" {
		t.Errorf("aaaFindSchemeMode(sch2) = (%q,%v), want (local,true)", mode, ok)
	}
	if _, ok := aaaFindSchemeMode(st, AAASchemeKindAuthen, "nosuch"); ok {
		t.Error("不存在的方案不得返回 ok=true")
	}
	refs := aaaDomainsReferencingScheme(st, AAASchemeKindAuthen, "sch1")
	if !reflect.DeepEqual(refs, []string{"abc", "huawei"}) {
		t.Errorf("引用 sch1 的域 = %v, want [abc huawei]（升序）", refs)
	}
	// 种类隔离：author 侧无人引用 sch1。
	if refs := aaaDomainsReferencingScheme(st, AAASchemeKindAuthor, "sch1"); len(refs) != 0 {
		t.Errorf("author 侧引用 = %v, want 空", refs)
	}
}

// —— 纯函数契约（AC13）——

// TestEvaluateAAAIsPure 断言 EvaluateAAA 无副作用且幂等。
func TestEvaluateAAAIsPure(t *testing.T) {
	st := aaaUnitState()
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPassword)] = "Huawei@123"
	st.DeviceConfig[aaaLocalUserKey("admin", aaaFieldPrivilege)] = "15"
	st.DeviceConfig[aaaSchemeKey(AAASchemeKindAuthen, "sch1", aaaFieldMode)] = "local"
	st.DeviceConfig[aaaDomainKey("huawei", aaaFieldAuthenScheme)] = "sch1"

	before := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		before[k] = v
	}
	view, sub := st.CurrentView, st.CurrentSub

	first := EvaluateAAA(st)
	second := EvaluateAAA(st)

	if !reflect.DeepEqual(st.DeviceConfig, before) {
		t.Error("EvaluateAAA 改写了 DeviceConfig（违反纯函数契约）")
	}
	if st.CurrentView != view || st.CurrentSub != sub {
		t.Error("EvaluateAAA 改写了视图状态（违反纯函数契约）")
	}
	if !reflect.DeepEqual(first, second) {
		t.Error("EvaluateAAA 两次调用结果不一致（违反确定性）")
	}
}

// TestEvaluateAAANilSafe 断言评估器对 nil state / nil map 不 panic。
func TestEvaluateAAANilSafe(t *testing.T) {
	if res := EvaluateAAA(nil); !res.IsEmpty() {
		t.Error("EvaluateAAA(nil) 应返回空结果")
	}
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig = nil
	if res := EvaluateAAA(st); !res.IsEmpty() {
		t.Error("DeviceConfig 为 nil 时应返回空结果")
	}
	if got := collectAAALocalUsers(st); got != nil {
		t.Errorf("collectAAALocalUsers(nil map) = %v, want nil", got)
	}
}

// —— 口令脱敏（AC9⑥）——

// TestMaskAAAPasswordAlwaysStars 断言脱敏函数**恒**返回 "****"，与入参无关。
func TestMaskAAAPasswordAlwaysStars(t *testing.T) {
	for _, raw := range []string{"", "Huawei@123", "anything", strings.Repeat("x", 128)} {
		if got := maskAAAPassword(raw); got != "****" {
			t.Errorf("maskAAAPassword(%q) = %q, want ****", raw, got)
		}
	}
	// 🔴 严禁伪造 VRP 密文串。
	if strings.Contains(maskAAAPassword("Huawei@123"), "%^%#") {
		t.Error("脱敏结果中出现伪造密文标记 %^%#")
	}
}

// —— 诚实占位（AC10）——

// TestAAAStatsAllPlaceholder 从**类型层面**断言运行态统计恒为 "-"。
//
// 用反射遍历全部字段：将来若有人给 AAAStats 加了 int 字段或填入数字，本测试立即失败。
func TestAAAStatsAllPlaceholder(t *testing.T) {
	stats := EvaluateAAA(aaaUnitState()).Stats
	v := reflect.ValueOf(stats)
	tp := v.Type()
	if v.NumField() == 0 {
		t.Fatal("AAAStats 无字段，断言失效")
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.String {
			t.Errorf("AAAStats.%s 类型为 %s，🔴 必须恒为 string（诚实占位红线）", tp.Field(i).Name, f.Kind())
			continue
		}
		if f.String() != AAAStatPlaceholder {
			t.Errorf("AAAStats.%s = %q, want %q（严禁编造运行态数据）",
				tp.Field(i).Name, f.String(), AAAStatPlaceholder)
		}
	}
}

// TestAAASimNoteHonest 断言注记逐字包含「无真实登录握手 / 无 RADIUS 协议交互」诚实说明。
func TestAAASimNoteHonest(t *testing.T) {
	note := aaaSimNote()
	for _, sub := range []string{"配置态模拟", "无真实登录握手", "无 RADIUS 协议交互"} {
		if !strings.Contains(note, sub) {
			t.Errorf("aaaSimNote() 缺少诚实说明子串 %q，实际：%s", sub, note)
		}
	}
	cipher := aaaCipherNote()
	for _, sub := range []string{"未实现 VRP 密文算法", "明文存于本地配置文件"} {
		if !strings.Contains(cipher, sub) {
			t.Errorf("aaaCipherNote() 缺少诚实说明子串 %q，实际：%s", sub, cipher)
		}
	}
}

// —— 子视图编码解析 ——

// TestAAAParseSchemeSub 断言含 '-' 的方案名能被正确还原（SplitN 而非 Split）。
func TestAAAParseSchemeSub(t *testing.T) {
	cases := []struct {
		sub      string
		wantKind string
		wantName string
		wantOK   bool
	}{
		{aaaSchemeSub(AAASchemeKindAuthen, "sch1"), AAASchemeKindAuthen, "sch1", true},
		{aaaSchemeSub(AAASchemeKindAuthor, "sch-1"), AAASchemeKindAuthor, "sch-1", true},
		{aaaSchemeSub(AAASchemeKindAcct, "a-b-c"), AAASchemeKindAcct, "a-b-c", true},
		{"authen-", "", "", false},
		{"unknown-sch1", "", "", false},
		{"sch1", "", "", false},
	}
	for _, c := range cases {
		kind, name, ok := aaaParseSchemeSub(c.sub)
		if kind != c.wantKind || name != c.wantName || ok != c.wantOK {
			t.Errorf("aaaParseSchemeSub(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.sub, kind, name, ok, c.wantKind, c.wantName, c.wantOK)
		}
	}
}

// TestAAAResultIsEmpty 断言空态判定覆盖全部五类实体。
func TestAAAResultIsEmpty(t *testing.T) {
	if !EvaluateAAA(aaaUnitState()).IsEmpty() {
		t.Error("无任何 AAA 键时 IsEmpty 应为 true")
	}
	for _, key := range []string{
		aaaLocalUserKey("u", aaaFieldPassword),
		aaaSchemeKey(AAASchemeKindAuthen, "s", aaaFieldMode),
		aaaSchemeKey(AAASchemeKindAuthor, "s", aaaFieldMode),
		aaaSchemeKey(AAASchemeKindAcct, "s", aaaFieldMode),
		aaaDomainKey("d", aaaFieldState),
	} {
		st := aaaUnitState()
		st.DeviceConfig[key] = ""
		if EvaluateAAA(st).IsEmpty() {
			t.Errorf("存在键 %q 时 IsEmpty 仍为 true", key)
		}
	}
}
