// lag_eval.go 实现「CLIState 层链路聚合 Eth-Trunk / LACP 评估器」（P2 第五项，华为 VRP 课程 63）。
//
// 背景与约束见 docs/p2-lag-prd.md 与 docs/p2-lag-design.md。本评估器把代码库里既有的
// 「只记键值、无聚合行为判定、成员事实源双写、display 非 VRP 保真、无诚实占位、无能力守卫」
// 的链路聚合残桩，升级为一条可对学员实验产生可观测反馈的二层链路捆绑链路。
//
// 架构基线（与 STP / VRRP / 端口安全 / NAT 完全一致，见设计 §1）：
//   - 单一事实源 = state.DeviceConfig：
//     聚合口级 interface:Eth-Trunk<id>:lag:<field>
//     成员口级 interface:<member>:eth-trunk / :agg-family / :lacp:priority
//     系统级   lacp:<field>
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化，
//     从根上修复残桩 save/reload 丢配置缺陷（P0-2）。
//   - **严禁新增 state.LAG 内嵌结构体**（架构铁律 1，同 STP 已移除的 state.STP）。
//     LAGMember / LAGResult 仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不双写。
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎实例、不 import internal/protocol），
//     可单测、可回归，与 stp_eval.go 的 EvaluateSTP、vrrp_eval.go 的 EvaluateVRRP 同一契约。
//   - 本文件仅读 sim.EngineModeName() 决定诚实占位注记的 lite/full 两态
//     （与 aclSimNote / natSimNote / portSecSimNote / vrrpSimNote / stpSimNote 同口径）。
//   - 复用既有 helper，严禁重定义（同包编译冲突）：
//     isPortDown        (stp_eval.go:175)  成员 up/down 唯一判定源
//     stpDeviceMAC      (stp_eval.go:164)  系统 MAC 来源（选举因子②）
//     normalizeMACHex   (stp_eval.go:249)  MAC 字典序比较归一化
//
// 诚实边界（主理人拍板 #1/#2/#3，见设计 §0 / §8）：
//   - 真实 LACP 选举依赖设备间 LACPDU 收发，当前 sim 引擎无 LACPDU 交互，
//     故本期为「本地视图选举」+ 诚实注记，四级比较链全部「数值/字典序小者优先」。
//   - PortState 位图 / Weight / 流量·报文计数为真机 LACPDU 协商与数据面统计产物，
//     本工具统一填 "-"；Partner 整块诚实占位，绝不列伪造行、绝不填随机数。
//   - load-balance 仅记录配置态 + 映射 Hash arithmetic 展示串，不做哈希数据面模拟。
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// LAGMode 工作模式（§3.1 键 interface:Eth-Trunk<id>:lag:mode 的取值）。
type LAGMode string

// 链路聚合缺省常量（设计 §3.1 / §4 常量表；缺省值在读取时合并）。
const (
	// LAGModeManual 手工负载分担模式（VRP: mode manual load-balance）。
	LAGModeManual LAGMode = "manual load-balance"
	// LAGModeLACP LACP 静态模式（VRP: mode lacp-static）。
	LAGModeLACP LAGMode = "lacp-static"
	// DefaultLAGMode 缺省工作模式。
	DefaultLAGMode = LAGModeManual
	// DefaultLoadBalance 缺省负载分担算法。
	// 修正现状 display 残桩误用的 src-dst-mac（设计 §9 O1，主理人已确认 src-dst-ip）。
	DefaultLoadBalance = "src-dst-ip"
	// DefaultLeastLink 活动接口数下限缺省值。
	DefaultLeastLink = 1
	// DefaultMaxActiveLink 活动接口数上限缺省值（仅 lacp-static 生效）。
	DefaultMaxActiveLink = 8
	// DefaultLACPSysPri 系统 LACP 优先级缺省值（选举因子①）。
	DefaultLACPSysPri = 32768
	// DefaultLACPPortPri 端口 LACP 优先级缺省值（选举因子③）。
	DefaultLACPPortPri = 32768
	// DefaultPreempt LACP 抢占开关缺省值。
	DefaultPreempt = "disable"
	// DefaultPreemptDelay LACP 抢占延时（秒）缺省值。
	DefaultPreemptDelay = 30
	// DefaultLACPTimeout LACPDU 收发周期缺省值。
	DefaultLACPTimeout = "slow"
)

