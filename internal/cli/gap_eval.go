// gap_eval.go —— 网闸（GAP, 安全隔离网闸）纯函数层（设计三件套之一）
//
// 仿真语义（区别于防火墙的包过滤）：
//   - 网闸内外网**物理隔离**，数据靠「摆渡通道」（channel）转发；
//   - 通道（mapping 规则）+ 策略（policy 白名单）齐备且 enable 才放行，
//     否则流量被隔离（仿真中 display gap channel 显示 Down / 流量不转发）。
//
// 本文件只做只读计算，不写状态；写状态在 gap_cmd.go，渲染在 gap_display.go。
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// GAPChannelStatus 计算指定通道的仿真状态。
// 规则：已配置 mapping 且 enable=true → Up；只配 mapping 未 enable → Config（未启用）；
// 未配置 → 不存在。
func GAPChannelStatus(cfg map[string]string, n string) string {
	enable := cfg["gap:channel:"+n+":enable"]
	if enable == "true" {
		return "Up"
	}
	if cfg["gap:channel:"+n+":mapping"] != "" {
		return "Config"
	}
	return ""
}

// GAPChannelState 通道状态汇总（供 display gap channel 渲染）。
type GAPChannelState struct {
	Number  string
	Mapping string // 形如 "tcp 10.1.1.10:8080 <-> 203.0.113.10:8080"
	Status  string // Up / Config / ""
	Enabled bool
}

// GAPChannelList 收集全部已配置通道（按编号排序）。
func GAPChannelList(cfg map[string]string) []GAPChannelState {
	seen := map[string]bool{}
	var out []GAPChannelState
	for k, v := range cfg {
		// 精确前缀 `gap:channel:` + 编号 + `:mapping`；禁止 Contains 匹配防误伤
		if !strings.HasPrefix(k, "gap:channel:") || !strings.HasSuffix(k, ":mapping") {
			continue
		}
		n := strings.TrimSuffix(strings.TrimPrefix(k, "gap:channel:"), ":mapping")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, GAPChannelState{
			Number:  n,
			Mapping: v,
			Enabled: cfg["gap:channel:"+n+":enable"] == "true",
			Status:  GAPChannelStatus(cfg, n),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return atoiSafe(out[i].Number) < atoiSafe(out[j].Number)
	})
	return out
}

// GAPPolicyList 收集全部已配置策略（按编号排序）。
func GAPPolicyList(cfg map[string]string) []map[string]string {
	seen := map[string]bool{}
	var out []map[string]string
	for k, v := range cfg {
		if !strings.HasPrefix(k, "gap:policy:") || !strings.HasSuffix(k, ":rule") {
			continue
		}
		n := strings.TrimSuffix(strings.TrimPrefix(k, "gap:policy:"), ":rule")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, map[string]string{
			"number":  n,
			"rule":    v,
			"enabled": cfg["gap:policy:"+n+":enable"],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return atoiSafe(out[i]["number"]) < atoiSafe(out[j]["number"])
	})
	return out
}

// GAPStatsDisplay 通道统计（诚实占位：仿真不模拟字节计数，恒 "-"）。
func GAPStatsDisplay() string {
	return fmt.Sprintf(
		"  Forwarded packets : %s\n  Forwarded bytes   : %s\n  Dropped packets   : %s\n  Sessions          : %s",
		"-", "-", "-", "-",
	)
}

// atoiSafe 解析十进制整数，失败返回 0（通道/策略编号排序用）。
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
