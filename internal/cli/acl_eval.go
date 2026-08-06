// acl_eval.go 实现「CLIState 层 ACL 评估器」（P1-C，路线 B）。
//
// 背景与约束见 docs/p1c-firewall-design.md。本评估器只读
// cli.CLIState.ACLs 与 cli.CLIState.DeviceConfig["traffic-filter:<dir>:<acl>"]
// 作为唯一事实源，在 ping / tracert / CheckReachability 三条路径上做
// permit/deny 判定；命中 deny 如实改写为丢包/不可达（延续 Bandwidth/PCAP
// 的「诚实占位」先例，绝不合成为功）。
//
// 评估器为纯函数（无副作用、不写 state、不碰 sim 引擎），可单测、可回归。
//
// 另两套 ACL 模型（旁路/废弃）：
//   - protocol.Firewall（firewall.go:362 的 HandlePacket 空桩）
//   - protocol.ProtocolSimulator.MatchACL（protocol.go:526）
// 本期由本评估器作为 ACL 判定的唯一消费方（见设计 §7 约定 #6 与 §9 拍板 #7）。
// 任何新代码不得新建对它们的调用；本文件不 import protocol 包。
package cli

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// Direction 表示 ACL 评估方向，对齐 DeviceConfig["traffic-filter:<dir>:<acl>"] 的 dir 取值。
type Direction string

const (
	// DirInbound 作用于「报文进入该设备」（traffic-filter inbound）。
	DirInbound Direction = "inbound"
	// DirOutbound 作用于「报文离开该设备」（traffic-filter outbound）。
	DirOutbound Direction = "outbound"
)

// PacketTuple 描述被评估的流五元组。
// 基础层只用 SrcIP/DstIP/Proto；SrcPort/DstPort 为 advanced ACL 预留占位（本期忽略）。
type PacketTuple struct {
	SrcIP   string // 已推导出的源 IP（见 ResolveSourceIP）
	DstIP   string // 目的 IP
	Proto   string // 协议名：ip|icmp|tcp|udp（对齐 ACLRule.Protocol 取值）
	SrcPort int    // 占位，本期忽略
	DstPort int    // 占位，本期忽略
}

// Decision 是单次（单设备单方向）ACL 评估结果。
type Decision struct {
	Action    string   // "permit" | "deny"
	Matched   bool     // 是否命中某条规则
	Rule      *ACLRule // 命中的规则（未命中为 nil）
	ACLNum    string   // 命中的 ACL 编号/名称（未命中为 ""）
	DeviceID  string   // 评估的设备
	Direction Direction // 评估方向
}

// DefaultACLTerminalAction 设备「已绑定」ACL/traffic-filter 但报文未命中任何 permit 规则时的默认动作。
// 真实华为 VRP 为隐式 deny any（2026-08-05 拍板）。设备「未绑定」ACL/traffic-filter 时
// 评估器直接返回 permit，不经此常量（见 EvaluateDeviceACL / 设计 §9 拍板 #2）。
const DefaultACLTerminalAction = "deny"

// isL3ACLDeviceType 报告该设备类型是否参与 ACL 评估（router/L3Switch/firewall）。
//
// 注：EvaluatePathACL 签名为 (states, path, flow)，states 为拓扑级
// deviceID→*CLIState 注册表；能力矩阵（capabilities.go:91-94）保证只有 L3 设备
// 能配 traffic-filter，因此未绑定 ACL 的设备（含 L2）经 EvaluateDeviceACL 自然
// 返回 permit，效果等价于「仅 L3 评估、L2 跳过」（见设计 §7 约定 #8 / 拍板 #1）。
func isL3ACLDeviceType(dt topology.DeviceType) bool {
	switch dt {
	case topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceFirewall:
		return true
	}
	return false
}

// isTerminalType 报告设备是否为终端类（PC/Client/Server）。
func isTerminalType(dt topology.DeviceType) bool {
	switch dt {
	case topology.DevicePC, topology.DeviceClient, topology.DeviceServer:
		return true
	}
	return false
}

// aclBoundOnDirection 返回绑定在该方向上的 ACL 编号（DeviceConfig 的 value）。
// DeviceConfig key 形如 traffic-filter:<dir>:<acl>，value 为 ACL 编号。
// 每个设备（state）仅一份 CLIState，故按方向扫描首个匹配键即可。
func aclBoundOnDirection(state *CLIState, dir Direction) string {
	if state == nil {
		return ""
	}
	prefix := fmt.Sprintf("traffic-filter:%s:", dir)
	for k, v := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) && v != "" {
			return v
		}
	}
	return ""
}

