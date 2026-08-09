// aaa_cmd.go 实现「AAA 本地认证命令族的副作用唯一出口」
// （P2 第八项，华为 VRP 课程 71，T2/T3/T5/T6）。
//
// 分层契约（与 gre_cmd.go 完全同构）：
//   - 本文件是 AAA 配置**唯一**写 state.DeviceConfig 的地方；
//     aaa_eval.go 纯读、aaa_display.go 纯渲染，二者严禁写 state。
//   - 全部键的拼接一律走 aaa_eval.go 的精确 helper（aaaLocalUserKey / aaaSchemeKey /
//     aaaDomainKey / aaaEntityPrefix），严禁裸串拼接（设计 §7.1 / A7）。
//   - 全部 undo 采用 **handled 模式** `(msg string, handled bool)`：未命中 AAA 命令族时
//     返回 handled=false，由 parser.go 交回既有 undo 分支，保证零回归
//     （复用 applyUndoGREInterface 同款范式，设计 §7.4）。
//
// 三态守卫顺序（全命令统一，设计 §1.9）：视图守卫 → 设备守卫 → 前置条件守卫。
// 任一守卫失败**一律不写任何键**（P0-11 / A10）。
//
// 🔴 A1 键碰撞红线：本文件所有清理动作必须以精确前缀（自带尾冒号）匹配，
// 严禁 strings.Contains(k, "aaa") / strings.Contains(k, "domain")。
// 反例：端口安全粘滞 MAC 键 interface:GE0/0/1:port-security-sticky-learned:00e0-fc12-0aaa
// 与 MAC 值 aaaa-bbbb-cccc 必须在 `undo aaa` 后**完好无损**（AC12 专项断言）。
//
// 🔴 C3 红线：authentication-mode radius / accounting-mode radius 仅接受为**配置态**，
// 不联动 state.RADIUS、不 import internal/protocol、不模拟任何协议交互。
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// —— 设备能力守卫（分支内守卫，设计 A5：capabilities.go 本期零改动）——

// aaaDeviceSupported 判定当前设备类型是否支持 AAA。
//
// 设备集**直接复用 capabilities.go:174 的 l3Devices()**（Router / L3Switch / Firewall / VTEP），
// 严禁在本文件重定义设备集合。
func aaaDeviceSupported(state *CLIState) bool {
	if state == nil || state.DeviceType == "" {
		return true
	}
	return l3Devices()[state.DeviceType]
}

// errAAANotSupported 返回设备类型能力拒绝文案。
func errAAANotSupported(dt string) string {
	return fmt.Sprintf("Error: AAA is not supported on %s", dt)
}

// aaaEnsureDeviceConfig 保证 DeviceConfig 可写（防御 nil map panic）。
func aaaEnsureDeviceConfig(state *CLIState) {
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
}

// —— 视图进入 / 子视图编码 ——

// aaaSchemeSubPrefix 由方案种类推导 CurrentSub 前缀，
// 使提示符恰为 [<dev>-aaa-authen-<name>] / [<dev>-aaa-author-<name>] / [<dev>-aaa-acct-<name>]。
func aaaSchemeSub(kind, name string) string {
	return kind + "-" + name
}

// aaaParseSchemeSub 从 CurrentSub 还原方案种类与名称。
//
// 只在**第一个** '-' 处切分：kind 取值 authen/author/acct 均不含 '-'，
// 而方案名允许含 '-'（如 sch-1），因此必须用 SplitN(.., 2) 而非 Split。
func aaaParseSchemeSub(sub string) (kind, name string, ok bool) {
	parts := strings.SplitN(sub, "-", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}
	switch parts[0] {
	case AAASchemeKindAuthen, AAASchemeKindAuthor, AAASchemeKindAcct:
		return parts[0], parts[1], true
	}
	return "", "", false
}

// applyAAAEnterView 处理系统视图下的 `aaa` 命令：进入 AAA 视图。
//
// VRP 风格：成功静默（返回空串），失败才 Error:（PRD §4.1）。
func applyAAAEnterView(state *CLIState) string {
	if state.CurrentView != ViewSystem {
		return "Error: must be in system view"
	}
	if !aaaDeviceSupported(state) {
		return errAAANotSupported(string(state.DeviceType))
	}
	state.CurrentView = ViewAAA
	state.CurrentSub = ""
	return ""
}

// —— T2：本地用户命令族 ——

