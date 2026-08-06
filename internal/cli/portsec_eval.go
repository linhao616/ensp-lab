// portsec_eval.go 实现「CLIState 层端口安全准入评估器」（P2 第二项，端口安全）。
//
// 背景与约束见 docs/p2-portsec-design.md 与 docs/p2-portsec-prd.md。本评估器
// 把端口安全从 P1-F 的「仅写 DeviceConfig 启用标记」升级为可配置、可持久化、
// 可忠实展示、可真实触发违规动作的 L2 接入控制特性。
//
// 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎），可单测、可回归，
// 与 acl_eval.go 的 EvaluatePathACL / applyNAT 同一契约（返回新值，调用方应用）。
// 任何新代码不得新建对 sim 引擎实例的调用；本文件仅读 sim.EngineModeName()
// 以决定诚实占位注记的 lite/full 两态（与 natSimNote/aclSimNote 同口径）。
package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// Frame 是 simulate frame 注入的 L2 帧（仅关心源 MAC 与 VLAN，目的/载荷本期无关）。
type Frame struct {
	SrcMAC string // VRP 连字符格式，如 "00e0-fc12-3456"
	VLAN   int    // 0 = 缺省 / 无 VLAN
}

// PortSecurityViolation 描述一次违规动作。
type PortSecurityViolation struct {
	Action    string // "protect" | "restrict" | "shutdown"
	ErrorDown bool   // shutdown 时为 true（端口 error-down 置位）
}

// PortSecurityResult 是 EvaluatePortSecurity 的纯函数返回。
//   - Admit=false 且 Violation!=nil：触发违规（按 Action 处置，命令处理器落地）。
//   - Admit=true 且 Learned!=nil：应学习该 MAC；命令处理器将其 append 到 MACTable
//     （Type 由 sticky 标志决定 sticky/security），并（若 sticky）写持久化键。
//   - Admit=true 且 Learned==nil：授权 MAC 准入，不学习不计数。
type PortSecurityResult struct {
	Admit     bool
	Violation *PortSecurityViolation
	Learned   *MACEntry
}

// 端口安全 DeviceConfig 键名约定（单一事实源，沿用 P1-F 的 interface:<iface>:port-security[-...]）。
const (
	psKeyEnabled        = "port-security"                 // = "enable"|"disable"（既有）
	psKeyMaxMAC         = "port-security-max-mac"         // = "<1-4096>"（既有）
	psKeySticky         = "port-security-sticky"          // = "enable"（既有，自动粘滞标志）
	psKeyProtect        = "port-security-protect-action"  // = "protect"|"restrict"|"shutdown"（NEW）
	psKeyAging          = "port-security-aging-time"      // = "<1-1440>"（NEW）
	psKeyStickyMACPre   = "port-security-sticky-mac:"     // + <mac> = "<vlan>"（NEW，手动绑定，多条）
	psKeyErrorDown      = "port-security-error-down"      // = "true"（NEW，运行态）
	psKeyViolations     = "port-security-violations"      // = "<n>"（NEW，运行态计数）
	psKeyStickyLearnedPre = "port-security-sticky-learned:" // + <mac> = "<vlan>"（NEW，自动学习粘滞，持久化）
)

// maxMACRange / agingRange 定义合法范围（拍板 #4：max-mac 1–4096、aging-time 1–1440）。
const (
	psMaxMACMin = 1
	psMaxMACMax = 4096
	psAgingMin  = 1
	psAgingMax  = 1440
)

// psKey 拼接端口安全键名：interface:<iface>:<suffix>。
func psKey(iface, suffix string) string {
	return fmt.Sprintf("interface:%s:%s", iface, suffix)
}

// vrpMACRe 匹配 VRP 连字符 MAC（xxxx-xxxx-xxxx）。
var vrpMACRe = regexp.MustCompile(`^[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}$`)

// colonMACRe 匹配冒号分隔 MAC（xx:xx:xx:xx:xx:xx）。
var colonMACRe = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

// canonMAC 把任意常见 MAC 表示规范化为 VRP 连字符小写格式（xxxx-xxxx-xxxx）。
// 返回 ("", false) 表示非法 MAC。接受：VRP 连字符、冒号分隔、无分隔符 12 位十六进制。
func canonMAC(mac string) (string, bool) {
	m := strings.ToLower(strings.TrimSpace(mac))
	switch {
	case vrpMACRe.MatchString(m):
		return m, true
	case colonMACRe.MatchString(m):
		parts := strings.Split(m, ":")
		return fmt.Sprintf("%s%s-%s%s-%s%s", parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]), true
	}
	clean := strings.NewReplacer("-", "", ":", "").Replace(m)
	if len(clean) == 12 {
		if _, err := strconv.ParseUint(clean, 16, 64); err == nil {
			return fmt.Sprintf("%s%s-%s%s-%s%s", clean[0:2], clean[2:4], clean[4:6], clean[6:8], clean[8:10], clean[10:12]), true
		}
	}
	return "", false
}

// psIsEnabled 报告端口安全是否启用（读 DeviceConfig 键，只读）。
func psIsEnabled(state *CLIState, iface string) bool {
	return state.DeviceConfig[psKey(iface, psKeyEnabled)] == "enable"
}

// psMaxMAC 返回端口安全 max-mac-num；启用但未配置时默认 1（VRP 最小槽位）。
func psMaxMAC(state *CLIState, iface string) int {
	v := state.DeviceConfig[psKey(iface, psKeyMaxMAC)]
	if v == "" {
		return 1
	}
	if n, err := strconv.Atoi(v); err == nil && n >= psMaxMACMin {
		return n
	}
	return 1
}