// EvaluateDeviceACL 评估单台设备在某方向上的 traffic-filter ACL。
//
// 读取 state.ACLs 与 state.DeviceConfig["traffic-filter:<dir>:<acl>"]：
//   - 无绑定 → permit（拍板 #2，评估器直接返回，不经 DefaultACLTerminalAction）；
//   - 命中 deny → deny；
//   - 遍历完未命中任何 permit/deny 规则 → DefaultACLTerminalAction（=deny，隐式 deny any）。
//
// 无副作用（纯函数）。
func EvaluateDeviceACL(state *CLIState, deviceID string, dir Direction, flow PacketTuple) Decision {
	dec := Decision{Action: "permit", Matched: false, DeviceID: deviceID, Direction: dir}
	if state == nil {
		return dec
	}
	aclNum := aclBoundOnDirection(state, dir)
	if aclNum == "" {
		// 设备未绑定任何该方向的 ACL/traffic-filter → 放行（拍板 #2）。
		return dec
	}
	rules, ok := state.ACLs[aclNum]
	if !ok || len(rules) == 0 {
		// 已绑定但无规则 → 隐式 deny any。
		return Decision{Action: DefaultACLTerminalAction, Matched: false, ACLNum: aclNum, DeviceID: deviceID, Direction: dir}
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		if action != "permit" && action != "deny" {
			// 规则动作非法/未定义，跳过（不臆造 permit）。
			continue
		}
		if matchACLRule(rule, flow) {
			return Decision{Action: action, Matched: true, Rule: rule, ACLNum: aclNum, DeviceID: deviceID, Direction: dir}
		}
	}
	// 遍历完未命中任何 permit/deny 规则 → 隐式 deny any（拍板 #2）。
	return Decision{Action: DefaultACLTerminalAction, Matched: false, ACLNum: aclNum, DeviceID: deviceID, Direction: dir}
}

// deviceStateFor 从拓扑级 CLIState 注册表（deviceID→*CLIState）解析某设备的状态；
// 缺失（未在注册表中）返回 nil。调用方 EvaluateDeviceACL(nil, ...) 会按「未绑定」处理
// → 放行（拍板 #2），故注册表中没有状态的设备自然视为无 ACL 拦截。
func deviceStateFor(states map[string]*CLIState, deviceID string) *CLIState {
	if states != nil {
		if ds, ok := states[deviceID]; ok {
			return ds
		}
	}
	return nil
}

