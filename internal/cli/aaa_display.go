// aaa_display.go 实现「AAA 本地认证的渲染层」
// （P2 第八项，华为 VRP 课程 71，T4/T6/T7）。
//
// 分层契约（与 gre_display.go 完全同构）：
//   - 本文件**只渲染**：全部数据来自 aaa_eval.go 的 EvaluateAAA / collect* 派生视图，
//     严禁写 state.DeviceConfig（那是 aaa_cmd.go 的唯一职责）。
//   - 全部列表按名称**升序**输出（确定性红线，AC8④：同状态连续 10 次调用字节级一致），
//     排序职责已下沉到 collectAAA*，本文件不再二次排序。
//   - 全部列格式串**唯一定义、表头与数据行共用**（aaaLocalUserRowFormat 等）。
//     教训来自 display interface brief 那轮：表头与数据行各写一份格式串必然错位。
//
// 🔴 诚实占位红线（P0-14 / AC10）：
//   - 运行态字段（Successful/Failed authentications、Online sessions、Last login time、
//     Online users、Access accepts、Access rejects）一律取自 AAAStats，值恒 "-"。
//     严禁在本文件出现任何 time.Now()、计数器、随机数或 "0 online" 之类的编造值。
//   - 全部 display 输出末尾必须附 aaaSimNote()。
//
// 🔴 口令脱敏红线（P0-13 / AC9）：
//   - Password 列一律走 maskAAAPassword()，已配显示 "****"、未配显示 "-"（两者必须可区分）。
//   - 严禁输出明文口令，亦严禁伪造形如 %^%#...%^%# 的 VRP 密文串。
package cli

import (
	"fmt"
	"strings"
)

// —— 列格式串（表头与数据行**共用**，杜绝列错位）——

const (
	// aaaLocalUserRowFormat 是 display local-user 的行格式（PRD §4.2 逐列对齐）。
	//   2 空缩进 | User-name(25) | State(9) | Privilege(12) | Service-type(22) | Password
	aaaLocalUserRowFormat = "  %-25s%-9s%-12s%-22s%s"

	// aaaSchemeRowFormat 是 display aaa 方案小表的行格式（PRD §4.3）。
	//   2 空缩进 | Name(25) | Mode
	aaaSchemeRowFormat = "  %-25s%s"

	// aaaDomainRowFormat 是域汇总表的行格式（PRD §4.3 / §4.4 共用）。
	//   2 空缩进 | Name(25) | Authen(16) | Author(16) | Acct(14) | State
	aaaDomainRowFormat = "  %-25s%-16s%-16s%-14s%s"

	// aaaSummaryLineFormat 是 "标签 : 值" 行格式（display aaa 头部 / display domain 详情）。
	aaaSummaryLineFormat = "%-28s: %s"

	// aaaUserStatFormat 是 display local-user 运行态统计行格式。
	aaaUserStatFormat = "  %-27s: %s"

	// aaaDomainStatFormat 是 display domain <name> 运行态统计行格式。
	aaaDomainStatFormat = "  %-26s: %s"

	// aaaVTYModeValueFormat 是 VTY authentication-mode 行的值格式（模式 + 引用标注）。
	aaaVTYModeValueFormat = "%-11s%s"
)

// —— 分隔线宽度（用 strings.Repeat 生成，避免手数横线个数）——

const (
	aaaSepWideWidth        = 80 // display 顶层分隔线
	aaaSepSchemeTableWidth = 36 // 方案小表分隔线（缩进 2 后）
	aaaSepDomainInAAAWidth = 74 // display aaa 内嵌域表分隔线（缩进 2 后）
	aaaSepDomainListWidth  = 76 // display domain 汇总表分隔线（缩进 2 后）
)

// aaaSepLine 返回 n 个 '-' 组成的分隔线。
func aaaSepLine(n int) string {
	return strings.Repeat("-", n)
}

// aaaIndentedSep 返回缩进 2 空格的分隔线。
func aaaIndentedSep(n int) string {
	return "  " + aaaSepLine(n)
}

// —— 小工具 ——