// 取值域边界与内部常量。
const (
	lagTrunkIDMin, lagTrunkIDMax           = 0, 63
	lagLinkNumberMin, lagLinkNumberMax     = 1, 8
	lagLACPPriMin, lagLACPPriMax           = 0, 65535
	lagPreemptDelayMin, lagPreemptDelayMax = 0, 180
	// lagMaxMembers 单聚合组成员口数量上限（P0-9 校验 ④）。
	lagMaxMembers = 8
	// lagPlaceholder 诚实占位符（PortState / Weight / 流量计数统一填此值，拍板 #3）。
	lagPlaceholder = "-"
	// aggFamilyHuawei / aggFamilyH3C 聚合族消歧（裁定 #7，拍板 #4 实现前提）。
	aggFamilyHuawei = "huawei"
	aggFamilyH3C    = "h3c"
	// lagRoleSelected / lagRoleUnselect LACP 静态模式下的成员角色。
	lagRoleSelected = "Selected"
	lagRoleUnselect = "Unselect"
)

// lagPartnerPlaceholder 是 Partner 块的整块诚实占位文案（拍板 #3：不列伪造行）。
const lagPartnerPlaceholder = "（对端未接入 LACPDU 交互，Partner 信息不可知，不列伪造行）"

// lagColumnPlaceholderNote 是 Local 块尾部的列级诚实占位说明（拍板 #3）。
const lagColumnPlaceholderNote = "（注：PortState 位图 / Weight / 流量·报文计数为真机 LACPDU 协商与数据面统计产物，本工具仅本地视图，统一填 \"-\"）"

// LAGMember 成员口评估结构（既是评估输入，也承载输出派生字段）。
//
// 说明：SysLACPPri / SysMAC 为设计 §4 结构体的两个派生占位字段（工程实现补充），
// 使 CompareLACPPort 的因子①②（系统 LACP 优先级 / 系统 MAC）成为**可执行、可单测**的
// 比较级，而非注释里的死代码；两字段恒由 collectLAGMembers 从同一设备填入，
// 故对同设备成员恒相等，与拍板 #1「本地视图无区分度」的定性完全一致。
type LAGMember struct {
	Name        string // 接口名，如 "GE0/0/1" / "GigabitEthernet0/0/10"
	TrunkID     int    // 归属 trunk id（来自 interface:<member>:eth-trunk 键）
	AggFamily   string // "huawei" | "h3c"（裁定 #7，拍板 #4 实现前提）
	PhyDown     bool   // 物理 down（复用 isPortDown，唯一判定源）
	PortLACPPri int    // 端口 LACP 优先级（:lacp:priority，缺省 32768，因子③）
	PortIndex   []int  // parsePortIndex(Name) 解析出的数字段（因子④，自然序）
	SysLACPPri  int    // 系统 LACP 优先级（lacp:priority，缺省 32768，因子①，占位）
	SysMAC      string // 系统 MAC（stpDeviceMAC 派生，因子②，占位）
	Selected    bool   // lacp-static 下由选举判定：是否 Selected（活动）
	Role        string // "Selected" | "Unselect"（手工模式为空串，无选举列）
	Status      string // "Up" | "Down"（派生，非键，= PhyDown?"Down":"Up"）
}

// LAGResult 聚合口评估结果（display 渲染唯一数据源）。
//
// 说明：Preempt / PreemptDelay / LACPTimeout / SysPriority / SysMAC / UpPortCount
// 为设计 §4 结构体的渲染必需补充字段（display 官方列需要），均由 DeviceConfig 读取或派生，
// 不新增任何持久化事实源。
type LAGResult struct {
	TrunkID        int
	Mode           LAGMode
	LoadBalance    string // 已校验取值（§3.1）
	Exists         bool   // 来自 :lag:exists 键 或 有成员指向（§1.3）
	OperateStatus  string // "up" | "down"（拍板 #2 实时派生）
	LeastLink      int    // least active-linknumber（缺省 1）
	MaxActiveLink  int    // max active-linknumber（缺省 8，仅 LACP 生效）
	Members        []LAGMember
	ActiveMembers  []LAGMember // 活动口（拍板 #2 定义）
	UpPortCount    int         // Number Of Up Port In Trunk = len(ActiveMembers)
	HashArithmetic string      // hashArithmetic(LoadBalance) 展示串
	SimNote        string      // lagSimNote()（lite/full 诚实注记）
	LocalBlock     []LAGMember // Local 块（保留全部官方列，不可产出填 "-"）
	PartnerBlock   string      // Partner 块诚实占位文案（整块占位，不列伪造行）
	AggFamily      string      // 组归属族（由成员 agg-family 归类，缺省 huawei）
	Preempt        string      // "enable" | "disable"（缺省 disable）
	PreemptDelay   int         // 抢占延时秒（缺省 30）
	LACPTimeout    string      // "fast" | "slow"（缺省 slow）
	SysPriority    int         // 系统 LACP 优先级（缺省 32768）
	SysMAC         string      // 系统 MAC（stpDeviceMAC 派生）
}

// —— 键名 helper（设计 §3 表） ——

// lagTrunkName 返回华为聚合口名：Eth-Trunk<id>。
func lagTrunkName(trunkID int) string {
	return fmt.Sprintf("Eth-Trunk%d", trunkID)
}