// EvaluatePathACL 沿「源→目的」有序设备路径逐跳评估，返回首个 deny（或全 permit）。
//
// 读取每个设备「自身」的 CLIState（states：deviceID→*CLIState 注册表）做 traffic-filter
// 评估；注册表中缺失的设备视为未绑定 ACL → 放行（见设计 §9 拍板 #2）。
//
// 方向规则：src=outbound；中转=inbound+outbound；dst=inbound。首 deny 即停
// （沿途所有设备取交集，任一 deny 即丢）。
//
// states 通常由 API 层 r.cliStateRegistry() 提供（拓扑内各设备 CLIState 的快照），
// 使途径 L3/防火墙设备「自身」配置的 traffic-filter ACL 也能被评估（修复 P1-C
// Round 1「中转设备 ACL 未生效」bug：此前仅用源设备 state 评估全部设备，导致落在
// 中转/目的设备自身 CLIState 上的 ACL 永不被读取）。CLI 单机/测试上下文若无注册表，
// 可传 nil，此时所有设备视为未绑定 → 全 permit（向后兼容）。
// EvaluatePathACL 沿「源→目的」有序设备路径逐跳评估，返回首个 deny（或全 permit）。
//
// 读取每个设备「自身」的 CLIState（states：deviceID→*CLIState 注册表）做 traffic-filter
// 与 NAT 评估；注册表中缺失的设备视为未绑定 ACL → 放行（见设计 §9 拍板 #2）。
//
// 方向规则：src=outbound；中转=inbound+outbound；dst=inbound。首 deny 即停
// （沿途所有设备取交集，任一 deny 即丢）。
//
// NAT 线程化（P2，设计 §1.4 / §4.1）：flow 在循环中为可变值，逐设备改写并向下游传递。
//   仅当该设备携带 NAT 配置（state.NAT != nil && state.NAT.Enabled）时才做地址转换；
//   无 NAT 配置的设备 applyNAT 原样返回，等价旧行为。顺序：
//   - 源(i==0)：applyNAT(outbound) 改写 SrcIP → 评估 outbound ACL（转换后）。
//   - 中转(0<i<n-1)：评估 inbound ACL（转换前）→ applyNAT(inbound) 改写 DstIP
//     → applyNAT(outbound) 改写 SrcIP → 评估 outbound ACL（转换后）。
//   - 末跳(i==n-1)：评估 inbound ACL，使用上游 NAT 设备已改写的 flow。
// 入向 ACL 在 NAT 之前（看转换前 IP），出向 ACL 在 NAT 之后（看转换后 IP）。
//
// 纯函数（无副作用、不写 state、不碰 sim 引擎）。
func EvaluatePathACL(states map[string]*CLIState, path []string, flow PacketTuple) Decision {
	permit := Decision{Action: "permit", Matched: false}
	n := len(path)
	if n == 0 {
		return permit
	}
	for i, dev := range path {
		st := deviceStateFor(states, dev)
		// 该设备是否携带 NAT 配置（仅 L3 设备能配 NAT，与能力白名单一致）。
		natEnabled := st != nil && st.NAT != nil && st.NAT.Enabled
		if i == 0 {
			// 源设备：仅 outbound（NAT 先改写，再评估转换后的 outbound ACL）。
			if natEnabled {
				flow, _ = applyNAT(st, DirOutbound, flow)
			}
			dec := EvaluateDeviceACL(st, dev, DirOutbound, flow)
			if dec.Action == "deny" {
				return dec
			}
		} else if i == n-1 {
			// 末跳：仅 inbound（flow 已是上游 NAT 改写后）。
			dec := EvaluateDeviceACL(st, dev, DirInbound, flow)
			if dec.Action == "deny" {
				return dec
			}
		} else {
			// 中转设备：inbound(转换前) → NAT(inbound+outbound) → outbound(转换后)。
			dec := EvaluateDeviceACL(st, dev, DirInbound, flow)
			if dec.Action == "deny" {
				return dec
			}
			if natEnabled {
				flow, _ = applyNAT(st, DirInbound, flow)
				flow, _ = applyNAT(st, DirOutbound, flow)
			}
			dec = EvaluateDeviceACL(st, dev, DirOutbound, flow)
			if dec.Action == "deny" {
				return dec
			}
		}
	}
	return permit
}

// aclPreCheck 对 ping 目标做「路径 + 源 IP + 方向模型」的 ACL 预判，返回首个 deny（或 permit）。
// 供 parser.executePingWithContext 在回落 CheckReachability 之前先行判定（设计 §4.1 / T03）。
// states 为拓扑级 CLIState 注册表（deviceID→*CLIState）；srcDeviceID 指定发起 ping 的设备。
func aclPreCheck(states map[string]*CLIState, srcDeviceID, targetIP string, t *topology.Topology) Decision {
	srcState := deviceStateFor(states, srcDeviceID)
	if srcState == nil {
		// 无源设备状态（退化/极端场景）→ 不做 ACL 预判，交由基础可达性判定。
		return Decision{Action: "permit", Matched: false}
	}
	path, _ := ComputeL3PathNAT(states, srcState, targetIP, t)
	srcIP := ResolveSourceIP(srcState, targetIP, t)
	flow := PacketTuple{SrcIP: srcIP, DstIP: targetIP, Proto: "icmp"}
	return EvaluatePathACL(states, path, flow)
}

// ComputeL3Path 由拓扑 BFS 计算 src→dst 的有序设备路径（含 src、dst）。
// 找不到目标设备或无拓扑时返回 nil（调用方据此退化为基础可达性判定）。
func ComputeL3Path(state *CLIState, targetIP string, t *topology.Topology) []string {
	if state == nil || t == nil || state.DeviceID == "" {
		return nil
	}
	src := state.DeviceID
	dstDev := deviceIDByIP(t, targetIP)
	if dstDev == "" {
		return nil
	}
	if src == dstDev {
		return []string{src}
	}
	// 无向 BFS 最短路。
	prev := make(map[string]string)
	visited := map[string]bool{src: true}
	q := []string{src}
	found := false
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, link := range t.Links {
			var next string
			if link.SourceDevice == cur {
				next = link.TargetDevice
			} else if link.TargetDevice == cur {
				next = link.SourceDevice
			} else {
				continue
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			if next == dstDev {
				found = true
				break
			}
			q = append(q, next)
		}
		if found {
			break
		}
	}
	if !found {
		return nil
	}
	// 回溯路径（dst → src 反转）。
	var path []string
	for d := dstDev; d != ""; d = prev[d] {
		path = append([]string{d}, path...)
		if d == src {
			break
		}
	}
	return path
}

