// gre_cmd.go 是 GRE 隧道配置命令的**副作用唯一出口**
// （P2 第七项，华为 VRP 课程 69，T0 骨架 / T2 主体 / T4 undo interface）。
//
// 分层契约（设计 A8，严格复刻 lag_cmd.go / dhcp_relay_cmd.go）：
//   - gre_eval.go  纯函数只读（键 helper / 校验 / 派生），无副作用；
//   - gre_cmd.go   **本文件**：唯一允许写 state.DeviceConfig 的 GRE 代码；
//   - gre_display.go 渲染 + 持久化 helper，只读。
//
// 命令族（Tunnel 接口视图，华为 VRP 真机形态）：
//
//	tunnel-protocol gre
//	source      <ip-address | interface-name>
//	destination <ip-address | interface-name>
//	gre key <0-4294967295>
//	gre checksum
//	keepalive [period <1-32767>] [retry-times <1-255>]
//	undo tunnel-protocol | undo source | undo destination
//	undo gre key | undo gre checksum | undo keepalive
//
// 回显口径（P2-3 / §7.5）：配置成功一律 **VRP 静默**（返回空串），失败才 `Error:`。
// 🔴 严禁自造 "GRE tunnel Tunnel0/0/1 created" 式欢快文案（旧 parser.go:2290 即此缺陷）。
package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// —— 固定文案（设计 §4.4，全仓唯一定义，便于 grep 与 QA 逐字断言）——

const (
	// errGRESystemViewGuide 是系统视图误用旧自造 `gre` 命令的报错引导（拍板 C1）。
	errGRESystemViewGuide = "Error: Please configure GRE in the Tunnel interface view. Run 'interface Tunnel0/0/1' first."
	// errGREMustBeInterface 是非接口视图的视图守卫文案。
	errGREMustBeInterface = "Error: must be in interface view"
	// errGRETunnelOnly 是接口视图但当前接口非 Tunnel 口的守卫文案。
	errGRETunnelOnly = "Error: This command is only supported on Tunnel interfaces."
	// errGRESameAddr 是 source == destination 的硬拒绝文案（拍板 C5 / 设计 A5 双向）。
	errGRESameAddr = "Error: The destination address cannot be the same as the source address."
	// errGREInvalidIP 是端点非法的统一文案（设计 A3 ③，保住 AC4 子串断言）。
	errGREInvalidIP = "Error: Invalid IP address %s"
	// errGREInvalidTunnelAddr 是特殊 IPv4 地址（0.0.0.0 / 广播 / 环回 / 组播）的拒绝文案（设计 A6）。
	errGREInvalidTunnelAddr = "Error: %s is not a valid tunnel address."
	// errGRESrcDstFirst 是「未先执行 tunnel-protocol gre」的前置守卫文案。
	errGRESrcDstFirst = "Error: Please run 'tunnel-protocol gre' on this interface first."
	// errGREInvalidKey 是 gre key 取值非法的文案。
	errGREInvalidKey = "Error: Invalid GRE key %s"
	// errGREUsageKey 是 gre key 缺参的用法提示。
	errGREUsageKey = "Error: usage: gre key <0-4294967295>"
	// errGREUsageSource / errGREUsageDestination 是端点命令缺参的用法提示（AC5 ④ 断言 `usage:`）。
	errGREUsageSource      = "Error: usage: source <ip-address|interface-name>"
	errGREUsageDestination = "Error: usage: destination <ip-address|interface-name>"
	// errGREUsageTunnelProtocol 是 tunnel-protocol 缺参的用法提示。
	errGREUsageTunnelProtocol = "Error: usage: tunnel-protocol gre"
	// errGREUsageKeepalive 是 keepalive 参数形态错误的用法提示。
	errGREUsageKeepalive = "Error: usage: keepalive [ period <1-32767> ] [ retry-times <1-255> ]"
	// errGREInvalidKeepalivePeriod / errGREInvalidKeepaliveRetry 是 keepalive 参数越界文案。
	errGREInvalidKeepalivePeriod = "Error: Invalid keepalive period %s"
	errGREInvalidKeepaliveRetry  = "Error: Invalid keepalive retry-times %s"
	// errGREUnrecognized 是未识别子命令的统一文案。
	errGREUnrecognized = "Error: unrecognized command"

	// infoNoGRE 是 display gre tunnel 的空态提示。
	infoNoGRE = "Info: No GRE tunnel configured."
	// infoGREOnIfaceNotCfg 是 Tunnel 口存在但未配 GRE 时的提示。
	infoGREOnIfaceNotCfg = "Info: GRE is not configured on this interface."
)

