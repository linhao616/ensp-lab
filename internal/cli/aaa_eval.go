// aaa_eval.go 实现「CLIState 层 AAA 本地认证纯函数评估器」
// （P2 第八项，华为 VRP 课程 71，T0）。
//
// 背景与约束见 docs/p2-aaa-prd.md 与 docs/p2-aaa-design.md。
//
// 架构基线（与 GRE / 端口安全 / STP / 链路聚合 完全同构，见设计 §1.9 / 附录对照表）：
//
//   - 单一事实源 = state.DeviceConfig 的 "aaa:" 命名空间：
//     本地用户   aaa:local-user:<name>:password        原样存明文（本仿真器未实现 VRP 密文算法）
//     aaa:local-user:<name>:privilege       "0".."15"；未配置 = 键不存在
//     aaa:local-user:<name>:service-type    规范化去重列表，如 "telnet ssh"
//     aaa:local-user:<name>:state           "block"；缺省 active = 键不存在（拍板 C8）
//     认证方案   aaa:authen-scheme:<name>:mode         "local"|"radius"|"none"
//     授权方案   aaa:author-scheme:<name>:mode         "local"|"none"
//     计费方案   aaa:acct-scheme:<name>:mode           "none"|"radius"
//     域         aaa:domain:<name>:authen-scheme       绑定的方案名
//     aaa:domain:<name>:author-scheme
//     aaa:domain:<name>:acct-scheme
//     aaa:domain:<name>:state               "active"|"block"
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化
//     （设计 A9：两者零改动）。
//
//   - **严禁在 state.go 新增任何 AAA / LocalUser / Domain / Scheme 内嵌结构体**
//     （架构铁律，设计 §7.1 / P0-2）。LocalUserView / SchemeView / DomainView / AAAResult
//     仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不双写。
//     被删除的旧 `LocalUser` 名字**禁止复用**。
//
//   - 🔴 **A1 键碰撞红线（本期最高危项）**：`aaa` 是一个合法的十六进制串，端口安全的
//     粘滞 MAC 键形如 `interface:GigabitEthernet0/0/1:port-security-sticky-learned:00e0-fc12-0aaa`
//     （含 `0aaa`），MAC 值亦可为 `aaaa-bbbb-cccc`；`domain` 一词还出现在 m-lag / MSTP 语境。
//     因此本文件**严禁**出现任何 `strings.Contains(k, "aaa")` / `strings.Contains(k, "domain")`
//     形式的模糊匹配；全部键解析必须走本文件的精确 helper：
//     精确前缀 `aaa:`（**含尾冒号**）+ 精确分段（name / field 各占一段，段内不含 ':'）。
//     反例实证：`HasPrefix("aaaa-bbbb-cccc", "aaa:")` == false（第 4 字符 'a' != ':'），
//     `HasPrefix("interface:...:00e0-fc12-0aaa", "aaa:")` == false（首段是 "interface"）。
//
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import internal/protocol），
//     与 gre_eval.go 的 EvaluateGRE、stp_eval.go 的 EvaluateSTP 同一契约。
//
//   - 🔴 **诚实占位红线**（设计 §7.3 / P0-14 / AC13）：AAAStats 全部字段类型恒 string 且值恒 "-"，
//     从类型层面杜绝填入编造的认证成功/失败次数、在线会话数、计费字节数与最后登录时间。
package cli

