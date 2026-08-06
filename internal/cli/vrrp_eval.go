// vrrp_eval.go 实现「CLIState 层 VRRP（虚拟路由冗余协议）评估器」（P2 第三项，VRRP，华为 VRP 课程 60/61）。
//
// 背景与约束见 docs/p2-vrrp-prd.md 与 docs/p2-vrrp-design.md。本评估器把代码库里
// 既有的「仅写 state.VRRP 结构化 map、不持久化、无状态机、无诚实占位」的 VRRP 残桩，
// 升级为一条可对学员实验产生可观测反馈的 L3 网关冗余链路。
//
// 架构基线（与端口安全 / NAT 完全一致，见设计 §1）：
//   - 单一事实源 = state.DeviceConfig（interface:<iface>:vrrp:<vrid>:<field> 键），
//     经既有 SerializeToDeviceConfigData / LoadFromDeviceConfigData 自动往返持久化，
//     从根上修复残桩 save/reload 丢配置缺陷。
//   - VRRPGroup / VRRPResult 仅为「从 DeviceConfig 即时派生的只读视图」，不缓存、不重复，
//     彻底消除双写不一致风险（state.VRRP 字段已移除）。
//   - 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎、不 import protocol 包），
//     可单测、可回归，与 acl_eval.go 的 EvaluatePathACL / applyNAT、
//     portsec_eval.go 的 EvaluatePortSecurity 同一契约（返回新值，调用方应用）。
//   - 任何新代码不得新建对 sim 引擎实例的调用；本文件仅读 sim.EngineModeName()
//     以决定诚实占位注记的 lite/full 两态（与 aclSimNote/natSimNote/portSecSimNote 同口径）。
//
// 诚实边界（主理人拍板，见设计 §0 / §8）：
//   - 真实 Master/Backup 选举依赖设备间 VRRP 通告报文（心跳），当前 sim 引擎无真实 HA 心跳，
//     故本期为「本地静态假设选举」+ 诚实注记；绝不臆造 Backup（Backup 留待 P2 跨设备选举）。
//   - virtual-ip 同网段校验仅借接口 IP 掩码做静态判定，非真实 ARP 代理 / 引擎可达性验证。
//   - authentication-mode 仅配置态 + 展示（key 脱敏），不做 md5 / simple 校验算法。
//   - track 被跟踪接口 Down 状态由学员显式 shutdown 触发（interface:<iface>:status=="Down"），
//     无自动链路事件。
package cli

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
)

// VRRPGroup 是从 DeviceConfig 派生的单组配置视图（collectVRRPGroups 产出，内存只读、不缓存）。
//
// 注意：InterfaceIP 为派生辅助字段（取自 interface:<iface>:ip），供 CompareVRRPPriority
// 的「同优先级比接口 IP 大者胜」tie-break 使用；不参与 DeviceConfig 持久化。
type VRRPGroup struct {
	VRID           int
	Interface      string
	VirtualIP      string
	Priority       int    // 1-254，默认 100
	Preempt        bool   // 默认 true（enable）
	Advertise      int    // 秒，默认 1
	TrackInterface string // P1：被跟踪上行口（"" 表示未配）
	TrackReduced   int    // P1：降优先级值，默认 10
	AuthMode       string // P1："simple" | "md5"（"" 表示未配）
	AuthKey        string // P1：仅存储，display 脱敏，不做真实认证
	PreemptDelay   int    // P2：抢占延迟秒，默认 0（仅配置态 + 诚实注记）
	InterfaceIP    string // 派生辅助字段：本设备接口 IP（CIDR/“IP MASK” 中的 IP 部分），供 tie-break
}

