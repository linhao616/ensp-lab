// lag_display.go 实现「链路聚合 display 命令的 VRP 保真渲染」（P2 第五项，T03）。
//
// 渲染层唯一数据源 = lag_eval.go 的 EvaluateLAG / collectLAGTrunks（纯函数派生），
// **不再直接读散落的 DeviceConfig 键**，从根上修复残桩的：
//   - map 双重随机遍历（同一配置两次 display 输出不同，AC5 不可回归）；
//   - 成员状态空值默认 "Up"（编造）；
//   - 聚合口状态读 interface:<trunk>:status 键（硬编码 Up，与成员无联动）；
//   - `display link-aggregation summary` 第二个重映射循环编造幽灵
//     Bridge-Aggregation<N>（P1-10 升级 P0，拍板 #4）。
//
// 诚实占位铁律（拍板 #3）：
//   - 保留官方列名，真机由 LACPDU 协商 / 数据面统计得出的列（PortState 位图、
//     Weight、流量·报文计数）统一填 "-"，**绝不填随机数**；
//   - Partner 整块诚实占位，绝不列伪造行；
//   - 末尾统一附 lagSimNote()（lite/full 两态）。
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// lagDisplaySeparator 是 VRP display 的 80 列分隔线。
const lagDisplaySeparator = "--------------------------------------------------------------------------------"

// buildEthTrunkDisplay 渲染 `display eth-trunk [<trunk-id>] [verbose | load-balance | interface <if>]`。
//
// 参数 args 为 display 子命令之后的剩余 token（不含 "eth-trunk" 本身）。
// 无 trunk-id 时按 trunk-id **升序**逐组输出完整块（裁定 #10）；
// 成员顺序由 collectLAGMembers 的 comparePortIndex 自然序保证确定性（AC5）。
func buildEthTrunkDisplay(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}

	// —— 解析子命令 ——
	var (
		trunkID   = -1
		verbose   bool
		showLB    bool
		memberArg string
	)
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if strings.TrimSpace(a) != "" {
			rest = append(rest, a)
		}
	}
	if len(rest) > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(rest[0])); err == nil {
			trunkID = n
			rest = rest[1:]
		}
	}
	for i := 0; i < len(rest); i++ {
		switch strings.ToLower(rest[i]) {
		case "verbose":
			verbose = true
		case "load-balance":
			showLB = true
		case "interface", "int":
			if i+1 < len(rest) {
				memberArg = rest[i+1]
				i++
			}
		case "brief":
			// 兼容既有 `display eth-trunk brief`，等价于无参列表
		default:
			return errLAGUnrecognized
		}
	}

	trunks := collectLAGTrunks(state)
	if len(trunks) == 0 {
		return "Error: The Eth-Trunk does not exist."
	}
	if trunkID >= 0 {
		if ok, msg := validTrunkID(trunkID); !ok {
			return msg
		}
		if !lagTrunkExists(state, trunkID) {
			return "Error: The Eth-Trunk does not exist."
		}
		trunks = []int{trunkID}
	}

	var out strings.Builder
	for i, id := range trunks {
		if i > 0 {
			out.WriteString("\n")
		}
		res := EvaluateLAG(state, id)
		switch {
		case showLB:
			out.WriteString(renderLAGLoadBalanceBlock(state, res))
		case memberArg != "":
			out.WriteString(renderLAGMemberBlock(state, res, memberArg))
		default:
			out.WriteString(renderLAGTrunkBlock(state, res, verbose))
		}
	}
	return out.String()
}