// applyAAALocalUser 处理 AAA 视图下的 `local-user <name> <attr> ...`。
//
// 支持属性（真机 VRP 子集，拍板 C4/C5/C6/C8）：
//
//	local-user <name> password cipher <pwd>      口令，长度 8..128
//	local-user <name> privilege level <0..15>    用户级别
//	local-user <name> service-type <t> [t ...]   服务类型，覆盖语义 + 固定枚举排序
//	local-user <name> state { active | block }   用户状态，active 为缺省不落盘
//
// 用户名允许含 '@'（C4：仅合法性校验，**不做**域解析，P2-4）。
// 未知子属性 → ErrUnrecognized，且**不得创建该用户**（PRD §4.1 末条）。
func applyAAALocalUser(state *CLIState, args []string) string {
	// ① 视图守卫：C1 重定向。系统视图下的旧自造命令一律引导到 AAA 视图。
	if state.CurrentView != ViewAAA {
		return ErrAAAViewFirst
	}
	// ② 设备守卫。
	if !aaaDeviceSupported(state) {
		return errAAANotSupported(string(state.DeviceType))
	}
	// ③ 前置条件守卫：必须有用户名 + 至少一个属性。
	if len(args) < 2 {
		return ErrUnrecognized
	}
	name := args[0]
	if name == "" || strings.ContainsAny(name, ":\t ") {
		return ErrUnrecognized
	}
	attr := strings.ToLower(args[1])
	rest := args[2:]

	switch attr {
	case "password":
		return applyAAALocalUserPassword(state, name, rest)
	case "privilege":
		return applyAAALocalUserPrivilege(state, name, rest)
	case "service-type":
		return applyAAALocalUserServiceType(state, name, rest)
	case "state":
		return applyAAALocalUserState(state, name, rest)
	default:
		// 🔴 不创建用户、不写任何键（PRD §4.1：`local-user admin foobar x` 后 admin 不存在）。
		return ErrUnrecognized
	}
}

// applyAAALocalUserPassword 处理 `local-user <name> password cipher <pwd>`。
//
// P2-3：`irreversible-cipher` 与 `simple` 一律 ErrUnrecognized ——
// 本仿真器未实现 VRP 不可逆加密算法，接受该关键字等于虚假实现。
func applyAAALocalUserPassword(state *CLIState, name string, rest []string) string {
	if len(rest) < 2 {
		return ErrPasswordUsage
	}
	if strings.ToLower(rest[0]) != "cipher" {
		return ErrUnrecognized
	}
	pwd := rest[1]
	if !aaaValidPasswordLen(pwd) {
		return ErrPasswordLength
	}
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaLocalUserKey(name, aaaFieldPassword)] = pwd
	return ""
}

// applyAAALocalUserPrivilege 处理 `local-user <name> privilege level <0..15>`。
func applyAAALocalUserPrivilege(state *CLIState, name string, rest []string) string {
	if len(rest) < 2 || strings.ToLower(rest[0]) != "level" {
		return ErrUnrecognized
	}
	if !aaaValidPrivilege(rest[1]) {
		return ErrPrivilegeRange
	}
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaLocalUserKey(name, aaaFieldPrivilege)] = rest[1]
	return ""
}

// applyAAALocalUserServiceType 处理 `local-user <name> service-type <t> [t ...]`。
//
// 覆盖语义（C6）：本次给出的集合**整体替换**旧值；
// 规范化后按 AAAServiceTypeOrder 排序落盘，保证输入顺序无关的字节级确定性。
func applyAAALocalUserServiceType(state *CLIState, name string, rest []string) string {
	if len(rest) == 0 {
		// 缺参走独立 usage 文案；复用 ErrServiceType 会渲染出
		// "Invalid service-type . Available: ..."（空类型名 + 缺参误报成非法值）。
		return ErrServiceTypeUsage
	}
	lowered := make([]string, 0, len(rest))
	for _, t := range rest {
		lowered = append(lowered, strings.ToLower(t))
	}
	normalized, bad := normalizeAAAServiceTypes(lowered)
	if bad != "" {
		return fmt.Sprintf(ErrServiceType, bad)
	}
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaLocalUserKey(name, aaaFieldServiceType)] = strings.Join(normalized, " ")
	return ""
}