// deviceIDByIP 在拓扑中查找拥有指定 IP 的设备（扫描其接口 IPAddress）。
func deviceIDByIP(t *topology.Topology, ip string) string {
	if t == nil {
		return ""
	}
	for id, dev := range t.Devices {
		if dev == nil || dev.Interfaces == nil {
			continue
		}
		for _, iface := range dev.Interfaces {
			if iface != nil && iface.IPAddress == ip {
				return id
			}
		}
	}
	return ""
}

// ResolveSourceIP 推导本机（state 所属设备）作为 ping/tracert 源的出接口 IP。
//
//   - 终端类（PC/Client/Server）：取 state.HostIP；
//   - L3 设备：按 state.Routes 对 dstIP 做最长前缀匹配，取命中路由出口
//     Interface 的 IP；无命中回退首个 Interfaces IP；再无则回退拓扑模型首个 IP。
//
// 该推导是 P0 简化假设（设计 §1.5 / 拍板 #6），不影响 deny 判定正确性
// （deny 通常覆盖整段网段）。
func ResolveSourceIP(state *CLIState, dstIP string, t *topology.Topology) string {
	if state == nil {
		return ""
	}
	dt := state.DeviceType
	if dt == "" && t != nil {
		if dev, ok := t.Devices[state.DeviceID]; ok {
			dt = dev.Type
		}
	}
	if isTerminalType(dt) {
		return state.HostIP
	}
	// L3 设备：最长前缀匹配出口 IP。
	if ip := longestPrefixEgressIP(state, dstIP); ip != "" {
		return ip
	}
	// 回退：首个接口 IP。
	for _, iface := range state.Interfaces {
		if iface != nil && iface.IP != "" {
			return iface.IP
		}
	}
	// 再回退：拓扑模型中该设备的首个接口 IP。
	if t != nil {
		if dev, ok := t.Devices[state.DeviceID]; ok && dev.Interfaces != nil {
			for _, iface := range dev.Interfaces {
				if iface != nil && iface.IPAddress != "" {
					return iface.IPAddress
				}
			}
		}
	}
	return ""
}

// longestPrefixEgressIP 按路由表对 dstIP 做最长前缀匹配，返回出口接口 IP；无命中返回 ""。
func longestPrefixEgressIP(state *CLIState, dstIP string) string {
	target := net.ParseIP(dstIP)
	if target == nil {
		return ""
	}
	bestLen := -1
	bestIP := ""
	for _, route := range state.Routes {
		if route == nil || route.Destination == "" {
			continue
		}
		ipNet := parseRouteDest(route.Destination, route.Mask, route.MaskLength)
		if ipNet == nil {
			continue
		}
		if ipNet.Contains(target) {
			ones, _ := ipNet.Mask.Size()
			if ones > bestLen {
				bestLen = ones
				bestIP = interfaceIPByName(state, route.Interface)
			}
		}
	}
	return bestIP
}

// parseRouteDest 把路由目的（可能带 "/" 的 CIDR，或配合 Mask/MaskLength）解析成 *net.IPNet。
func parseRouteDest(dest, mask string, maskLen int) *net.IPNet {
	if strings.Contains(dest, "/") {
		_, ipNet, err := net.ParseCIDR(dest)
		if err == nil {
			return ipNet
		}
		return nil
	}
	ip := net.ParseIP(dest)
	if ip == nil {
		return nil
	}
	ml := maskLen
	if ml <= 0 && mask != "" {
		ml = subnetToPrefix(mask)
	}
	if ml <= 0 {
		ml = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(ml, 32)}
}

// interfaceIPByName 返回指定接口名的 IP（state.Interfaces）。
func interfaceIPByName(state *CLIState, ifaceName string) string {
	if ifaceName == "" {
		return ""
	}
	if iface, ok := state.Interfaces[ifaceName]; ok && iface != nil {
		return iface.IP
	}
	return ""
}