// VRRPResult 是 EvaluateVRRP 的纯函数返回。
//   - Configured=false → 组未配齐（无 virtual-ip 键），Role="Initialize"。
//   - Role="Master"（拍板 #2(a) 本地静态假设）或未来 P2 跨设备 "Backup"。
//   - EffectivePriority = Priority −（P1：被跟踪接口 Down 时 TrackReduced，缺省 10）。
//   - IsOwner=true 表示 Priority==255（虚拟 IP 拥有者）。
type VRRPResult struct {
	Configured        bool
	Role              string // "Master" | "Backup" | "Initialize"
	Reason            string // 选举原因/诚实文案
	VirtualIP         string
	Priority          int
	EffectivePriority int
	Preempt           bool
	Advertise         int
	IsOwner           bool
}

// VRRP DeviceConfig 键名约定（单一事实源）。
//   interface:<iface>:vrrp:<vrid>:virtual-ip    = "<ip>"
//   interface:<iface>:vrrp:<vrid>:priority      = "<1-254>"        (缺省 100)
//   interface:<iface>:vrrp:<vrid>:preempt       = "enable"|"disable" (缺省 enable)
//   interface:<iface>:vrrp:<vrid>:advertise     = "<1-255>"        (缺省 1)
//   interface:<iface>:vrrp:<vrid>:track-iface   = "<iface>"        (P1)
//   interface:<iface>:vrrp:<vrid>:track-reduced = "<1-255>"        (P1, 缺省 10)
//   interface:<iface>:vrrp:<vrid>:auth-mode     = "simple"|"md5"   (P1)
//   interface:<iface>:vrrp:<vrid>:auth-key      = "<key>"          (P1, 仅存不显明文)
//   interface:<iface>:vrrp:<vrid>:preempt-delay = "<0-3600>"       (P2, 仅配置态)
// 组存在标记：interface:<iface>:vrrp:<vrid>:virtual-ip 存在即视为一组已配。
const (
	vrrpVRIDMin, vrrpVRIDMax                  = 1, 255
	vrrpPriMin, vrrpPriMax                    = 1, 254
	vrrpAdvMin, vrrpAdvMax                    = 1, 255
	vrrpPriDefault                            = 100
	vrrpAdvDefault                            = 1
	vrrpTrackReducedDefault                   = 10
	vrrpTrackReducedMin, vrrpTrackReducedMax  = 1, 255
	vrrpPreemptDelayMin, vrrpPreemptDelayMax  = 0, 3600
	vrrpOwnerPriority                         = 255 // 虚拟 IP 拥有者优先级（保留值）
)

// vrrpGroupRef 是 display vrrp 渲染时的 (接口, vrid) 引用（仅渲染用途的轻量结构）。
type vrrpGroupRef struct {
	iface string
	vrid  int
}

// vrrpKey 拼接 VRRP 键名：interface:<iface>:vrrp:<vrid>:<field>。
func vrrpKey(iface string, vrid int, field string) string {
	return fmt.Sprintf("interface:%s:vrrp:%d:%s", iface, vrid, field)
}

// vrrpInterfaces 返回所有「配置过 VRRP（存在 virtual-ip 键）」的接口名，按字典序去重。
// 纯函数（只读 DeviceConfig）。
func vrrpInterfaces(state *CLIState) []string {
	if state == nil {
		return nil
	}
	set := make(map[string]bool)
	prefix := "interface:"
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, ":virtual-ip") {
			continue
		}
		if !strings.Contains(k, ":vrrp:") {
			continue
		}
		mid := strings.TrimPrefix(k, prefix) // "<iface>:vrrp:<vrid>:virtual-ip"
		idx := strings.Index(mid, ":vrrp:")
		if idx > 0 {
			set[mid[:idx]] = true
		}
	}
	out := make([]string, 0, len(set))
	for iface := range set {
		out = append(out, iface)
	}
	sort.Strings(out)
	return out
}

