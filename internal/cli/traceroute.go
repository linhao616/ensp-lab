// traceroute.go 负责把 sim.Engine 返回的真实 ping/traceroute 结果
// 渲染成贴近华为 VRP / Linux 的命令行文本，供 CLI 终端直接输出。
//
// 此前 ping/tracert 由前端或 parser.go 返回硬编码/随机结果（parser.go:1123
// ping 恒 success、parser.go:1139 tracert 硬编码 2 跳）。P0-B 让 CLI 走真实
// 引擎后，这里成为唯一的渲染出口，保证「看到的结果」与「引擎计算的真实拓扑」
// 一致。cli 包 import sim 不形成循环依赖（sim 不反向依赖 cli）。
package cli

import (
	"fmt"
	"strings"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// FormatEnginePing 把真实引擎的 PingResult 渲染成 VRP 风格的统计摘要。
// target 为目标 IP，用于标题行。不可达（Received==0）时渲染
// "Destination unreachable" 而非伪造成功。
func FormatEnginePing(res *sim.PingResult, target string) string {
	if res == nil {
		return fmt.Sprintf("Ping %s: no result from engine", target)
	}
	if res.Received == 0 {
		// 全部丢包：真实引擎未收到任何回显，如实报告不可达。
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Ping %s: destination unreachable (100%% packet loss)\n", target))
		for _, d := range res.Details {
			b.WriteString("  " + d + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	var min, max, sum float64
	min = res.RTTMs[0]
	max = res.RTTMs[0]
	for _, r := range res.RTTMs {
		sum += r
		if r < min {
			min = r
		}
		if r > max {
			max = r
		}
	}
	avg := sum / float64(len(res.RTTMs))
	loss := float64(res.Lost) / float64(res.Sent) * 100

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Pinging %s with 64 bytes of data:\n", target))
	for _, d := range res.Details {
		b.WriteString("  " + d + "\n")
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("--- %s ping statistics ---\n", target))
	b.WriteString(fmt.Sprintf("%d packets transmitted, %d received, %.0f%% packet loss\n",
		res.Sent, res.Received, loss))
	b.WriteString(fmt.Sprintf("round-trip min/avg/max = %.2f/%.2f/%.2f ms\n",
		min, avg, max))
	return strings.TrimRight(b.String(), "\n")
}

// FormatEngineTraceroute 把真实引擎的 TracerouteResult 渲染成 VRP 风格的逐跳输出。
// 未到达（Reached==false 或 Hops 为空）时渲染 "* * *  Request timed out."，
// 不再伪造固定 2 跳。maxTTL 用于标题行。
func FormatEngineTraceroute(res *sim.TracerouteResult, maxTTL int) string {
	if res == nil {
		return "Tracing route: no result from engine"
	}
	if maxTTL <= 0 {
		maxTTL = res.MaxTTL
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tracing route to %s over a maximum of %d hops:\n", res.TargetIP, maxTTL))
	if len(res.Hops) == 0 {
		// 拓扑不连通或目标不可解析：如实报告超时，而非编造路径。
		for i := 1; i <= maxTTL; i++ {
			b.WriteString(fmt.Sprintf("  %2d    *        *        *     Request timed out.\n", i))
		}
		b.WriteString("\nTrace incomplete (no route to destination).")
		return strings.TrimRight(b.String(), "\n")
	}

	for _, hop := range res.Hops {
		ip := hop.IP
		if ip == "" {
			ip = "?"
		}
		delayStr := "<1 ms"
		if hop.DelayMs >= 1 {
			delayStr = fmt.Sprintf("%.0f ms", hop.DelayMs)
		}
		// VRP 风格：每跳输出三列延迟 + 设备名/IP。
		b.WriteString(fmt.Sprintf("  %2d    %-8s %-8s %-8s  %s\n",
			hop.Hop, delayStr, delayStr, delayStr, deviceLabel(hop.DeviceID, ip)))
	}

	if res.Reached {
		b.WriteString("\nTrace complete.")
	} else {
		// 路径被 maxTTL 截断：续写超时行说明未完成。
		for i := len(res.Hops) + 1; i <= maxTTL; i++ {
			b.WriteString(fmt.Sprintf("  %2d    *        *        *     Request timed out.\n", i))
		}
		b.WriteString("\nTrace incomplete (TTL expired before reaching destination).")
	}
	return strings.TrimRight(b.String(), "\n")
}

// deviceLabel 组合设备名与 IP，便于阅读（设备名未知时退化为 IP）。
func deviceLabel(deviceID, ip string) string {
	if deviceID == "" {
		return ip
	}
	if ip == "" || ip == deviceID {
		return deviceID
	}
	return fmt.Sprintf("%s (%s)", deviceID, ip)
}

// RenderTracerouteWithACL 在 FormatEngineTraceroute 之上叠加 ACL 判定（P1-C T02）+ NAT 显示（P2）。
//
// 用 res.Hops[].DeviceID 组路径（并前置源设备）→ EvaluatePathACL（按 deviceID
// 读取各设备「自身」的 CLIState）→ 命中 deny 渲染「前 k-1 跳 + 第 k 跳起 * * * +
// ACL 拦截注记」；全 permit 则退化为 FormatEngineTraceroute（延续如实渲染风格）。
//
// P2 NAT 分支（设计 §3.2 / §4.4 / O1）：当 target 命中某 NAT 设备的 GlobalIP 时，
// 走 ComputeL3PathNAT 推导路径 [...,natDev, insideDev]，在 NAT 设备那一跳显示
// "NAT→<InsideIP>" + natSimNote，并对该路径调 EvaluatePathACL 做 ACL 评估；
// 非 NAT 目标保持原 res.Hops 行为不变。t 为拓扑（NAT 解析依赖）；为 nil 时跳过 NAT 分支。
//
// states 为拓扑级 CLIState 注册表（deviceID→*CLIState），使途径 L3/防火墙设备
// 自身配置的 traffic-filter ACL 也能被评估（修复 P1-C Round 1 中转设备 ACL 未生效）。
func RenderTracerouteWithACL(states map[string]*CLIState, state *CLIState, res *sim.TracerouteResult, target string, maxTTL int, t *topology.Topology) string {
	if res == nil {
		return FormatEngineTraceroute(nil, maxTTL)
	}
	if maxTTL <= 0 {
		maxTTL = res.MaxTTL
	}
	src := ""
	if state != nil {
		src = state.DeviceID
	}
	// P2 NAT 分支：target 命中某 NAT GlobalIP → 用 NAT 感知路径渲染。
	if t != nil {
		if natPath, natTranslated := ComputeL3PathNAT(states, state, target, t); natTranslated && natPath != nil {
			return renderTracerouteNAT(states, state, target, maxTTL, t, natPath)
		}
	}
	// 非 NAT 目标：原有行为（基于引擎 res.Hops）。
	path := buildTraceroutePath(src, res.Hops)
	flow := PacketTuple{
		SrcIP: bestEffortSourceIP(state),
		DstIP: res.TargetIP,
		Proto: "icmp",
	}
	if dec := EvaluatePathACL(states, path, flow); dec.Action == "deny" {
		return renderTracerouteBlocked(res, maxTTL, path, dec)
	}
	return FormatEngineTraceroute(res, maxTTL)
}

// RenderPingWithACL 在 FormatEnginePing 之上叠加 ACL 判定（P1-C T03，API 主路径）。
//
// 经 ComputeL3PathNAT + ResolveSourceIP 得路径与源 IP → EvaluatePathACL（按 deviceID
// 读取各设备「自身」的 CLIState）；命中 deny 渲染不可达(ACL 拦截)；全 permit 退化为
// FormatEnginePing。ACL 代表真实过滤，优先于引擎拓扑结果（命中 deny 即视为不可达）。
//
// P2：路径经 ComputeL3PathNAT 推导（公网 GlobalIP → 解析到内网设备）；当 natTranslated
// 为真（路径存在 NAT 转换）时在输出追加 natSimNote() 诚实占位（对齐 AC2/AC6）。
//
// states 为拓扑级 CLIState 注册表（deviceID→*CLIState），使途径 L3/防火墙设备
// 自身配置的 traffic-filter ACL 也能被评估（修复 P1-C Round 1 中转设备 ACL 未生效）。
func RenderPingWithACL(states map[string]*CLIState, state *CLIState, res *sim.PingResult, targetIP string, t *topology.Topology) string {
	path, natTranslated := ComputeL3PathNAT(states, state, targetIP, t)
	srcIP := ResolveSourceIP(state, targetIP, t)
	flow := PacketTuple{SrcIP: srcIP, DstIP: targetIP, Proto: "icmp"}
	if dec := EvaluatePathACL(states, path, flow); dec.Action == "deny" {
		ruleID := 0
		if dec.Rule != nil {
			ruleID = dec.Rule.ID
		}
		// P2：存在 NAT 转换时统一用 natSimNote 诚实占位（无 NAT 用 ACL 注记）。
		note := aclSimNote()
		if natTranslated {
			note = natSimNote()
		}
		return fmt.Sprintf("Ping %s: destination unreachable (ACL 拦截: %s acl %s rule %d, %s) %s",
			targetIP, dec.DeviceID, dec.ACLNum, ruleID, dec.Direction, note)
	}
	return FormatEnginePing(res, targetIP)
}

// buildTraceroutePath 把 res.Hops 的设备序列前置源设备，构成完整 ACL 评估路径
// （含 src、各中转、dst），供方向模型（src=outbound、中转=inbound+outbound、dst=inbound）使用。
func buildTraceroutePath(src string, hops []sim.TracerouteHop) []string {
	path := make([]string, 0, len(hops)+1)
	if src != "" {
		path = append(path, src)
	}
	for _, h := range hops {
		if h.DeviceID != "" {
			path = append(path, h.DeviceID)
		}
	}
	return path
}

// renderTracerouteBlocked 渲染命中 ACL deny 的 tracert 结果：
// 拦截跳之前逐跳正常渲染，拦截跳起 "* * *" + ACL 拦截注记（诚实占位）。
func renderTracerouteBlocked(res *sim.TracerouteResult, maxTTL int, path []string, dec Decision) string {
	// 计算拦截发生在 res.Hops 中的 0-based 下标（blockFrom）：
	// 入向拦截 → 该设备本身即拦截跳；出向拦截 → 报文已抵达该设备、离开时被丢弃，从下一跳起超时。
	blockedIdx := indexInPath(dec.DeviceID, path)
	blockFrom := blockedIdx - 1
	if blockedIdx <= 0 {
		blockFrom = 0 // 源设备出向被拦截 → 自第 1 跳起全超时
	}
	if dec.Direction == DirOutbound && blockedIdx > 0 {
		blockFrom = blockedIdx
	}
	if blockFrom < 0 {
		blockFrom = 0
	}
	if blockFrom > len(res.Hops) {
		blockFrom = len(res.Hops)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tracing route to %s over a maximum of %d hops:\n", res.TargetIP, maxTTL))
	for i := 0; i < blockFrom && i < len(res.Hops); i++ {
		h := res.Hops[i]
		ip := h.IP
		if ip == "" {
			ip = "?"
		}
		delayStr := "<1 ms"
		if h.DelayMs >= 1 {
			delayStr = fmt.Sprintf("%.0f ms", h.DelayMs)
		}
		b.WriteString(fmt.Sprintf("  %2d    %-8s %-8s %-8s  %s\n",
			h.Hop, delayStr, delayStr, delayStr, deviceLabel(h.DeviceID, ip)))
	}
	ruleID := 0
	if dec.Rule != nil {
		ruleID = dec.Rule.ID
	}
	note := fmt.Sprintf("ACL 拦截: %s acl %s rule %d, %s %s",
		dec.DeviceID, dec.ACLNum, ruleID, dec.Direction, aclSimNote())
	for i := blockFrom; i < maxTTL; i++ {
		line := fmt.Sprintf("  %2d    *        *        *     Request timed out.", i+1)
		if i == blockFrom {
			line += "  (" + note + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\nTrace incomplete (blocked by ACL).")
	return strings.TrimRight(b.String(), "\n")
}

// indexInPath 返回设备首次出现在 path 中的下标；未找到返回 -1。
func indexInPath(deviceID string, path []string) int {
	for i, d := range path {
		if d == deviceID {
			return i
		}
	}
	return -1
}

// natDeviceInfo 扫描 states 注册表，返回拥有 targetIP 作为 GlobalIP 的 NAT 设备、
// 对应内网设备 ID（InsideIP → deviceIDByIP）与 InsideIP 本身（设计 §4.2 / §4.4）。
func natDeviceInfo(states map[string]*CLIState, t *topology.Topology, targetIP string) (natDev, insideDev, insideIP string) {
	if states == nil {
		return "", "", ""
	}
	for devID, st := range states {
		if st == nil || st.NAT == nil || !st.NAT.Enabled {
			continue
		}
		for _, server := range st.NAT.Servers {
			if server.GlobalIP != "" && server.GlobalIP == targetIP {
				return devID, deviceIDByIP(t, server.InsideIP), server.InsideIP
			}
		}
	}
	return "", "", ""
}

// deviceFirstIP 返回拓扑中某设备的首个非空接口 IP（用于 NAT 路径跳的标签展示）。
func deviceFirstIP(t *topology.Topology, devID string) string {
	if t == nil {
		return ""
	}
	dev, ok := t.Devices[devID]
	if !ok || dev == nil || dev.Interfaces == nil {
		return ""
	}
	for _, iface := range dev.Interfaces {
		if iface != nil && iface.IPAddress != "" {
			return iface.IPAddress
		}
	}
	return ""
}

// natHopLine 渲染 NAT 路径中某一跳的文本行；natDev 那一跳追加 " (NAT→<InsideIP>)" + natSimNote 注记。
func natHopLine(hop int, devID string, t *topology.Topology, natDev, insideIP string) string {
	ip := deviceFirstIP(t, devID)
	label := deviceLabel(devID, ip)
	if devID == natDev {
		label = fmt.Sprintf("%s (NAT→%s) %s", label, insideIP, natSimNote())
	}
	delayStr := "<1 ms"
	return fmt.Sprintf("  %2d    %-8s %-8s %-8s  %s\n", hop, delayStr, delayStr, delayStr, label)
}

// renderTracerouteNAT 渲染 NAT 目标（公网 GlobalIP → 内网 InsideIP）的 tracert 结果。
// natPath 由 ComputeL3PathNAT 推导（含 src、各中转、natDev、insideDev）。
// 命中 ACL deny → 前跳正常、拦截跳起 * * * + 注记；全 permit → 逐跳渲染（NAT 跳带注记）。
func renderTracerouteNAT(states map[string]*CLIState, state *CLIState, target string, maxTTL int, t *topology.Topology, natPath []string) string {
	srcIP := bestEffortSourceIP(state)
	flow := PacketTuple{SrcIP: srcIP, DstIP: target, Proto: "icmp"}
	dec := EvaluatePathACL(states, natPath, flow)
	natDev, _, insideIP := natDeviceInfo(states, t, target)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tracing route to %s over a maximum of %d hops:\n", target, maxTTL))

	if dec.Action == "deny" {
		// 拦截跳之前逐跳正常渲染（NAT 跳也带注记），拦截跳起 * * * + ACL 注记（诚实占位）。
		blockedIdx := indexInPath(dec.DeviceID, natPath)
		blockFrom := blockedIdx - 1
		if blockedIdx <= 0 {
			blockFrom = 0
		}
		if dec.Direction == DirOutbound && blockedIdx > 0 {
			blockFrom = blockedIdx
		}
		if blockFrom < 0 {
			blockFrom = 0
		}
		if blockFrom > len(natPath) {
			blockFrom = len(natPath)
		}
		for i := 0; i < blockFrom && i < len(natPath); i++ {
			b.WriteString(natHopLine(i+1, natPath[i], t, natDev, insideIP))
		}
		ruleID := 0
		if dec.Rule != nil {
			ruleID = dec.Rule.ID
		}
		note := fmt.Sprintf("ACL 拦截: %s acl %s rule %d, %s %s",
			dec.DeviceID, dec.ACLNum, ruleID, dec.Direction, natSimNote())
		for i := blockFrom; i < maxTTL; i++ {
			line := fmt.Sprintf("  %2d    *        *        *     Request timed out.", i+1)
			if i == blockFrom {
				line += "  (" + note + ")"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\nTrace incomplete (blocked by ACL).")
		return strings.TrimRight(b.String(), "\n")
	}

	// 全 permit：逐跳渲染（NAT 跳带 (NAT→InsideIP) 注记）。
	for i, devID := range natPath {
		b.WriteString(natHopLine(i+1, devID, t, natDev, insideIP))
	}
	b.WriteString("\nTrace complete.")
	return strings.TrimRight(b.String(), "\n")
}