// lagBridgeAggName 返回 H3C 聚合口名：Bridge-Aggregation<id>（仅 display 归类用）。
func lagBridgeAggName(trunkID int) string {
	return fmt.Sprintf("Bridge-Aggregation%d", trunkID)
}

// lagTrunkKey 拼接聚合口级键：interface:Eth-Trunk<id>:lag:<field>。
func lagTrunkKey(trunkID int, field string) string {
	return fmt.Sprintf("interface:Eth-Trunk%d:lag:%s", trunkID, field)
}

// lagMemberKey 拼接成员口级键：interface:<member>:<field>。
func lagMemberKey(iface string, field string) string {
	return fmt.Sprintf("interface:%s:%s", iface, field)
}

// lagSysKey 拼接系统级键：lacp:<field>。
func lagSysKey(field string) string {
	return "lacp:" + field
}

// —— 接口名工具（纯函数） ——

// trunkFamilyPrefixes 是聚合口族（逻辑口）的接口名前缀集合。
//
// ⚠️ "ET" 是 Eth-Trunk 的合法缩写，但 "Ethernet0/0/1" 也以 "Et" 开头。
// 因此匹配时**必须要求前缀之后紧跟数字**，否则会把所有 Ethernet 物理口误判为聚合口，
// 进而在 parser.go 的通用 interface 命令里跳过 :status="Up" 初始化，造成大范围回归。
var trunkFamilyPrefixes = []string{"Eth-Trunk", "Bridge-Aggregation", "BAGG", "ET"}

// isTrunkFamilyInterface 判定接口名是否属于聚合口族（Eth-Trunk / ET / Bridge-Aggregation / BAGG）。
// 大小写不敏感；要求前缀后紧跟数字（见 trunkFamilyPrefixes 注释的 Ethernet 误判风险）。纯函数。
func isTrunkFamilyInterface(name string) bool {
	_, ok := lagTrunkIDFromName(name)
	return ok
}