// collectVRRPGroups 从 DeviceConfig 派生某接口下所有已配 VRRP 组（合并默认值）。
//
// 扫描 interface:<iface>:vrrp:<vrid>:virtual-ip 存在即视为一组；逐键回填字段；
// Priority 缺省 100、Preempt 缺省 true、Advertise 缺省 1、TrackReduced 缺省 10。
// 只读 DeviceConfig，无副作用；返回按 vrid 升序。
func collectVRRPGroups(state *CLIState, iface string) []VRRPGroup {
	if state == nil || iface == "" {
		return nil
	}
	prefix := fmt.Sprintf("interface:%s:vrrp:", iface)
	vridSet := make(map[int]bool)
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, ":virtual-ip") {
			continue
		}
		mid := strings.TrimPrefix(k, prefix)        // "<vrid>:virtual-ip"
		vridStr := strings.TrimSuffix(mid, ":virtual-ip") // "<vrid>"
		if n, err := strconv.Atoi(vridStr); err == nil {
			vridSet[n] = true
		}
	}
	if len(vridSet) == 0 {
		return nil
	}
	vrids := make([]int, 0, len(vridSet))
	for v := range vridSet {
		vrids = append(vrids, v)
	}
	sort.Ints(vrids)
	groups := make([]VRRPGroup, 0, len(vrids))
	for _, vrid := range vrids {
		groups = append(groups, vrrpGroupFromDeviceConfig(state, iface, vrid))
	}
	return groups
}

// collectVRRPGroup 返回单个 (iface, vrid) 组的派生视图；不存在（无 virtual-ip）时 ok=false。
// 纯函数（只读 DeviceConfig）。
func collectVRRPGroup(state *CLIState, iface string, vrid int) (VRRPGroup, bool) {
	if state == nil {
		return VRRPGroup{}, false
	}
	if state.DeviceConfig[vrrpKey(iface, vrid, "virtual-ip")] == "" {
		return VRRPGroup{}, false
	}
	return vrrpGroupFromDeviceConfig(state, iface, vrid), true
}

// vrrpGroupFromDeviceConfig 内部：把单组 DeviceConfig 键回填为 VRRPGroup（合并默认值）。
func vrrpGroupFromDeviceConfig(state *CLIState, iface string, vrid int) VRRPGroup {
	g := VRRPGroup{
		VRID:         vrid,
		Interface:    iface,
		Priority:     vrrpPriDefault,
		Preempt:      true,
		Advertise:    vrrpAdvDefault,
		TrackReduced: vrrpTrackReducedDefault,
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "virtual-ip")]; v != "" {
		g.VirtualIP = v
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "priority")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			g.Priority = n
		}
	}
	if state.DeviceConfig[vrrpKey(iface, vrid, "preempt")] == "disable" {
		g.Preempt = false
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "advertise")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			g.Advertise = n
		}
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "track-iface")]; v != "" {
		g.TrackInterface = v
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "track-reduced")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			g.TrackReduced = n
		}
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "auth-mode")]; v != "" {
		g.AuthMode = v
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "auth-key")]; v != "" {
		g.AuthKey = v
	}
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "preempt-delay")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			g.PreemptDelay = n
		}
	}
	// 派生 InterfaceIP（取自 interface:<iface>:ip = "<IP> <MASK>"）。
	g.InterfaceIP = vrrpInterfaceIPOnly(state, iface)
	return g
}