// applyAAALocalUserState 处理 `local-user <name> state { active | block }`。
//
// C8：active 为生效缺省，**键不落盘** —— 显式设为 active 等价于删除该键，
// EvaluateAAA 会回退渲染成 active，可观察行为完全一致。
func applyAAALocalUserState(state *CLIState, name string, rest []string) string {
	if len(rest) < 1 {
		return ErrStateUsage
	}
	switch strings.ToLower(rest[0]) {
	case AAADefaultUserState:
		aaaEnsureDeviceConfig(state)
		delete(state.DeviceConfig, aaaLocalUserKey(name, aaaFieldState))
		return ""
	case AAAUserStateBlock:
		aaaEnsureDeviceConfig(state)
		state.DeviceConfig[aaaLocalUserKey(name, aaaFieldState)] = AAAUserStateBlock
		return ""
	default:
		return ErrStateUsage
	}
}

// —— T3/T6：方案命令族（认证 / 授权 / 计费三种同构）——

// applyAAAAuthenticationScheme 处理 `authentication-scheme <name>`。
//
// 该命令**双语义**，按当前视图分派：
//   - ViewAAA        → 创建方案（幂等）并进入方案子视图；
//   - ViewAAADomain  → 把方案**绑定**到当前域（引用完整性硬校验，A10）。
func applyAAAAuthenticationScheme(state *CLIState, args []string) string {
	return applyAAASchemeCommand(state, AAASchemeKindAuthen, args)
}

// applyAAAAuthorizationScheme 处理 `authorization-scheme <name>`（P1-4，与认证方案同构）。
func applyAAAAuthorizationScheme(state *CLIState, args []string) string {
	return applyAAASchemeCommand(state, AAASchemeKindAuthor, args)
}

// applyAAAAccountingScheme 处理 `accounting-scheme <name>`（P2-1，纯配置态）。
func applyAAAAccountingScheme(state *CLIState, args []string) string {
	return applyAAASchemeCommand(state, AAASchemeKindAcct, args)
}

// applyAAASchemeCommand 是三种方案命令的共同实现（创建 / 绑定双语义分派）。
func applyAAASchemeCommand(state *CLIState, kind string, args []string) string {
	switch state.CurrentView {
	case ViewAAA:
		// —— 创建 + 进入方案子视图 ——
		if !aaaDeviceSupported(state) {
			return errAAANotSupported(string(state.DeviceType))
		}
		if len(args) < 1 || args[0] == "" {
			return ErrUnrecognized
		}
		name := args[0]
		if strings.ContainsAny(name, ":\t ") {
			return ErrUnrecognized
		}
		aaaEnsureDeviceConfig(state)
		// 方案的「存在性」由 mode 键承载，创建时写入**空值**作为存在性标记：
		//   值为 ""     → 方案已创建但 mode 未显式配置；
		//   值为非空串 → mode 已被 *-mode 命令显式配置。
		//
		// 这样一个键同时编码了「存在」与「是否显式配置」两件事，从而：
		//   - display aaa 的 Mode 列回退渲染生效缺省（PRD §4.3 中 default 显示 local）；
		//   - display current-configuration 只为**显式配置过**的方案输出
		//     `authentication-mode` 子行，缺省不冗余输出（PRD §4.5：default 无子行、
		//     sch1 有子行）。二者用单一键即可同时精确复现，无需新增键。
		// 空值键随 SerializeToDeviceConfigData 的全量 map 拷贝正常往返（A9 已复核）。
		key := aaaSchemeKey(kind, name, aaaFieldMode)
		if _, exists := state.DeviceConfig[key]; !exists {
			state.DeviceConfig[key] = ""
		}
		state.CurrentView = ViewAAAAuthen
		state.CurrentSub = aaaSchemeSub(kind, name)
		return ""

	case ViewAAADomain:
		// —— 绑定到当前域（引用完整性）——
		return applyAAADomainBindScheme(state, kind, args)

	case ViewAAAAuthen:
		// 方案子视图内再敲同名命令：VRP 会切换到另一个方案，这里等价于回到
		// AAA 视图后重新执行，保持层级不塌陷。
		state.CurrentView = ViewAAA
		state.CurrentSub = ""
		return applyAAASchemeCommand(state, kind, args)

	default:
		return ErrAAAViewFirst
	}
}

