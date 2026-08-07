// lag_cmd.go 实现「链路聚合 Eth-Trunk 配置命令的副作用落地层」（P2 第五项，T02/T04）。
//
// 与 lag_eval.go 的分工（架构铁律 2）：
//   - lag_eval.go：**纯函数**评估器，只读 DeviceConfig / Interfaces，不写任何 state；
//   - lag_cmd.go ：**唯一副作用出口**，把已校验的解析结果落到 DeviceConfig 单一事实源。
//
// 单一事实源（设计 §1.2 / §3）：
//
//	成员归属       interface:<member>:eth-trunk   = "<trunk-id>"
//	成员聚合族     interface:<member>:agg-family  = "huawei" | "h3c"
//	成员 LACP 优先 interface:<member>:lacp:priority
//	聚合口级配置   interface:Eth-Trunk<id>:lag:<field>
//	系统级 LACP    lacp:<field>
//
// **已废弃**：interface:<trunk>:members 逗号串（双写事实源，P0-1 根因）。
// 本文件在写入成员时会主动清理遗留的 :members 键，防止 reload 后两份事实源打架。
//
// 回显口径（P0-18）：配置成功一律 VRP 静默（返回空串），失败返回 `Error: ...`，
// 去掉现状残桩的 "Port added to Eth-Trunk 1" / "Aggregation mode set to x" 等非 VRP 文案。
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// lagCommandNames 是入顶层能力矩阵的链路聚合命令名集合（设计 §1.7）。
// `mode` 刻意不在其中（通用动词，改走 applyLAGMode 分支内守卫）。
var lagCommandNames = map[string]bool{
	"eth-trunk":        true,
	"trunkport":        true,
	"load-balance":     true,
	"link-aggregation": true,
}

// isLAGCommandName 判定命令名是否属链路聚合命令族（用于能力拒绝时给 VRP 专用文案）。
func isLAGCommandName(command string) bool {
	return lagCommandNames[strings.ToLower(strings.TrimSpace(command))]
}

// lagDeviceSupported 判定当前设备类型是否支持链路聚合（分支内守卫，设计 §1.7）。
// 与 ExecuteCommandOn 顶层守卫同口径：**未绑定设备类型（空串）时放行**，
// 避免单测 / 无拓扑场景被误拒。
func lagDeviceSupported(state *CLIState) bool {
	if state == nil || state.DeviceType == "" {
		return true
	}
	return isCommandSupported("eth-trunk", state.DeviceType)
}

// —— 视图与接口名工具 ——

// lagCurrentTrunkID 返回当前接口视图对应的聚合口 trunk id。
// 当前视图不是聚合口（Eth-Trunk / Bridge-Aggregation）视图时返回 (0,false)。
func lagCurrentTrunkID(state *CLIState) (int, bool) {
	if state == nil || state.CurrentView != ViewInterface {
		return 0, false
	}
	return lagTrunkIDFromName(state.CurrentSub)
}

// lagCurrentTrunkFamily 返回当前聚合口视图所属聚合族（由视图接口名判定）。
func lagCurrentTrunkFamily(state *CLIState) string {
	if state == nil {
		return aggFamilyHuawei
	}
	l := strings.ToLower(state.CurrentSub)
	if strings.HasPrefix(l, "bridge-aggregation") || strings.HasPrefix(l, "bagg") {
		return aggFamilyH3C
	}
	return aggFamilyHuawei
}

// lagCanonIface 把用户输入的接口名规范化为拓扑中的原始写法（大小写不敏感匹配）。
// 未在 state.Interfaces 中命中时原样返回（允许对尚未出现在拓扑里的接口配置）。
func lagCanonIface(state *CLIState, name string) string {
	n := strings.TrimSpace(name)
	if n == "" || state == nil || len(state.Interfaces) == 0 {
		return n
	}
	if canon, err := parseInterface(n, interfaceKeys(state.Interfaces)); err == nil {
		return canon
	}
	return n
}