// lagTrunkIDFromName 从聚合口名解析 trunk id（如 "Eth-Trunk10" → 10）。
// 非聚合口族返回 (0,false)。大小写不敏感。纯函数。
func lagTrunkIDFromName(name string) (int, bool) {
	l := strings.ToLower(strings.TrimSpace(name))
	if l == "" {
		return 0, false
	}
	for _, p := range trunkFamilyPrefixes {
		lp := strings.ToLower(p)
		if !strings.HasPrefix(l, lp) {
			continue
		}
		rest := strings.TrimSpace(l[len(lp):])
		if rest == "" {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// lagPortTypeToken 返回接口名去掉尾部「数字/斜杠」段后的类型串（小写）。
// 例："GigabitEthernet0/0/1" → "gigabitethernet"；"10GE1/0/1" → "10ge"。纯函数。
func lagPortTypeToken(name string) string {
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
	return strings.ToLower(s[:i])
}

// parsePortIndex 把接口名尾部的 a/b/c 段解析为整数切片（拍板 #1 因子④）。
//
// 例："GE0/0/2"→[0,0,2]；"GigabitEthernet0/0/10"→[0,0,10]；"10GE1/0/1"→[1,0,1]。
// 从字符串末尾向前取「数字与斜杠」的最长连续段，避免把类型串里的数字（如 10GE 的 10）误吞。
// 非数字段按 0 处理。纯函数。
func parsePortIndex(name string) []int {
	s := strings.TrimSpace(name)
	if s == "" {
		return nil
	}
	i := len(s)
	for i > 0 {
		c := s[i-1]
		if (c >= '0' && c <= '9') || c == '/' {
			i--
			continue
		}
		break
	}
	tail := strings.Trim(s[i:], "/")
	if tail == "" {
		return nil
	}
	segs := strings.Split(tail, "/")
	out := make([]int, 0, len(segs))
	for _, seg := range segs {
		n, err := strconv.Atoi(seg)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// comparePortIndex 按段逐级数值比较（纯函数）。
//
// 返回 >0 表示 a 数字序更小（a 胜）、<0 表示 b 更小、0 表示相等；长度不同则短者优先。
// ⚠️ 保证 GE0/0/2([0,0,2]) < GE0/0/10([0,0,10])（段内**数值**比较，非字符串比较）。
func comparePortIndex(a, b []int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] < b[i] {
			return 1
		}
		return -1
	}
	if len(a) == len(b) {
		return 0
	}
	if len(a) < len(b) {
		return 1
	}
	return -1
}

// compareLAGMemberName 是**全链路唯一排序口径**（设计 §1.6 / §8 #5）：
// 先按接口类型串字典序（不同类型），再按 parsePortIndex 自然序（同类型），
// 最后按接口名全串兜底，保证全序与确定性（AC5 连续 10 次字节级一致的前提）。
//
// 返回 >0 表示 a 排在前（a 胜）、<0 表示 b 排在前、0 表示完全相同。
func compareLAGMemberName(a, b string) int {
	ta, tb := lagPortTypeToken(a), lagPortTypeToken(b)
	if ta != tb {
		if ta < tb {
			return 1
		}
		return -1
	}
	if c := comparePortIndex(parsePortIndex(a), parsePortIndex(b)); c != 0 {
		return c
	}
	if a == b {
		return 0
	}
	if a < b {
		return 1
	}
	return -1
}

// lagPortMediaType 由接口名派生 VRP `PortType` 列展示值（真值列，非占位）。
// 未知介质类型返回 lagPlaceholder（诚实占位，绝不编造 1GE）。纯函数。
func lagPortMediaType(name string) string {
	switch lagPortTypeToken(name) {
	case "gigabitethernet", "ge":
		return "1GE"
	case "xgigabitethernet", "10ge":
		return "10GE"
	case "25ge":
		return "25GE"
	case "40ge", "xgigabitethernet40":
		return "40GE"
	case "100ge":
		return "100GE"
	case "ethernet", "eth":
		return "100M"
	case "fastethernet", "fe":
		return "100M"
	}
	return lagPlaceholder
}

// lagPortNo 返回 VRP `PortNo` 列展示值（取接口号末段，真值列）。
// 无法解析时返回 0。纯函数。
func lagPortNo(name string) int {
	idx := parsePortIndex(name)
	if len(idx) == 0 {
		return 0
	}
	return idx[len(idx)-1]
}

// lagPortKey 返回 VRP `PortKey` 列展示值（聚合键，由 trunk id 派生，真值列）。
// 真机 PortKey 由「聚合组号 + 端口速率/双工」编码而成；本工具无速率协商，
// 故取 trunk id 作为确定性聚合键（与设计 §4.3 golden 样例一致）。纯函数。
func lagPortKey(trunkID int) int {
	return trunkID
}

// —— 配置读取 helper（只读，缺省值在读取时合并，设计 §3.1） ——

// lagCfgString 读取字符串键，键缺失/空串时返回 def。纯函数。
func lagCfgString(state *CLIState, key string, def string) string {
	if state == nil {
		return def
	}
	if v := strings.TrimSpace(state.DeviceConfig[key]); v != "" {
		return v
	}
	return def
}

// lagCfgInt 读取整型键，键缺失/非法时返回 def。纯函数。
func lagCfgInt(state *CLIState, key string, def int) int {
	if state == nil {
		return def
	}
	v := strings.TrimSpace(state.DeviceConfig[key])
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// lagSysLACPPriority 返回系统 LACP 优先级（lacp:priority，缺省 32768）。纯函数（选举因子①）。
func lagSysLACPPriority(state *CLIState) int {
	return lagCfgInt(state, lagSysKey("priority"), DefaultLACPSysPri)
}

// lagBridgeTrunkKey 拼接 H3C 聚合口级键：interface:Bridge-Aggregation<id>:lag:<field>。
// 仅用于兼容「用户经 H3C 路径 interface Bridge-Aggregation <id> 建组」的存在判据，
// 聚合口级配置事实源统一收敛到 lagTrunkKey（设计 §3 单一键命名空间）。
func lagBridgeTrunkKey(trunkID int, field string) string {
	return fmt.Sprintf("interface:Bridge-Aggregation%d:lag:%s", trunkID, field)
}

// —— 聚合组收集与存在判据 ——

// collectLAGTrunks 返回已配置 trunk id 升序列表（纯函数，只读 DeviceConfig）。
//
// 存在判据（设计 §1.3）：
//   - interface:Eth-Trunk<id>:lag:exists == "true"（显式 interface Eth-Trunk <id> 创建），或
//   - interface:Bridge-Aggregation<id>:lag:exists == "true"（H3C 路径创建），或
//   - 存在成员口 interface:<m>:eth-trunk == <id>（兼容仅经 eth-trunk <id> 隐式建组）。
//
// ⚠️ 绝不基于任何「重映射/推导」产出组号（幽灵 Bridge-Aggregation 根因，拍板 #4）。
func collectLAGTrunks(state *CLIState) []int {
	if state == nil || len(state.DeviceConfig) == 0 {
		return nil
	}
	set := make(map[int]bool, 4)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") {
			continue
		}
		switch {
		case strings.HasSuffix(k, ":lag:exists"):
			if !strings.EqualFold(strings.TrimSpace(v), "true") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(k, "interface:"), ":lag:exists")
			if id, ok := lagTrunkIDFromName(name); ok {
				set[id] = true
			}
		case strings.HasSuffix(k, ":eth-trunk"):
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				continue
			}
			if n < lagTrunkIDMin || n > lagTrunkIDMax {
				continue
			}
			set[n] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// lagTrunkExists 判定指定 trunk 是否已存在（纯函数，判据同 collectLAGTrunks §1.3）。
func lagTrunkExists(state *CLIState, trunkID int) bool {
	if state == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[lagTrunkKey(trunkID, "exists")]), "true") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[lagBridgeTrunkKey(trunkID, "exists")]), "true") {
		return true
	}
	return len(collectLAGMembers(state, trunkID)) > 0
}