// renderLAGTrunkBlock 渲染单个聚合组的官方 `display eth-trunk <id>` 块。
//
// 手工负载分担与 LACP 静态两套官方字段集（VRP 真机行为不同）：
//   - manual load-balance：WorkingMode: NORMAL，成员表 PortName / Status / Weight；
//   - lacp-static        ：WorkingMode: LACP，Local 块含 System Priority / System ID，
//     成员表 ActorPortName / Status / PortType / PortPri / PortNo / PortKey / PortState，
//     并附 Partner 整块诚实占位。
func renderLAGTrunkBlock(state *CLIState, res LAGResult, verbose bool) string {
	var b strings.Builder
	name := lagDisplayTrunkName(state, res.TrunkID)
	b.WriteString(fmt.Sprintf("%s's state information is:\n", name))

	if res.Mode == LAGModeLACP {
		b.WriteString("Local:\n")
		b.WriteString(fmt.Sprintf("LAG ID: %-18d WorkingMode: %s\n", res.TrunkID, lagWorkingModeName(res.Mode)))
		b.WriteString(fmt.Sprintf("Preempt Delay Time: %-6d Hash arithmetic: %s\n", res.PreemptDelay, res.HashArithmetic))
		b.WriteString(fmt.Sprintf("System Priority: %-9d System ID: %s\n", res.SysPriority, res.SysMAC))
		b.WriteString(fmt.Sprintf("Least Active-linknumber: %-1d Max Active-linknumber: %d\n", res.LeastLink, res.MaxActiveLink))
		b.WriteString(fmt.Sprintf("Operate status: %-10s Number Of Up Port In Trunk: %d\n", res.OperateStatus, res.UpPortCount))
		b.WriteString(lagDisplaySeparator + "\n")
		b.WriteString(fmt.Sprintf("%-22s %-8s %-10s %-9s %-8s %-9s %s\n",
			"ActorPortName", "Status", "PortType", "PortPri", "PortNo", "PortKey", "PortState"))
		for _, m := range res.LocalBlock {
			role := m.Role
			if role == "" {
				role = lagRoleUnselect
			}
			b.WriteString(fmt.Sprintf("%-22s %-8s %-10s %-9d %-8d %-9d %s\n",
				m.Name, role, lagPortMediaType(m.Name), m.PortLACPPri,
				lagPortNo(m.Name), lagPortKey(res.TrunkID), lagPlaceholder))
		}
		if len(res.LocalBlock) == 0 {
			b.WriteString("（该聚合组当前无成员接口）\n")
		}
		b.WriteString(lagColumnPlaceholderNote + "\n")
		b.WriteString("Partner:\n")
		b.WriteString(res.PartnerBlock + "\n")
	} else {
		b.WriteString(fmt.Sprintf("WorkingMode: %-16s Hash arithmetic: %s\n", lagWorkingModeName(res.Mode), res.HashArithmetic))
		b.WriteString(fmt.Sprintf("Least Active-linknumber: %-4d Max Bandwidth-affected-linknumber: %d\n", res.LeastLink, res.MaxActiveLink))
		b.WriteString(fmt.Sprintf("Operate status: %-13s Number Of Up Port In Trunk: %d\n", res.OperateStatus, res.UpPortCount))
		b.WriteString(lagDisplaySeparator + "\n")
		b.WriteString(fmt.Sprintf("%-30s %-11s %s\n", "PortName", "Status", "Weight"))
		for _, m := range res.LocalBlock {
			b.WriteString(fmt.Sprintf("%-30s %-11s %s\n", m.Name, m.Status, lagPlaceholder))
		}
		if len(res.LocalBlock) == 0 {
			b.WriteString("（该聚合组当前无成员接口）\n")
		}
		b.WriteString(lagColumnPlaceholderNote + "\n")
	}

	if verbose {
		b.WriteString(renderLAGVerboseTail(res))
	}
	b.WriteString(res.SimNote + "\n")
	return b.String()
}