// syncLAGTrunkIfaceStatus 把聚合口的运行状态从 EvaluateLAG **实时派生**到 state.Interfaces
// 展示快照（P0-11：绝不硬编码 Up）。管理性 shutdown（interface:<trunk>:status=="Down"）优先。
//
// 注意：聚合口运行状态**不落 DeviceConfig 键**（设计 §1.5），此处仅刷新内存展示视图，
// 保证 display interface / display ip interface brief 与 display eth-trunk 口径一致。
func syncLAGTrunkIfaceStatus(state *CLIState, trunkID int) {
	if state == nil || state.Interfaces == nil {
		return
	}
	res := EvaluateLAG(state, trunkID)
	status := "Down"
	if res.OperateStatus == "up" {
		status = "Up"
	}
	for _, name := range []string{lagTrunkName(trunkID), lagBridgeAggName(trunkID)} {
		ic, ok := state.Interfaces[name]
		if !ok || ic == nil {
			continue
		}
		adminKey := fmt.Sprintf("interface:%s:status", name)
		if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[adminKey]), "Down") {
			ic.Status = "Down"
			ic.Protocol = "Down"
			continue
		}
		ic.Status = status
		ic.Protocol = status
	}
}

// lagDropLegacyMembersKey 清理遗留的 interface:<trunk>:members 双写事实源（P0-1 根因）。
func lagDropLegacyMembersKey(state *CLIState, trunkID int) {
	if state == nil {
		return
	}
	for _, name := range []string{lagTrunkName(trunkID), lagBridgeAggName(trunkID)} {
		delete(state.DeviceConfig, fmt.Sprintf("interface:%s:members", name))
		delete(state.DeviceConfig, fmt.Sprintf("interface:%s:mode", name))
		delete(state.DeviceConfig, fmt.Sprintf("interface:%s:load-balance", name))
	}
}

// —— 成员加入 / 退出（P0-9 五项校验） ——