// lagMemberTrunkID 返回接口当前归属的 trunk id（唯一事实源 interface:<m>:eth-trunk）。
// 未归属任何聚合组返回 (0,false)。纯函数。
func lagMemberTrunkID(state *CLIState, iface string) (int, bool) {
	if state == nil || strings.TrimSpace(iface) == "" {
		return 0, false
	}
	v := strings.TrimSpace(state.DeviceConfig[lagMemberKey(iface, "eth-trunk")])
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// collectLAGMemberNames 返回归属 trunkID 的成员接口名，按 compareLAGMemberName 自然序升序。
// 成员唯一事实源 = interface:<m>:eth-trunk（设计 §1.2，已废弃 :members 逗号串双写）。纯函数。
func collectLAGMemberNames(state *CLIState, trunkID int) []string {
	if state == nil || len(state.DeviceConfig) == 0 {
		return nil
	}
	names := make([]string, 0, lagMaxMembers)
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") || !strings.HasSuffix(k, ":eth-trunk") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n != trunkID {
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

// collectLAGMembers 返回归属 trunkID 的成员列表，按接口号自然序升序（纯函数）。
//
// 每成员填 Name / TrunkID / AggFamily / PhyDown（复用 isPortDown 唯一判定源）/
// PortLACPPri（:lacp:priority，缺省 32768）/ PortIndex / SysLACPPri / SysMAC / Status。
// Selected / Role 由 EvaluateLAG 在模式判定后回填（本函数不做选举）。
func collectLAGMembers(state *CLIState, trunkID int) []LAGMember {
	names := collectLAGMemberNames(state, trunkID)
	if len(names) == 0 {
		return nil
	}
	sysPri := lagSysLACPPriority(state)
	sysMAC := stpDeviceMAC(state)
	out := make([]LAGMember, 0, len(names))
	for _, name := range names {
		down := isPortDown(state, name)
		m := LAGMember{
			Name:        name,
			TrunkID:     trunkID,
			AggFamily:   lagCfgString(state, lagMemberKey(name, "agg-family"), aggFamilyHuawei),
			PhyDown:     down,
			PortLACPPri: lagCfgInt(state, lagMemberKey(name, "lacp:priority"), DefaultLACPPortPri),
			PortIndex:   parsePortIndex(name),
			SysLACPPri:  sysPri,
			SysMAC:      sysMAC,
		}
		if down {
			m.Status = "Down"
		} else {
			m.Status = "Up"
		}
		out = append(out, m)
	}
	return out
}

// lagTrunkAggFamily 返回聚合组归属族（"huawei" | "h3c"，缺省 huawei）。
//
// 归类依据（裁定 #7，拍板 #4 实现前提）：优先看成员 interface:<m>:agg-family；
// 无成员时看是否经 H3C 路径 interface Bridge-Aggregation <id> 建组。
// **绝不由 Eth-Trunk 键重映射编造 Bridge-Aggregation 组**。纯函数。
func lagTrunkAggFamily(state *CLIState, trunkID int) string {
	if state == nil {
		return aggFamilyHuawei
	}
	for _, name := range collectLAGMemberNames(state, trunkID) {
		fam := strings.ToLower(lagCfgString(state, lagMemberKey(name, "agg-family"), aggFamilyHuawei))
		if fam == aggFamilyH3C {
			return aggFamilyH3C
		}
		return aggFamilyHuawei
	}
	if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[lagBridgeTrunkKey(trunkID, "exists")]), "true") &&
		!strings.EqualFold(strings.TrimSpace(state.DeviceConfig[lagTrunkKey(trunkID, "exists")]), "true") {
		return aggFamilyH3C
	}
	return aggFamilyHuawei
}

// lagDisplayTrunkName 返回该组在 display 中应使用的聚合口名（按 agg-family 归类）。纯函数。
func lagDisplayTrunkName(state *CLIState, trunkID int) string {
	if lagTrunkAggFamily(state, trunkID) == aggFamilyH3C {
		return lagBridgeAggName(trunkID)
	}
	return lagTrunkName(trunkID)
}

// —— LACP 选举（纯函数，拍板 #1/#2） ——