// —— 顶层命令名常量（parser.go 顶层 switch 合并 case 的分派键）——

const (
	greCmdTunnelProtocol = "tunnel-protocol"
	greCmdSource         = "source"
	greCmdDestination    = "destination"
	greCmdKeepalive      = "keepalive"
	greCmdGRE            = "gre"
)

// errGRENotSupported 返回设备类型能力拒绝文案（设计 A2）。
func errGRENotSupported(dt string) string {
	return fmt.Sprintf("Error: GRE is not supported on %s", dt)
}

// isGRECommandName 报告某顶层命令是否属于 GRE 命令族。
// 供 parser.go 顶层分派与测试使用（对照 isLAGCommandName 范式）。
func isGRECommandName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case greCmdTunnelProtocol, greCmdSource, greCmdDestination, greCmdKeepalive, greCmdGRE:
		return true
	}
	return false
}

// —— 三态守卫（顺序固定：视图 → 设备 → GRE 前置，设计 T2.1 / 风险 R4）——

// greDeviceSupported 判定当前设备类型是否支持 GRE（分支内守卫，设计 A2）。
//
// 设备集**直接复用 capabilities.go:174 的 l3Devices()**（Router / L3Switch / Firewall / VTEP），
// 严禁在本文件重定义设备集合；capabilities.go 本期零改动。
func greDeviceSupported(state *CLIState) bool {
	if state == nil || state.DeviceType == "" {
		return true
	}
	return l3Devices()[state.DeviceType]
}

// greProtocolConfigured 判定该接口是否已执行 tunnel-protocol gre（前置守卫）。
func greProtocolConfigured(state *CLIState, iface string) bool {
	if state == nil || state.DeviceConfig == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(state.DeviceConfig[tunnelProtocolKey(iface)]), GRETunnelProtocolValue)
}

// greTunnelViewGuard 执行「视图 → 设备」两级守卫，返回 (Tunnel 口名, 错误文案)。
// 错误文案非空即表示守卫未通过，调用方应直接返回该文案且**不写任何键**。
func greTunnelViewGuard(state *CLIState) (string, string) {
	if state == nil {
		return "", "Error: internal state unavailable"
	}
	// ① 视图守卫
	if state.CurrentView != ViewInterface {
		return "", errGREMustBeInterface
	}
	iface := strings.TrimSpace(state.CurrentSub)
	if iface == "" {
		return "", errGREMustBeInterface
	}
	if !isTunnelInterface(iface) {
		return "", errGRETunnelOnly
	}
	// ② 设备守卫
	if !greDeviceSupported(state) {
		return "", errGRENotSupported(string(state.DeviceType))
	}
	return iface, ""
}

// —— 命令分派（副作用唯一入口）——

// applyGREInterfaceCmd 是 GRE 接口视图命令族的统一入口。
//
// top 为顶层命令名（已由调用方保证是 GRE 命令族之一）；args 为其后全部参数。
// 三态守卫顺序固定：① 视图（含 Tunnel 口判定）→ ② 设备类型 → ③ GRE 前置条件。
//
// 🔴 守卫未通过时**绝不写任何键**（AC3 ① / AC5 ①）。
func applyGREInterfaceCmd(state *CLIState, top string, args []string) string {
	if state == nil {
		return "Error: internal state unavailable"
	}
	iface, guardErr := greTunnelViewGuard(state)
	if guardErr != "" {
		return guardErr
	}
	if state.DeviceConfig == nil {
		state.DeviceConfig = make(map[string]string)
	}
	switch strings.ToLower(strings.TrimSpace(top)) {
	case greCmdTunnelProtocol:
		return applyTunnelProtocol(state, iface, args)
	case greCmdSource:
		if !greProtocolConfigured(state, iface) {
			return errGRESrcDstFirst
		}
		return applyGRESource(state, iface, args)
	case greCmdDestination:
		if !greProtocolConfigured(state, iface) {
			return errGRESrcDstFirst
		}
		return applyGREDestination(state, iface, args)
	case greCmdKeepalive:
		if !greProtocolConfigured(state, iface) {
			return errGRESrcDstFirst
		}
		return applyGREKeepalive(state, iface, args)
	case greCmdGRE:
		// `gre key <n>` / `gre checksum`（Tunnel 接口视图子命令族）。
		if len(args) == 0 {
			return errGREUnrecognized
		}
		if !greProtocolConfigured(state, iface) {
			return errGRESrcDstFirst
		}
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "key":
			return applyGREKey(state, iface, args[1:])
		case "checksum":
			if len(args) > 1 {
				return errGREUnrecognized
			}
			return applyGREChecksum(state, iface)
		}
		return errGREUnrecognized
	}
	return errGREUnrecognized
}