// applyEthTrunkMember 把 iface 加入 trunkID 聚合组（成员加入唯一入口，eth-trunk / trunkport 共用）。
//
// 五项校验（P0-9 官方硬约束，任一不满足即返回 VRP 风格 Error）：
//
//	① trunk-id 范围 0~63；
//	② 目标 Eth-Trunk 必须已存在（未创建 → Error: Eth-Trunk N does not exist）；
//	③ Eth-Trunk 不能作为另一个 Eth-Trunk 的成员；
//	④ 一个以太接口只能加入一个 Eth-Trunk（已属其他组 → 明确报错；已属本组 → 幂等静默）；
//	⑤ 单个 Eth-Trunk 成员数上限 8。
//
// 另加聚合族相容性校验（裁定 #7）：同一组内成员必须同族（huawei / h3c）。
// 成功时仅写 interface:<iface>:eth-trunk 与 :agg-family 两个键（单一事实源），返回空串。
func applyEthTrunkMember(state *CLIState, iface string, trunkID int, family string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	iface = lagCanonIface(state, iface)
	if iface == "" {
		return "Error: need interface name"
	}
	// ① trunk-id 范围
	if ok, msg := validTrunkID(trunkID); !ok {
		return msg
	}
	// ③ Eth-Trunk 不能作为另一个 Eth-Trunk 的成员
	if isTrunkFamilyInterface(iface) {
		return errLAGNestedTrunk
	}
	// ② 目标聚合组必须已存在
	if !lagTrunkExists(state, trunkID) {
		return errLAGTrunkNotExist(trunkID)
	}
	// ④ 一个以太接口只能加入一个 Eth-Trunk
	if cur, ok := lagMemberTrunkID(state, iface); ok {
		if cur == trunkID {
			// 幂等：已在本组，静默成功
			return ""
		}
		return errLAGMemberJoined(cur)
	}
	// ⑤ 成员数上限 8
	existing := collectLAGMemberNames(state, trunkID)
	if len(existing) >= lagMaxMembers {
		return errLAGMemberOverLimit
	}
	// 聚合族相容性
	fam := strings.ToLower(strings.TrimSpace(family))
	if fam != aggFamilyH3C {
		fam = aggFamilyHuawei
	}
	if len(existing) > 0 {
		cur := lagTrunkAggFamily(state, trunkID)
		if cur != fam {
			return fmt.Sprintf("Error: The member interface family (%s) does not match the aggregation group family (%s)", fam, cur)
		}
	}

	state.DeviceConfig[lagMemberKey(iface, "eth-trunk")] = strconv.Itoa(trunkID)
	state.DeviceConfig[lagMemberKey(iface, "agg-family")] = fam
	lagDropLegacyMembersKey(state, trunkID)
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// applyUndoEthTrunkMember 把 iface 从其所属聚合组移出（undo eth-trunk）。
// 接口未加入任何聚合组时返回 VRP 风格错误。成功静默。
func applyUndoEthTrunkMember(state *CLIState, iface string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	iface = lagCanonIface(state, iface)
	trunkID, ok := lagMemberTrunkID(state, iface)
	if !ok {
		return "Error: The interface is not a member of any Eth-Trunk"
	}
	delete(state.DeviceConfig, lagMemberKey(iface, "eth-trunk"))
	delete(state.DeviceConfig, lagMemberKey(iface, "agg-family"))
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// —— trunkport（聚合口视图批量纳管成员） ——

// splitTrunkportSegments 把接口号串（如 "0/0/1"）拆成段。空串返回 nil。
func splitTrunkportSegments(num string) []string {
	n := strings.TrimSpace(num)
	if n == "" {
		return nil
	}
	return strings.Split(n, "/")
}

// expandTrunkportRange 展开 `trunkport <type> <num1> to <num2>` 的接口号区间（裁定 #9）。
//
// 规则：**仅末段可变**，前段必须完全相同（否则 Error: invalid interface range）；
// 起止均须为非负整数且 num1 ≤ num2；单次展开数量 ≤ 8（成员上限）。
// 返回展开后的完整接口名列表（ifType 直接前缀拼接）。
func expandTrunkportRange(ifType, start, end string) ([]string, string) {
	sSeg := splitTrunkportSegments(start)
	eSeg := splitTrunkportSegments(end)
	if len(sSeg) == 0 || len(eSeg) == 0 || len(sSeg) != len(eSeg) {
		return nil, errLAGInvalidRange
	}
	for i := 0; i < len(sSeg)-1; i++ {
		if sSeg[i] != eSeg[i] {
			return nil, errLAGInvalidRange
		}
	}
	sn, err1 := strconv.Atoi(sSeg[len(sSeg)-1])
	en, err2 := strconv.Atoi(eSeg[len(eSeg)-1])
	if err1 != nil || err2 != nil || sn < 0 || en < sn {
		return nil, errLAGInvalidRange
	}
	if en-sn+1 > lagMaxMembers {
		return nil, errLAGMemberOverLimit
	}
	out := make([]string, 0, en-sn+1)
	prefix := ""
	if len(sSeg) > 1 {
		prefix = strings.Join(sSeg[:len(sSeg)-1], "/") + "/"
	}
	for i := sn; i <= en; i++ {
		out = append(out, ifType+prefix+strconv.Itoa(i))
	}
	return out, ""
}

// parseTrunkportArgs 解析 trunkport 参数，返回待纳管的成员接口名列表。
//
// 支持三种写法：
//
//	trunkport GigabitEthernet 0/0/1                  （单口，官方）
//	trunkport GigabitEthernet 0/0/1 to 0/0/3         （区间，官方：to 后只给接口号）
//	trunkport GigabitEthernet 0/0/1 to GigabitEthernet 0/0/3（宽容：兼容既有用法）
//	trunkport GE0/0/1                                 （宽容：类型与编号连写）
func parseTrunkportArgs(args []string) ([]string, string) {
	usage := "Error: usage: trunkport <interface-type> <interface-number> [ to <interface-number> ]"
	if len(args) == 0 {
		return nil, usage
	}
	// 连写形式：trunkport GE0/0/1 [to GE0/0/3]
	if len(args) == 1 {
		return []string{args[0]}, ""
	}
	if len(args) == 3 && strings.EqualFold(args[1], "to") {
		ifType := lagPortTypeTokenRaw(args[0])
		if ifType == "" {
			return nil, usage
		}
		startNum := strings.TrimPrefix(args[0], ifType)
		endNum := strings.TrimPrefix(args[2], ifType)
		return expandTrunkportRange(ifType, startNum, endNum)
	}

	ifType := args[0]
	startNum := args[1]
	if len(args) == 2 {
		return []string{ifType + startNum}, ""
	}
	if !strings.EqualFold(args[2], "to") {
		return nil, usage
	}
	switch len(args) {
	case 4: // 官方：to 后只给接口号
		return expandTrunkportRange(ifType, startNum, args[3])
	case 5: // 宽容：to 后重复接口类型
		if !strings.EqualFold(args[3], ifType) {
			return nil, errLAGInvalidRange
		}
		return expandTrunkportRange(ifType, startNum, args[4])
	}
	return nil, usage
}

// lagPortTypeTokenRaw 返回接口名去掉尾部「数字/斜杠」段后的类型串（保留原大小写）。
func lagPortTypeTokenRaw(name string) string {
	s := strings.TrimSpace(name)
	i := len(s)
	for i > 0 {
		c := s[i-1]
		if (c >= '0' && c <= '9') || c == '/' {
			i--
			continue
		}
		break
	}
	return s[:i]
}

// applyLAGTrunkport 处理聚合口视图的 `trunkport ...`（批量纳管成员，复用 P0-9 校验）。
// 任一成员校验失败即整体报错返回（VRP 遇错即停）。成功静默。
func applyLAGTrunkport(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: trunkport command is only available in Eth-Trunk interface view"
	}
	names, errMsg := parseTrunkportArgs(args)
	if errMsg != "" {
		return errMsg
	}
	family := lagCurrentTrunkFamily(state)
	for _, n := range names {
		if msg := applyEthTrunkMember(state, n, trunkID, family); msg != "" {
			return msg
		}
	}
	return ""
}

// applyUndoLAGTrunkport 处理 `undo trunkport ...`（批量移出成员）。成功静默。
func applyUndoLAGTrunkport(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: trunkport command is only available in Eth-Trunk interface view"
	}
	names, errMsg := parseTrunkportArgs(args)
	if errMsg != "" {
		return errMsg
	}
	for _, n := range names {
		canon := lagCanonIface(state, n)
		cur, has := lagMemberTrunkID(state, canon)
		if !has || cur != trunkID {
			return fmt.Sprintf("Error: %s is not a member of %s", canon, lagTrunkName(trunkID))
		}
		delete(state.DeviceConfig, lagMemberKey(canon, "eth-trunk"))
		delete(state.DeviceConfig, lagMemberKey(canon, "agg-family"))
	}
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// —— mode / load-balance / least|max active-linknumber ——

// applyLAGMode 处理聚合口视图的 `mode { manual load-balance | lacp-static }`（P0-6）。
//
// 设备能力守卫在**分支内**完成（设计 §1.7：`mode` 不入顶层能力矩阵，爆炸半径为零）。
// 两 token 整体识别（修复现状只存 "manual" 的缺陷）；
// LACP → 手工且存在成员时强制拒绝（裁定 #5）。成功静默。
func applyLAGMode(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	// 分支内设备类型守卫（§1.7）
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	if state.CurrentView != ViewInterface {
		return "Error: must be in Eth-Trunk interface view"
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: mode command is only available in Eth-Trunk interface view"
	}
	if len(args) == 0 {
		return "Error: usage: mode { manual load-balance | lacp-static }"
	}
	mode, valid, msg := validLAGMode(strings.Join(args, " "))
	if !valid {
		return msg
	}
	cur := LAGMode(lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode)))
	// 裁定 #5：LACP → 手工必须先清空成员（真机硬约束）
	if cur == LAGModeLACP && mode == LAGModeManual && len(collectLAGMemberNames(state, trunkID)) > 0 {
		return errLAGModeSwitchMember
	}
	state.DeviceConfig[lagTrunkKey(trunkID, "mode")] = string(mode)
	lagDropLegacyMembersKey(state, trunkID)
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// applyUndoLAGMode 处理 `undo mode`（恢复缺省 manual load-balance）。
func applyUndoLAGMode(state *CLIState) string {
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: mode command is only available in Eth-Trunk interface view"
	}
	if LAGMode(lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode))) == LAGModeLACP &&
		len(collectLAGMemberNames(state, trunkID)) > 0 {
		return errLAGModeSwitchMember
	}
	delete(state.DeviceConfig, lagTrunkKey(trunkID, "mode"))
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// applyLAGLoadBalance 处理聚合口视图的 `load-balance <算法>`（六值枚举校验）。成功静默。
func applyLAGLoadBalance(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: load-balance command is only available in Eth-Trunk interface view"
	}
	if len(args) == 0 {
		return "Error: usage: load-balance { dst-ip | dst-mac | src-ip | src-mac | src-dst-ip | src-dst-mac }"
	}
	lb := strings.ToLower(strings.TrimSpace(args[0]))
	if valid, msg := validLoadBalance(lb); !valid {
		return msg
	}
	state.DeviceConfig[lagTrunkKey(trunkID, "load-balance")] = lb
	lagDropLegacyMembersKey(state, trunkID)
	return ""
}