// CompareLACPPort 比较两成员决定选举胜负（纯函数，四级全「数值/字典序小者优先」）。
//
// 返回语义（与 stp_eval.go:260 CompareBridgeID 同基准）：
//
//	>0 → a 胜（a 排在 b 之前 / a 优先成为 Selected）
//	<0 → b 胜
//	 0 → 完全相等（端口名唯一，正常不应发生）
//
// 逐级短路（任一级分出胜负即返回）：
//
//	① 系统 LACP 优先级 SysLACPPri 小者胜（同设备成员恒相等，占位级）
//	② 系统 MAC normalizeMACHex 后字典序小者胜（同设备成员恒相等，占位级）
//	③ 端口 LACP 优先级 PortLACPPri 小者胜                    ← 主区分因子
//	④ 端口号 PortIndex 自然序小者胜（comparePortIndex 数值比较，保证 GE0/0/2 < GE0/0/10）
//	⑤ 接口名字典序小者胜（确定性 tie-break，保证全序）
func CompareLACPPort(a, b LAGMember) int {
	// ① 系统 LACP 优先级（小者胜）
	if a.SysLACPPri != b.SysLACPPri {
		if a.SysLACPPri < b.SysLACPPri {
			return 1
		}
		return -1
	}
	// ② 系统 MAC（归一化后字典序小者胜）
	ma, mb := normalizeMACHex(a.SysMAC), normalizeMACHex(b.SysMAC)
	if ma != mb {
		if ma < mb {
			return 1
		}
		return -1
	}
	// ③ 端口 LACP 优先级（小者胜）—— 主区分因子
	if a.PortLACPPri != b.PortLACPPri {
		if a.PortLACPPri < b.PortLACPPri {
			return 1
		}
		return -1
	}
	// ④ 端口号自然序（数值小者胜，非字符串序）
	if c := comparePortIndex(a.PortIndex, b.PortIndex); c != 0 {
		return c
	}
	// ⑤ 接口名兜底（保证全序与确定性）
	if a.Name == b.Name {
		return 0
	}
	if a.Name < b.Name {
		return 1
	}
	return -1
}

// SelectLACPActivePorts 在 lacp-static 模式下选举活动口（纯函数，拍板 #1/#2）。
//
// 流程：滤掉物理 down 的成员 → 按 CompareLACPPort 全序降序（胜者在前）稳定排序 →
// 取前 min(len, maxActive) 个标记为 Selected 返回。
// maxActive ≤ 0 时按缺省 8 处理；成员不足 maxActive 时全部 Selected。
// 返回切片为输入的**拷贝**，不修改入参（纯函数契约）。
func SelectLACPActivePorts(members []LAGMember, maxActive int) []LAGMember {
	if len(members) == 0 {
		return nil
	}
	if maxActive <= 0 {
		maxActive = DefaultMaxActiveLink
	}
	if maxActive > lagMaxMembers {
		maxActive = lagMaxMembers
	}
	up := make([]LAGMember, 0, len(members))
	for _, m := range members {
		if m.PhyDown {
			continue
		}
		up = append(up, m)
	}
	if len(up) == 0 {
		return nil
	}
	sort.SliceStable(up, func(i, j int) bool {
		return CompareLACPPort(up[i], up[j]) > 0
	})
	if len(up) > maxActive {
		up = up[:maxActive]
	}
	out := make([]LAGMember, 0, len(up))
	for _, m := range up {
		m.Selected = true
		m.Role = lagRoleSelected
		out = append(out, m)
	}
	return out
}

// —— 主评估器 ——