// applyAAADomainBindScheme 在域子视图内把方案绑定到当前域。
//
// 🔴 A10 / P0-10 引用完整性（本期最高教学价值）：被绑方案必须**已存在**，
// 否则硬拒绝且**不写任何键**（严禁隐式创建，A12）。
func applyAAADomainBindScheme(state *CLIState, kind string, args []string) string {
	domain := state.CurrentSub
	if domain == "" {
		return ErrAAAViewFirst
	}
	if len(args) < 1 || args[0] == "" {
		return ErrUnrecognized
	}
	scheme := args[0]
	if !aaaSchemeExists(state, kind, scheme) {
		return fmt.Sprintf(ErrSchemeNotExist, scheme)
	}
	field := aaaDomainSchemeField(kind)
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaDomainKey(domain, field)] = scheme
	return ""
}

// aaaDomainSchemeField 由方案种类映射到域侧的绑定字段名。
func aaaDomainSchemeField(kind string) string {
	switch kind {
	case AAASchemeKindAuthor:
		return aaaFieldAuthorScheme
	case AAASchemeKindAcct:
		return aaaFieldAcctScheme
	default:
		return aaaFieldAuthenScheme
	}
}

// —— T3：方案模式命令（authentication-mode 视图分派的 AAA 侧落点）——

// applyAAAAuthenticationMode 处理方案子视图下的 `authentication-mode <local|radius|none>`。
//
// 🔴 A2 最高危约束：本函数**不是**新的顶层 case —— parser.go 既有的
// `case "authentication-mode"` 已硬守卫 ViewVTY，必须改为按 CurrentView 分派后调用本函数，
// 严禁新增第二个同名顶层 case（Go 编译期 duplicate case 错误）。
//
// C3：radius 仅记录为配置态，**不联动** state.RADIUS、不发起任何协议交互。
func applyAAAAuthenticationMode(state *CLIState, args []string) string {
	return applyAAASchemeMode(state, AAASchemeKindAuthen, []string{"local", "radius", "none"}, args)
}

// applyAAAAuthorizationMode 处理 `authorization-mode <local|none>`（P1-4）。
func applyAAAAuthorizationMode(state *CLIState, args []string) string {
	return applyAAASchemeMode(state, AAASchemeKindAuthor, []string{"local", "none"}, args)
}

// applyAAAAccountingMode 处理 `accounting-mode <none|radius>`（P2-1）。
func applyAAAAccountingMode(state *CLIState, args []string) string {
	return applyAAASchemeMode(state, AAASchemeKindAcct, []string{"none", "radius"}, args)
}

// applyAAASchemeMode 是三种 *-mode 命令的共同实现。
//
// 守卫顺序：必须身处**匹配种类**的方案子视图 → 参数合法 → 落键。
func applyAAASchemeMode(state *CLIState, kind string, allowed []string, args []string) string {
	if state.CurrentView != ViewAAAAuthen {
		// AAA 视图直接敲 *-mode：提示先建方案（PRD §4.1 第二条拒错样例）。
		if state.CurrentView == ViewAAA {
			return ErrAuthSchemeFirst
		}
		return ErrAAAViewFirst
	}
	curKind, name, ok := aaaParseSchemeSub(state.CurrentSub)
	if !ok || curKind != kind {
		// 例如在 authorization-scheme 子视图里敲 authentication-mode。
		return ErrAuthSchemeFirst
	}
	if len(args) < 1 {
		return fmt.Sprintf("Error: usage: %s-mode %s", kind, strings.Join(allowed, " | "))
	}
	mode := strings.ToLower(args[0])
	if !aaaContains(allowed, mode) {
		return fmt.Sprintf("Error: usage: %s-mode %s", kind, strings.Join(allowed, " | "))
	}
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaSchemeKey(kind, name, aaaFieldMode)] = mode
	return ""
}