// applyUndoLAGLoadBalance 处理 `undo load-balance`（恢复缺省 src-dst-ip）。
func applyUndoLAGLoadBalance(state *CLIState) string {
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: load-balance command is only available in Eth-Trunk interface view"
	}
	delete(state.DeviceConfig, lagTrunkKey(trunkID, "load-balance"))
	return ""
}

// applyLAGLinkNumber 处理 `least active-linknumber <n>` / `max active-linknumber <n>`（P0-14）。
//
// kind 取 "least" | "max"；校验 1 ≤ n ≤ 8 且 least ≤ max。
// `max active-linknumber` 仅 LACP 模式有效，手工模式下给出官方语义提示（配置仍记录）。
func applyLAGLinkNumber(state *CLIState, kind string, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return fmt.Sprintf("Error: %s active-linknumber command is only available in Eth-Trunk interface view", kind)
	}
	if len(args) < 2 || !strings.EqualFold(args[0], "active-linknumber") {
		return fmt.Sprintf("Error: usage: %s active-linknumber <1-8>", kind)
	}
	n, err := parseNum(args[1])
	if err != nil {
		return fmt.Sprintf("Error: usage: %s active-linknumber <1-8>", kind)
	}
	if valid, msg := validLinkNumber(n); !valid {
		return msg
	}
	least := lagCfgInt(state, lagTrunkKey(trunkID, "least-active-linknumber"), DefaultLeastLink)
	max := lagCfgInt(state, lagTrunkKey(trunkID, "max-active-linknumber"), DefaultMaxActiveLink)
	if strings.EqualFold(kind, "least") {
		if n > max {
			return errLAGLeastGreaterMax
		}
		state.DeviceConfig[lagTrunkKey(trunkID, "least-active-linknumber")] = strconv.Itoa(n)
		syncLAGTrunkIfaceStatus(state, trunkID)
		return ""
	}
	if n < least {
		return errLAGLeastGreaterMax
	}
	state.DeviceConfig[lagTrunkKey(trunkID, "max-active-linknumber")] = strconv.Itoa(n)
	syncLAGTrunkIfaceStatus(state, trunkID)
	mode := LAGMode(lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode)))
	if mode != LAGModeLACP {
		// 官方语义提示：max active-linknumber 仅 LACP 模式生效（P0-14）
		return "Info: The max active-linknumber takes effect only in LACP mode."
	}
	return ""
}