// EvaluateLAG 给定 trunk id，返回完整评估结果（纯函数，只读 DeviceConfig / Interfaces）。
//
// 流程（设计 §4）：
//  1. 判存在（§1.3）；
//  2. 读 mode / load-balance / least / max / preempt / timeout（缺省值读取时合并，§3.1）；
//  3. collectLAGMembers（自然序）；
//  4. 按 mode 算 ActiveMembers：
//     manual load-balance → 所有物理 up 的成员（无选举）；
//     lacp-static         → SelectLACPActivePorts（受 max active-linknumber 约束）；
//  5. 算 OperateStatus（拍板 #2）：活动口数 ≥ least → "up"，否则 "down"；
//  6. 填 HashArithmetic / LocalBlock / PartnerBlock 诚实占位 / SimNote。
//
// **不做任何写操作**（不写 DeviceConfig、不碰 sim 引擎实例、不 import protocol）。
func EvaluateLAG(state *CLIState, trunkID int) LAGResult {
	res := LAGResult{
		TrunkID:       trunkID,
		Mode:          DefaultLAGMode,
		LoadBalance:   DefaultLoadBalance,
		LeastLink:     DefaultLeastLink,
		MaxActiveLink: DefaultMaxActiveLink,
		Preempt:       DefaultPreempt,
		PreemptDelay:  DefaultPreemptDelay,
		LACPTimeout:   DefaultLACPTimeout,
		SysPriority:   DefaultLACPSysPri,
		AggFamily:     aggFamilyHuawei,
		OperateStatus: "down",
		PartnerBlock:  lagPartnerPlaceholder,
		SimNote:       lagSimNote(),
	}
	res.HashArithmetic = hashArithmetic(res.LoadBalance)
	if state == nil {
		return res
	}

	// 1) 存在判据
	res.Exists = lagTrunkExists(state, trunkID)
	res.AggFamily = lagTrunkAggFamily(state, trunkID)

	// 2) 聚合口级配置（缺省值合并）
	res.Mode = LAGMode(lagCfgString(state, lagTrunkKey(trunkID, "mode"), string(DefaultLAGMode)))
	res.LoadBalance = lagCfgString(state, lagTrunkKey(trunkID, "load-balance"), DefaultLoadBalance)
	res.HashArithmetic = hashArithmetic(res.LoadBalance)
	res.LeastLink = lagCfgInt(state, lagTrunkKey(trunkID, "least-active-linknumber"), DefaultLeastLink)
	res.MaxActiveLink = lagCfgInt(state, lagTrunkKey(trunkID, "max-active-linknumber"), DefaultMaxActiveLink)

	// 系统级作为兜底、聚合口级优先（preempt / preempt-delay / lacp-timeout）
	res.Preempt = lagCfgString(state, lagTrunkKey(trunkID, "preempt"),
		lagCfgString(state, lagSysKey("preempt"), DefaultPreempt))
	res.PreemptDelay = lagCfgInt(state, lagTrunkKey(trunkID, "preempt-delay"),
		lagCfgInt(state, lagSysKey("preempt-delay"), DefaultPreemptDelay))
	res.LACPTimeout = lagCfgString(state, lagTrunkKey(trunkID, "lacp-timeout"),
		lagCfgString(state, lagSysKey("timeout"), DefaultLACPTimeout))
	res.SysPriority = lagSysLACPPriority(state)
	res.SysMAC = stpDeviceMAC(state)

	// 3) 成员（自然序）
	res.Members = collectLAGMembers(state, trunkID)

	// 4) 活动口
	switch res.Mode {
	case LAGModeLACP:
		res.ActiveMembers = SelectLACPActivePorts(res.Members, res.MaxActiveLink)
	default:
		// 手工负载分担：所有物理 up 的成员均为活动口（无选举，拍板 #2）
		for _, m := range res.Members {
			if m.PhyDown {
				continue
			}
			res.ActiveMembers = append(res.ActiveMembers, m)
		}
	}

	// 回填成员角色（仅 LACP 模式有 Selected/Unselect 列）
	active := make(map[string]bool, len(res.ActiveMembers))
	for _, m := range res.ActiveMembers {
		active[m.Name] = true
	}
	for i := range res.Members {
		if res.Mode != LAGModeLACP {
			res.Members[i].Selected = !res.Members[i].PhyDown
			res.Members[i].Role = ""
			continue
		}
		if active[res.Members[i].Name] {
			res.Members[i].Selected = true
			res.Members[i].Role = lagRoleSelected
			continue
		}
		res.Members[i].Selected = false
		res.Members[i].Role = lagRoleUnselect
	}

	// 5) 运行状态（实时派生，绝不落键、绝不硬编码 Up）
	res.UpPortCount = len(res.ActiveMembers)
	least := res.LeastLink
	if least < lagLinkNumberMin {
		least = DefaultLeastLink
	}
	if res.Exists && res.UpPortCount >= least && res.UpPortCount > 0 {
		res.OperateStatus = "up"
	} else {
		res.OperateStatus = "down"
	}

	// 6) 渲染块
	res.LocalBlock = res.Members
	res.PartnerBlock = lagPartnerPlaceholder
	res.SimNote = lagSimNote()
	return res
}

// —— 诚实占位注记与展示串映射 ——

// lagSimNote 返回 LACP「诚实占位」注记（lite/full 两态，口径同 stpSimNote / vrrpSimNote）。
//
//	lite → "（LACP 为本地视图选举，对端未接入 LACPDU 交互，以下按本地视图选举）"
//	full → "（LACP 选举为本地视图模拟，非真实对端协商）"
func lagSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（LACP 为本地视图选举，对端未接入 LACPDU 交互，以下按本地视图选举）"
	}
	return "（LACP 选举为本地视图模拟，非真实对端协商）"
}

// hashArithmetic 把 load-balance 取值映射为 display 的 `Hash arithmetic` 展示串
// （裁定 #4：仅配置态 + 展示串，**不做哈希数据面模拟**）。纯查表函数。
func hashArithmetic(lb string) string {
	switch strings.ToLower(strings.TrimSpace(lb)) {
	case "src-mac":
		return "SMAC 源 MAC"
	case "dst-mac":
		return "DMAC 目的 MAC"
	case "src-dst-mac":
		return "SMAC 源 MAC 与目的 MAC"
	case "src-ip":
		return "SA 源 IP"
	case "dst-ip":
		return "DA 目的 IP"
	case "src-dst-ip":
		return "SA 源 IP 与目的 IP"
	}
	return lagPlaceholder
}