import (
	"sort"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// —— 规格常量（设计 §4.4，拍板 C5/C6/C8）——

const (
	// AAAPasswordMinLen / AAAPasswordMaxLen 是本地用户口令的合法长度区间（拍板 C5）。
	AAAPasswordMinLen = 8
	AAAPasswordMaxLen = 128

	// AAAPrivilegeMin / AAAPrivilegeMax 是本地用户级别的合法取值区间。
	AAAPrivilegeMin = 0
	AAAPrivilegeMax = 15

	// AAAStatPlaceholder 是全部**运行态**统计字段的恒定占位符（诚实占位红线，AC13）。
	// 🔴 严禁被替换为任何数字、随机数或 time.Now() 派生值。
	AAAStatPlaceholder = "-"

	// AAANotConfiguredPlaceholder 是**配置态**字段「未配置」的渲染占位符。
	// 特别地：privilege 未配时显示 "-" 而**不是** "0"（直击旧 parser.go:3427 的死字段假 0 缺陷）。
	AAANotConfiguredPlaceholder = "-"

	// AAADefaultUserState 是本地用户的生效缺省状态；「生效缺省」= 键不落盘（拍板 C8）。
	AAADefaultUserState = "active"

	// AAAUserStateBlock 是本地用户被阻断的状态值。
	AAAUserStateBlock = "block"

	// AAADefaultAuthMode 是认证方案的生效缺省模式。
	AAADefaultAuthMode = "local"
)

// —— 键片段常量（A1 红线：精确匹配专用，全仓拼键 / 解键的唯一素材）——

const (
	// aaaNamespacePrefix 是 AAA 命名空间的**精确前缀**。
	//
	// 🔴 尾部冒号不可省略：省略后 "aaa" 会误命中 "aaaa-bbbb-cccc" 之类的裸 MAC 键名，
	// 这正是 §1.7 键碰撞实证所指的最高危场景。
	aaaNamespacePrefix = "aaa:"

	// aaaLocalUserPrefix 是本地用户键的精确前缀（aaa:local-user:<name>:<field>）。
	aaaLocalUserPrefix = aaaNamespacePrefix + "local-user:"

	// aaaDomainPrefix 是域键的精确前缀（aaa:domain:<name>:<field>）。
	//
	// 🔴 严禁退化为 strings.Contains(k, "domain")：m-lag 的 `domain` 命令与 MSTP
	// 的 region 语境同样含该词（§1.8 顶层 token 冲突复核）。
	aaaDomainPrefix = aaaNamespacePrefix + "domain:"

	// aaaSchemeSuffixSeg 是方案键第二段的固定后缀（<kind>-scheme）。
	aaaSchemeSuffixSeg = "-scheme:"
)

// —— 方案种类常量（aaaSchemeKey 的 kind 入参）——

const (
	AAASchemeKindAuthen = "authen"
	AAASchemeKindAuthor = "author"
	AAASchemeKindAcct   = "acct"
)

// —— 字段名常量（键 helper 的 field 入参，避免手写裸串）——

const (
	aaaFieldPassword     = "password"
	aaaFieldPrivilege    = "privilege"
	aaaFieldServiceType  = "service-type"
	aaaFieldState        = "state"
	aaaFieldMode         = "mode"
	aaaFieldAuthenScheme = "authen-scheme"
	aaaFieldAuthorScheme = "author-scheme"
	aaaFieldAcctScheme   = "acct-scheme"
)

// AAAServiceTypeOrder 是 service-type 的固定枚举顺序（拍板 C6）。
//
// 规范化去重一律以本切片的下标为序，保证 `local-user u service-type ssh telnet`
// 与 `... telnet ssh` 落盘与展示**完全一致**（确定性红线）。
var AAAServiceTypeOrder = []string{"telnet", "ssh", "ftp", "http", "terminal", "ppp"}

// —— 错误文案常量（设计 §4.4 / §7.2，QA 逐字断言用）——

const (
	ErrAAAViewFirst     = "Error: Please configure it in the AAA view. Run 'aaa' first."
	ErrMustBeInVTY      = "Error: must be in VTY user interface view"
	ErrAuthSchemeFirst  = "Error: Please run 'authentication-scheme <name>' first."
	ErrSchemeNotExist   = "Error: The authentication scheme %s does not exist."
	ErrSchemeReferenced = "Error: The authentication scheme %s is referenced by domain %s and cannot be deleted."
	ErrDomainNotExist   = "Error: The domain %s does not exist."
	ErrPrivilegeRange   = "Error: Privilege level must be between 0 and 15."
	ErrServiceType      = "Error: Invalid service-type %s. Available: telnet, ssh, ftp, http, terminal, ppp."
	// ErrServiceTypeUsage 用于「service-type 一个类型都没给」的**缺参**场景。
	//
	// 不得复用 ErrServiceType：那会渲染成 "Invalid service-type . Available: ..."
	// —— 空的类型名 + 多余空格，且把「缺参」误报成「非法值」，
	// 对照 PRD §5 AC7⑥「缺参 → 含 usage:」的口径属于缺陷文案。
	ErrServiceTypeUsage = "Error: usage: local-user <name> service-type { telnet | ssh | ftp | http | terminal | ppp } [...]"
	ErrStateUsage       = "Error: usage: local-user <name> state { active | block }"
	ErrPasswordUsage    = "Error: usage: local-user <name> password cipher <password>"
	ErrPasswordLength   = "Error: The password length must be between 8 and 128."
	ErrUnrecognized     = "Error: unrecognized command"

	// —— 缺参 usage 文案（设计 §4.4 未定义，本期补齐）——
	//
	// 设计 §4.4 只给出了 ErrStateUsage / ErrPasswordUsage / ErrServiceTypeUsage 三条
	// `usage:` 文案，却未覆盖 local-user 本身与 privilege 的**缺参**分支。
	// 但 PRD AC7⑥ 明确要求「`local-user`（缺参）→ 含 `usage:`」，
	// 且同为属性缺参，password / state / service-type 给 usage 而 privilege
	// 给 "unrecognized command"，在教学上自相矛盾（学员看不出少打了什么）。
	//
	// 故按既有句式补齐两条，使「缺参 → usage:，值非法 → 具体约束文案」
	// 成为全命令族的统一口径：
	//   local-user admin privilege          → ErrPrivilegeUsage（少打了 level <n>）
	//   local-user admin privilege level 16 → ErrPrivilegeRange（打了但超范围）
	// 🔴 ErrPrivilegeRange 仍**专用于**取值越界（AC7① 逐字断言），不得挪作缺参用。
	ErrLocalUserUsage = "Error: usage: local-user <name> { password cipher <password> | " +
		"privilege level <0-15> | service-type <type> ... | state { active | block } }"
	ErrPrivilegeUsage = "Error: usage: local-user <name> privilege level <0-15>"
)

// —— 只读派生视图类型（设计 §4.2；严禁反向写回 CLIState）——

// LocalUserView 是单个本地用户的只读派生视图。
type LocalUserView struct {
	// Name 是用户名，可含 '@'（拍板 C4：仅做合法性校验，不做域解析）。
	Name string
	// HasPassword 表示是否已配置口令。
	//
	// 🔴 诚实边界：展示层据此区分 "****"（已配）与 "-"（未配），
	// **严禁**输出任何形如 %^%#...%^%# 的伪造 VRP 密文串。
	HasPassword bool
	// Privilege 是用户级别字符串；未配置时为 "-"（**不得**回退成 "0"）。
	Privilege string
	// ServiceType 是已规范化去重、按 AAAServiceTypeOrder 排序的服务类型列表；未配置为空。
	ServiceType []string
	// State 是用户状态，"active" | "block"；键不存在时回退 AAADefaultUserState。
	State string
}

// SchemeView 是单个认证/授权/计费方案的只读派生视图。
type SchemeView struct {
	Name string
	// Mode 是方案模式；方案创建时即写入该种类的生效缺省值，故恒非空。
	Mode string
}

// DomainView 是单个 AAA 域的只读派生视图。
//
// 三个 *Scheme 字段保存**原始绑定名**（未绑定为 "-"）。跨对象解引用与
// "- (not found)" 装饰由展示层完成（渲染归渲染，评估器保持纯净），
// 这样汇总表列宽不会被装饰串撑破，与 PRD §4.4 样例一致。
type DomainView struct {
	Name         string
	AuthenScheme string
	AuthorScheme string
	AcctScheme   string
	State        string
}

// AAAStats 是 AAA 运行态统计的**诚实占位**容器。
//
// 🔴 设计 §7.3 红线：全部字段类型恒 string 且值恒 AAAStatPlaceholder（"-"）。
// 本仿真器为配置态模拟，无真实登录握手、无 RADIUS 协议交互、无计费采集，
// 因此不存在任何真实数据源。**严禁**把任一字段改成 int 或填入编造值。
type AAAStats struct {
	OnlineUsers  string
	AuthSuccess  string
	AuthFail     string
	AcctSessions string
	AcctInput    string
	AcctOutput   string
	AcctRecords  string
}

// AAAResult 是一次 AAA 评估的完整只读结果。
type AAAResult struct {
	Users         []LocalUserView
	AuthenSchemes []SchemeView
	AuthorSchemes []SchemeView
	AcctSchemes   []SchemeView
	Domains       []DomainView
	// VTYAuthMode 只读自 state.VTY.AuthenticationMode（P1-8：只展示引用关系，不模拟登录）。
	VTYAuthMode string
	Stats       AAAStats
}

// IsEmpty 报告是否不存在任何 AAA 配置（用于展示层空态 Info: 分支）。
func (r AAAResult) IsEmpty() bool {
	return len(r.Users) == 0 &&
		len(r.AuthenSchemes) == 0 &&
		len(r.AuthorSchemes) == 0 &&
		len(r.AcctSchemes) == 0 &&
		len(r.Domains) == 0
}

// —— 键构造 helper（设计 §4.2 / A7，全仓拼键与解键的唯一素材）——

// aaaKeyPrefix 返回 AAA 命名空间的精确前缀 "aaa:"（含尾冒号）。
//
// 任何扫描 / 级联清理必须以 strings.HasPrefix(k, aaaKeyPrefix()) 起手，
// 严禁 strings.Contains(k, "aaa")（A1 红线）。
func aaaKeyPrefix() string {
	return aaaNamespacePrefix
}

// aaaLocalUserKey 构造本地用户键：aaa:local-user:<name>:<field>。
func aaaLocalUserKey(name, field string) string {
	return aaaLocalUserPrefix + name + ":" + field
}

// aaaSchemeKey 构造方案键：aaa:<kind>-scheme:<name>:<field>。
// kind ∈ {authen, author, acct}。
func aaaSchemeKey(kind, name, field string) string {
	return aaaNamespacePrefix + kind + aaaSchemeSuffixSeg + name + ":" + field
}

// aaaDomainKey 构造域键：aaa:domain:<name>:<field>。
func aaaDomainKey(name, field string) string {
	return aaaDomainPrefix + name + ":" + field
}

// aaaSchemePrefix 返回某种类方案键的精确前缀（aaa:<kind>-scheme:）。
func aaaSchemePrefix(kind string) string {
	return aaaNamespacePrefix + kind + aaaSchemeSuffixSeg
}

// aaaEntityPrefix 返回某实体（用户/方案/域）全部键的精确前缀，用于级联清理。
// 形如 aaa:local-user:<name>: —— **必须带尾冒号**，否则 "admin" 会误删 "administrator"。
func aaaEntityPrefix(base, name string) string {
	return base + name + ":"
}

// —— 收集器（精确前缀 + 精确分段扫描，返回名称升序去重）——

// collectAAANamesByPrefix 是三个 collect* 的共同实现。
//
// 精确分段规则：剥掉 prefix 后剩余串必须恰好是 "<name>:<field>" 两段，
// 即 name 段与 field 段均非空、且 field 段内不再含 ':'。任何不满足者一律跳过，
// 从结构上排除「前缀碰巧相同但层级不同」的异形键。
func collectAAANamesByPrefix(state *CLIState, prefix string) []string {
	if state == nil || state.DeviceConfig == nil {
		return nil
	}
	seen := make(map[string]bool)
	names := make([]string, 0, 4)
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		idx := strings.IndexByte(rest, ':')
		if idx <= 0 {
			// 没有 field 段，或 name 段为空 —— 非法键，跳过。
			continue
		}
		name := rest[:idx]
		field := rest[idx+1:]
		if field == "" || strings.IndexByte(field, ':') >= 0 {
			// field 段为空，或还有更深层级 —— 非本层实体键，跳过。
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// collectAAALocalUsers 返回全部本地用户名（升序去重）。
func collectAAALocalUsers(state *CLIState) []string {
	return collectAAANamesByPrefix(state, aaaLocalUserPrefix)
}

// collectAAASchemes 返回某种类的全部方案名（升序去重）。kind ∈ {authen, author, acct}。
func collectAAASchemes(state *CLIState, kind string) []string {
	return collectAAANamesByPrefix(state, aaaSchemePrefix(kind))
}

// collectAAADomains 返回全部域名（升序去重）。
func collectAAADomains(state *CLIState) []string {
	return collectAAANamesByPrefix(state, aaaDomainPrefix)
}

// —— 存在性判定（副作用层的前置守卫与引用完整性校验共用）——

// aaaLocalUserExists 报告本地用户是否存在（至少有一个属性键）。
func aaaLocalUserExists(state *CLIState, name string) bool {
	return aaaEntityExists(state, aaaEntityPrefix(aaaLocalUserPrefix, name))
}

// aaaSchemeExists 报告某种类的方案是否存在。
func aaaSchemeExists(state *CLIState, kind, name string) bool {
	return aaaEntityExists(state, aaaEntityPrefix(aaaSchemePrefix(kind), name))
}

// aaaDomainExists 报告域是否存在。
func aaaDomainExists(state *CLIState, name string) bool {
	return aaaEntityExists(state, aaaEntityPrefix(aaaDomainPrefix, name))
}

// aaaEntityExists 报告是否存在以 prefix 开头的任意键（prefix 必须自带尾冒号）。
func aaaEntityExists(state *CLIState, prefix string) bool {
	if state == nil || state.DeviceConfig == nil {
		return false
	}
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// —— 校验 helper（纯函数，副作用层调用）——

// aaaValidPrivilege 报告 privilege 字符串是否是 0..15 的合法十进制整数。
func aaaValidPrivilege(raw string) bool {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return false
	}
	return n >= AAAPrivilegeMin && n <= AAAPrivilegeMax
}

// aaaValidPasswordLen 报告口令长度是否落在 [AAAPasswordMinLen, AAAPasswordMaxLen]（拍板 C5）。
func aaaValidPasswordLen(raw string) bool {
	n := len(raw)
	return n >= AAAPasswordMinLen && n <= AAAPasswordMaxLen
}

// aaaServiceTypeIndex 返回 service-type 在固定枚举中的下标；非法返回 -1。
func aaaServiceTypeIndex(t string) int {
	for i, v := range AAAServiceTypeOrder {
		if v == t {
			return i
		}
	}
	return -1
}

// normalizeAAAServiceTypes 校验并规范化 service-type 列表（拍板 C6）。
//
// 返回 (规范化后的列表, 第一个非法 token)。第二个返回值非空即表示校验失败，
// 此时第一个返回值无意义，调用方必须报 ErrServiceType 且**不写任何键**。
//
// 规范化 = 去重 + 按 AAAServiceTypeOrder 下标升序，保证同一集合的任意输入顺序
// 产生**字节级一致**的落盘值。
func normalizeAAAServiceTypes(tokens []string) ([]string, string) {
	picked := make(map[int]bool, len(tokens))
	for _, t := range tokens {
		idx := aaaServiceTypeIndex(t)
		if idx < 0 {
			return nil, t
		}
		picked[idx] = true
	}
	out := make([]string, 0, len(picked))
	for i, v := range AAAServiceTypeOrder {
		if picked[i] {
			out = append(out, v)
		}
	}
	return out, ""
}

// —— 评估主入口 ——

// EvaluateAAA 从 DeviceConfig 即时派生 AAA 只读视图。
//
// 纯函数：不修改 state、不落任何键、不触碰 sim 引擎实例。
// 生效缺省一律在此回退渲染（privilege 未配 → "-"；state 未配 → "active"），
// 与「缺省键不落盘」（设计 A8）配套。
func EvaluateAAA(state *CLIState) AAAResult {
	result := AAAResult{
		Users:         []LocalUserView{},
		AuthenSchemes: []SchemeView{},
		AuthorSchemes: []SchemeView{},
		AcctSchemes:   []SchemeView{},
		Domains:       []DomainView{},
		VTYAuthMode:   AAANotConfiguredPlaceholder,
		Stats:         newAAAStats(),
	}
	if state == nil {
		return result
	}
	// P1-8：只读引用既有 VTY 认证模式，闭合旧实现的悬空引用；**绝不回写**。
	if state.VTY != nil && state.VTY.AuthenticationMode != "" {
		result.VTYAuthMode = state.VTY.AuthenticationMode
	}
	if state.DeviceConfig == nil {
		return result
	}

	for _, name := range collectAAALocalUsers(state) {
		result.Users = append(result.Users, buildLocalUserView(state, name))
	}
	result.AuthenSchemes = buildSchemeViews(state, AAASchemeKindAuthen)
	result.AuthorSchemes = buildSchemeViews(state, AAASchemeKindAuthor)
	result.AcctSchemes = buildSchemeViews(state, AAASchemeKindAcct)
	for _, name := range collectAAADomains(state) {
		result.Domains = append(result.Domains, buildDomainView(state, name))
	}
	return result
}

// newAAAStats 构造全 "-" 的运行态统计占位（诚实占位红线，AC13）。
func newAAAStats() AAAStats {
	return AAAStats{
		OnlineUsers:  AAAStatPlaceholder,
		AuthSuccess:  AAAStatPlaceholder,
		AuthFail:     AAAStatPlaceholder,
		AcctSessions: AAAStatPlaceholder,
		AcctInput:    AAAStatPlaceholder,
		AcctOutput:   AAAStatPlaceholder,
		AcctRecords:  AAAStatPlaceholder,
	}
}

// buildLocalUserView 派生单个本地用户的只读视图。
func buildLocalUserView(state *CLIState, name string) LocalUserView {
	view := LocalUserView{
		Name:        name,
		Privilege:   AAANotConfiguredPlaceholder,
		ServiceType: nil,
		State:       AAADefaultUserState,
	}
	if pwd, ok := state.DeviceConfig[aaaLocalUserKey(name, aaaFieldPassword)]; ok && pwd != "" {
		view.HasPassword = true
	}
	if priv, ok := state.DeviceConfig[aaaLocalUserKey(name, aaaFieldPrivilege)]; ok && priv != "" {
		view.Privilege = priv
	}
	if svc, ok := state.DeviceConfig[aaaLocalUserKey(name, aaaFieldServiceType)]; ok && svc != "" {
		view.ServiceType = strings.Fields(svc)
	}
	if st, ok := state.DeviceConfig[aaaLocalUserKey(name, aaaFieldState)]; ok && st != "" {
		view.State = st
	}
	return view
}

// buildSchemeViews 派生某种类的全部方案视图（名称升序）。
func buildSchemeViews(state *CLIState, kind string) []SchemeView {
	names := collectAAASchemes(state, kind)
	views := make([]SchemeView, 0, len(names))
	for _, name := range names {
		mode := aaaDefaultSchemeMode(kind)
		if v, ok := state.DeviceConfig[aaaSchemeKey(kind, name, aaaFieldMode)]; ok && v != "" {
			mode = v
		}
		views = append(views, SchemeView{Name: name, Mode: mode})
	}
	return views
}

// buildDomainView 派生单个域的只读视图（保存原始绑定名，不做解引用装饰）。
func buildDomainView(state *CLIState, name string) DomainView {
	view := DomainView{
		Name:         name,
		AuthenScheme: AAANotConfiguredPlaceholder,
		AuthorScheme: AAANotConfiguredPlaceholder,
		AcctScheme:   AAANotConfiguredPlaceholder,
		State:        AAADefaultUserState,
	}
	if v, ok := state.DeviceConfig[aaaDomainKey(name, aaaFieldAuthenScheme)]; ok && v != "" {
		view.AuthenScheme = v
	}
	if v, ok := state.DeviceConfig[aaaDomainKey(name, aaaFieldAuthorScheme)]; ok && v != "" {
		view.AuthorScheme = v
	}
	if v, ok := state.DeviceConfig[aaaDomainKey(name, aaaFieldAcctScheme)]; ok && v != "" {
		view.AcctScheme = v
	}
	if v, ok := state.DeviceConfig[aaaDomainKey(name, aaaFieldState)]; ok && v != "" {
		view.State = v
	}
	return view
}

// aaaDefaultSchemeMode 返回某种类方案的生效缺省模式。
//
// 认证/授权缺省 local，计费缺省 none —— 与真机 VRP 一致。
func aaaDefaultSchemeMode(kind string) string {
	switch kind {
	case AAASchemeKindAcct:
		return "none"
	default:
		return AAADefaultAuthMode
	}
}

// aaaFindSchemeMode 查找某方案的模式；方案不存在时返回 ("", false)。
// 供展示层做跨对象解引用（P1-7）与副作用层做引用完整性校验（A10）。
func aaaFindSchemeMode(state *CLIState, kind, name string) (string, bool) {
	if !aaaSchemeExists(state, kind, name) {
		return "", false
	}
	if v, ok := state.DeviceConfig[aaaSchemeKey(kind, name, aaaFieldMode)]; ok && v != "" {
		return v, true
	}
	return aaaDefaultSchemeMode(kind), true
}

// aaaDomainsReferencingScheme 返回引用了指定方案的全部域名（升序）。
//
// 用于 undo 的引用完整性硬拒绝（拍板 C7 / ErrSchemeReferenced）。
func aaaDomainsReferencingScheme(state *CLIState, kind, name string) []string {
	field := aaaFieldAuthenScheme
	switch kind {
	case AAASchemeKindAuthor:
		field = aaaFieldAuthorScheme
	case AAASchemeKindAcct:
		field = aaaFieldAcctScheme
	}
	var refs []string
	for _, d := range collectAAADomains(state) {
		if state.DeviceConfig[aaaDomainKey(d, field)] == name {
			refs = append(refs, d)
		}
	}
	return refs
}

// —— 脱敏 + 诚实占位 ——

// maskAAAPassword 恒返回 "****"（口令脱敏红线，P0-13）。
//
// 入参仅为调用点自解释；实现刻意**忽略**它，从而在类型层面杜绝口令明文外泄。
// 🔴 严禁改为返回明文、长度提示，或任何形如 %^%#...%^%# 的伪造 VRP 密文串：
// 本仿真器未实现 VRP 不可逆加密算法，伪造密文属于虚假实现。
func maskAAAPassword(raw string) string {
	_ = raw
	return "****"
}

// aaaSimNote 返回 AAA「诚实占位」注记（lite / full 两态，
// 口径同 greSimNote / stpSimNote / portSecSimNote，读 sim.EngineModeName()）。
//
// 全部 display aaa / display local-user / display domain 输出
// **末尾必须附加该注记**（P0-14 / AC13 红线）。
func aaaSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（AAA 为配置态模拟（lite 引擎），无真实登录握手、无 RADIUS 协议交互与计费采集，认证统计与在线会话不可用）"
	}
	return "（AAA 为配置态模拟，无真实登录握手、无 RADIUS 协议交互与计费采集）"
}

// aaaCipherNote 返回口令脱敏的诚实说明（display local-user 专用，P0-13）。
func aaaCipherNote() string {
	return "（口令未做不可逆加密：本仿真器未实现 VRP 密文算法，口令以明文存于本地配置文件，此处仅做展示脱敏）"
}