// renderLAGVerboseTail 渲染 `display eth-trunk <id> verbose` 的补充块。
// 仅输出配置态真值 + 诚实占位，不编造任何协商/统计数据。
func renderLAGVerboseTail(res LAGResult) string {
	var b strings.Builder
	b.WriteString(lagDisplaySeparator + "\n")
	b.WriteString(fmt.Sprintf("Load-Balance Profile: %s\n", res.LoadBalance))
	b.WriteString(fmt.Sprintf("LACP Preempt: %s    Preempt Delay: %ds    LACP Timeout: %s\n",
		res.Preempt, res.PreemptDelay, res.LACPTimeout))
	b.WriteString(fmt.Sprintf("Aggregation Family: %s    Member Count: %d    Active Member Count: %d\n",
		res.AggFamily, len(res.Members), len(res.ActiveMembers)))
	b.WriteString(fmt.Sprintf("Input/Output Statistics: %s（真机数据面统计，本工具不模拟）\n", lagPlaceholder))
	return b.String()
}

// renderLAGLoadBalanceBlock 渲染 `display eth-trunk [<id>] load-balance`。
func renderLAGLoadBalanceBlock(state *CLIState, res LAGResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s's load-balance configuration is:\n", lagDisplayTrunkName(state, res.TrunkID)))
	b.WriteString(fmt.Sprintf("Load-Balance Profile: %s\n", res.LoadBalance))
	b.WriteString(fmt.Sprintf("Hash arithmetic: %s\n", res.HashArithmetic))
	b.WriteString("（负载分担仅记录配置态并映射展示串，本工具无二层数据面，不做哈希转发模拟）\n")
	b.WriteString(res.SimNote + "\n")
	return b.String()
}

// renderLAGMemberBlock 渲染 `display eth-trunk <id> interface <interface-name>`。
func renderLAGMemberBlock(state *CLIState, res LAGResult, member string) string {
	canon := lagCanonIface(state, member)
	for _, m := range res.Members {
		if !strings.EqualFold(m.Name, canon) {
			continue
		}
		role := m.Role
		if role == "" {
			role = m.Status
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%s's member %s information is:\n", lagDisplayTrunkName(state, res.TrunkID), m.Name))
		b.WriteString(fmt.Sprintf("PortName: %s    PortType: %s    Status: %s\n", m.Name, lagPortMediaType(m.Name), m.Status))
		b.WriteString(fmt.Sprintf("PortPri: %d    PortNo: %d    PortKey: %d    Role: %s\n",
			m.PortLACPPri, lagPortNo(m.Name), lagPortKey(res.TrunkID), role))
		b.WriteString(fmt.Sprintf("PortState: %s    Weight: %s\n", lagPlaceholder, lagPlaceholder))
		b.WriteString(lagColumnPlaceholderNote + "\n")
		b.WriteString(res.SimNote + "\n")
		return b.String()
	}
	return fmt.Sprintf("Error: %s is not a member of %s", canon, lagTrunkName(res.TrunkID))
}

// buildLinkAggregationSummary 渲染 `display link-aggregation summary`（拍板 #4 幽灵组修复）。
//
// 铁律：**仅输出用户真实配置的聚合组**，组名按 agg-family 归类
// （huawei → Eth-Trunk<id>，h3c → Bridge-Aggregation<id>），
// 绝不由 Eth-Trunk 键重映射编造 Bridge-Aggregation<N>（现状残桩第二个循环的根因，已删除）。
// 输出按组名确定性升序（替换 map 随机遍历）。
func buildLinkAggregationSummary(state *CLIState) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunks := collectLAGTrunks(state)

	var b strings.Builder
	b.WriteString("Flags:  A - LAG Active        B - LAG Backup        C - LAG Configured\n")
	b.WriteString("        S - LAG Standingby\n")
	b.WriteString(lagDisplaySeparator + "\n")
	if len(trunks) == 0 {
		b.WriteString("No link aggregation group is configured.\n")
		b.WriteString(lagSimNote() + "\n")
		return b.String()
	}

	type row struct {
		group  string
		bundle string
		mode   string
		member string
		status string
	}
	rows := make([]row, 0, len(trunks)*2)
	for _, id := range trunks {
		res := EvaluateLAG(state, id)
		group := lagDisplayTrunkName(state, id)
		mode := lagWorkingModeName(res.Mode)
		if len(res.Members) == 0 {
			rows = append(rows, row{group: group, bundle: "C", mode: mode, member: lagPlaceholder, status: lagPlaceholder})
			continue
		}
		active := make(map[string]bool, len(res.ActiveMembers))
		for _, m := range res.ActiveMembers {
			active[m.Name] = true
		}
		for _, m := range res.Members {
			bundle := "C"
			status := m.Status
			if active[m.Name] {
				bundle = "A"
				if res.Mode == LAGModeLACP {
					status = lagRoleSelected
				}
			} else if res.Mode == LAGModeLACP {
				status = lagRoleUnselect
			}
			rows = append(rows, row{group: group, bundle: bundle, mode: mode, member: m.Name, status: status})
		}
	}
	// 确定性升序：先按组名（自然序），再沿用 collectLAGMembers 已保证的成员自然序。
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return compareLAGMemberName(rows[i].group, rows[j].group) > 0
		}
		return false
	})

	b.WriteString(fmt.Sprintf("%-14s %-10s %-15s %-18s %s\n", "Bundle", "Mode", "Eth-Trunk", "Member", "Status"))
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-14s %-10s %-15s %-18s %s\n", r.bundle, r.mode, r.group, r.member, r.status))
	}
	b.WriteString(lagSimNote() + "\n")
	return b.String()
}