// applyTunnelProtocol 处理 `tunnel-protocol gre`。
//
// 拍板 C7：本期仅接受 gre；none / ipv4-ipv6 / mpls 一律 errGREUnrecognized
// （`tunnel-protocol none` 不实现，回落走 undo tunnel-protocol）。
// 重复执行**幂等**：不报错、不产生重复键（§7.5）。
//
// 说明（对设计 §4.3 签名的最小扩展）：设计列出的签名为
// applyTunnelProtocol(state, iface)，但 T2.2 要求校验协议值，故补入 args 形参。
func applyTunnelProtocol(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errGREUsageTunnelProtocol
	}
	if len(args) > 1 {
		return errGREUnrecognized
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), GRETunnelProtocolValue) {
		return errGREUnrecognized
	}
	state.DeviceConfig[tunnelProtocolKey(iface)] = GRETunnelProtocolValue
	return ""
}

// applyGRESource 处理 `source <ip-address | interface-name>`（拍板 C3 双形态）。
//
// 校验顺序：形态判定 → 特殊地址（IP 形态）→ 同址双向校验（设计 A5）→ 落键。
// 🔴 校验未通过时**绝不残留空串键**（AC4）。
func applyGRESource(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errGREUsageSource
	}
	if len(args) > 1 {
		return errGREUsageSource
	}
	value := strings.TrimSpace(args[0])
	kind, ok, reason := validGRETunnelEndpoint(value)
	if !ok {
		return reason
	}
	// 设计 A5：source 侧同样比对已存 destination（对称补强，语义仍是「两端同 IP 才拒」）。
	if kind == greEndpointKindIP {
		existing := strings.TrimSpace(state.DeviceConfig[greKey(iface, greFieldDestination)])
		if greSameEndpoint(existing, value) {
			return errGRESameAddr
		}
	}
	state.DeviceConfig[greKey(iface, greFieldSource)] = value
	return ""
}

// applyGREDestination 处理 `destination <ip-address | interface-name>`（拍板 C3 双形态）。
func applyGREDestination(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errGREUsageDestination
	}
	if len(args) > 1 {
		return errGREUsageDestination
	}
	value := strings.TrimSpace(args[0])
	kind, ok, reason := validGRETunnelEndpoint(value)
	if !ok {
		return reason
	}
	// 拍板 C5：写 destination 前比对已存 source，同址硬拒。
	if kind == greEndpointKindIP {
		existing := strings.TrimSpace(state.DeviceConfig[greKey(iface, greFieldSource)])
		if greSameEndpoint(existing, value) {
			return errGRESameAddr
		}
	}
	state.DeviceConfig[greKey(iface, greFieldDestination)] = value
	return ""
}

// applyGREKey 处理 `gre key <0-4294967295>`。args 为 "key" 之后的参数。
//
// 值经 normalizeGREKeyValue 规范化后落键（string 语义，设计 A7：
// 未配置与 key 0 从类型层面即可区分，渲染层未配显 "-" 而非 "0"）。
func applyGREKey(state *CLIState, iface string, args []string) string {
	if len(args) == 0 {
		return errGREUsageKey
	}
	if len(args) > 1 {
		return errGREUsageKey
	}
	raw := strings.TrimSpace(args[0])
	normalized, ok := normalizeGREKeyValue(raw)
	if !ok {
		return fmt.Sprintf(errGREInvalidKey, raw)
	}
	state.DeviceConfig[greKey(iface, greFieldKey)] = normalized
	return ""
}