// applyUndoLAGLinkNumber 处理 `undo least|max active-linknumber`（恢复缺省）。
func applyUndoLAGLinkNumber(state *CLIState, kind string) string {
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return fmt.Sprintf("Error: %s active-linknumber command is only available in Eth-Trunk interface view", kind)
	}
	if strings.EqualFold(kind, "least") {
		delete(state.DeviceConfig, lagTrunkKey(trunkID, "least-active-linknumber"))
	} else {
		delete(state.DeviceConfig, lagTrunkKey(trunkID, "max-active-linknumber"))
	}
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// —— H3C 变体（降级 + 消歧，裁定 #7） ——

// applyH3CLinkAggregationMode 处理 H3C 聚合口视图 `link-aggregation mode { static | dynamic }`。
// dynamic → lacp-static；static → manual load-balance（写入统一的 :lag:mode 键）。成功静默。
func applyH3CLinkAggregationMode(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunkID, ok := lagCurrentTrunkID(state)
	if !ok {
		return "Error: link-aggregation command is only available in Bridge-Aggregation interface view"
	}
	if len(args) < 2 || !strings.EqualFold(args[0], "mode") {
		return "Error: usage: link-aggregation mode { static | dynamic }"
	}
	var mode LAGMode
	switch strings.ToLower(args[1]) {
	case "dynamic":
		mode = LAGModeLACP
	case "static":
		mode = LAGModeManual
	default:
		return errLAGUnrecognized
	}
	if LAGMode(lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode))) == LAGModeLACP &&
		mode == LAGModeManual && len(collectLAGMemberNames(state, trunkID)) > 0 {
		return errLAGModeSwitchMember
	}
	state.DeviceConfig[lagTrunkKey(trunkID, "mode")] = string(mode)
	lagDropLegacyMembersKey(state, trunkID)
	syncLAGTrunkIfaceStatus(state, trunkID)
	return ""
}

