// paramspec.go 实现「参数级 Tab 补全」的统一数据模型与匹配算法
// （v0.12.1，对应 .workbuddy/参数补全实现提示词_2026-08-20.md）。
//
// 设计要点：
//   - 此前 Complete 只解析到「命令关键字」这一层；命令关键字之后可携带的参数
//     （二级子命令 / 值占位符）没有补全。本文件补齐参数层。
//   - 统一数据模型 ParamSpec / CommandGrammar 让 display 家族与配置视图命令
//     共用同一套补全逻辑（一致的数据来源 + 一致的匹配算法）。
//   - completeParams 只读、零副作用：绝不执行命令、绝不修改 CLIState（与 Complete 一致）。
//   - 语法符号（<> / [] / {} / | / * / <cr>）沿用华为 VRP 命令行帮助规范；
//     <cr> 在补全层不单独成候选——其语义由前端「Enter 即执行」自然承载，故 Complete
//     返回值仍为纯候选字符串，前端浮层无需改动。
package cli

import (
	"sort"
	"strings"
)

// SlotKind 描述一个参数槽位的类型。
type SlotKind int

const (
	// SlotKeyword 关键字 / 多选一：候选来自 Candidates（静态）+ 可选的 StateProvider
	// （动态值，用于「关键字 or 值」混合槽位，如 display interface 的 brief|接口名）。
	SlotKeyword SlotKind = iota
	// SlotValue 值占位符：候选来自 StateProvider（真实实例名），否则仅回显 ValueHint。
	SlotValue
)

// ParamSpec 描述命令其后一个参数槽位（参数级补全单一事实源）。
type ParamSpec struct {
	// Index 槽位序号（从 0 计）。同一序号可挂多条 ParamSpec，以表达「按上一 token 分支」
	// （如 dis aaa 的 Index1 既有 user-name（After=local-user）也有 domain-name（After=domain））。
	Index int
	// Name 槽位名（调试 / 帮助用）。
	Name string
	// Kind 关键字 or 值。
	Kind SlotKind
	// After 仅当上一已消费 token 命中其中之一时才启用本槽位（空 = 恒启用）。
	After []string
	// Candidates 静态关键字 / 多选一候选（值槽位可空）。
	Candidates []string
	// ValueHint 值占位符展示，如 "<user-name>" / "<interface-name>"。
	ValueHint string
	// Help 一行说明（前端浮层可选展示）。
	Help string
	// StateProvider 可选；从 CLIState 动态取候选（接口名 / 用户名 / 域名等真实实例名）。
	StateProvider func(state *CLIState) []string
}

// CommandGrammar 一条命令（display 子命令 / 配置视图命令）的完整参数定义（不含命令本身）。
type CommandGrammar struct {
	// CrAllowed 参数全部满足后是否可直接 <cr> 执行（几乎所有 display 为 true）。
	CrAllowed bool
	// Params 有序参数槽位（按 Index 检索）。
	Params []ParamSpec
}

// —— display 子命令参数语法（单一事实源；新增 display 子命令参数在此登记）——
// 键为 normalizeDisplaySubCmd 归一化结果（与 displayRegistry 同键空间）。
var displayParamSpecs = map[string]CommandGrammar{
	"aaa": {
		CrAllowed: true,
		Params: []ParamSpec{
			{
				Index:      0,
				Name:       "option",
				Kind:       SlotKeyword,
				Candidates: []string{"configuration", "statistics", "online-user", "local-user", "domain"},
				Help:       "display aaa 二级子命令",
			},
			{
				Index:         1,
				Name:          "user-name",
				Kind:          SlotValue,
				After:         []string{"local-user"},
				ValueHint:     "<user-name>",
				StateProvider: collectAAALocalUsers,
				Help:          "本地用户名（来自已配置 local-user）",
			},
			{
				Index:         1,
				Name:          "domain-name",
				Kind:          SlotValue,
				After:         []string{"domain"},
				ValueHint:     "<domain-name>",
				StateProvider: collectAAADomains,
				Help:          "域名（来自已配置 domain）",
			},
		},
	},
	"ip": {
		CrAllowed: true,
		Params: []ParamSpec{
			{
				Index:      0,
				Name:       "sub",
				Kind:       SlotKeyword,
				Candidates: []string{"interface", "pool", "routing-table"},
				Help:       "display ip 二级子命令",
			},
			{
				Index:         1,
				Name:          "if-name",
				Kind:          SlotValue,
				After:         []string{"interface"},
				ValueHint:     "<interface-name>",
				StateProvider: interfaceNames,
				Help:          "接口名",
			},
		},
	},
	"interface": {
		CrAllowed: true,
		Params: []ParamSpec{
			{
				Index:         0,
				Name:          "target",
				Kind:          SlotKeyword,
				Candidates:    []string{"brief"},
				StateProvider: interfaceNames,
				Help:          "接口名或 brief",
			},
		},
	},
}