// psProtectAction 返回 protect-action；缺省即 "restrict"（拍板 #5）。
func psProtectAction(state *CLIState, iface string) string {
	if v := state.DeviceConfig[psKey(iface, psKeyProtect)]; v != "" {
		return v
	}
	return "restrict"
}

// psIsSticky 报告自动粘滞标志是否开启（只读）。
func psIsSticky(state *CLIState, iface string) bool {
	return state.DeviceConfig[psKey(iface, psKeySticky)] == "enable"
}

// psIsAuthorized 报告来源 MAC 是否授权（只读）：手动绑定 sticky-mac 或 MACTable 中
// 本端口的 sticky/security 条目。
func psIsAuthorized(state *CLIState, iface, mac string) bool {
	c, ok := canonMAC(mac)
	if !ok {
		return false
	}
	// 手动绑定 sticky-mac
	prefix := psKey(iface, psKeyStickyMACPre)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			boundMAC, bok := canonMAC(strings.TrimPrefix(k, prefix))
			if bok && boundMAC == c {
				return true
			}
		}
	}
	// 已学安全/粘滞 MAC
	for _, e := range state.MACTable {
		if e == nil || e.Interface != iface {
			continue
		}
		if e.Type == "sticky" || e.Type == "security" {
			if ec, eok := canonMAC(e.MAC); eok && ec == c {
				return true
			}
		}
	}
	return false
}

// psCountSecureMACs 返回「已占用安全 MAC 数」= 手动绑定 sticky-mac 条数 +
// MACTable 中本端口 Type∈{sticky,security} 条数（含手动绑定，贴近 VRP，O1）。
func psCountSecureMACs(state *CLIState, iface string) int {
	count := 0
	prefix := psKey(iface, psKeyStickyMACPre)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			count++
		}
	}
	for _, e := range state.MACTable {
		if e == nil || e.Interface != iface {
			continue
		}
		if e.Type == "sticky" || e.Type == "security" {
			count++
		}
	}
	return count
}

// EvaluatePortSecurity 端口安全准入判定纯函数（无副作用、不写引擎、可单测）。
//
// 行为矩阵（只读 state.DeviceConfig 中 port-security 键与 state.MACTable）：
//  1) 端口未 enable            → {Admit:true}（不介入 L2）。
//  2) 端口已 error-down        → {Admit:false}（shutdown 后后续帧一律丢弃，不再计数）。
//  3) 来源 MAC 属授权（手动绑定 / MACTable 中 Type∈{sticky,security}）
//                             → {Admit:true}（不学习不计数）。
//  4) 已占用安全 MAC 数(手动绑定+MACTable sticky/security) >= max-mac 且非授权
//                             → 触发 protect-action：protect=丢不记录；restrict=丢+violation；
//                                shutdown=丢+error-down 置位+violation。
//  5) 未达上限                → {Admit:true, Learned:&MACEntry{...,Type: sticky?}}。
//
// 不修改任何 state 字段（包括不 append MACTable、不改 DeviceConfig、不 import sim 引擎实例）；
// 副作用（写 MACTable / error-down / 计数 / 持久化键）由 handleSimulateFrame 依据返回值落地。
func EvaluatePortSecurity(state *CLIState, iface string, frame Frame) PortSecurityResult {
	if state == nil {
		return PortSecurityResult{Admit: true}
	}
	// 1) 未启用：不介入 L2，直接准入。
	if !psIsEnabled(state, iface) {
		return PortSecurityResult{Admit: true}
	}
	// 2) 已 error-down：后续帧一律丢弃，不再计数。
	if state.DeviceConfig[psKey(iface, psKeyErrorDown)] == "true" {
		return PortSecurityResult{Admit: false}
	}
	// 规范化来源 MAC 用于授权判定与学习写入。
	src := frame.SrcMAC
	if c, ok := canonMAC(frame.SrcMAC); ok {
		src = c
	}
	// 3) 授权 MAC（手动绑定 / 已学安全·粘滞）→ 准入，不学习不计数。
	if psIsAuthorized(state, iface, src) {
		return PortSecurityResult{Admit: true}
	}
	// 4) 安全 MAC 槽位已满 → 触发 protect-action。
	used := psCountSecureMACs(state, iface)
	max := psMaxMAC(state, iface)
	if used >= max {
		action := psProtectAction(state, iface)
		v := &PortSecurityViolation{Action: action, ErrorDown: action == "shutdown"}
		return PortSecurityResult{Admit: false, Violation: v}
	}
	// 5) 未达上限 → 准入且应学习；粘滞开启则 Type=sticky，否则 security。
	learnType := "security"
	if psIsSticky(state, iface) {
		learnType = "sticky"
	}
	learned := &MACEntry{
		MAC:       src,
		VLAN:      frame.VLAN,
		Interface: iface,
		Type:      learnType,
	}
	return PortSecurityResult{Admit: true, Learned: learned}
}

// portSecSimNote 返回端口安全诚实占位注记（lite/full 两态，口径同 natSimNote/aclSimNote）。
//   lite → "（模拟帧注入（lite 引擎），非内核级真实 MAC 学习）"
//   full → "（模拟帧注入）"
func portSecSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（模拟帧注入（lite 引擎），非内核级真实 MAC 学习）"
	}
	return "（模拟帧注入）"
}

// incPortSecurityViolations 递增指定端口的违规计数（运行态）。由 handleSimulateFrame 在
// restrict/shutdown 违规时调用；protect 动作不调用（protect=丢不记录）。
func (state *CLIState) incPortSecurityViolations(iface string) {
	key := psKey(iface, psKeyViolations)
	n := 0
	if v, ok := state.DeviceConfig[key]; ok {
		if m, err := strconv.Atoi(v); err == nil {
			n = m
		}
	}
	n++
	state.DeviceConfig[key] = strconv.Itoa(n)
}