// aaaTitleState 把内部状态值（active/block）渲染为 VRP 风格首字母大写形式。
// 未知值原样返回，保证不会静默吞掉异常数据。
func aaaTitleState(s string) string {
	switch s {
	case AAADefaultUserState:
		return "Active"
	case AAAUserStateBlock:
		return "Block"
	}
	return s
}

// aaaPasswordCell 渲染 Password 列。
//
// 🔴 诚实边界（AC9②③）：已配口令恒 "****"，未配口令为 "-"，两者必须可区分。
func aaaPasswordCell(hasPassword bool) string {
	if hasPassword {
		return maskAAAPassword("")
	}
	return AAANotConfiguredPlaceholder
}

// aaaServiceTypeCell 渲染 Service-type 列（未配置为 "-"）。
func aaaServiceTypeCell(types []string) string {
	if len(types) == 0 {
		return AAANotConfiguredPlaceholder
	}
	return strings.Join(types, " ")
}

// —— T4：display local-user ——

// buildAAALocalUserDisplay 渲染 `display local-user`（PRD §4.2 为权威输出源）。
//
// 只读、无副作用、任意设备类型均可执行（AC12b：PC/Server 上输出空态 Info:，
// **不得**返回 "is not supported on"）。
func buildAAALocalUserDisplay(state *CLIState) string {
	result := EvaluateAAA(state)
	var b strings.Builder

	if len(result.Users) == 0 {
		b.WriteString("Info: No local user configured.\n")
		b.WriteString(aaaSimNote())
		b.WriteString("\n")
		return b.String()
	}

	sep := aaaSepLine(aaaSepWideWidth)
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf(aaaLocalUserRowFormat,
		"User-name", "State", "Privilege", "Service-type", "Password") + "\n")
	b.WriteString(sep + "\n")
	for _, u := range result.Users {
		b.WriteString(fmt.Sprintf(aaaLocalUserRowFormat,
			u.Name,
			aaaTitleState(u.State),
			u.Privilege,
			aaaServiceTypeCell(u.ServiceType),
			aaaPasswordCell(u.HasPassword),
		) + "\n")
	}
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf("  Total %d user(s)\n", len(result.Users)))

	// 🔴 运行态统计：全部取自 AAAStats（恒 "-"），严禁在此处引入任何真实/编造计数。
	b.WriteString("\n")
	b.WriteString("  --- Authentication runtime statistics ---\n")
	b.WriteString(fmt.Sprintf(aaaUserStatFormat, "Successful authentications", result.Stats.AuthSuccess) + "\n")
	b.WriteString(fmt.Sprintf(aaaUserStatFormat, "Failed authentications", result.Stats.AuthFail) + "\n")
	b.WriteString(fmt.Sprintf(aaaUserStatFormat, "Online sessions", result.Stats.OnlineUsers) + "\n")
	// Last login time 在 AAAStats 中无对应字段（本仿真器不记录登录时刻），
	// 直接取占位常量。🔴 严禁改为 time.Now() 或 "Never" 之类的派生值（AC10）。
	b.WriteString(fmt.Sprintf(aaaUserStatFormat, "Last login time", AAAStatPlaceholder) + "\n")
	b.WriteString(aaaCipherNote() + "\n")
	b.WriteString(aaaSimNote() + "\n")
	return b.String()
}

// —— T4：display aaa ——