// —— 配置视图命令参数语法（system / aaa 等视图首 token 之后的参数）——
// 键为视图首 token（小写）。同一命令名在不同视图若语义不同，应加视图前缀区分；
// 当前仅挂无歧义命令（interface / local-user）。
var viewParamSpecs = map[string]CommandGrammar{
	"interface": {
		CrAllowed: false,
		Params: []ParamSpec{
			{
				Index:         0,
				Name:          "if-name",
				Kind:          SlotValue,
				ValueHint:     "<interface-name>",
				StateProvider: interfaceNames,
				Help:          "接口名",
			},
		},
	},
	"local-user": {
		CrAllowed: false,
		Params: []ParamSpec{
			{
				Index:         0,
				Name:          "user-name",
				Kind:          SlotValue,
				ValueHint:     "<user-name>",
				StateProvider: collectAAALocalUsers,
				Help:          "本地用户名",
			},
		},
	},
}

// interfaceNames 返回排序后的真实接口名（与 completeInterfaceNames 同源，供 StateProvider 复用）。
func interfaceNames(state *CLIState) []string {
	out := make([]string, 0, len(state.Interfaces))
	for name := range state.Interfaces {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// hasPrefixFold 大小写不敏感前缀判断。
func hasPrefixFold(s, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(s), prefix)
}

// afterMatches 报告 prev token 是否命中槽位的 After 约束（After 为空视为恒命中）。
func afterMatches(after []string, prev string) bool {
	if len(after) == 0 {
		return true
	}
	for _, a := range after {
		if a == prev {
			return true
		}
	}
	return false
}

// slotCandidates 计算单个槽位在给定前缀下的候选（静态 + 动态并集，未排序去重）。
func (p ParamSpec) slotCandidates(state *CLIState, prefix string) []string {
	var out []string
	if p.Kind == SlotValue {
		if p.StateProvider != nil {
			for _, c := range p.StateProvider(state) {
				if hasPrefixFold(c, prefix) {
					out = append(out, c)
				}
			}
		} else if prefix == "" {
			out = append(out, p.ValueHint)
		}
		return out
	}
	// SlotKeyword：静态候选 + 可选动态候选（如 brief|接口名 混合槽位）
	for _, c := range p.Candidates {
		if hasPrefixFold(c, prefix) {
			out = append(out, c)
		}
	}
	if p.StateProvider != nil {
		for _, c := range p.StateProvider(state) {
			if hasPrefixFold(c, prefix) {
				out = append(out, c)
			}
		}
	}
	return out
}

// completeParams 在命令关键字已解析后，按语法补全其后的参数 token。
// remaining = SplitCommandTokens(input) 中「命令关键字之后」的部分
// （remaining[0] 为第一个参数 token；末尾元素为当前正在输入的前缀，可能为空）。
// 只读、零副作用。
func completeParams(g CommandGrammar, state *CLIState, remaining []string) []string {
	if len(remaining) == 0 {
		return nil
	}
	prefix := strings.ToLower(remaining[len(remaining)-1])
	idx := len(remaining) - 1
	prev := ""
	if idx > 0 {
		prev = strings.ToLower(remaining[idx-1])
	}
	var cands []string
	for _, p := range g.Params {
		if p.Index != idx {
			continue
		}
		if !afterMatches(p.After, prev) {
			continue
		}
		cands = append(cands, p.slotCandidates(state, prefix)...)
	}
	// 去重保序
	seen := make(map[string]bool, len(cands))
	uniq := cands[:0]
	for _, c := range cands {
		if seen[c] {
			continue
		}
		seen[c] = true
		uniq = append(uniq, c)
	}
	sort.Strings(uniq)
	return uniq
}