// matchACLRule 判断单条 ACLRule 是否匹配流 flow（基础层：src/dst IP 通配符 + 协议号）。
//
//   - Protocol=="" 或不限制协议（"ip"）视为匹配任意协议；否则需 EqualFold 相等（对齐 protocol.matchRule）；
//   - SrcIP/DstIP 为空视为不限制该侧；
//   - DstPort/SourcePort 本期忽略（advanced ACL，设计 §7 约定 #1）。
func matchACLRule(rule *ACLRule, flow PacketTuple) bool {
	if rule == nil {
		return false
	}
	if rule.Protocol != "" && !strings.EqualFold(rule.Protocol, "ip") && !strings.EqualFold(rule.Protocol, flow.Proto) {
		return false
	}
	if rule.SrcIP != "" && !matchIP(flow.SrcIP, rule.SrcIP, rule.SrcWildcard) {
		return false
	}
	if rule.DstIP != "" && !matchIP(flow.DstIP, rule.DstIP, rule.DstWildcard) {
		return false
	}
	return true
}

// matchIP 判断 ip 是否落在由 ruleIP + wildcard（反掩码）描述的网段内。
// wildcard 为空时按主机路由（/32）处理（对齐 protocol.parseIPWithWildcard）。
func matchIP(ip, ruleIP, wildcard string) bool {
	if ruleIP == "" {
		return true
	}
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return false
	}
	if wildcard == "" {
		wildcard = "255.255.255.255"
	}
	maskBits := wildcardToMask(wildcard)
	ruleNet := net.ParseIP(ruleIP)
	if ruleNet == nil {
		return false
	}
	ipNet := &net.IPNet{IP: ruleNet, Mask: net.CIDRMask(maskBits, 32)}
	return ipNet.Contains(ipAddr)
}

// wildcardToMask 把通配符（反掩码）转换成掩码位数。
//
// 位级对齐 protocol.wildcardToMask（protocol.go:586-603）：每个八位的 0 位计为掩码位。
// 两处实现各自独立但行为必须等价；若 protocol.wildcardToMask 变更需同步本函数。
func wildcardToMask(wildcard string) int {
	mask := 0
	parts := strings.Split(wildcard, ".")
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			n = 0
		}
		for i := 0; i < 8; i++ {
			if (n & (1 << i)) == 0 {
				mask++
			}
		}
	}
	return mask
}

// aclSimNote 返回「诚实占位」注记。lite 引擎标注「模拟过滤，非内核级真实过滤」；
// full 引擎返回较轻量的注记（设计 §7 约定 #7）。
func aclSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（ACL 为模拟过滤（lite 引擎），非内核级真实过滤）"
	}
	return "（ACL 为模拟过滤）"
}

// natSimNote 返回 NAT「诚实占位」注记（设计 §7 约定 #5 / §8 拍板 #4）。
// lite 引擎标注「模拟转换，非内核级真实 NAT」；full 引擎返回较轻量注记。
func natSimNote() string {
	if sim.EngineModeName() == "lite" {
		return "（NAT 为模拟转换（lite 引擎），非内核级真实 NAT）"
	}
	return "（NAT 为模拟转换）"
}