// —— 配置快照输出（T04 改动点 10，AC2 save→reload 复现） ——

// lagAllMemberNames 返回**全部**已归属任意聚合组的成员接口名（自然序升序，纯函数）。
// 唯一事实源 = interface:<m>:eth-trunk（与 collectLAGMemberNames 同键约定，仅不筛 trunk-id）。
func lagAllMemberNames(state *CLIState) []string {
	if state == nil || len(state.DeviceConfig) == 0 {
		return nil
	}
	names := make([]string, 0, lagMaxMembers)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") || !strings.HasSuffix(k, ":eth-trunk") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			continue
		}
		iface := strings.TrimSuffix(strings.TrimPrefix(k, "interface:"), ":eth-trunk")
		if iface == "" {
			continue
		}
		names = append(names, iface)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Slice(names, func(i, j int) bool {
		return compareLAGMemberName(names[i], names[j]) > 0
	})
	return names
}

// buildSavedLAGInterfaceConfig 输出指定接口下的链路聚合 VRP 合规配置行
// （已缩进，无 interface 包装；口径完全对齐 buildSavedVRRPConfig，parser.go:5536）。
//
// 聚合口（Eth-Trunk / Bridge-Aggregation）→ mode / load-balance / least|max active-linknumber /
// lacp preempt|preempt delay|timeout；成员口 → eth-trunk <id> / lacp priority <n>。
//
// **仅输出与缺省值不同的项**（AC12：缺省值不冗余输出，对齐 VRP 只落差异值的惯例）。
// 纯函数：只读 DeviceConfig，无副作用。
func buildSavedLAGInterfaceConfig(state *CLIState, iface string) string {
	if state == nil || strings.TrimSpace(iface) == "" {
		return ""
	}
	var b strings.Builder
	if trunkID, ok := lagTrunkIDFromName(iface); ok {
		if mode := lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode)); LAGMode(mode) != DefaultLAGMode {
			b.WriteString(fmt.Sprintf(" mode %s\n", mode))
		}
		if lb := lagCfgString(state, lagTrunkKey(trunkID, "load-balance"), DefaultLoadBalance); lb != DefaultLoadBalance {
			b.WriteString(fmt.Sprintf(" load-balance %s\n", lb))
		}
		if n := lagCfgInt(state, lagTrunkKey(trunkID, "least-active-linknumber"), DefaultLeastLink); n != DefaultLeastLink {
			b.WriteString(fmt.Sprintf(" least active-linknumber %d\n", n))
		}
		if n := lagCfgInt(state, lagTrunkKey(trunkID, "max-active-linknumber"), DefaultMaxActiveLink); n != DefaultMaxActiveLink {
			b.WriteString(fmt.Sprintf(" max active-linknumber %d\n", n))
		}
		if v := lagCfgString(state, lagTrunkKey(trunkID, "preempt"), DefaultPreempt); v != DefaultPreempt {
			b.WriteString(fmt.Sprintf(" lacp preempt %s\n", v))
		}
		if n := lagCfgInt(state, lagTrunkKey(trunkID, "preempt-delay"), DefaultPreemptDelay); n != DefaultPreemptDelay {
			b.WriteString(fmt.Sprintf(" lacp preempt delay %d\n", n))
		}
		if v := lagCfgString(state, lagTrunkKey(trunkID, "lacp-timeout"), DefaultLACPTimeout); v != DefaultLACPTimeout {
			b.WriteString(fmt.Sprintf(" lacp timeout %s\n", v))
		}
		return b.String()
	}
	if id, joined := lagMemberTrunkID(state, iface); joined {
		b.WriteString(fmt.Sprintf(" eth-trunk %d\n", id))
	}
	if p := lagCfgInt(state, lagMemberKey(iface, "lacp:priority"), DefaultLACPPortPri); p != DefaultLACPPortPri {
		b.WriteString(fmt.Sprintf(" lacp priority %d\n", p))
	}
	return b.String()
}

