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