// applyNAT 在「一台 NAT 设备上、某一方向」对 flow 做地址转换（纯函数，无副作用）。
//
//   - dir == DirInbound：遍历 nat server，若 flow.DstIP == 某 NATServer.GlobalIP
//     （精确匹配；协议/端口本期忽略）→ 改写 flow.DstIP = server.InsideIP。
//   - dir == DirOutbound：遍历 nat outbound，对每个 outbound 用
//     state.ACLs[outbound.ACLNum]（按 ACLNum 字符串匹配；ACL 缺失=permit any）
//     判断 flow.SrcIP 是否命中其 permit 域（仅看 SrcIP：rule.Action=="permit" 且
//     matchIP(flow.SrcIP, rule.SrcIP, rule.SrcWildcard)）；命中则改写 flow.SrcIP：
//     Type=="easy-ip" → longestPrefixEgressIP(state, flow.DstIP)（空则回退首个接口 IP）；
//     Type=="address-group" → 对应 state.NAT.AddressPools 中 ID 匹配的 StartIP（首 IP）。
//
// 返回改写后的 flow 与 translated 标记；未命中任何转换规则则原样返回、translated=false。
//
// 注意：返回 PacketTuple 而非 Decision——NAT 是改写而非拦截，下游 ACL 需拿转换后 IP
// 续判（拦截仍由 EvaluateDeviceACL 负责）。纯函数：只读 state.NAT/ACLs/Interfaces/Routes，
// 绝不写 state、不碰引擎。无 NAT 配置（state.NAT==nil 或未 Enabled）原样返回。
func applyNAT(state *CLIState, dir Direction, flow PacketTuple) (PacketTuple, bool) {
	if state == nil || state.NAT == nil || !state.NAT.Enabled {
		return flow, false
	}
	switch dir {
	case DirInbound:
		for _, server := range state.NAT.Servers {
			if server.GlobalIP != "" && server.GlobalIP == flow.DstIP {
				nf := flow
				nf.DstIP = server.InsideIP
				return nf, true
			}
		}
		return flow, false
	case DirOutbound:
		for _, ob := range state.NAT.Outbounds {
			if !natOutboundMatches(state, ob, flow) {
				continue
			}
			nf := flow
			if ob.Type == "address-group" {
				// 地址池模式：取对应 AddressPool 的 StartIP（首 IP，多 IP 取首，诚实占位）。
				if startIP := addressPoolStartIP(state, ob.AddressPool); startIP != "" {
					nf.SrcIP = startIP
					return nf, true
				}
				return flow, false
			}
			// easy-ip（默认）：取朝目标出接口的 IP；路由缺失回退首个接口 IP。
			if egressIP := longestPrefixEgressIP(state, flow.DstIP); egressIP != "" {
				nf.SrcIP = egressIP
				return nf, true
			}
			if firstIP := firstInterfaceIP(state); firstIP != "" {
				nf.SrcIP = firstIP
				return nf, true
			}
			return flow, false
		}
		return flow, false
	default:
		return flow, false
	}
}

// natOutboundMatches 判断 flow.SrcIP 是否命中某条 nat outbound 的 permit 域。
//
// 用 state.ACLs[outbound.ACLNum]（字符串键）匹配；若 ACLNum 未命中但 ACLName 非空，
// 再尝试 ACLName。ACL 不存在（缺失）视为 permit any（命中）。仅看 SrcIP，忽略 DstIP。
func natOutboundMatches(state *CLIState, ob NATOutbound, flow PacketTuple) bool {
	rules, ok := state.ACLs[strconv.Itoa(ob.ACLNum)]
	if !ok && ob.ACLName != "" {
		rules, ok = state.ACLs[ob.ACLName]
	}
	if !ok || len(rules) == 0 {
		// ACL 缺失 = permit any（本期简化）。
		return true
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(rule.Action), "permit") {
			continue
		}
		if rule.SrcIP == "" || matchIP(flow.SrcIP, rule.SrcIP, rule.SrcWildcard) {
			return true
		}
	}
	return false
}

// addressPoolStartIP 返回指定 ID 的 NAT 地址池的 StartIP（首 IP）。
func addressPoolStartIP(state *CLIState, poolID int) string {
	if state == nil || state.NAT == nil {
		return ""
	}
	for _, pool := range state.NAT.AddressPools {
		if pool.ID == poolID {
			return pool.StartIP
		}
	}
	return ""
}

// firstInterfaceIP 返回设备首个非空接口 IP（Easy IP 出接口路由缺失时的回退）。
func firstInterfaceIP(state *CLIState) string {
	if state == nil {
		return ""
	}
	for _, iface := range state.Interfaces {
		if iface != nil && iface.IP != "" {
			return iface.IP
		}
	}
	return ""
}