// vrrpInterfaceIPOnly 返回接口 IP（忽略掩码部分）。纯函数（只读 DeviceConfig）。
func vrrpInterfaceIPOnly(state *CLIState, iface string) string {
	raw := state.DeviceConfig[fmt.Sprintf("interface:%s:ip", iface)]
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// EvaluateVRRP 本地静态主备选举纯函数（无副作用、不写引擎、不 import protocol、可单测）。
//
// 规则（拍板 #2(a) + 设计 §1.5）：
//  1) vrid 在 iface 下无 virtual-ip 键（未配齐）→ {Configured:false, Role:"Initialize", Reason:"VRRP group not configured"}。
//  2) Priority==255（虚拟 IP 拥有者）→ {Role:"Master", Reason:"Virtual IP owner (priority 255)", IsOwner:true}。
//  3) 否则（拍板 #2(a)）→ {Role:"Master", Reason:"Local static assumption (highest priority); not a cross-device advertisement"}。
//
// EffectivePriority = Priority −（P1：TrackInterface 非空且 isInterfaceDown(TrackInterface) 时 TrackReduced，缺省 10）。
//
// 不修改任何 state 字段、不写 DeviceConfig、不 import sim 引擎实例；仅 vrrpSimNote 读
// sim.EngineModeName()。本期任何情况下对「已配组」恒返回 Master（绝不臆造 Backup，O2）。
func EvaluateVRRP(state *CLIState, iface string, vrid int) VRRPResult {
	res := VRRPResult{
		Configured: false,
		Role:       "Initialize",
		Reason:     "VRRP group not configured",
	}
	if state == nil {
		return res
	}
	vip := state.DeviceConfig[vrrpKey(iface, vrid, "virtual-ip")]
	if vip == "" {
		return res
	}
	res.Configured = true
	res.VirtualIP = vip

	pri := vrrpPriDefault
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "priority")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pri = n
		}
	}
	res.Priority = pri

	res.Preempt = true
	if state.DeviceConfig[vrrpKey(iface, vrid, "preempt")] == "disable" {
		res.Preempt = false
	}

	adv := vrrpAdvDefault
	if v := state.DeviceConfig[vrrpKey(iface, vrid, "advertise")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			adv = n
		}
	}
	res.Advertise = adv

	// 有效优先级：被跟踪接口 Down 时减去 TrackReduced（缺省 10）。
	eff := pri
	if trackIface := state.DeviceConfig[vrrpKey(iface, vrid, "track-iface")]; trackIface != "" {
		if isInterfaceDown(state, trackIface) {
			reduced := vrrpTrackReducedDefault
			if v := state.DeviceConfig[vrrpKey(iface, vrid, "track-reduced")]; v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					reduced = n
				}
			}
			eff = pri - reduced
			if eff < 1 {
				eff = 1
			}
		}
	}
	res.EffectivePriority = eff

	// 角色判定（本地静态假设，恒 Master / Initialize）。
	if pri == vrrpOwnerPriority {
		res.Role = "Master"
		res.Reason = "Virtual IP owner (priority 255)"
		res.IsOwner = true
		return res
	}
	res.Role = "Master"
	res.Reason = "Local static assumption (highest priority); not a cross-device advertisement"
	return res
}

// CompareVRRPPriority 比较两组优先级决定胜负（纯函数，无副作用；AC4 / P2 跨设备预留）。
//
// 规则：
//   - Priority==255（虚拟 IP 拥有者）直接胜；
//   - 否则高 Priority 胜；
//   - 同 Priority 比接口 IP 大者胜（a.InterfaceIP / b.InterfaceIP，取自 DeviceConfig
//     interface:<iface>:ip，见 VRRPGroup.InterfaceIP 派生字段）；
//
// 返回 >0 表示 a 胜、<0 表示 b 胜、0 表示完全相等（确定性 tie-break）。
func CompareVRRPPriority(a, b VRRPGroup) int {
	aOwner := a.Priority == vrrpOwnerPriority
	bOwner := b.Priority == vrrpOwnerPriority
	if aOwner != bOwner {
		if aOwner {
			return 1
		}
		return -1
	}
	if a.Priority != b.Priority {
		if a.Priority > b.Priority {
			return 1
		}
		return -1
	}
	// 同优先级：比接口 IP（数值大者胜）。
	return compareIPString(a.InterfaceIP, b.InterfaceIP)
}