// applyGREChecksum 处理 `gre checksum`（P2-1）。
func applyGREChecksum(state *CLIState, iface string) string {
	state.DeviceConfig[greKey(iface, greFieldChecksum)] = "true"
	return ""
}

// applyGREKeepalive 处理 `keepalive [period <p>] [retry-times <r>]`（拍板 C2：仅配置态）。
//
// 差异值口径：裸 `keepalive` 只写 gre-keepalive；period / retry **仅在显式指定时**落键，
// 未显式指定则由 readGREConfig 合并生效缺省 5 / 3（不冗余落盘）。
//
// 🔴 先校验全部参数再落键：任一参数越界即整条拒绝，不产生半截配置。
func applyGREKeepalive(state *CLIState, iface string, args []string) string {
	period := ""
	retry := ""
	for i := 0; i < len(args); {
		switch strings.ToLower(strings.TrimSpace(args[i])) {
		case "period":
			if i+1 >= len(args) {
				return errGREUsageKeepalive
			}
			period = strings.TrimSpace(args[i+1])
			i += 2
		case "retry-times", "retry":
			if i+1 >= len(args) {
				return errGREUsageKeepalive
			}
			retry = strings.TrimSpace(args[i+1])
			i += 2
		default:
			return errGREUsageKeepalive
		}
	}
	periodVal := 0
	if period != "" {
		n, err := strconv.Atoi(period)
		if err != nil || n < GREKeepalivePeriodMin || n > GREKeepalivePeriodMax {
			return fmt.Sprintf(errGREInvalidKeepalivePeriod, period)
		}
		periodVal = n
	}
	retryVal := 0
	if retry != "" {
		n, err := strconv.Atoi(retry)
		if err != nil || n < GREKeepaliveRetryMin || n > GREKeepaliveRetryMax {
			return fmt.Sprintf(errGREInvalidKeepaliveRetry, retry)
		}
		retryVal = n
	}
	state.DeviceConfig[greKey(iface, greFieldKeepalive)] = "true"
	if period != "" {
		state.DeviceConfig[greKey(iface, greFieldKeepalivePeriod)] = strconv.Itoa(periodVal)
	}
	if retry != "" {
		state.DeviceConfig[greKey(iface, greFieldKeepaliveRetry)] = strconv.Itoa(retryVal)
	}
	return ""
}

// —— 级联清理 ——

// clearGREKeys 删除该接口 **interface:<if>:gre- 精确前缀**的全部键。
//
// 🔴 A1 红线：只用精确前缀，绝不可退化为 Contains("gre")——后者会连
// interface:Bridge-Aggregation1:lag:mode 一并删除（不可恢复的数据破坏，AC12 ②）。
// 同时严格不碰 interface:<if>:ip / :status / :description（它们不含 :gre- 中缀）。
func clearGREKeys(state *CLIState, iface string) {
	if state == nil || state.DeviceConfig == nil {
		return
	}
	prefix := greKeyPrefix(iface)
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			delete(state.DeviceConfig, k)
		}
	}
}

// —— 接口视图 undo（handled 模式，范式 lag_cmd.go:773 / dhcp_relay_cmd.go:316）——