// ComputeL3PathNAT 是「NAT 感知」的路径解析（设计 §3.2 / §4.4）。
//
//  1. 先 deviceIDByIP(t, targetIP)：命中普通拓扑接口 IP → 直接委托 ComputeL3Path，
//     返回 (path, false)（natTranslated=false，向后兼容）。
//  2. 否则 deviceIDByNATGlobalIP(states, t, targetIP)：扫描各设备 CLIState.NAT.Servers，
//     找到 GlobalIP == targetIP 的 NAT 设备 natDev 及其 InsideIP；
//     - BFS 算到 natDev（复用同一 BFS，仅 dstDev=natDev）；
//     - insideDev = deviceIDByIP(t, server.InsideIP)（内网真实服务器）；
//     - 路径 append insideDev 作为最后一跳：[src,...,natDev, insideDev]；
//     - 返回 (path, true)（natTranslated=true）。
//
// BFS 仍是最短路径，insideDev 仅作为 NAT 设备之后的「真实终点」追加，不参与 BFS 队列
// （NAT 设备已是 BFS 终点；inside 设备与 NAT 设备拓扑相连，是 NAT 映射的真实终点）。
// 若该 NAT 设备无法从 src 经 BFS 到达（不连通），返回 (nil, true)。
//
// 不破坏既有 ComputeL3Path 签名（保护 diagnostic_handlers.go 与既有测试）。
func ComputeL3PathNAT(states map[string]*CLIState, srcState *CLIState, targetIP string, t *topology.Topology) ([]string, bool) {
	if srcState == nil || t == nil {
		return nil, false
	}
	// 1) 普通拓扑接口 IP：直接委托，保持旧行为。
	if deviceIDByIP(t, targetIP) != "" {
		return ComputeL3Path(srcState, targetIP, t), false
	}
	// 2) NAT GlobalIP：扫描各设备 CLIState 的 nat server。
	natDev, insideDev, ok := deviceIDByNATGlobalIP(states, t, targetIP)
	if !ok {
		return nil, false
	}
	path := computeL3PathToDevice(srcState.DeviceID, natDev, t)
	if path == nil {
		// NAT 设备从 src 不可达：诚实退化，natTranslated=true 告知调用方目标为公网 IP。
		return nil, true
	}
	if insideDev != "" {
		path = append(path, insideDev)
	}
	return path, true
}

// deviceIDByNATGlobalIP 扫描 states 注册表，返回拥有 targetIP 作为 GlobalIP 的
// NAT 设备 ID 与对应内网设备 ID（InsideIP → deviceIDByIP）。
func deviceIDByNATGlobalIP(states map[string]*CLIState, t *topology.Topology, targetIP string) (natDev, insideDev string, ok bool) {
	if states == nil {
		return "", "", false
	}
	for devID, st := range states {
		if st == nil || st.NAT == nil || !st.NAT.Enabled {
			continue
		}
		for _, server := range st.NAT.Servers {
			if server.GlobalIP != "" && server.GlobalIP == targetIP {
				insideDev = deviceIDByIP(t, server.InsideIP)
				return devID, insideDev, true
			}
		}
	}
	return "", "", false
}

// computeL3PathToDevice 由拓扑 BFS 计算 src→dstDev 的有序设备路径（含 src、dstDev）。
// 找不到目标设备或无拓扑时返回 nil（调用方据此退化为基础可达性判定）。
func computeL3PathToDevice(src, dstDev string, t *topology.Topology) []string {
	if src == "" || dstDev == "" || t == nil {
		return nil
	}
	if src == dstDev {
		return []string{src}
	}
	prev := make(map[string]string)
	visited := map[string]bool{src: true}
	q := []string{src}
	found := false
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, link := range t.Links {
			var next string
			if link.SourceDevice == cur {
				next = link.TargetDevice
			} else if link.TargetDevice == cur {
				next = link.SourceDevice
			} else {
				continue
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			if next == dstDev {
				found = true
				break
			}
			q = append(q, next)
		}
		if found {
			break
		}
	}
	if !found {
		return nil
	}
	var path []string
	for d := dstDev; d != ""; d = prev[d] {
		path = append([]string{d}, path...)
		if d == src {
			break
		}
	}
	return path
}

// bestEffortSourceIP 在缺少拓扑 t 时推导 tracert 渲染所需的源 IP（尽力而为）：
// 终端取 HostIP；L3 设备取首个接口 IP；否则 ""。
//
// 注：RenderTracerouteWithACL 签名不含 t（设计 §3.2），故无法做最长前缀路由推导；
// 真实 CLI tracert 路径（parser.go）有 t 时可经 ComputeL3Path/ResolveSourceIP 更精确评估。
func bestEffortSourceIP(state *CLIState) string {
	if state == nil {
		return ""
	}
	if isTerminalType(state.DeviceType) {
		return state.HostIP
	}
	for _, iface := range state.Interfaces {
		if iface != nil && iface.IP != "" {
			return iface.IP
		}
	}
	return ""
}