// compareIPString 比较两个 IPv4/v6 字符串的数值大小：aIP>bIP → 1；aIP<bIP → -1；相等或无效 → 0。
func compareIPString(aIP, bIP string) int {
	a := net.ParseIP(strings.TrimSpace(aIP))
	b := net.ParseIP(strings.TrimSpace(bIP))
	if a == nil || b == nil {
		// 无效 IP 退化为字符串字典序比较，保证确定性。
		if aIP == bIP {
			return 0
		}
		if aIP > bIP {
			return 1
		}
		return -1
	}
	a4 := a.To4()
	b4 := b.To4()
	if a4 != nil && b4 != nil {
		for i := 0; i < 4; i++ {
			if a4[i] != b4[i] {
				if a4[i] > b4[i] {
					return 1
				}
				return -1
			}
		}
		return 0
	}
	ab := a.To16()
	bb := b.To16()
	for i := 0; i < 16; i++ {
		if ab[i] != bb[i] {
			if ab[i] > bb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// vrrpSimNote 返回 VRRP「诚实占位」注记（lite/full 两态，口径同 aclSimNote/natSimNote/portSecSimNote）。
//   lite → "（VRRP 为模拟选举（lite 引擎），非内核级真实 VRRP 故障切换）"
//   full → "（VRRP 为模拟选举）"
func vrrpSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（VRRP 为模拟选举（lite 引擎），非内核级真实 VRRP 故障切换）"
	}
	return "（VRRP 为模拟选举）"
}

// vrrpSameSubnet 校验 virtual-ip 与接口 IP 同网段（纯函数，P0 同网段校验）。
//
// 读 DeviceConfig["interface:<iface>:ip"]（= "<IP> <MASK>"）；用 MASK 算
// network(virtualIP) 与 network(ifaceIP) 比较是否相等。接口未配 IP 视为校验失败（无参照网段）。
//
// 返回 (ok, ifaceIP, errMsg)：失败时 errMsg 形如
//   "Error: virtual-ip X is not in the same subnet as interface IP Y"
//   "Error: interface X has no IP address configured"
// 仅静态掩码校验、非真实 ARP 代理 / 引擎可达性验证（诚实边界 O7）。
// 不依赖 sim 引擎、不写任何 state。
func vrrpSameSubnet(state *CLIState, iface, virtualIP string) (ok bool, ifaceIP, errMsg string) {
	vip := net.ParseIP(virtualIP)
	if vip == nil {
		return false, "", fmt.Sprintf("Error: invalid virtual-ip %q", virtualIP)
	}
	raw := ""
	if state != nil {
		raw = state.DeviceConfig[fmt.Sprintf("interface:%s:ip", iface)]
	}
	if raw == "" {
		return false, "", fmt.Sprintf("Error: interface %s has no IP address configured", iface)
	}
	fields := strings.Fields(raw)
	ipStr := fields[0]
	maskStr := ""
	if len(fields) > 1 {
		maskStr = fields[1]
	}
	ifaceIPAddr := net.ParseIP(ipStr)
	if ifaceIPAddr == nil {
		return false, ipStr, fmt.Sprintf("Error: interface %s has invalid IP address %q", iface, ipStr)
	}
	mask := resolveMask(maskStr, ipStr)
	ifaceNet := &net.IPNet{IP: ifaceIPAddr.Mask(mask), Mask: mask}
	if ifaceNet.Contains(vip) {
		return true, ipStr, ""
	}
	return false, ipStr, fmt.Sprintf("Error: virtual-ip %s is not in the same subnet as interface IP %s", virtualIP, ipStr)
}

// resolveMask 把掩码字符串（点分十进制，或前缀长度）解析为 net.IPMask；无法解析则退化为 /32。
func resolveMask(maskStr, ipStr string) net.IPMask {
	if maskStr != "" {
		if m := net.ParseIP(maskStr); m != nil {
			if m4 := m.To4(); m4 != nil {
				return net.IPMask(m4)
			}
		}
		// 可能直接是前缀长度（如 "24"）。
		if _, n, err := net.ParseCIDR(fmt.Sprintf("%s/%s", ipStr, maskStr)); err == nil {
			return n.Mask
		}
	}
	return net.CIDRMask(32, 32)
}

// isInterfaceDown 读 DeviceConfig["interface:<iface>:status"]=="Down" 判定被跟踪接口是否 Down（P1 track 用）。
// 纯函数（只读 DeviceConfig）。
func isInterfaceDown(state *CLIState, iface string) bool {
	if state == nil {
		return false
	}
	return state.DeviceConfig[fmt.Sprintf("interface:%s:status", iface)] == "Down"
}