// applyUndoGREInterface 在**Tunnel 接口视图**统一拦截 GRE 相关的 undo 子命令。
//
// 返回 (回显, 是否已处理)；未命中时 handled=false，交回既有 undo 分支（零回归）。
// 当前接口非 Tunnel 口时一律 handled=false —— 物理口 / Vlanif / Eth-Trunk 的
// undo 行为逐字不变（AC11c）。
//
// 覆盖分支（AC10）：
//
//	undo tunnel-protocol   删协议键 + **级联清理** interface:<if>:gre- 精确前缀全部键
//	undo source            删 gre-source
//	undo destination       删 gre-destination
//	undo gre key           删 gre-key
//	undo gre checksum      删 gre-checksum
//	undo keepalive         **按 greKeepaliveFields 枚举**删三键（设计 A10，严禁前缀匹配）
//
// 🔴 一律 delete(map, key) 删键，而非写空串（AC10 ① 断言 `_, ok := DeviceConfig[k]; ok == false`）。
func applyUndoGREInterface(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) == 0 {
		return "", false
	}
	if state.CurrentView != ViewInterface {
		return "", false
	}
	iface := strings.TrimSpace(state.CurrentSub)
	if iface == "" || !isTunnelInterface(iface) {
		return "", false
	}
	head := strings.ToLower(strings.TrimSpace(args[0]))
	switch head {
	case greCmdTunnelProtocol, greCmdSource, greCmdDestination, greCmdKeepalive, greCmdGRE:
		// 命中 GRE 命令族，继续处理。
	default:
		return "", false
	}
	if !greDeviceSupported(state) {
		return errGRENotSupported(string(state.DeviceType)), true
	}
	if state.DeviceConfig == nil {
		return "", true
	}
	switch head {
	case greCmdTunnelProtocol:
		delete(state.DeviceConfig, tunnelProtocolKey(iface))
		clearGREKeys(state, iface)
		return "", true
	case greCmdSource:
		delete(state.DeviceConfig, greKey(iface, greFieldSource))
		return "", true
	case greCmdDestination:
		delete(state.DeviceConfig, greKey(iface, greFieldDestination))
		return "", true
	case greCmdKeepalive:
		// 设计 A10：枚举式删除，严禁 HasPrefix(k, "...gre-keepalive")。
		for _, field := range greKeepaliveFields {
			delete(state.DeviceConfig, greKey(iface, field))
		}
		return "", true
	case greCmdGRE:
		if len(args) < 2 {
			return errGREUnrecognized, true
		}
		switch strings.ToLower(strings.TrimSpace(args[1])) {
		case "key":
			delete(state.DeviceConfig, greKey(iface, greFieldKey))
			return "", true
		case "checksum":
			delete(state.DeviceConfig, greKey(iface, greFieldChecksum))
			return "", true
		}
		return errGREUnrecognized, true
	}
	return "", false
}

// —— 系统视图 undo interface Tunnel<x>（P1-6，改动点 #8，handled 模式）——

// applyUndoInterfaceTunnel 处理系统视图的 `undo interface Tunnel<x>`。
//
// 挂载位置：parser.go 的 `case "interface"` 分支中、`applyUndoInterfaceTrunk` 调用**之前**。
// 仅当接口名精确命中 isTunnelInterface 时 handled=true；否则交回既有聚合口分支。
//
// 🔴 零回归设计（风险 R6）：lag_cmd.go **一行不改**，Eth-Trunk / Bridge-Aggregation
// 的 undo 逻辑结构性不受影响（AC10 ③）。
//
// 清理范围：interface:<name>: 前缀的**全部键**（含 ip / status / description / gre-*）
// + state.Interfaces 条目 + 复位 CurrentSub / CurrentView。
// 幂等：接口不存在时静默成功，不报错。
func applyUndoInterfaceTunnel(state *CLIState, args []string) (string, bool) {
	if state == nil || len(args) < 2 {
		return "", false
	}
	name := strings.TrimSpace(strings.Join(args[1:], ""))
	if !isTunnelInterface(name) {
		return "", false
	}
	// 规范化到已存在的接口名大小写形态（用户可能输入 tunnel0/0/1）。
	target := name
	for existing := range state.Interfaces {
		if strings.EqualFold(existing, name) {
			target = existing
			break
		}
	}
	if state.DeviceConfig != nil {
		prefix := greIfaceKeyNamespace + target + ":"
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, prefix) {
				delete(state.DeviceConfig, k)
			}
		}
		// 兼容用户输入大小写与 DeviceConfig 中键名不一致的情况。
		if !strings.EqualFold(target, name) {
			altPrefix := greIfaceKeyNamespace + name + ":"
			for k := range state.DeviceConfig {
				if strings.HasPrefix(k, altPrefix) {
					delete(state.DeviceConfig, k)
				}
			}
		}
	}
	if state.Interfaces != nil {
		delete(state.Interfaces, target)
	}
	if strings.EqualFold(strings.TrimSpace(state.CurrentSub), target) {
		state.CurrentSub = ""
		if state.CurrentView == ViewInterface {
			state.CurrentView = ViewSystem
		}
	}
	return "", true
}
