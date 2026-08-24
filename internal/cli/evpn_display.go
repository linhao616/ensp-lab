// evpn_display.go EVPN 展示入口（P1-1 起，控制面渲染逻辑迁至 evpn_ctrl_display.go）。
//
// 入口：
//   - dis evpn [vni|peer|routing-table|instance <id>]  -> regEvpnDisplay
//   - dis bgp evpn [peer|routing-table|vni]            -> regBgpDisplay 的 arg1=="evpn" 分支委托 buildEVPNBGPDisplay
//
// 渲染实现见 evpn_ctrl_display.go（真实配置态 + 运行态诚实占位）。
// 运行态（邻居 / VNI 路由）恒 "-"，严禁编造邻居 IP / VNI 列表 / 路由条目。
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// regEvpnDisplay 是 `dis evpn` 的注册表入口（handler 签名与 registry 一致）。
func regEvpnDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	// v0.12.1：二级子命令仅放行白名单（vni/peer/routing-table/instance，无缩写支持），
	// 其余输入报 unknown command 指向完整命令，不再静默回退到 EVPN 概览。
	sub := strings.ToLower(strings.TrimSpace(arg1))
	switch sub {
	case "", "vni", "peer", "routing-table":
		return buildEVPNDisplay(state, arg1, 0)
	case "instance":
		// display evpn instance <id>
		id := 0
		if len(cmd.Args) >= 3 {
			if n, err := strconv.Atoi(strings.TrimSpace(cmd.Args[2])); err == nil {
				id = n
			}
		}
		return buildEVPNDisplay(state, "instance", id)
	default:
		return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
	}
}

// evpnRoutingPlaceholder 渲染 EVPN 路由表诚实占位（运行态恒 '-'）。
func evpnRoutingPlaceholder() string {
	var out strings.Builder
	out.WriteString("EVPN routing-table:\n")
	out.WriteString("  Routes : -\n")
	out.WriteString(evpnCtrlSimNote())
	return out.String()
}
