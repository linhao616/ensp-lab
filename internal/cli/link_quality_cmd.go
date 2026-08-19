// link_quality_cmd.go 实现接口视图链路质量命令的**状态写入层**
// （v0.12 链路质量模拟，T2）。
//
// 职责边界（架构铁律）：本文件**只**做视图守卫 + 调 eval 层校验 + 写
// state.DeviceConfig，不渲染、不做业务判断、不碰 sim 引擎。
// 配置生效路径：DeviceConfig -> api 层 syncInterfaceLinkQuality -> topology.Link
// -> syncEngine 重建 -> 引擎 Ping/Traceroute 读 Link.Delay / Link.Loss。
//
// 命令族（仿真扩展，非 VRP 真机命令，display 输出中已显式标注）：
//
//	[Huawei-GE0/0/1] delay 20        # 单向时延 20ms
//	[Huawei-GE0/0/1] loss 0.5        # 单向丢包 0.5%
//	[Huawei-GE0/0/1] undo delay
//	[Huawei-GE0/0/1] undo loss
package cli

import (
	"strconv"
	"strings"
)

// isLinkQualityCommandName 判断 token 是否为链路质量命令首别名。
// 供 parser.go 顶层 switch 与补全漂移测试共同引用，避免两处硬编码。
func isLinkQualityCommandName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "delay", "loss":
		return true
	}
	return false
}

// linkQualityViewGuard 校验当前必须处于接口视图，返回 (接口名, 错误文案)。
// 错误文案非空时调用方直接返回该文案。
func linkQualityViewGuard(state *CLIState, usage string) (string, string) {
	if state == nil {
		return "", "Error: invalid state"
	}
	if state.CurrentView != ViewInterface || strings.TrimSpace(state.CurrentSub) == "" {
		return "", "Error: must be in interface view, usage: " + usage
	}
	return state.CurrentSub, ""
}

// applyLinkDelay 处理接口视图 `delay <ms>`。
func applyLinkDelay(state *CLIState, args []string) string {
	iface, errMsg := linkQualityViewGuard(state, "delay <0-10000>")
	if errMsg != "" {
		return errMsg
	}
	if len(args) == 0 {
		return "Error: incomplete command, usage: delay <0-10000>"
	}
	if len(args) > 1 {
		return "Error: too many parameters, usage: delay <0-10000>"
	}
	ms, errMsg := ValidateLinkDelay(args[0])
	if errMsg != "" {
		return errMsg
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	value := strconv.Itoa(ms)
	state.DeviceConfig[linkDelayKey(iface)] = value
	return "Link delay of " + iface + " set to " + value + " ms (one-way)"
}

// applyLinkLoss 处理接口视图 `loss <percent>`。
func applyLinkLoss(state *CLIState, args []string) string {
	iface, errMsg := linkQualityViewGuard(state, "loss <0-100>")
	if errMsg != "" {
		return errMsg
	}
	if len(args) == 0 {
		return "Error: incomplete command, usage: loss <0-100>"
	}
	if len(args) > 1 {
		return "Error: too many parameters, usage: loss <0-100>"
	}
	pct, errMsg := ValidateLinkLoss(args[0])
	if errMsg != "" {
		return errMsg
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	value := FormatLinkLoss(pct)
	state.DeviceConfig[linkLossKey(iface)] = value
	return "Link loss of " + iface + " set to " + value + "% (one-way)"
}

// applyUndoLinkQuality 处理接口视图 `undo delay` / `undo loss`。
// handled=false 表示不是链路质量 undo，调用方应继续走原有分支（零回归）。
func applyUndoLinkQuality(state *CLIState, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	if sub != "delay" && sub != "loss" {
		return "", false
	}
	iface, errMsg := linkQualityViewGuard(state, "undo "+sub)
	if errMsg != "" {
		return errMsg, true
	}
	if state.DeviceConfig == nil {
		return "Link " + sub + " of " + iface + " restored to default", true
	}
	switch sub {
	case "delay":
		delete(state.DeviceConfig, linkDelayKey(iface))
	case "loss":
		delete(state.DeviceConfig, linkLossKey(iface))
	}
	return "Link " + sub + " of " + iface + " restored to default", true
}