// buildSavedLAGConfig 输出「系统级 LACP 配置行」+「state.Interfaces 未重建的聚合口 / 成员口」
// 的完整 interface 块（**复用 VRRP 独立输出通道范式**，parser.go:5529-5541）。
//
// 作用：save→reload 后 state.Interfaces 可能未包含成员物理口，若只遍历 state.Interfaces
// 就会丢掉聚合配置（残桩 P0-2 丢配置缺陷）。本通道按 DeviceConfig 单一事实源补齐，
// 保证 display current-configuration 完整复现（AC2 ③）。
//
// 输出确定性：聚合组按 trunk-id 升序、成员口按接口号自然序（无 map 随机遍历）。
func buildSavedLAGConfig(state *CLIState) string {
	if state == nil {
		return ""
	}
	var b strings.Builder
	// 系统级 LACP（仅差异值）
	sys := 0
	if p := lagCfgInt(state, lagSysKey("priority"), DefaultLACPSysPri); p != DefaultLACPSysPri {
		b.WriteString(fmt.Sprintf("lacp priority %d\n", p))
		sys++
	}
	if v := lagCfgString(state, lagSysKey("preempt"), DefaultPreempt); v != DefaultPreempt {
		b.WriteString(fmt.Sprintf("lacp preempt %s\n", v))
		sys++
	}
	if n := lagCfgInt(state, lagSysKey("preempt-delay"), DefaultPreemptDelay); n != DefaultPreemptDelay {
		b.WriteString(fmt.Sprintf("lacp preempt delay %d\n", n))
		sys++
	}
	if v := lagCfgString(state, lagSysKey("timeout"), DefaultLACPTimeout); v != DefaultLACPTimeout {
		b.WriteString(fmt.Sprintf("lacp timeout %s\n", v))
		sys++
	}
	if sys > 0 {
		b.WriteString("#\n")
	}

	// 聚合口块（trunk-id 升序；已在 state.Interfaces 主循环输出的跳过，避免重复 interface 标题）
	for _, id := range collectLAGTrunks(state) {
		name := lagDisplayTrunkName(state, id)
		if _, ok := state.Interfaces[name]; ok {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", name))
		b.WriteString(buildSavedLAGInterfaceConfig(state, name))
		b.WriteString("#\n")
	}

	// 成员口块（自然序；同样跳过主循环已输出的接口）
	for _, m := range lagAllMemberNames(state) {
		if _, ok := state.Interfaces[m]; ok {
			continue
		}
		lines := buildSavedLAGInterfaceConfig(state, m)
		if lines == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", m))
		b.WriteString(lines)
		b.WriteString("#\n")
	}
	return b.String()
}