// applyH3CPortLinkAggregationGroup 处理 H3C 物理口视图 `port link-aggregation group <id>`。
// 复用 P0-9 五项校验，写 agg-family="h3c" 消歧（拍板 #4 幽灵组修复的实现前提）。成功静默。
func applyH3CPortLinkAggregationGroup(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	if state.CurrentView != ViewInterface {
		return "Error: must be in physical interface view"
	}
	if len(args) < 3 || !strings.EqualFold(args[1], "group") {
		return "Error: usage: port link-aggregation group <id>"
	}
	groupID, err := parseNum(args[2])
	if err != nil {
		return errLAGInvalidTrunkID
	}
	return applyEthTrunkMember(state, state.CurrentSub, groupID, aggFamilyH3C)
}

// —— LACP 扩展（T04 改动点 13，与 M-LAG 共存不冲突） ——

// applyLACPFeature 处理非 M-LAG 的 lacp 子命令族：
//
//	系统视图      lacp priority <0-65535>
//	              lacp preempt { enable | disable }
//	              lacp preempt delay <0-180>
//	              lacp timeout { fast | slow }
//	成员口视图    lacp priority <0-65535>            → interface:<m>:lacp:priority
//	聚合口视图    lacp preempt { enable | disable }  → :lag:preempt
//	              lacp preempt delay <0-180>          → :lag:preempt-delay
//	              lacp timeout { fast | slow }        → :lag:lacp-timeout
//
// 成功静默；未识别返回 VRP 风格错误。
func applyLACPFeature(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if len(args) == 0 {
		return "Error: incomplete command"
	}
	if !lagDeviceSupported(state) {
		return errLAGNotSupported(string(state.DeviceType))
	}
	trunkID, inTrunkView := lagCurrentTrunkID(state)
	sub := strings.ToLower(args[0])
	switch sub {
	case "priority":
		if len(args) < 2 {
			return "Error: usage: lacp priority <0-65535>"
		}
		p, err := parseNum(args[1])
		if err != nil {
			return "Error: usage: lacp priority <0-65535>"
		}
		if valid, msg := validLACPPriority(p); !valid {
			return msg
		}
		switch {
		case state.CurrentView == ViewSystem:
			state.DeviceConfig[lagSysKey("priority")] = strconv.Itoa(p)
		case state.CurrentView == ViewInterface && !inTrunkView:
			state.DeviceConfig[lagMemberKey(state.CurrentSub, "lacp:priority")] = strconv.Itoa(p)
		default:
			return "Error: lacp priority is only available in system view or member interface view"
		}
		return ""
	case "preempt":
		if len(args) < 2 {
			return "Error: usage: lacp preempt { enable | disable | delay <0-180> }"
		}
		if strings.EqualFold(args[1], "delay") {
			if len(args) < 3 {
				return "Error: usage: lacp preempt delay <0-180>"
			}
			d, err := parseNum(args[2])
			if err != nil {
				return "Error: usage: lacp preempt delay <0-180>"
			}
			if valid, msg := validPreemptDelay(d); !valid {
				return msg
			}
			if inTrunkView {
				state.DeviceConfig[lagTrunkKey(trunkID, "preempt-delay")] = strconv.Itoa(d)
			} else if state.CurrentView == ViewSystem {
				state.DeviceConfig[lagSysKey("preempt-delay")] = strconv.Itoa(d)
			} else {
				return "Error: lacp preempt is only available in system view or Eth-Trunk interface view"
			}
			return ""
		}
		v := strings.ToLower(args[1])
		if v != "enable" && v != "disable" {
			return errLAGUnrecognized
		}
		if inTrunkView {
			state.DeviceConfig[lagTrunkKey(trunkID, "preempt")] = v
		} else if state.CurrentView == ViewSystem {
			state.DeviceConfig[lagSysKey("preempt")] = v
		} else {
			return "Error: lacp preempt is only available in system view or Eth-Trunk interface view"
		}
		return ""
	case "timeout":
		if len(args) < 2 {
			return "Error: usage: lacp timeout { fast | slow }"
		}
		v := strings.ToLower(args[1])
		if v != "fast" && v != "slow" {
			return errLAGUnrecognized
		}
		if inTrunkView {
			state.DeviceConfig[lagTrunkKey(trunkID, "lacp-timeout")] = v
		} else if state.CurrentView == ViewSystem {
			state.DeviceConfig[lagSysKey("timeout")] = v
		} else {
			return "Error: lacp timeout is only available in system view or Eth-Trunk interface view"
		}
		return ""
	}
	return "Error: invalid LACP command"
}