// buildAAADisplay 渲染 `display aaa`（PRD §4.3 为权威输出源）。
func buildAAADisplay(state *CLIState) string {
	result := EvaluateAAA(state)
	var b strings.Builder

	if result.IsEmpty() {
		b.WriteString("Info: No AAA configuration.\n")
		b.WriteString(aaaSimNote())
		b.WriteString("\n")
		return b.String()
	}

	sep := aaaSepLine(aaaSepWideWidth)
	b.WriteString("AAA configuration information\n")
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Local user count", fmt.Sprintf("%d", len(result.Users))) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Authentication scheme count", fmt.Sprintf("%d", len(result.AuthenSchemes))) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Authorization scheme count", fmt.Sprintf("%d", len(result.AuthorSchemes))) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Accounting scheme count", fmt.Sprintf("%d", len(result.AcctSchemes))) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Domain count", fmt.Sprintf("%d", len(result.Domains))) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "VTY authentication-mode", aaaVTYModeCell(result.VTYAuthMode)) + "\n")
	b.WriteString(sep + "\n")

	writeAAASchemeSection(&b, "Authentication schemes:", result.AuthenSchemes, sep)
	writeAAASchemeSection(&b, "Authorization schemes:", result.AuthorSchemes, sep)
	writeAAASchemeSection(&b, "Accounting schemes:", result.AcctSchemes, sep)

	if len(result.Domains) > 0 {
		b.WriteString("Domains:\n")
		b.WriteString(fmt.Sprintf(aaaDomainRowFormat,
			"Name", "Authen-scheme", "Author-scheme", "Acct-scheme", "State") + "\n")
		b.WriteString(aaaIndentedSep(aaaSepDomainInAAAWidth) + "\n")
		for _, d := range result.Domains {
			b.WriteString(fmt.Sprintf(aaaDomainRowFormat,
				d.Name, d.AuthenScheme, d.AuthorScheme, d.AcctScheme, aaaTitleState(d.State)) + "\n")
		}
		b.WriteString(sep + "\n")
	}

	b.WriteString(aaaSimNote() + "\n")
	return b.String()
}

// aaaVTYModeCell 渲染 "VTY authentication-mode" 行的值（P1-8：只读引用，闭合悬空关系）。
func aaaVTYModeCell(mode string) string {
	tag := "(AAA not referenced by VTY)"
	if mode == "aaa" {
		tag = "(user-interface vty, referenced)"
	}
	return fmt.Sprintf(aaaVTYModeValueFormat, mode, tag)
}

// writeAAASchemeSection 输出一个方案小表；列表为空时整段不输出（保持 PRD §4.3 样例形态）。
func writeAAASchemeSection(b *strings.Builder, title string, schemes []SchemeView, sep string) {
	if len(schemes) == 0 {
		return
	}
	b.WriteString(title + "\n")
	b.WriteString(fmt.Sprintf(aaaSchemeRowFormat, "Name", "Mode") + "\n")
	b.WriteString(aaaIndentedSep(aaaSepSchemeTableWidth) + "\n")
	for _, s := range schemes {
		b.WriteString(fmt.Sprintf(aaaSchemeRowFormat, s.Name, s.Mode) + "\n")
	}
	b.WriteString(sep + "\n")
}

// —— T4：display domain [<name>] ——

// buildAAADomainDisplay 渲染 `display domain [<name>]`（PRD §4.4 为权威输出源）。
//
// name == "" 时输出汇总表；指定域名时输出详情（含跨对象解引用的方案 mode，P1-7）。
func buildAAADomainDisplay(state *CLIState, name string) string {
	result := EvaluateAAA(state)

	if name != "" {
		return buildAAADomainDetail(state, result, name)
	}

	var b strings.Builder
	if len(result.Domains) == 0 {
		b.WriteString("Info: No domain configured.\n")
		b.WriteString(aaaSimNote() + "\n")
		return b.String()
	}
	sep := aaaIndentedSep(aaaSepDomainListWidth)
	b.WriteString(fmt.Sprintf(aaaDomainRowFormat,
		"Domain-name", "Authen-scheme", "Author-scheme", "Acct-scheme", "State") + "\n")
	b.WriteString(sep + "\n")
	for _, d := range result.Domains {
		b.WriteString(fmt.Sprintf(aaaDomainRowFormat,
			d.Name, d.AuthenScheme, d.AuthorScheme, d.AcctScheme, aaaTitleState(d.State)) + "\n")
	}
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf("  Total %d domain(s)\n", len(result.Domains)))
	b.WriteString(aaaSimNote() + "\n")
	return b.String()
}