// lagWorkingModeName 返回 display 的 `WorkingMode` 列展示值（Normal = 手工负载分担）。纯函数。
func lagWorkingModeName(mode LAGMode) string {
	if mode == LAGModeLACP {
		return "LACP"
	}
	return "Normal"
}

// —— 校验纯函数（供 apply* 拒错，拍板 #5/#6/#9；错误文案见 PRD §4 / AC7） ——

// LAG 错误文案常量（VRP 风格，parser.go 的 apply* 与测试共用，避免散落硬编码）。
const (
	errLAGInvalidTrunkID   = "Error: invalid Eth-Trunk ID (0-63)"
	errLAGUnrecognized     = "Error: unrecognized command found at '^' position"
	errLAGNestedTrunk      = "Error: An Eth-Trunk interface cannot be a member of another Eth-Trunk"
	errLAGMemberOverLimit  = "Error: The number of member interfaces exceeds the upper limit (8)"
	errLAGHasMembers       = "Error: The Eth-Trunk interface has member ports, please delete them first"
	errLAGModeSwitchMember = "Error: Please delete member interfaces before changing the working mode from LACP to manual"
	errLAGInvalidRange     = "Error: invalid interface range"
	errLAGLinkNumberRange  = "Error: The active link number is not in the range 1 to 8"
	errLAGLeastGreaterMax  = "Error: The least active-linknumber cannot be greater than the max active-linknumber"
	errLAGLACPPriRange     = "Error: The LACP priority is not in the range 0 to 65535"
	errLAGPreemptDelay     = "Error: The preempt delay time is not in the range 0 to 180"
)

// errLAGTrunkNotExist 组装「目标 Eth-Trunk 未创建」错误（P0-9 校验 ①）。
func errLAGTrunkNotExist(trunkID int) string {
	return fmt.Sprintf("Error: Eth-Trunk %d does not exist", trunkID)
}

// errLAGMemberJoined 组装「接口已加入其他 Eth-Trunk」错误（P0-9 校验 ②）。
func errLAGMemberJoined(trunkID int) string {
	return fmt.Sprintf("Error: The interface has been added to Eth-Trunk %d", trunkID)
}

// errLAGNotSupported 组装能力矩阵/分支守卫拒绝文案（拍板 #4，§1.7）。
func errLAGNotSupported(deviceType string) string {
	return fmt.Sprintf("Error: Eth-Trunk is not supported on %s", deviceType)
}

// validTrunkID 校验 Eth-Trunk ID ∈ [0,63]（纯函数）。越界返回 (false, VRP 风格错误)。
func validTrunkID(id int) (bool, string) {
	if id < lagTrunkIDMin || id > lagTrunkIDMax {
		return false, errLAGInvalidTrunkID
	}
	return true, ""
}

// lagLoadBalanceValues 是 load-balance 的六值合法枚举（设计 §3.1）。
var lagLoadBalanceValues = []string{"dst-ip", "dst-mac", "src-ip", "src-mac", "src-dst-ip", "src-dst-mac"}

// validLoadBalance 校验 load-balance 六值枚举（纯函数）。非法返回 (false, VRP 风格错误)。
func validLoadBalance(lb string) (bool, string) {
	l := strings.ToLower(strings.TrimSpace(lb))
	for _, v := range lagLoadBalanceValues {
		if l == v {
			return true, ""
		}
	}
	return false, errLAGUnrecognized
}

// validLinkNumber 校验 least/max active-linknumber ∈ [1,8]（纯函数）。
func validLinkNumber(n int) (bool, string) {
	if n < lagLinkNumberMin || n > lagLinkNumberMax {
		return false, errLAGLinkNumberRange
	}
	return true, ""
}

// validLACPPriority 校验 LACP 优先级 ∈ [0,65535]（纯函数）。
func validLACPPriority(p int) (bool, string) {
	if p < lagLACPPriMin || p > lagLACPPriMax {
		return false, errLAGLACPPriRange
	}
	return true, ""
}

// validPreemptDelay 校验 LACP 抢占延时 ∈ [0,180] 秒（纯函数）。
func validPreemptDelay(d int) (bool, string) {
	if d < lagPreemptDelayMin || d > lagPreemptDelayMax {
		return false, errLAGPreemptDelay
	}
	return true, ""
}

// validLAGMode 校验并归一化 mode 取值（两 token 整体识别，P0-6）。
// 放行 "manual load-balance" / "lacp-static"；"lacp-dynamic" 本期不放行（P2-1）。
func validLAGMode(raw string) (LAGMode, bool, string) {
	l := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	switch l {
	case "manual load-balance":
		return LAGModeManual, true, ""
	case "lacp-static":
		return LAGModeLACP, true, ""
	}
	return "", false, errLAGUnrecognized
}