// aaaContains 报告字符串切片是否包含目标（小工具，避免引入额外依赖）。
func aaaContains(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// —— T3：域命令族 ——

// applyAAADomain 处理 AAA 视图下的 `domain <name>`：创建（幂等）并进入域子视图。
//
// 注意：m-lag 的 `domain <id>` 位于 `case "m-lag"` 的**内层** switch（parser.go:1282），
// 与本顶层 case 无冲突（设计 §1.8 已复核）。
func applyAAADomain(state *CLIState, args []string) string {
	switch state.CurrentView {
	case ViewAAA:
		if !aaaDeviceSupported(state) {
			return errAAANotSupported(string(state.DeviceType))
		}
		if len(args) < 1 || args[0] == "" {
			return ErrUnrecognized
		}
		name := args[0]
		if strings.ContainsAny(name, ":\t ") {
			return ErrUnrecognized
		}
		aaaEnsureDeviceConfig(state)
		// 域的「存在性」由 state 键承载（设计 §4.1 未给域 state 标注「缺省键不存在」）。
		key := aaaDomainKey(name, aaaFieldState)
		if _, exists := state.DeviceConfig[key]; !exists {
			state.DeviceConfig[key] = AAADefaultUserState
		}
		state.CurrentView = ViewAAADomain
		state.CurrentSub = name
		return ""
	case ViewAAADomain:
		// 域子视图内切换到另一个域：先回退层级再复用创建逻辑。
		state.CurrentView = ViewAAA
		state.CurrentSub = ""
		return applyAAADomain(state, args)
	default:
		return ErrAAAViewFirst
	}
}

// applyAAADomainState 处理域子视图下的 `state { active | block }`。
func applyAAADomainState(state *CLIState, args []string) string {
	if state.CurrentView != ViewAAADomain || state.CurrentSub == "" {
		return ErrAAAViewFirst
	}
	if len(args) < 1 {
		return ErrStateUsage
	}
	mode := strings.ToLower(args[0])
	if mode != AAADefaultUserState && mode != AAAUserStateBlock {
		return ErrStateUsage
	}
	aaaEnsureDeviceConfig(state)
	state.DeviceConfig[aaaDomainKey(state.CurrentSub, aaaFieldState)] = mode
	return ""
}

// —— T5：undo 级联清理（handled 模式，精确前缀）——

// aaaDeleteByPrefix 删除全部以 prefix 开头的键，返回删除条数。
//
// 🔴 A1 红线核心实现：prefix 必须由 aaaEntityPrefix / aaaKeyPrefix 构造（自带尾冒号），
// 严禁传入不带冒号的裸串 —— 否则 "admin" 会误删 "administrator"，
// "aaa" 会误删 "aaaa-bbbb-cccc" 之类的异族键。
func aaaDeleteByPrefix(state *CLIState, prefix string) int {
	if state == nil || state.DeviceConfig == nil {
		return 0
	}
	victims := make([]string, 0, 8)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			victims = append(victims, k)
		}
	}
	sort.Strings(victims)
	for _, k := range victims {
		delete(state.DeviceConfig, k)
	}
	return len(victims)
}

// applyUndoAAALocalUser 处理 `undo local-user <name> [privilege level|service-type|state]`。
//
// handled=false 表示该 undo 不属于本命令族，parser.go 应交回既有分支。
func applyUndoAAALocalUser(state *CLIState, args []string) (string, bool) {
	if len(args) == 0 || strings.ToLower(args[0]) != "local-user" {
		return "", false
	}
	if state.CurrentView != ViewAAA {
		return ErrAAAViewFirst, true
	}
	if len(args) < 2 || args[1] == "" {
		return ErrUnrecognized, true
	}
	name := args[1]
	if !aaaLocalUserExists(state, name) {
		return fmt.Sprintf("Error: The local user %s does not exist.", name), true
	}
	// 整用户级联删除。
	if len(args) == 2 {
		aaaDeleteByPrefix(state, aaaEntityPrefix(aaaLocalUserPrefix, name))
		return "", true
	}
	// 属性级删除（P1-2）。
	switch strings.ToLower(args[2]) {
	case "password":
		delete(state.DeviceConfig, aaaLocalUserKey(name, aaaFieldPassword))
	case "privilege":
		delete(state.DeviceConfig, aaaLocalUserKey(name, aaaFieldPrivilege))
	case "service-type":
		delete(state.DeviceConfig, aaaLocalUserKey(name, aaaFieldServiceType))
	case "state":
		delete(state.DeviceConfig, aaaLocalUserKey(name, aaaFieldState))
	default:
		return ErrUnrecognized, true
	}
	return "", true
}