// buildAAADomainDetail 渲染单个域的详情。
func buildAAADomainDetail(state *CLIState, result AAAResult, name string) string {
	var target *DomainView
	for i := range result.Domains {
		if result.Domains[i].Name == name {
			target = &result.Domains[i]
			break
		}
	}
	if target == nil {
		return fmt.Sprintf(ErrDomainNotExist, name)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Domain-name", target.Name) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "State", aaaTitleState(target.State)) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Authentication-scheme",
		aaaBoundSchemeCell(state, AAASchemeKindAuthen, target.AuthenScheme)) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Authorization-scheme",
		aaaBoundSchemeCell(state, AAASchemeKindAuthor, target.AuthorScheme)) + "\n")
	b.WriteString(fmt.Sprintf(aaaSummaryLineFormat, "Accounting-scheme",
		aaaBoundSchemeCell(state, AAASchemeKindAcct, target.AcctScheme)) + "\n")

	// 🔴 运行态统计：全部取自 AAAStats（恒 "-"）。
	b.WriteString("  --- Domain runtime statistics ---\n")
	b.WriteString(fmt.Sprintf(aaaDomainStatFormat, "Online users", result.Stats.OnlineUsers) + "\n")
	b.WriteString(fmt.Sprintf(aaaDomainStatFormat, "Access accepts", result.Stats.AuthSuccess) + "\n")
	b.WriteString(fmt.Sprintf(aaaDomainStatFormat, "Access rejects", result.Stats.AuthFail) + "\n")
	b.WriteString(aaaSimNote() + "\n")
	return b.String()
}

// aaaBoundSchemeCell 渲染域详情里的方案绑定行（P1-7 跨对象解引用）。
//
// 未绑定 → "-"；已绑定且方案存在 → "<name>  (mode: <mode>)"；
// 已绑定但方案已不存在 → "<name>  (mode: - (not found))"（设计 §4.2 DomainView 注释口径）。
func aaaBoundSchemeCell(state *CLIState, kind, bound string) string {
	if bound == "" || bound == AAANotConfiguredPlaceholder {
		return AAANotConfiguredPlaceholder
	}
	mode, ok := aaaFindSchemeMode(state, kind, bound)
	if !ok {
		mode = AAANotConfiguredPlaceholder + " (not found)"
	}
	return fmt.Sprintf("%s  (mode: %s)", bound, mode)
}

// —— T6：display current-configuration 的 AAA 块 ——

// buildSavedAAAConfig 输出系统级 AAA 配置块（PRD §4.5 为权威格式）。
//
// 输出顺序固定：
//
//	#
//	aaa
//	 authentication-scheme <name>            （按名称升序；authorization / accounting 同构）
//	  authentication-mode <mode>             （仅在**非生效缺省**时输出）
//	 local-user <name> password cipher ****  （按用户名升序，每用户内 password → privilege
//	 local-user <name> privilege level <n>    → service-type → state 固定顺序）
//	 local-user <name> service-type <list>
//	 local-user <name> state block           （active 为缺省，不输出）
//	 domain <name>                           （按名称升序）
//	  authentication-scheme <name>            绑定行
//	#
//
// **缺省值不冗余输出**（PRD §4.5 注 + AC3④「11 行」口径）：
// 生效缺省的 authentication-mode local / state active 一律不落行，与
// buildSavedGREInterfaceConfig / buildSavedSTPConfig 的差异值口径一致。
//
// 🔴 口令行恒输出 "****"（P0-13）——该快照因此**不可回灌**，与既有 STP / GRE 快照定位一致。
// 返回 "" 表示无任何 AAA 配置。
func buildSavedAAAConfig(state *CLIState) string {
	result := EvaluateAAA(state)
	if result.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("#\n")
	b.WriteString("aaa\n")

	writeSavedAAASchemes(&b, state, "authentication-scheme", AAASchemeKindAuthen, "authentication-mode", result.AuthenSchemes)
	writeSavedAAASchemes(&b, state, "authorization-scheme", AAASchemeKindAuthor, "authorization-mode", result.AuthorSchemes)
	writeSavedAAASchemes(&b, state, "accounting-scheme", AAASchemeKindAcct, "accounting-mode", result.AcctSchemes)

	for _, u := range result.Users {
		if u.HasPassword {
			b.WriteString(fmt.Sprintf(" local-user %s password cipher %s\n", u.Name, maskAAAPassword("")))
		}
		if u.Privilege != AAANotConfiguredPlaceholder {
			b.WriteString(fmt.Sprintf(" local-user %s privilege level %s\n", u.Name, u.Privilege))
		}
		if len(u.ServiceType) > 0 {
			b.WriteString(fmt.Sprintf(" local-user %s service-type %s\n", u.Name, strings.Join(u.ServiceType, " ")))
		}
		if u.State != AAADefaultUserState {
			b.WriteString(fmt.Sprintf(" local-user %s state %s\n", u.Name, u.State))
		}
	}

	for _, d := range result.Domains {
		b.WriteString(fmt.Sprintf(" domain %s\n", d.Name))
		if d.AuthenScheme != AAANotConfiguredPlaceholder {
			b.WriteString(fmt.Sprintf("  authentication-scheme %s\n", d.AuthenScheme))
		}
		if d.AuthorScheme != AAANotConfiguredPlaceholder {
			b.WriteString(fmt.Sprintf("  authorization-scheme %s\n", d.AuthorScheme))
		}
		if d.AcctScheme != AAANotConfiguredPlaceholder {
			b.WriteString(fmt.Sprintf("  accounting-scheme %s\n", d.AcctScheme))
		}
		if d.State != AAADefaultUserState {
			b.WriteString(fmt.Sprintf("  state %s\n", d.State))
		}
	}

	b.WriteString("#\n")
	return b.String()
}

