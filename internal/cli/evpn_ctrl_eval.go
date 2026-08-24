// evpn_ctrl_eval.go 是 EVPN-BGP 控制面（P1-1）的纯函数 / 键构造 / 校验层。
//
// 仅含确定性工具函数与 DeviceConfig 精确前缀键构造，不写运行态、不编造数字
// （诚实占位原则）。命令副作用落盘见 evpn_ctrl_cmd.go，渲染见 evpn_ctrl_display.go。
//
// 键命名（与键碰撞红线一致，禁 strings.Contains 子串扫描）：
//
//	evpn:instance:<id>:id          实例存在标记（reload 重建用）
//	evpn:instance:<id>:rd          Route Distinguisher，如 "100:1"
//	evpn:instance:<id>:rt          逗号分隔 RT，元素形如 "both:100:1" / "import:100:1" / "export:100:1"
//	evpn:instance:<id>:bd          逗号分隔关联 BD id
//	evpn:bd:<id>:vni               Bridge Domain 关联 VNI
//	evpn:bd:<id>:vlan             逗号分隔关联 VLAN id
//	evpn:bd:<id>:vlanif           三层网关接口名（如 Vlanif10）
//	bgp:l2vpn-evpn:enabled
//	bgp:l2vpn-evpn:peer:<ip>:enabled
//	bgp:l2vpn-evpn:advertise-irb
package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// EVPN-BGP 控制面 DeviceConfig 命名空间前缀（精确前缀，避免误伤异族键）。
const (
	evpnInstanceNS = "evpn:instance:"
	evpnBDNS       = "evpn:bd:"
	bgpL2NS        = "bgp:l2vpn-evpn:"
)

// —— 键构造（精确前缀）——
func evpnInstanceFieldKey(id int, field string) string {
	return fmt.Sprintf("%s%d:%s", evpnInstanceNS, id, field)
}
func evpnBDFieldKey(id int, field string) string {
	return fmt.Sprintf("%s%d:%s", evpnBDNS, id, field)
}
func bgpL2PeerEnabledKey(ip string) string {
	return fmt.Sprintf("%speer:%s:enabled", bgpL2NS, ip)
}

// parsePositiveID 解析正整数 ID（实例 / BD / VLAN），失败返回 (0,false)。
func parsePositiveID(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// rtDirection 归一化 vpn-target 方向参数，缺省为 both。
func rtDirection(arg string) string {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "import":
		return "import"
	case "export":
		return "export"
	case "both":
		return "both"
	default:
		return "both"
	}
}

// parseRTEntry 将存储的 RT 条目 "dir:value" 拆为 (dir, value)。
func parseRTEntry(entry string) (dir, value string) {
	entry = strings.TrimSpace(entry)
	if i := strings.Index(entry, ":"); i > 0 {
		d := entry[:i]
		v := entry[i+1:]
		if d == "import" || d == "export" || d == "both" {
			return d, v
		}
	}
	return "both", entry
}

// joinInts 将 int 切片以逗号连接（确定性，便于落盘与 display 升序渲染）。
func joinInts(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

// splitInts 将逗号分隔字符串解析为去重升序 int 切片。
func splitInts(s string) []int {
	if s == "" {
		return nil
	}
	seen := make(map[int]bool)
	out := make([]int, 0)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// ensureEVPNInstance 确保 state.EVPN.Instances[id] 存在并写入存在标记键。
func ensureEVPNInstance(state *CLIState, id int) *EVPNInstance {
	inst, ok := state.EVPN.Instances[id]
	if !ok {
		inst = &EVPNInstance{ID: id}
		state.EVPN.Instances[id] = inst
	}
	state.DeviceConfig[evpnInstanceFieldKey(id, "id")] = strconv.Itoa(id)
	return inst
}

// ensureBridgeDomain 确保 state.EVPN.BDs[id] 存在并写入存在标记键。
func ensureBridgeDomain(state *CLIState, id int) *BridgeDomain {
	bd, ok := state.EVPN.BDs[id]
	if !ok {
		bd = &BridgeDomain{ID: id}
		state.EVPN.BDs[id] = bd
	}
	return bd
}

// evpnCtrlSimNote 诚实占位注记（运行态：EVPN 路由 / 邻居会话未仿真，恒 '-'）。
func evpnCtrlSimNote() string {
	return "\nNote: EVPN runtime state (Type-2/3/5 routes, neighbor sessions) is not simulated by the lite engine; fields shown as '-' are placeholders."
}

// loadEVPNFromDeviceConfig 从 DeviceConfig 的 evpn:/bgp:l2vpn-evpn: 键重建
// state.EVPN 与 state.BGP.L2VPNEvpn 结构化字段，保证 save/reload 后 display 一致。
// 键集合已由 SerializeToDeviceConfigData 整体拷贝，此处仅做结构化重建。
func loadEVPNFromDeviceConfig(state *CLIState) {
	// EVPN 实例
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, evpnInstanceNS) {
			continue
		}
		rest := strings.TrimPrefix(k, evpnInstanceNS)
		// rest 形如 "<id>:<field>"
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			continue
		}
		idStr, field := rest[:colon], rest[colon+1:]
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		inst := ensureEVPNInstance(state, id)
		switch field {
		case "rd":
			inst.RD = v
		case "rt":
			inst.RTs = splitRTList(v)
		case "bd":
			inst.BDs = splitInts(v)
		}
	}
	// Bridge Domain
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, evpnBDNS) {
			continue
		}
		rest := strings.TrimPrefix(k, evpnBDNS)
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			continue
		}
		idStr, field := rest[:colon], rest[colon+1:]
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		bd := ensureBridgeDomain(state, id)
		switch field {
		case "vni":
			if n, e := strconv.Atoi(v); e == nil {
				bd.VNI = n
			}
		case "vlan":
			bd.VLANs = splitInts(v)
		case "vlanif":
			bd.Vlanif = v
		}
	}
	// BGP L2VPN EVPN 地址族
	if enabled, ok := state.DeviceConfig[bgpL2NS+"enabled"]; ok && enabled == "true" {
		if state.BGP.L2VPNEvpn == nil {
			state.BGP.L2VPNEvpn = &L2VPNEvpnAF{Peers: make(map[string]*EvpnPeer)}
		}
		state.BGP.L2VPNEvpn.Enabled = true
		if v, ok := state.DeviceConfig[bgpL2NS+"advertise-irb"]; ok && v == "true" {
			state.BGP.L2VPNEvpn.AdvertiseIRB = true
		}
		for k, v := range state.DeviceConfig {
			if !strings.HasPrefix(k, bgpL2NS+"peer:") || !strings.HasSuffix(k, ":enabled") {
				continue
			}
			ip := strings.TrimPrefix(k, bgpL2NS+"peer:")
			ip = strings.TrimSuffix(ip, ":enabled")
			if ip == "" {
				continue
			}
			state.BGP.L2VPNEvpn.Peers[ip] = &EvpnPeer{IP: ip, Enabled: v == "true"}
		}
	}
}

// splitRTList 解析逗号分隔、方向前缀的 RT 列表（去重保序）。
func splitRTList(s string) []string {
	if s == "" {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// sortedIntKeys 返回 map[int]*T 的键升序切片（确定性 display 渲染）。
func sortedIntKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortInts(keys)
	return keys
}

// sortedStrKeys 返回 map[string]*T 的键字典序切片（确定性 display 渲染）。
func sortedStrKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortInts 原地升序排序（避免重复 import sort 于各文件，集中于此）。
func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