// applyUndoLACPFeature 处理 `undo lacp priority|preempt|timeout`（恢复缺省）。
func applyUndoLACPFeature(state *CLIState, args []string) string {
	if state == nil || len(args) == 0 {
		return "Error: incomplete command"
	}
	trunkID, inTrunkView := lagCurrentTrunkID(state)
	switch strings.ToLower(args[0]) {
	case "priority":
		if state.CurrentView == ViewSystem {
			delete(state.DeviceConfig, lagSysKey("priority"))
			return ""
		}
		if state.CurrentView == ViewInterface && !inTrunkView {
			delete(state.DeviceConfig, lagMemberKey(state.CurrentSub, "lacp:priority"))
			return ""
		}
		return "Error: lacp priority is only available in system view or member interface view"
	case "preempt":
		if len(args) >= 2 && strings.EqualFold(args[1], "delay") {
			if inTrunkView {
				delete(state.DeviceConfig, lagTrunkKey(trunkID, "preempt-delay"))
			} else {
				delete(state.DeviceConfig, lagSysKey("preempt-delay"))
			}
			return ""
		}
		if inTrunkView {
			delete(state.DeviceConfig, lagTrunkKey(trunkID, "preempt"))
		} else {
			delete(state.DeviceConfig, lagSysKey("preempt"))
		}
		return ""
	case "timeout":
		if inTrunkView {
			delete(state.DeviceConfig, lagTrunkKey(trunkID, "lacp-timeout"))
		} else {
			delete(state.DeviceConfig, lagSysKey("timeout"))
		}
		return ""
	}
	return "Error: invalid LACP command"
}

// —— undo interface Eth-Trunk <id>（T04 改动点 12，AC11） ——

// applyUndoInterfaceTrunk 处理系统视图 `undo interface Eth-Trunk <id>`。
//
// 官方硬约束（P0-5 / AC11）：**存在成员时必须拒绝**；无成员才允许删除，
// 删除时清理 interface:Eth-Trunk<id>:* 全部键与 state.Interfaces 条目。成功静默。
func applyUndoInterfaceTrunk(state *CLIState, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	if len(args) < 2 {
		return "Error: usage: undo interface <interface-name>"
	}
	name := strings.Join(args[1:], "")
	trunkID, ok := lagTrunkIDFromName(name)
	if !ok {
		return fmt.Sprintf("Error: undo interface '%s' is not supported", strings.Join(args[1:], " "))
	}
	if valid, msg := validTrunkID(trunkID); !valid {
		return msg
	}
	if len(collectLAGMemberNames(state, trunkID)) > 0 {
		return errLAGHasMembers
	}
	for _, tn := range []string{lagTrunkName(trunkID), lagBridgeAggName(trunkID)} {
		prefix := "interface:" + tn + ":"
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, prefix) {
				delete(state.DeviceConfig, k)
			}
		}
		delete(state.Interfaces, tn)
	}
	if state.CurrentSub != "" {
		if id, isTrunk := lagTrunkIDFromName(state.CurrentSub); isTrunk && id == trunkID {
			state.CurrentSub = ""
		}
	}
	return ""
}

// applyUndoLAGInterface 在接口视图统一拦截链路聚合相关的 undo 子命令。
// 返回 (回显, 是否已处理)；未命中时 handled=false，交回既有 undo 分支。
func applyUndoLAGInterface(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 {
		return "", false
	}
	switch strings.ToLower(args[0]) {
	case "eth-trunk":
		return applyUndoEthTrunkMember(state, state.CurrentSub), true
	case "trunkport":
		return applyUndoLAGTrunkport(state, args[1:]), true
	case "mode":
		if _, ok := lagCurrentTrunkID(state); ok {
			return applyUndoLAGMode(state), true
		}
	case "load-balance":
		if _, ok := lagCurrentTrunkID(state); ok {
			return applyUndoLAGLoadBalance(state), true
		}
	case "least", "max":
		if _, ok := lagCurrentTrunkID(state); ok {
			return applyUndoLAGLinkNumber(state, strings.ToLower(args[0])), true
		}
	case "lacp":
		return applyUndoLACPFeature(state, args[1:]), true
	case "port":
		// undo port link-aggregation group
		if len(args) >= 3 && strings.EqualFold(args[1], "link-aggregation") && strings.EqualFold(args[2], "group") {
			return applyUndoEthTrunkMember(state, state.CurrentSub), true
		}
	}
	return "", false
}