// writeSavedAAASchemes 输出一类方案的快照行（含 mode 子行）。
//
// 🔴 子行的输出判据是「该方案的 mode 是否被**显式配置过**」，**不是**「mode 是否等于缺省值」。
// 二者不等价：`authentication-scheme sch1` + `authentication-mode local` 显式配置了 local，
// 真机 VRP 会回显该行；而从未配过 mode 的 `default` 方案则不回显。
// 若误用 `s.Mode != aaaDefaultSchemeMode(kind)` 判据，显式配置的 local 会被静默吞掉，
// 与 PRD §4.5 权威样例（default 无子行、sch1 有子行）不符。
//
// 判据落在**原始键值**上：aaa_cmd.go 建方案时写入空串作为存在性标记，
// *-mode 命令才把它改成非空值 —— 因此 raw != "" 恰好等价于「显式配置过」。
// 读原始键必须在渲染层做（SchemeView.Mode 已被 EvaluateAAA 回退渲染成生效缺省，
// 丢失了「是否显式配置」这一位信息），故本函数需要 state 入参。
func writeSavedAAASchemes(b *strings.Builder, state *CLIState, createCmd, kind, modeCmd string, schemes []SchemeView) {
	for _, s := range schemes {
		b.WriteString(fmt.Sprintf(" %s %s\n", createCmd, s.Name))
		raw := ""
		if state != nil && state.DeviceConfig != nil {
			raw = state.DeviceConfig[aaaSchemeKey(kind, s.Name, aaaFieldMode)]
		}
		if raw != "" {
			b.WriteString(fmt.Sprintf("  %s %s\n", modeCmd, raw))
		}
	}
}

// —— T7：display ssh 的 Local Users 段修复 ——

// fixSSHLocalUsersDisplay 渲染 `display ssh` 中的 "Local Users" 段（P0-13 / T7）。
//
// 修复旧实现（parser.go 中对本地用户结构体 map 的直接 range 遍历）的三处缺陷：
//  1. map 随机遍历 → 改读 EvaluateAAA 的**升序**列表；
//  2. `Privilege: %d` 死字段恒 0 → 改为真实值，未配显示 "-"；
//  3. 无脱敏、无注记 → 补 Password 列脱敏与 aaaSimNote()。
//
// 返回 "" 表示无本地用户（调用方据此整段跳过，保持旧的「无用户不输出该段」行为）。
func fixSSHLocalUsersDisplay(state *CLIState) string {
	result := EvaluateAAA(state)
	if len(result.Users) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nLocal Users:\n")
	b.WriteString(aaaSepLine(52) + "\n")
	for _, u := range result.Users {
		b.WriteString(fmt.Sprintf("User: %s, Service-Type: %s, Privilege: %s, State: %s, Password: %s\n",
			u.Name,
			aaaServiceTypeCell(u.ServiceType),
			u.Privilege,
			aaaTitleState(u.State),
			aaaPasswordCell(u.HasPassword),
		))
	}
	b.WriteString(aaaSimNote() + "\n")
	return b.String()
}