// applyUndoAAAScheme 处理 `undo {authentication|authorization|accounting}-scheme <name>`。
//
// 双语义（与正向命令对称）：
//   - ViewAAA       → 删除方案（引用完整性硬拒绝，C7）；
//   - ViewAAADomain → 解除当前域对该类方案的绑定。
func applyUndoAAAScheme(state *CLIState, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	var kind string
	switch strings.ToLower(args[0]) {
	case "authentication-scheme":
		kind = AAASchemeKindAuthen
	case "authorization-scheme":
		kind = AAASchemeKindAuthor
	case "accounting-scheme":
		kind = AAASchemeKindAcct
	default:
		return "", false
	}

	if state.CurrentView == ViewAAADomain {
		// 解绑：不校验方案存在性（域侧记录本身就是要清掉的东西）。
		delete(state.DeviceConfig, aaaDomainKey(state.CurrentSub, aaaDomainSchemeField(kind)))
		return "", true
	}
	if state.CurrentView != ViewAAA {
		return ErrAAAViewFirst, true
	}
	if len(args) < 2 || args[1] == "" {
		return ErrUnrecognized, true
	}
	name := args[1]
	if !aaaSchemeExists(state, kind, name) {
		return fmt.Sprintf(ErrSchemeNotExist, name), true
	}
	// 🔴 C7 引用完整性：仍被域引用的方案不得删除，且**方案键必须原样保留**。
	if refs := aaaDomainsReferencingScheme(state, kind, name); len(refs) > 0 {
		return fmt.Sprintf(ErrSchemeReferenced, name, refs[0]), true
	}
	aaaDeleteByPrefix(state, aaaEntityPrefix(aaaSchemePrefix(kind), name))
	return "", true
}

// applyUndoAAADomain 处理 `undo domain <name>`（AAA 视图）。
func applyUndoAAADomain(state *CLIState, args []string) (string, bool) {
	if len(args) == 0 || strings.ToLower(args[0]) != "domain" {
		return "", false
	}
	if state.CurrentView != ViewAAA {
		return ErrAAAViewFirst, true
	}
	if len(args) < 2 || args[1] == "" {
		return ErrUnrecognized, true
	}
	name := args[1]
	if !aaaDomainExists(state, name) {
		return fmt.Sprintf(ErrDomainNotExist, name), true
	}
	aaaDeleteByPrefix(state, aaaEntityPrefix(aaaDomainPrefix, name))
	return "", true
}

// applyUndoAAA 处理系统视图下的 `undo aaa`：级联清理整个 aaa: 命名空间。
//
// 🔴 本函数是键碰撞红线的**最高危触发点**（AC12 专项断言）：
// 必须以 aaaKeyPrefix()（== "aaa:"，含尾冒号）精确前缀匹配。
// 严禁退化为 strings.Contains(k, "aaa") —— 那会连带删除
// interface:GE0/0/1:port-security-sticky-learned:00e0-fc12-0aaa 这类端口安全键。
func applyUndoAAA(state *CLIState, args []string) (string, bool) {
	if len(args) == 0 || strings.ToLower(args[0]) != "aaa" {
		return "", false
	}
	aaaDeleteByPrefix(state, aaaKeyPrefix())
	return "", true
}

// applyUndoAAAInView 是 AAA 三档视图下 undo 的统一入口（handled 链）。
//
// 未命中任何 AAA 命令族时返回 handled=false，由 parser.go 输出既有的
// "Error: undo '%s' is not supported"，保持与其它视图一致的口径。
func applyUndoAAAInView(state *CLIState, args []string) (string, bool) {
	if msg, handled := applyUndoAAALocalUser(state, args); handled {
		return msg, true
	}
	if msg, handled := applyUndoAAAScheme(state, args); handled {
		return msg, true
	}
	if msg, handled := applyUndoAAADomain(state, args); handled {
		return msg, true
	}
	if len(args) > 0 && strings.ToLower(args[0]) == "state" && state.CurrentView == ViewAAADomain {
		// undo state → 回到生效缺省 active。
		// 注意：域的 state 键同时承载「域存在性」，因此是**改写**而不是删除。
		aaaEnsureDeviceConfig(state)
		state.DeviceConfig[aaaDomainKey(state.CurrentSub, aaaFieldState)] = AAADefaultUserState
		return "", true
	}
	return "", false
}

// —— 命令族归属判定（供 parser.go 顶层分派与测试使用）——

// isAAACommandName 报告某顶层命令是否属于 AAA 命令族。
// 对照 isGRECommandName / isLAGCommandName 范式。
func isAAACommandName(name string) bool {
	switch strings.ToLower(name) {
	case "aaa",
		"local-user",
		"authentication-scheme",
		"authorization-scheme",
		"accounting-scheme",
		"authentication-mode",
		"authorization-mode",
		"accounting-mode",
		"domain":
		return true
	}
	return false
}

// aaaInAnyView 报告当前是否身处 AAA 三档视图之一。
func aaaInAnyView(state *CLIState) bool {
	switch state.CurrentView {
	case ViewAAA, ViewAAAAuthen, ViewAAADomain:
		return true
	}
	return false
}
