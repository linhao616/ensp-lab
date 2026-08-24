// parser.go 负责命令行解析与分发：
//   - ParseCommand              把原始字符串拆成 Command 结构
//   - ExecuteCommand / On       命中能力校验后进入大 switch 分发
//   - GetPrompt                 根据当前视图生成华为风格提示符
//   - Serialize/Load            CLIState <-> DeviceConfigData 互转
//
// 大 switch（ExecuteCommandOn）目前仍是单函数，按协议家族做了分段注释，
// 后续可按 case 拆成子分发器。
package cli

import (
	"ensp-lab/internal/topology"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func ParseCommand(input string) *Command {
	input = sanitizeInput(input)
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}
	return &Command{Raw: input, Command: parts[0], Args: parts[1:]}
}

// sanitizeInput 在解析前剥离行首可能夹带的 [设备名] 提示符。
// 终端回显常会把提示符一起发回来，例如 "[leaf-1] [leaf-1] dis ip int"，
// 若直接按空格分词，第一个 token 会变成 "[leaf-1]"，被误当成命令而报
// "unknown command '[leaf-1]'"。这里迭代剥离所有行首的 [xxx] 片段，
// 再整体 TrimSpace，得到干净的 "dis ip int"。
func sanitizeInput(input string) string {
	re := regexp.MustCompile(`^\[[^\]]+\]\s*`)
	for {
		loc := re.FindStringIndex(input)
		if loc == nil {
			break
		}
		input = input[loc[1]:]
	}
	return strings.TrimSpace(input)
}

// fullCommandText 重组完整命令行（首 token + 参数），用于 unknown command 等
// 报错回显，避免只显示首 token 造成误导（如 dis aa 报 'dis'）。
func fullCommandText(cmd *Command) string {
	if cmd == nil {
		return ""
	}
	if len(cmd.Args) == 0 {
		return cmd.Command
	}
	return cmd.Command + " " + strings.Join(cmd.Args, " ")
}

// ExecuteCommand 在 CLI 状态机上执行一条命令。
// deviceType 标识目标设备类型，传入空字符串表示未绑定（不校验能力，保持向后兼容）。
func ExecuteCommand(state *CLIState, cmd *Command) string {
	return ExecuteCommandOn(state, cmd, state.DeviceType)
}

func ExecuteCommandWithContext(states map[string]*CLIState, state *CLIState, cmd *Command, dt topology.DeviceType, t *topology.Topology) string {
	if cmd == nil {
		return ""
	}
	command := strings.ToLower(cmd.Command)

	if command == "ping" && len(cmd.Args) > 0 {
		return executePingWithContext(states, state, cmd.Args[0], t)
	}

	return ExecuteCommandOn(state, cmd, dt)
}

func executePingWithContext(states map[string]*CLIState, state *CLIState, target string, t *topology.Topology) string {
	ifaces := getHostInterfaces(state)
	for _, iface := range ifaces {
		ip := iface["ip"]
		if idx := strings.Index(ip, "/"); idx > 0 {
			ip = ip[:idx]
		}
		if ip == target {
			return fmt.Sprintf("Ping %s: success (local interface)", target)
		}
	}

	if t != nil {
		// P1-C T03：先经 ACL 评估路径（ComputeL3Path + ResolveSourceIP + EvaluatePathACL）。
		// 命中 deny → 返回不可达(ACL 拦截)；否则回落 CheckReachability（T04 亦注入 ACL）。
		// states 为拓扑级 CLIState 注册表，使评估器能读取途径 L3/防火墙设备「自身」的 ACL
		// （修复 P1-C Round 1 中转设备 ACL 未生效）。
		if dec := aclPreCheck(states, state.DeviceID, target, t); dec.Action == "deny" {
			ruleID := 0
			if dec.Rule != nil {
				ruleID = dec.Rule.ID
			}
			return fmt.Sprintf("Ping %s: unreachable (ACL 拦截: %s acl %s rule %d, %s) %s",
				target, dec.DeviceID, dec.ACLNum, ruleID, dec.Direction, aclSimNote())
		}
		if CheckReachability(states, state, target, t) {
			return fmt.Sprintf("Ping %s: success", target)
		} else {
			return fmt.Sprintf("Ping %s: unreachable", target)
		}
	}

	return fmt.Sprintf("Ping %s: success", target)
}

func CheckReachability(states map[string]*CLIState, state *CLIState, targetIP string, t *topology.Topology) bool {
	if state.DeviceID == "" {
		return true
	}

	// P1-C T04：源设备出向 ACL 评估。命中 deny → 可达性视为不可达。
	// 源设备用其自身 state（恒正确）；途径设备按 deviceID 从注册表取「自身」state。
	srcFlow := PacketTuple{
		SrcIP: ResolveSourceIP(state, targetIP, t),
		DstIP: targetIP,
		Proto: "icmp",
	}
	if dec := EvaluateDeviceACL(state, state.DeviceID, DirOutbound, srcFlow); dec.Action == "deny" {
		return false
	}

	visited := make(map[string]bool)
	queue := []string{state.DeviceID}
	visited[state.DeviceID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if device, exists := t.Devices[current]; exists {
			if isDeviceIPMatch(device, targetIP) {
				return true
			}
		}

		for _, link := range t.Links {
			var next string
			if link.SourceDevice == current {
				next = link.TargetDevice
			} else if link.TargetDevice == current {
				next = link.SourceDevice
			} else {
				continue
			}

			if visited[next] {
				continue
			}
			// P1-C T04：报文进入 next 设备的入向 ACL 评估；命中 deny → 不可达。
			// 按 deviceID 从注册表取 next 自身 state（缺省视为未绑定 → 放行）。
			if dec := EvaluateDeviceACL(deviceStateFor(states, next), next, DirInbound, srcFlow); dec.Action == "deny" {
				return false
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}

	return false
}

// IsDeviceIPMatch 检查设备是否配置了指定 IP（导出版，供 api 包调用）。
func IsDeviceIPMatch(device *topology.Device, targetIP string) bool {
	return isDeviceIPMatch(device, targetIP)
}

func isDeviceIPMatch(device *topology.Device, targetIP string) bool {
	if device.Type == topology.DevicePC || device.Type == topology.DeviceClient || device.Type == topology.DeviceServer {
		if device.Interfaces != nil {
			for _, iface := range device.Interfaces {
				if iface.IPAddress == targetIP {
					return true
				}
			}
		}
		if device.ConfigData != nil && device.ConfigData.Interfaces != nil {
			for _, v := range device.ConfigData.Interfaces {
				if strings.Contains(v, targetIP) {
					return true
				}
			}
		}
	}
	return false
}

// buildVXLANTunnelDisplay 渲染 VXLAN 隧道信息（display vxlan tunnel）。
// 修复仓库既有未定义引用（parser.go:3717），仅做忠实展示，不改变任何 NAT/ACL 语义。
func buildVXLANTunnelDisplay(state *CLIState) string {
	if state == nil || state.VXLAN == nil {
		return "VXLAN: not configured"
	}
	enabled := "Disabled"
	if state.VXLAN.Enabled {
		enabled = "Enabled"
	}
	var b strings.Builder
	b.WriteString("VXLAN Tunnel Information:\n")
	b.WriteString("----------------------------------------------------\n")
	b.WriteString(fmt.Sprintf("VXLAN: %s\n", enabled))
	b.WriteString(fmt.Sprintf("VNI: %d\n", state.VXLAN.VNI))
	b.WriteString(fmt.Sprintf("Local VTEP IP: %s\n", state.VXLAN.VTEPIP))
	b.WriteString(fmt.Sprintf("Peer VTEP IP: %s\n", state.VXLAN.PeerVTEPIP))
	if state.VXLAN.VRFName != "" {
		b.WriteString(fmt.Sprintf("VRF: %s\n", state.VXLAN.VRFName))
	}
	if state.VXLAN.EvpnEnabled {
		b.WriteString("EVPN: Enabled\n")
	}
	if len(state.VXLAN.VSIs) > 0 {
		b.WriteString("\nVSI Information:\n")
		for name, vsi := range state.VXLAN.VSIs {
			if vsi == nil {
				continue
			}
			b.WriteString(fmt.Sprintf("  %s: VNI=%d Gateway=%s Status=%s\n", name, vsi.VNI, vsi.Gateway, vsi.Status))
		}
	}
	return b.String()
}

func ExecuteCommandOn(state *CLIState, cmd *Command, dt topology.DeviceType) string {
	if cmd == nil {
		return ""
	}

	// save 的 Y/N 确认阶段：必须在能力校验之前处理，因为确认输入（y/n）本身
	// 不是受支持命令，否则会被能力校验拦截，无法完成保存确认。
	if state.PendingSave {
		answer := strings.ToLower(strings.TrimSpace(cmd.Raw))
		if answer == "y" || answer == "yes" {
			state.PendingSave = false
			state.doSave()
			return "Now saving the current configuration to the device.\nPlease wait for a while...\nSave the configuration successfully."
		}
		if answer == "n" || answer == "no" {
			state.PendingSave = false
			return "Info: Configuration saving cancelled."
		}
		return "Error: invalid input, please enter Y or N."
	}

	command := strings.ToLower(cmd.Command)

	// 能力校验：未绑定设备类型时跳过
	if dt != "" && !isCommandSupported(command, dt) {
		// P2 #5：链路聚合命令族给出 VRP 风格专用文案（PRD AC8 / 设计 §1.7），
		// 而非通用 "command 'x' is not supported on device type" 兜底串。
		if isLAGCommandName(command) {
			return errLAGNotSupported(string(dt))
		}
		return fmt.Sprintf("Error: command '%s' is not supported on device type %q", command, dt)
	}

	switch command {
	case "system-view", "sys":
		state.CurrentView = ViewSystem
		return "Enter system view"
	case "gap":
		// 网闸配置视图入口（仅 GAP 设备；capabilities.go "gap": gapDevices() 已先行拦截）。
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if consumed, out := execGAPSystem(state, cmd); consumed {
			return out
		}
		return "Error: gap command is only supported on GAP devices"
	case "channel":
		// [dev-gap] 视图：进入摆渡通道子视图。
		if state.CurrentView == ViewGAP {
			return enterGAPChannel(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "policy":
		// [dev-gap] 视图：进入摆渡策略子视图（与 acl 等其他视图的 policy 互不干扰）。
		if state.CurrentView == ViewGAP {
			return enterGAPPolicy(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "mapping":
		if state.CurrentView == ViewGAPChannel {
			return execGAPChannelView(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "permit":
		if state.CurrentView == ViewGAPPolicy {
			return execGAPPolicyView(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "enable":
		// 多语义：STP/OSPF 等原有 enable 不受影响；仅在网闸通道/策略子视图内消费。
		if state.CurrentView == ViewGAPChannel || state.CurrentView == ViewGAPPolicy {
			return execGAPToggle(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "disable":
		if state.CurrentView == ViewGAPChannel || state.CurrentView == ViewGAPPolicy {
			return execGAPToggle(state, cmd)
		}
		return "Error: Unrecognized command found at '^' position."
	case "user-interface":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system-view"
		}
		if len(cmd.Args) < 3 {
			return "Error: usage: user-interface vty <first> <last>"
		}
		if strings.ToLower(cmd.Args[0]) != "vty" {
			return "Error: usage: user-interface vty <first> <last>"
		}
		first, err := parseNum(cmd.Args[1])
		if err != nil {
			return "Error: invalid VTY first number"
		}
		last, err := parseNum(cmd.Args[2])
		if err != nil {
			return "Error: invalid VTY last number"
		}
		if first < 0 || last < 0 || first > last || last > 15 {
			return "Error: VTY number must be between 0 and 15"
		}
		state.CurrentView = ViewVTY
		state.CurrentSub = fmt.Sprintf("vty %d %d", first, last)
		return fmt.Sprintf("Enter VTY view, user interface range %d to %d", first, last)
	case "quit", "q":
		if state.CurrentView == ViewVTY {
			state.CurrentView = ViewSystem
			state.CurrentSub = ""
		} else if state.CurrentView == ViewDHCPPool {
			state.CurrentView = ViewSystem
			state.CurrentSub = ""
		} else if state.CurrentView == ViewAAAAuthen || state.CurrentView == ViewAAADomain {
			// 🔴 AAA 嵌套子视图必须在链中**显式列出**（设计 A3 / AC1③）：
			// 本 if-else 链末尾的 else 会把任何未列出的视图兜底弹回 ViewSystem，
			// 导致 [R1-aaa-authen-sch1] quit 越级直接回到 [R1]，而真机应回 [R1-aaa]。
			state.CurrentView = ViewAAA
			state.CurrentSub = ""
		} else if state.CurrentView == ViewAAA {
			state.CurrentView = ViewSystem
			state.CurrentSub = ""
		} else if state.CurrentView == ViewGAPChannel || state.CurrentView == ViewGAPPolicy {
			// 网闸通道/策略子视图 quit 回 [dev-gap]（同 AAA 嵌套规则，必须显式列出）。
			state.CurrentView = ViewGAP
			state.CurrentSub = ""
		} else if state.CurrentView == ViewGAP {
			state.CurrentView = ViewSystem
			state.CurrentSub = ""
		} else if state.CurrentView == ViewRoutePolicy {
			// 路由策略节点视图 quit 回系统视图，并清理节点上下文指针（避免残留）。
			state.CurrentView = ViewSystem
			state.CurrentSub = ""
			state.RoutePolicyName = ""
			state.RoutePolicyNode = 0
		} else if state.CurrentView == ViewSystem {
			state.CurrentView = ViewUser
		} else {
			state.CurrentView = ViewSystem
		}
		state.CurrentSub = ""
		return "Return"
	case "return":
		state.CurrentView = ViewUser
		state.CurrentSub = ""
		return "Return to user view"
	case "save":
		// 贴近华为 eNSP / VRP：save 先弹出确认，需用户输入 Y/N 才真正落盘。
		state.PendingSave = true
		return "The current configuration will be written to the device.\nAre you sure to continue? [Y/N]"
	case "reboot":
		return "System is rebooting..."
	case "reset":
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "saved-configuration" {
			state.Saved = false
			state.SavedConfig = ""
			state.SaveTime = ""
			return "Saved configuration cleared"
		}
		return "Error: usage: reset saved-configuration"
	case "interface", "int":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system-view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need interface name"
		}
		ifName := cmd.Args[0]
		// 支持华为 VRP 风格：interface Vlanif 10 / interface GigabitEthernet 0/0/1
		// 某些接口类型（Vlanif/Vlan）名称和编号之间有空格
		vlanifPrefixes := []string{"Vlanif", "Vlan", "LoopBack", "Loop", "Serial", "Ser", "NULL", "Tunnel", "Tun", "Eth-Trunk", "Eth-trunk", "ET", "Bridge-Aggregation", "BAGG"}
		for _, prefix := range vlanifPrefixes {
			if strings.HasPrefix(strings.ToLower(ifName), strings.ToLower(prefix)) && len(cmd.Args) >= 2 {
				rest := strings.Join(cmd.Args, " ")
				// 去掉前缀部分，重新拼接
				rest = strings.TrimPrefix(rest, ifName)
				rest = strings.TrimPrefix(rest, " ")
				ifName = ifName + rest
				break
			}
		}
		// 接口名称大小写不敏感：优先在已存在接口列表中做大小写不敏感匹配，
		// 并规范化为拓扑里原始的接口名（如用户输入 10Ge5/0/1 → 映射到 10GE5/0/1），
		// 避免后续 ip address 等配置落到大小写不一致的 key 上。
		if len(state.Interfaces) > 0 {
			if canon, err := parseInterface(ifName, interfaceKeys(state.Interfaces)); err == nil {
				ifName = canon
			}
		}
		// 支持多种接口命名格式（大小写不敏感）：
		// 百兆: Ethernet/E/eth
		// 千兆: GigabitEthernet/GE/Gigabit/giga/gibit
		// 万兆: Ten-GigabitEthernet/XGE/10GE/Ten/10g/10gbit
		// 40G: FortyGigE/40GE/40g/40gbit
		// 100G: HundredGigE/100GE/100g/100gbit
		// 其他: Vlanif/Vlan, LoopBack/Loop, Eth-Trunk/Eth-trunk/ET, NULL, Serial/Ser, Tunnel/Tun, Bridge-Aggregation/BAGG
		re := regexp.MustCompile(`(?i)^(GE|GigabitEthernet|Gigabit|giga|gibit|Ethernet|E|eth|Ten-GigabitEthernet|XGE|10GE|Ten|10g|10gbit|FortyGigE|40GE|40g|40gbit|HundredGigE|100GE|100g|100gbit|LoopBack|Loop|Vlanif|Vlan|Serial|Ser|NULL|Eth-Trunk|Eth-trunk|ET|Tunnel|Tun|Bridge-Aggregation|BAGG)\d+(/\d+)*$`)
		if !re.MatchString(ifName) {
			return fmt.Sprintf("Error: invalid interface '%s'", ifName)
		}
		// Vlanif 接口仅在三层交换机、路由器、防火墙、VTEP 上支持
		if strings.HasPrefix(strings.ToLower(ifName), "vlanif") {
			isRouter := state.DeviceType == topology.DeviceRouter
			isL3Switch := state.DeviceType == topology.DeviceL3Switch
			isFirewall := state.DeviceType == topology.DeviceFirewall
			isVTEP := state.DeviceType == topology.DeviceVTEP
			if !isRouter && !isL3Switch && !isFirewall && !isVTEP {
				return fmt.Sprintf("Error: Vlanif interface is only supported on L3 Switch, Router, Firewall and VTEP")
			}
		}
		// —— 聚合口族（Eth-Trunk / ET / Bridge-Aggregation / BAGG）专用分支 ——
		// P2 #5 改动点 1（设计 §1.4）：聚合口是**逻辑口**，其 up/down 由成员实时派生，
		// 绝不能像物理口那样无条件写 :status="Up"（那是编造）。
		// ⚠️ 仅对「前缀后紧跟数字」的名字生效（isTrunkFamilyInterface），
		//    Ethernet0/0/1 等物理口不受影响，避免大范围回归。
		isTrunkIface := isTrunkFamilyInterface(ifName)
		if isTrunkIface {
			trunkID, _ := lagTrunkIDFromName(ifName)
			// 能力守卫：Eth-Trunk 仅二层/三层交换机支持
			if !lagDeviceSupported(state) {
				return errLAGNotSupported(string(state.DeviceType))
			}
			if ok, msg := validTrunkID(trunkID); !ok {
				return msg
			}
			state.CurrentView = ViewInterface
			state.CurrentSub = ifName
			// 存在标记（§1.3 存在判据的显式来源）
			if strings.EqualFold(strings.TrimSpace(ifName[:1]), "b") ||
				strings.HasPrefix(strings.ToLower(ifName), "bagg") {
				state.DeviceConfig[lagBridgeTrunkKey(trunkID, "exists")] = "true"
			} else {
				state.DeviceConfig[lagTrunkKey(trunkID, "exists")] = "true"
			}
			if state.Interfaces == nil {
				state.Interfaces = make(map[string]*InterfaceConfig)
			}
			if _, ok := state.Interfaces[ifName]; !ok {
				state.Interfaces[ifName] = &InterfaceConfig{Name: ifName}
			}
			// 状态实时派生（绝不硬编码 Up，P0-11）
			syncLAGTrunkIfaceStatus(state, trunkID)
			return "Enter interface view"
		}

		// —— Tunnel 逻辑口分支（P2 GRE，改动点 #10 / A4）——
		// Tunnel 是逻辑口，协议态（Protocol）一律 display 期派生（greLineProtocolState），
		// **绝不在此硬编码 "Protocol":"Up"**（旧基线即此缺陷，AC9③）。
		// 仅写管理态 :status="Up"（真实，shutdown 可改写）；不写 Protocol 字段。
		if isTunnelInterface(ifName) {
			state.CurrentView = ViewInterface
			state.CurrentSub = ifName
			if _, ok := state.DeviceConfig[fmt.Sprintf("interface:%s:status", ifName)]; !ok {
				state.DeviceConfig[fmt.Sprintf("interface:%s:status", ifName)] = "Up"
			}
			if state.Interfaces == nil {
				state.Interfaces = make(map[string]*InterfaceConfig)
			}
			if _, ok := state.Interfaces[ifName]; !ok {
				state.Interfaces[ifName] = &InterfaceConfig{Name: ifName, Status: "Up"}
			}
			return "Enter interface view"
		}

		state.CurrentView = ViewInterface
		state.CurrentSub = ifName
		// 初始化接口配置（如果不存在）
		if _, ok := state.DeviceConfig[fmt.Sprintf("interface:%s:status", ifName)]; !ok {
			state.DeviceConfig[fmt.Sprintf("interface:%s:status", ifName)] = "Up"
		}
		// 如果 Interfaces 中没有这个接口，添加一个
		if state.Interfaces == nil {
			state.Interfaces = make(map[string]*InterfaceConfig)
		}
		if _, ok := state.Interfaces[ifName]; !ok {
			state.Interfaces[ifName] = &InterfaceConfig{Name: ifName, Status: "Up", Protocol: "Up"}
		}
		return "Enter interface view"
	case "ip":
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "a", "addr", "address":
			arg1 := ""
			if len(cmd.Args) > 1 {
				arg1 = strings.ToLower(cmd.Args[1])
			}
			isHost := dt == topology.DevicePC ||
				dt == topology.DeviceClient ||
				dt == topology.DeviceServer

			if arg1 == "show" || arg1 == "list" || arg1 == "" {
				return buildHostIPAddr(state)
			}

			// Linux add/del 格式
			if arg1 == "add" || arg1 == "del" {
				if len(cmd.Args) >= 3 {
					ipWithMask := cmd.Args[2]
					if strings.Contains(ipWithMask, "/") {
						dev := "Ethernet0"
						if len(cmd.Args) >= 5 && strings.ToLower(cmd.Args[3]) == "dev" {
							dev = cmd.Args[4]
						}
						key := fmt.Sprintf("interface:%s:ip", dev)
						if arg1 == "add" {
							state.DeviceConfig[key] = ipWithMask
							state.DeviceConfig[fmt.Sprintf("interface:%s:mac", dev)] = "00-0C-29-01-02-03"
							state.DeviceConfig[fmt.Sprintf("interface:%s:status", dev)] = "Up"
							return fmt.Sprintf("IP address %s dev %s added", ipWithMask, dev)
						} else {
							delete(state.DeviceConfig, key)
							return fmt.Sprintf("IP address %s dev %s deleted", ipWithMask, dev)
						}
					}
				}
				return "Error: invalid syntax"
			}

			// 终端设备：ip address <IP> <MASK> 或 ip address <IP/MASK>
			if isHost && state.CurrentView == ViewUser {
				ipWithMask := cmd.Args[1]
				// 格式1: ip address 192.168.1.100/24 (CIDR)
				if strings.Contains(ipWithMask, "/") {
					parts := strings.Split(ipWithMask, "/")
					state.HostIP = parts[0]
					if len(parts) > 1 {
						state.HostSubnet = subnetFromCIDR(parts[1])
					}
					return fmt.Sprintf("Set IP address to %s", ipWithMask)
				}
				// 格式2: ip address 192.168.1.100 255.255.255.0 (双参数)
				if len(cmd.Args) >= 3 {
					state.HostIP = ipWithMask
					state.HostSubnet = cmd.Args[2]
					return fmt.Sprintf("Set IP address to %s mask %s", ipWithMask, cmd.Args[2])
				}
				return "Error: invalid syntax. Use 'ip address <IP> <MASK>' or 'ip address <IP/MASK>'"
			}

			// 华为风格：接口视图下 ip address <IP> <MASK>
			if state.CurrentView != ViewInterface {
				if isHost {
					return "Error: invalid syntax. Use 'ip address <IP> <MASK>'"
				}
				return "Error: must be in interface view"
			}
			if len(cmd.Args) < 3 {
				return "Error: invalid syntax. Use 'ip address <IP> <MASK>'"
			}
			key := fmt.Sprintf("interface:%s:ip", state.CurrentSub)
			mask := cmd.Args[2]
			// 同时更新 Interfaces map
			if state.Interfaces != nil {
				if iface, ok := state.Interfaces[state.CurrentSub]; ok {
					iface.IP = cmd.Args[1]
					iface.Mask = mask
				}
			}
			state.DeviceConfig[key] = fmt.Sprintf("%s %s", cmd.Args[1], mask)
			return fmt.Sprintf("Set IP %s mask %s on %s", cmd.Args[1], mask, state.CurrentSub)
		case "default-gateway", "gateway", "gw":
			isHost := state.DeviceType == topology.DevicePC ||
				state.DeviceType == topology.DeviceClient ||
				state.DeviceType == topology.DeviceServer
			if !isHost {
				return "Error: gateway is not supported on this device type"
			}
			if len(cmd.Args) < 2 {
				return "Error: need gateway IP"
			}
			state.DefaultGateway = cmd.Args[1]
			return fmt.Sprintf("Default gateway set to %s", cmd.Args[1])
		case "dns":
			isHost := state.DeviceType == topology.DevicePC ||
				state.DeviceType == topology.DeviceClient ||
				state.DeviceType == topology.DeviceServer
			if !isHost {
				return "Error: dns is not supported on this device type"
			}
			if len(cmd.Args) < 2 {
				return "Error: need DNS IP"
			}
			state.HostDNS = cmd.Args[1]
			return fmt.Sprintf("DNS server set to %s", cmd.Args[1])
		case "link":
			return buildHostIPLink(state)
		case "route", "r":
			return buildHostIPRoute(state)
		case "route-static":
			if state.CurrentView != ViewSystem || len(cmd.Args) < 4 {
				return "Error: invalid"
			}
			maskLen := subnetToPrefix(cmd.Args[2])
			state.Routes = append(state.Routes, &RouteEntry{
				Destination: cmd.Args[1],
				Mask:        cmd.Args[2],
				MaskLength:  maskLen,
				Protocol:    "static",
				Pre:         60,
				Cost:        0,
				Flags:       "RD",
				NextHop:     cmd.Args[3],
				Interface:   "NULL0",
			})
			return "Static route added"
		case "vpn-instance":
			if state.CurrentView != ViewSystem || len(cmd.Args) < 2 {
				return "Error: invalid"
			}
			vrfName := cmd.Args[1]
			state.VRF[vrfName] = &VRFConfig{
				RouteTargets: []string{},
				Interfaces:   []string{},
			}
			if len(cmd.Args) >= 3 {
				state.VRF[vrfName].RD = cmd.Args[2]
			}
			return fmt.Sprintf("VPN instance %s created", vrfName)
		case "routing":
			// ip routing - 启用三层路由功能
			if state.CurrentView != ViewSystem {
				return "Error: must be in system view"
			}
			state.IPRouting = true
			return "IP routing enabled"
		}
	case "port":
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		// 规范化第一个参数（支持缩写）
		portCmd := strings.ToLower(cmd.Args[0])
		switch {
		case portCmd == "link-type" || (strings.HasPrefix("link-type", portCmd) && len(portCmd) >= 3):
			// port link-type access/trunk/hybrid
			if len(cmd.Args) < 2 {
				return "Error: need link type (access/trunk/hybrid)"
			}
			linkType := strings.ToLower(cmd.Args[1])
			if linkType != "access" && linkType != "trunk" && linkType != "hybrid" {
				return "Error: invalid link type"
			}
			state.DeviceConfig[fmt.Sprintf("interface:%s:port-link-type", state.CurrentSub)] = linkType
			return fmt.Sprintf("Port link-type set to %s", linkType)
		case portCmd == "default" || (strings.HasPrefix("default", portCmd) && len(portCmd) >= 2):
			// port default vlan <id>
			if len(cmd.Args) < 3 || strings.ToLower(cmd.Args[1]) != "vlan" {
				return "Error: usage: port default vlan <vlan-id>"
			}
			vlanID, err := parseNum(cmd.Args[2])
			if err != nil {
				return "Error: invalid VLAN ID"
			}
			state.DeviceConfig[fmt.Sprintf("interface:%s:port-default-vlan", state.CurrentSub)] = fmt.Sprintf("%d", vlanID)
			// 同时更新 VLAN 配置
			if state.VLANs == nil {
				state.VLANs = make(map[int]*VLANConfig)
			}
			if _, ok := state.VLANs[vlanID]; !ok {
				state.VLANs[vlanID] = &VLANConfig{ID: vlanID, Name: fmt.Sprintf("VLAN%d", vlanID), Status: "Up", Ports: []string{}}
			}
			// 添加端口到 VLAN
			found := false
			for _, p := range state.VLANs[vlanID].Ports {
				if p == state.CurrentSub {
					found = true
					break
				}
			}
			if !found {
				state.VLANs[vlanID].Ports = append(state.VLANs[vlanID].Ports, state.CurrentSub)
			}
			return fmt.Sprintf("Port default VLAN set to %d", vlanID)
		case portCmd == "trunk" || (strings.HasPrefix("trunk", portCmd) && len(portCmd) >= 2):
			// port trunk allow-pass vlan / port trunk pvid vlan
			if len(cmd.Args) < 2 {
				return "Error: need trunk subcommand"
			}
			trunkCmd := strings.ToLower(cmd.Args[1])
			switch {
			case trunkCmd == "allow-pass" || (strings.HasPrefix("allow-pass", trunkCmd) && len(trunkCmd) >= 3):
				// port trunk allow-pass vlan <id-list>
				if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[2]) != "vlan" {
					return "Error: usage: port trunk allow-pass vlan <vlan-list>"
				}
				vlanList := cmd.Args[3:]
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-trunk-allow-vlan", state.CurrentSub)] = strings.Join(vlanList, " ")
				// 更新 VLAN 配置
				if state.VLANs == nil {
					state.VLANs = make(map[int]*VLANConfig)
				}
				for _, v := range vlanList {
					vid, err := parseNum(v)
					if err == nil {
						if _, ok := state.VLANs[vid]; !ok {
							state.VLANs[vid] = &VLANConfig{ID: vid, Name: fmt.Sprintf("VLAN%d", vid), Status: "Up", Ports: []string{}}
						}
						found := false
						for _, p := range state.VLANs[vid].Ports {
							if p == state.CurrentSub {
								found = true
								break
							}
						}
						if !found {
							state.VLANs[vid].Ports = append(state.VLANs[vid].Ports, state.CurrentSub)
						}
					}
				}
				return fmt.Sprintf("Port trunk allow-pass VLAN: %s", strings.Join(vlanList, " "))
			case trunkCmd == "pvid":
				// port trunk pvid vlan <id>
				if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[2]) != "vlan" {
					return "Error: usage: port trunk pvid vlan <vlan-id>"
				}
				vlanID, err := parseNum(cmd.Args[3])
				if err != nil {
					return "Error: invalid VLAN ID"
				}
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-trunk-pvid", state.CurrentSub)] = fmt.Sprintf("%d", vlanID)
				return fmt.Sprintf("Port trunk PVID set to %d", vlanID)
			case trunkCmd == "permit":
				// H3C: port trunk permit vlan <id-list>
				if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[2]) != "vlan" {
					return "Error: usage: port trunk permit vlan <vlan-list>"
				}
				vlanList := cmd.Args[3:]
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-trunk-allow-vlan", state.CurrentSub)] = strings.Join(vlanList, " ")
				return fmt.Sprintf("Port trunk permit VLAN: %s", strings.Join(vlanList, " "))
			default:
				return fmt.Sprintf("Error: unknown trunk command '%s'", trunkCmd)
			}
		case portCmd == "hybrid":
			// port hybrid tagged vlan <list> / port hybrid untagged vlan <list>
			if len(cmd.Args) < 3 {
				return "Error: usage: port hybrid tagged|untagged vlan <vlan-list>"
			}
			hybridType := strings.ToLower(cmd.Args[1])
			if hybridType != "tagged" && hybridType != "untagged" {
				return "Error: usage: port hybrid tagged|untagged vlan <vlan-list>"
			}
			if strings.ToLower(cmd.Args[2]) != "vlan" {
				return "Error: usage: port hybrid tagged|untagged vlan <vlan-list>"
			}
			vlanList := cmd.Args[3:]
			if len(vlanList) == 0 {
				return "Error: usage: port hybrid tagged|untagged vlan <vlan-list>"
			}
			key := fmt.Sprintf("interface:%s:port-hybrid-%s-vlan", state.CurrentSub, hybridType)
			state.DeviceConfig[key] = strings.Join(vlanList, " ")
			// 更新 VLAN 配置
			if state.VLANs == nil {
				state.VLANs = make(map[int]*VLANConfig)
			}
			for _, v := range vlanList {
				vid, err := parseNum(v)
				if err == nil {
					if _, ok := state.VLANs[vid]; !ok {
						state.VLANs[vid] = &VLANConfig{ID: vid, Name: fmt.Sprintf("VLAN%d", vid), Status: "Up", Ports: []string{}}
					}
					found := false
					for _, p := range state.VLANs[vid].Ports {
						if p == state.CurrentSub {
							found = true
							break
						}
					}
					if !found {
						state.VLANs[vid].Ports = append(state.VLANs[vid].Ports, state.CurrentSub)
					}
				}
			}
			return fmt.Sprintf("Port hybrid %s VLAN: %s", hybridType, strings.Join(vlanList, " "))
		case portCmd == "link-aggregation":
			// H3C: port link-aggregation group <id> / port link-agg group <id>
			// P2 #5 改动点 2（设计 §1.4）：复用 P0-9 五项校验，只写 :eth-trunk + agg-family="h3c"，
			// **删除 :status / :members 双写**——这是幽灵 Bridge-Aggregation 组的根因之一（拍板 #4）。
			return applyH3CPortLinkAggregationGroup(state, cmd.Args)
		case portCmd == "m-lag":
			// H3C: port m-lag peer-link <id> / port m-lag group <id>
			if len(cmd.Args) < 2 {
				return "Error: usage: port m-lag <peer-link|group> <id>"
			}
			subCmd := strings.ToLower(cmd.Args[1])
			if subCmd == "peer-link" {
				if len(cmd.Args) >= 3 {
					peerID, _ := parseNum(cmd.Args[2])
					state.DeviceConfig[fmt.Sprintf("interface:%s:m-lag-peer-link", state.CurrentSub)] = fmt.Sprintf("%d", peerID)
					return fmt.Sprintf("M-LAG peer-link %d configured", peerID)
				}
				state.DeviceConfig[fmt.Sprintf("interface:%s:m-lag-peer-link", state.CurrentSub)] = "1"
				return "M-LAG peer-link configured"
			} else if subCmd == "group" {
				if len(cmd.Args) >= 3 {
					groupID, _ := parseNum(cmd.Args[2])
					state.DeviceConfig[fmt.Sprintf("interface:%s:m-lag-group", state.CurrentSub)] = fmt.Sprintf("%d", groupID)
					return fmt.Sprintf("M-LAG group %d configured", groupID)
				}
				return "Error: need group ID"
			}
			return "Error: invalid m-lag subcommand"
		case portCmd == "link-mode":
			// H3C: port link-mode route / port link-mode bridge
			if len(cmd.Args) < 2 {
				return "Error: usage: port link-mode <route|bridge>"
			}
			state.DeviceConfig[fmt.Sprintf("interface:%s:link-mode", state.CurrentSub)] = cmd.Args[1]
			return fmt.Sprintf("Port link-mode set to %s", cmd.Args[1])
		case portCmd == "security":
			// 复用顶层 port-security 逻辑的 helper（保证行为一致）：
			// port security <sub> 中 cmd.Args[0]=="security"，子命令从 cmd.Args[1:] 取。
			return applyPortSecurity(state, cmd.Args[1:])
		default:
			return fmt.Sprintf("Error: unknown port command '%s'", portCmd)
		}
	case "eth-trunk":
		// 物理接口视图下加入聚合组（P2 #5 改动点 3）。
		// 重写为 applyEthTrunkMember：P0-9 五项校验 + 仅写 :eth-trunk / :agg-family 单一事实源，
		// **删除 :status="Up" 硬编码与 :members 逗号串双写**。
		if state.CurrentView != ViewInterface || len(cmd.Args) == 0 {
			return "Error: must be in physical interface view, usage: eth-trunk <id>"
		}
		if !lagDeviceSupported(state) {
			return errLAGNotSupported(string(state.DeviceType))
		}
		trunkID, err := parseNum(cmd.Args[0])
		if err != nil {
			return errLAGInvalidTrunkID
		}
		return applyEthTrunkMember(state, state.CurrentSub, trunkID, aggFamilyHuawei)
	case "mode":
		// 聚合口视图 mode { manual load-balance | lacp-static }（P2 #5 改动点 4）。
		// 两 token 整体识别 + 枚举校验 + 写 :lag:mode；
		// 设备能力守卫在 applyLAGMode **分支内**完成（设计 §1.7：mode 不入顶层能力矩阵）。
		return applyLAGMode(state, cmd.Args)
	case "trunkport":
		// 聚合口视图 trunkport <type> <num> [to <num>]（P2 #5 改动点 5）。
		// 官方 to 语法 + 区间展开（仅末段可变、≤8）+ 复用 P0-9 校验。
		return applyLAGTrunkport(state, cmd.Args)
	case "load-balance":
		// 聚合口视图 load-balance <六值枚举>（P2 #5 改动点 6）。
		return applyLAGLoadBalance(state, cmd.Args)
	case "least", "max":
		// 聚合口视图 least|max active-linknumber <1-8>（P0-14）。
		return applyLAGLinkNumber(state, command, cmd.Args)
	case "link-aggregation":
		// H3C 聚合口视图 link-aggregation mode { static | dynamic }（P2 #5 改动点 7）。
		return applyH3CLinkAggregationMode(state, cmd.Args)
	case "description":
		// 设置接口描述
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		desc := strings.Join(cmd.Args, " ")
		state.DeviceConfig[fmt.Sprintf("interface:%s:description", state.CurrentSub)] = desc
		return fmt.Sprintf("Description set to '%s'", desc)
	case "shutdown":
		// 关闭接口
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		// 同时写 DeviceConfig（持久化）与 Interfaces map（display 回显），
		// 否则 display ip interface 的 Interfaces map 合并会覆盖 status。
		state.DeviceConfig[fmt.Sprintf("interface:%s:status", state.CurrentSub)] = "Down"
		if iface, ok := state.Interfaces[state.CurrentSub]; ok {
			iface.Status = "Down"
		}
		return "Interface is shutdown"
	case "undo":
		// 华为 VRP 风格 undo 命令体系。按当前视图分发：
		//   - 接口视图：反向操作 shutdown / ip address / description（既有行为不变）；
		//   - 系统视图：反向清理各协议/特性 state（P1-F，T04 扩展）；
		//   - 其它视图：维持原 "must be in interface view" 提示，避免静默吞掉未知 undo。
		switch state.CurrentView {
		case ViewInterface:
			if len(cmd.Args) == 0 {
				return "Error: incomplete command"
			}
			sub := strings.ToLower(strings.Join(cmd.Args, " "))
			if strings.HasPrefix(sub, "vrrp") {
				// undo vrrp vrid <id> [virtual-ip <ip>]（P1，T04）。
				return applyUndoVRRP(state, cmd.Args)
			}
			// undo eth-trunk / trunkport / mode / load-balance / least|max / lacp / port link-aggregation
			// （P2 #5，T02/T04：链路聚合接口视图 undo 语义）。
			if msg, handled := applyUndoLAGInterface(state, cmd.Args); handled {
				return msg
			}
			// undo dhcp select / undo dhcp relay ...（P2 #6，T2：DHCP 中继接口视图 undo 语义）。
			// handled 模式：未命中 dhcp 前缀时交回下方既有 undo 分支，零回归。
			if msg, handled := applyUndoDHCPInterface(state, cmd.Args); handled {
				return msg
			}
			// undo tunnel-protocol / undo source / undo destination / undo gre key /
			// undo gre checksum / undo keepalive（P2 第七项，T2：GRE 接口视图 undo 语义）。
			// handled 模式：未命中 GRE 前缀时交回下方既有 undo 分支，零回归。
			if msg, handled := applyUndoGREInterface(state, cmd.Args); handled {
				return msg
			}
			// undo ipv6 enable/address | undo ripng <pid> enable | undo ospfv3 <pid> area
			// （P2 第九项，T04：接口视图 IPv6/RIPng/OSPFv3 undo 级联）。
			// handled 模式：未命中交回下方既有 undo 分支，零回归（AC10 ⑤）。
			if msg, handled := applyUndoIPv6Interface(state, cmd.Args); handled {
				return msg
			}
			// undo delay / undo loss（v0.12 链路质量模拟，T2）。
			// handled 模式：未命中交回下方既有 undo 分支，零回归。
			if msg, handled := applyUndoLinkQuality(state, cmd.Args); handled {
				return msg
			}
			switch sub {
			case "shutdown":
				// 开启接口：与 shutdown 一致，同步 DeviceConfig 与 Interfaces map。
				state.DeviceConfig[fmt.Sprintf("interface:%s:status", state.CurrentSub)] = "Up"
				if iface, ok := state.Interfaces[state.CurrentSub]; ok {
					iface.Status = "Up"
				}
				return "Interface is up"
			case "ip address":
				// 删除接口 IP：同时清 DeviceConfig 与 Interfaces map，
				// 保证 display ip interface 立即回显 unassigned。
				key := fmt.Sprintf("interface:%s:ip", state.CurrentSub)
				delete(state.DeviceConfig, key)
				if iface, ok := state.Interfaces[state.CurrentSub]; ok {
					iface.IP = ""
					iface.Mask = ""
				}
				return fmt.Sprintf("Interface %s IP address deleted", state.CurrentSub)
			case "description":
				// 删除接口描述
				key := fmt.Sprintf("interface:%s:description", state.CurrentSub)
				delete(state.DeviceConfig, key)
				if iface, ok := state.Interfaces[state.CurrentSub]; ok {
					iface.Description = ""
				}
				return fmt.Sprintf("Description of %s deleted", state.CurrentSub)
			default:
				return fmt.Sprintf("Error: undo '%s' is not supported", sub)
			}
		case ViewSystem:
			if len(cmd.Args) == 0 {
				return "Error: incomplete command"
			}
			return applyUndoSystemFeature(state, cmd.Args)
		case ViewAAA, ViewAAAAuthen, ViewAAADomain:
			// undo local-user / undo {authentication|authorization|accounting}-scheme /
			// undo domain / undo state（P2 第八项，T5：AAA 视图 undo 语义）。
			// handled 模式：未命中 AAA 命令族时落到下方统一的 not supported 文案，
			// 与其它视图口径一致，零回归。
			if len(cmd.Args) == 0 {
				return "Error: incomplete command"
			}
			if msg, handled := applyUndoAAAInView(state, cmd.Args); handled {
				return msg
			}
			return fmt.Sprintf("Error: undo '%s' is not supported", strings.ToLower(strings.Join(cmd.Args, " ")))
		default:
			return "Error: must be in interface view"
		}
	case "speed":
		// 设置接口速率
		if state.CurrentView != ViewInterface || len(cmd.Args) == 0 {
			return "Error: must be in interface view, usage: speed 1000"
		}
		speed := cmd.Args[0]
		state.DeviceConfig[fmt.Sprintf("interface:%s:speed", state.CurrentSub)] = speed
		return fmt.Sprintf("Speed set to %s", speed)
	case "duplex":
		// 设置双工模式
		if state.CurrentView != ViewInterface || len(cmd.Args) == 0 {
			return "Error: must be in interface view, usage: duplex full|half|auto"
		}
		duplex := strings.ToLower(cmd.Args[0])
		state.DeviceConfig[fmt.Sprintf("interface:%s:duplex", state.CurrentSub)] = duplex
		return fmt.Sprintf("Duplex set to %s", duplex)
	case "mtu":
		// 设置 MTU
		if state.CurrentView != ViewInterface || len(cmd.Args) == 0 {
			return "Error: must be in interface view, usage: mtu 1500"
		}
		mtu := cmd.Args[0]
		state.DeviceConfig[fmt.Sprintf("interface:%s:mtu", state.CurrentSub)] = mtu
		return fmt.Sprintf("MTU set to %s", mtu)
	case "delay":
		// 设置接口单向链路时延（v0.12 链路质量模拟，仿真扩展命令）。
		// 写入 DeviceConfig 后由 api 层同步到 topology.Link.Delay 并触发引擎重建。
		return applyLinkDelay(state, cmd.Args)
	case "loss":
		// 设置接口单向链路丢包率（v0.12 链路质量模拟，仿真扩展命令）。
		return applyLinkLoss(state, cmd.Args)
	case "acl":
		if state.CurrentView != ViewSystem || len(cmd.Args) == 0 {
			return "Error: invalid"
		}
		// 检查是否是命名 ACL: acl name <name> basic|advanced
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[0]) == "name" {
			aclName := cmd.Args[1]
			aclType := strings.ToLower(cmd.Args[2])
			if aclType != "basic" && aclType != "advanced" {
				return "Error: ACL type must be basic or advanced"
			}
			if _, ok := state.ACLs[aclName]; !ok {
				state.ACLs[aclName] = []*ACLRule{}
			}
			state.CurrentView = ViewACL
			state.CurrentSub = aclName
			// 保存 ACL 类型
			if state.ACLs[aclName] == nil || len(state.ACLs[aclName]) == 0 {
				state.ACLs[aclName] = []*ACLRule{{Name: aclName, Type: aclType}}
			}
			return fmt.Sprintf("Enter ACL view, name %s, type %s", aclName, aclType)
		}
		// 数字 ACL: acl <num>
		aclNum := cmd.Args[0]
		if _, ok := state.ACLs[aclNum]; !ok {
			state.ACLs[aclNum] = []*ACLRule{}
		}
		state.CurrentView = ViewACL
		state.CurrentSub = aclNum
		return "Enter ACL view"
	case "rule":
		if state.CurrentView != ViewACL || len(cmd.Args) < 3 {
			return "Error: invalid"
		}
		ruleID := 0
		idx := 0
		if n, err := parseNum(cmd.Args[0]); err == nil {
			ruleID = n
			idx = 1
		}
		action := strings.ToLower(cmd.Args[idx])
		proto := strings.ToLower(cmd.Args[idx+1])
		rule := &ACLRule{ID: ruleID, Action: action, Protocol: proto}
		for i := idx + 2; i < len(cmd.Args); i += 2 {
			if i+1 >= len(cmd.Args) {
				break
			}
			key := strings.ToLower(cmd.Args[i])
			val := cmd.Args[i+1]
			switch key {
			case "source", "src":
				rule.SrcIP = val
				if i+2 < len(cmd.Args) && !isKeyword(cmd.Args[i+2]) {
					rule.SrcWildcard = cmd.Args[i+2]
					i++
				}
			case "destination", "dest":
				rule.DstIP = val
				if i+2 < len(cmd.Args) && !isKeyword(cmd.Args[i+2]) {
					rule.DstWildcard = cmd.Args[i+2]
					i++
				}
			}
		}
		if rule.ID == 0 {
			rule.ID = len(state.ACLs[state.CurrentSub])*10 + 10
		}
		state.ACLs[state.CurrentSub] = append(state.ACLs[state.CurrentSub], rule)
		return fmt.Sprintf("Rule %d %s %s added", rule.ID, action, proto)
	case "nat":
		// NAT 命令处理
		if state.CurrentView != ViewSystem && state.CurrentView != ViewInterface {
			return "Error: must be in system view or interface view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need NAT arguments"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "address-group":
			// nat address-group <id> <start-ip> <end-ip>
			if len(cmd.Args) < 4 {
				return "Error: usage: nat address-group <id> <start-ip> <end-ip>"
			}
			id, err := parseNum(cmd.Args[1])
			if err != nil {
				return "Error: invalid address-group ID"
			}
			startIP := cmd.Args[2]
			endIP := cmd.Args[3]
			if state.NAT == nil {
				state.NAT = &NATConfig{}
			}
			state.NAT.Enabled = true
			// 检查是否已存在该地址池
			found := false
			for i := range state.NAT.AddressPools {
				if state.NAT.AddressPools[i].ID == id {
					state.NAT.AddressPools[i].StartIP = startIP
					state.NAT.AddressPools[i].EndIP = endIP
					found = true
					break
				}
			}
			if !found {
				state.NAT.AddressPools = append(state.NAT.AddressPools, NATAddressPool{
					ID:      id,
					StartIP: startIP,
					EndIP:   endIP,
				})
			}
			return fmt.Sprintf("NAT address-group %d: %s - %s configured", id, startIP, endIP)
		case "outbound":
			// nat outbound <acl-num> [address-group <id>] 或 nat outbound <acl-num> (Easy IP)
			if len(cmd.Args) < 2 {
				return "Error: usage: nat outbound <acl-num> [address-group <id>]"
			}
			aclNum := 0
			var aclName string
			if n, err := parseNum(cmd.Args[1]); err == nil {
				aclNum = n
			} else {
				aclName = cmd.Args[1]
			}
			natType := "easy-ip"
			addrPool := 0
			// 检查是否有 address-group
			for i := 2; i < len(cmd.Args); i++ {
				if strings.ToLower(cmd.Args[i]) == "address-group" && i+1 < len(cmd.Args) {
					if n, err := parseNum(cmd.Args[i+1]); err == nil {
						addrPool = n
						natType = "address-group"
						break
					}
				}
			}
			if state.NAT == nil {
				state.NAT = &NATConfig{}
			}
			state.NAT.Enabled = true
			state.NAT.Outbounds = append(state.NAT.Outbounds, NATOutbound{
				ACLNum:      aclNum,
				ACLName:     aclName,
				AddressPool: addrPool,
				Type:        natType,
			})
			if natType == "easy-ip" {
				return fmt.Sprintf("NAT outbound %s configured (Easy IP)", cmd.Args[1])
			}
			return fmt.Sprintf("NAT outbound %s address-group %d configured", cmd.Args[1], addrPool)
		case "server":
			// nat server global <ip> <port> protocol <proto> inside <ip> <port>
			// 这是一个简化的处理
			if len(cmd.Args) < 8 {
				return "Error: usage: nat server global <ip> <port> protocol <proto> inside <ip> <port>"
			}
			globalIP := cmd.Args[2]
			globalPort := cmd.Args[3]
			protocol := strings.ToLower(cmd.Args[5])
			insideIP := cmd.Args[7]
			insidePort := ""
			if len(cmd.Args) > 8 {
				insidePort = cmd.Args[8]
			}
			if state.NAT == nil {
				state.NAT = &NATConfig{}
			}
			state.NAT.Enabled = true
			state.NAT.Servers = append(state.NAT.Servers, NATServer{
				GlobalIP:   globalIP,
				GlobalPort: globalPort,
				Protocol:   protocol,
				InsideIP:   insideIP,
				InsidePort: insidePort,
			})
			return fmt.Sprintf("NAT server %s:%s -> %s:%s %s configured", globalIP, globalPort, insideIP, insidePort, protocol)
		default:
			return fmt.Sprintf("Error: unknown NAT command '%s'", subCmd)
		}
	case "sysname":
		if state.CurrentView != ViewSystem || len(cmd.Args) == 0 {
			return "Error: invalid"
		}
		state.DeviceConfig["sysname"] = cmd.Args[0]
		return fmt.Sprintf("Sysname set to '%s'", cmd.Args[0])
	case "vlan":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "batch":
			// vlan batch 10 20 30 或 vlan batch 10 to 20
			vlans := []string{}
			i := 1
			for i < len(cmd.Args) {
				v := cmd.Args[i]
				if strings.ToLower(v) == "to" {
					i++
					continue
				}
				// 检查是否是范围格式 (例如 10 to 20)
				if i+2 < len(cmd.Args) && strings.ToLower(cmd.Args[i+1]) == "to" {
					start, err1 := parseNum(v)
					end, err2 := parseNum(cmd.Args[i+2])
					if err1 == nil && err2 == nil && start <= end {
						for vid := start; vid <= end; vid++ {
							vlans = append(vlans, fmt.Sprintf("%d", vid))
							if _, ok := state.VLANs[vid]; !ok {
								state.VLANs[vid] = &VLANConfig{ID: vid, Name: fmt.Sprintf("VLAN%d", vid), Status: "Up", Ports: []string{}}
							}
						}
						i += 3
						continue
					}
				}
				// 单个 VLAN
				vlans = append(vlans, v)
				if vid, err := parseNum(v); err == nil {
					if _, ok := state.VLANs[vid]; !ok {
						state.VLANs[vid] = &VLANConfig{ID: vid, Name: fmt.Sprintf("VLAN%d", vid), Status: "Up", Ports: []string{}}
					}
				}
				i++
			}
			return fmt.Sprintf("VLANs created: %s", strings.Join(vlans, ", "))
		default:
			vlanID := cmd.Args[0]
			if vid, err := parseNum(vlanID); err == nil {
				state.VLANs[vid] = &VLANConfig{ID: vid, Name: fmt.Sprintf("VLAN%d", vid), Status: "Up", Ports: []string{}}
			}
			return fmt.Sprintf("VLAN %s created", vlanID)
		}
	case "ping":
		if len(cmd.Args) == 0 {
			return "Error: need target"
		}
		target := cmd.Args[0]
		ifaces := getHostInterfaces(state)
		for _, iface := range ifaces {
			ip := iface["ip"]
			if idx := strings.Index(ip, "/"); idx > 0 {
				ip = ip[:idx]
			}
			if ip == target {
				return fmt.Sprintf("Ping %s: success (local interface)", target)
			}
		}
		return fmt.Sprintf("Ping %s: success", target)
	case "tracert", "traceroute":
		// P1-F，T07（风险1）：优先通过 CLIState 引擎钩子走真实路径；
		// 无引擎上下文时返回与 FormatEngineTraceroute(nil,...) 一致的提示，
		// 不再硬编码固定 2 跳，也不 panic（真实路径仍由 API 主路径承担）。
		if len(cmd.Args) == 0 {
			return "Error: need target"
		}
		target := cmd.Args[0]
		if state.ResolveTraceroute != nil {
			res := state.ResolveTraceroute(target)
			return RenderTracerouteWithACL(nil, state, res, target, 30, nil)
		}
		return RenderTracerouteWithACL(nil, state, nil, target, 30, nil)
	case "ipconfig":
		if len(cmd.Args) >= 1 {
			sub := strings.ToLower(cmd.Args[0])
			switch sub {
			case "/set", "set":
				if len(cmd.Args) < 2 {
					return "Usage: ipconfig /set <ip-address> [subnet-mask] [default-gateway]"
				}
				ip, mask := parseIPFormat(cmd.Args[1])
				state.HostIP = ip
				if mask != "" {
					state.HostSubnet = mask
				}
				if len(cmd.Args) >= 3 {
					state.HostSubnet = normalizeMask(cmd.Args[2])
				}
				if len(cmd.Args) >= 4 {
					state.DefaultGateway = cmd.Args[3]
				}
				return fmt.Sprintf("IP 地址已设置为 %s/%s%s\n", state.HostIP, state.HostSubnet, gwNote(state.DefaultGateway)) + buildHostIfconfig(state)
			case "/ip":
				if len(cmd.Args) >= 2 {
					state.HostIP = cmd.Args[1]
				}
				if len(cmd.Args) >= 4 && strings.ToLower(cmd.Args[2]) == "/mask" {
					state.HostSubnet = cmd.Args[3]
				}
				return buildHostIfconfig(state)
			case "/release":
				state.HostIP = ""
				state.HostSubnet = ""
				return "Ethernet0: IP 地址已释放 (DHCP Release)\n" + buildHostIfconfig(state)
			case "/renew":
				state.HostIP = ""
				state.HostSubnet = ""
				return "Ethernet0: IP 地址已续租 (DHCP Renew)\n" + buildHostIfconfig(state)
			case "/all":
				return buildHostIfconfig(state)
			}
		}
		return buildHostIfconfig(state)
	case "netsh":
		return executeNetsh(state, cmd.Args)
	case "ifconfig":
		return buildHostIfconfig(state)
	case "arp", "arp -a":
		return buildHostARPTable(state)
	case "netstat":
		return buildHostNetstat(state)
	case "?", "help":
		return "Help: system-view, interface, ip address, acl, rule, display, quit, ospf, m-lag, lldp, stp, vrrp, ipsec, snmp, syslog, ntp, ssh, vxlan, bgp, ipconfig, ipconfig /set, ip address, ip gateway, ip dns, netsh"
	case "ip address":
		if len(cmd.Args) < 1 {
			return "Usage: ip address <ip-address> [subnet-mask]"
		}
		state.HostIP = cmd.Args[0]
		if len(cmd.Args) >= 2 {
			state.HostSubnet = cmd.Args[1]
		}
		return fmt.Sprintf("IP address set to %s/%s", state.HostIP, state.HostSubnet)
	case "ip gateway":
		if len(cmd.Args) < 1 {
			return "Usage: ip gateway <gateway-ip>"
		}
		state.DefaultGateway = cmd.Args[0]
		return fmt.Sprintf("Default gateway set to %s", state.DefaultGateway)
	case "ip dns":
		if len(cmd.Args) < 1 {
			return "Usage: ip dns <dns-ip>"
		}
		state.HostDNS = cmd.Args[0]
		return fmt.Sprintf("DNS server set to %s", state.HostDNS)
	case "traffic-filter":
		// traffic-filter inbound/outbound acl <num>
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "acl" {
			direction := cmd.Args[0]
			aclNum := cmd.Args[2]
			key := fmt.Sprintf("traffic-filter:%s:%s", direction, aclNum)
			state.DeviceConfig[key] = aclNum
			return fmt.Sprintf("Traffic filter %s ACL %s applied", direction, aclNum)
		}
		// 兼容旧格式: traffic-filter acl <num>
		if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[0]) == "acl" {
			key := fmt.Sprintf("traffic-filter:inbound:%s", cmd.Args[1])
			state.DeviceConfig[key] = cmd.Args[1]
			return fmt.Sprintf("Traffic filter inbound ACL %s applied", cmd.Args[1])
		}
	case "m-lag", "mlag":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "domain":
			if len(cmd.Args) < 2 {
				return "Error: need domain id"
			}
			domainID, err := parseNum(cmd.Args[1])
			if err != nil {
				return "Error: invalid domain id"
			}
			state.MLAG.DomainID = domainID
			state.CurrentView = ViewMLAG
			state.CurrentSub = "domain"
			return fmt.Sprintf("Enter M-LAG domain %d view", domainID)
		case "system-mac":
			if len(cmd.Args) < 2 {
				return "Error: need MAC address"
			}
			state.MLAG.SystemMAC = cmd.Args[1]
			return fmt.Sprintf("M-LAG system MAC set to %s", cmd.Args[1])
		case "system-number":
			if len(cmd.Args) < 2 {
				return "Error: need system number"
			}
			num, err := parseNum(cmd.Args[1])
			if err != nil {
				return "Error: invalid system number"
			}
			state.MLAG.SystemNumber = num
			return fmt.Sprintf("M-LAG system number set to %d", num)
		case "system-priority":
			if len(cmd.Args) < 2 {
				return "Error: need system priority"
			}
			prio, err := parseNum(cmd.Args[1])
			if err != nil {
				return "Error: invalid system priority"
			}
			state.MLAG.SystemPriority = prio
			return fmt.Sprintf("M-LAG system priority set to %d", prio)
		case "keepalive":
			if len(cmd.Args) < 5 {
				return "Error: usage: m-lag keepalive ip destination <ip> source <ip>"
			}
			var dstIP, srcIP string
			for i := 1; i < len(cmd.Args); i++ {
				if strings.ToLower(cmd.Args[i]) == "destination" && i+1 < len(cmd.Args) {
					dstIP = cmd.Args[i+1]
				}
				if strings.ToLower(cmd.Args[i]) == "source" && i+1 < len(cmd.Args) {
					srcIP = cmd.Args[i+1]
				}
			}
			if dstIP == "" || srcIP == "" {
				return "Error: need destination and source IP"
			}
			state.MLAG.KeepaliveDest = dstIP
			state.MLAG.KeepaliveSrc = srcIP
			return fmt.Sprintf("M-LAG keepalive configured: source %s, destination %s", srcIP, dstIP)
		case "mad":
			if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[1]) != "exclude" || strings.ToLower(cmd.Args[2]) != "int" {
				return "Error: usage: m-lag mad exclude interface <iface>"
			}
			iface := cmd.Args[3]
			state.MLAG.MADExclude = append(state.MLAG.MADExclude, iface)
			return fmt.Sprintf("Interface %s excluded from MAD", iface)
		}
	case "dfs-group":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: dfs-group <id> [m-lag <m-lag-id>|priority <prio>|source <ip> peer <ip>]"
		}
		dfsID, err := parseNum(cmd.Args[0])
		if err != nil {
			return "Error: invalid DFS group ID"
		}
		if state.MLAG.DFSGroup == nil {
			state.MLAG.DFSGroup = make(map[int]*DFSGroupConfig)
		}
		if _, ok := state.MLAG.DFSGroup[dfsID]; !ok {
			state.MLAG.DFSGroup[dfsID] = &DFSGroupConfig{ID: dfsID, Enabled: true}
		}
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "m-lag" {
			mlagID, _ := parseNum(cmd.Args[2])
			state.MLAG.DFSGroup[dfsID].MLAGID = mlagID
			return fmt.Sprintf("DFS Group %d M-LAG %d configured", dfsID, mlagID)
		}
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "priority" {
			prio, _ := parseNum(cmd.Args[2])
			state.MLAG.DFSGroup[dfsID].Priority = prio
			return fmt.Sprintf("DFS Group %d priority set to %d", dfsID, prio)
		}
		if len(cmd.Args) >= 5 && strings.ToLower(cmd.Args[1]) == "source" && strings.ToLower(cmd.Args[3]) == "peer" {
			state.MLAG.DFSGroup[dfsID].SourceIP = cmd.Args[2]
			state.MLAG.DFSGroup[dfsID].PeerIP = cmd.Args[4]
			return fmt.Sprintf("DFS Group %d source %s peer %s configured", dfsID, cmd.Args[2], cmd.Args[4])
		}
		state.MLAG.DFSGroupID = dfsID
		return fmt.Sprintf("DFS Group %d created", dfsID)
	case "ospf":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 {
			processID, err := parseNum(cmd.Args[0])
			if err == nil {
				state.OSPF.Enabled = true
				state.OSPF.ProcessID = processID
				if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "area" {
					areaID, _ := parseNum(cmd.Args[2])
					state.OSPF.AreaID = areaID
					state.DeviceConfig["ospf:area-id"] = fmt.Sprintf("%d", areaID)
				}
				// 镜像到 DeviceConfig 以便随拓扑 save/reload 落盘（L2 修复，对齐 isis:* 键）。
				state.DeviceConfig["ospf:enabled"] = "true"
				state.DeviceConfig["ospf:process-id"] = fmt.Sprintf("%d", processID)
				return fmt.Sprintf("OSPF process %d started", processID)
			}
		}
		return "Error: invalid OSPF config"
	case "lldp":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "enable":
			// lldp enable 或 lldp enable interface <type> <num>
			if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[1]) == "interface" {
				// lldp enable interface <type> <num>
				if len(cmd.Args) < 4 {
					return "Error: usage: lldp enable interface <type> <num>"
				}
				ifaceType := cmd.Args[2]
				ifaceNum := cmd.Args[3]
				ifaceName := ifaceType + ifaceNum
				if state.LLDP.PortConfig == nil {
					state.LLDP.PortConfig = make(map[string]bool)
				}
				state.LLDP.PortConfig[ifaceName] = true
				return fmt.Sprintf("LLDP enabled on interface %s", ifaceName)
			}
			// lldp enable 全局启用
			state.LLDP.Enabled = true
			return "LLDP enabled"
		case "disable":
			// lldp disable 或 lldp disable interface <type> <num>
			if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[1]) == "interface" {
				if len(cmd.Args) < 4 {
					return "Error: usage: lldp disable interface <type> <num>"
				}
				ifaceType := cmd.Args[2]
				ifaceNum := cmd.Args[3]
				ifaceName := ifaceType + ifaceNum
				if state.LLDP.PortConfig == nil {
					state.LLDP.PortConfig = make(map[string]bool)
				}
				state.LLDP.PortConfig[ifaceName] = false
				return fmt.Sprintf("LLDP disabled on interface %s", ifaceName)
			}
			state.LLDP.Enabled = false
			return "LLDP disabled"
		case "system-name":
			if len(cmd.Args) < 2 {
				return "Error: usage: lldp system-name <name>"
			}
			state.LLDP.SystemName = cmd.Args[1]
			return fmt.Sprintf("LLDP system-name set to '%s'", cmd.Args[1])
		case "system-description":
			if len(cmd.Args) < 2 {
				return "Error: usage: lldp system-description <desc>"
			}
			state.LLDP.SystemDescription = strings.Join(cmd.Args[1:], " ")
			return fmt.Sprintf("LLDP system-description set to '%s'", state.LLDP.SystemDescription)
		case "management-address":
			if len(cmd.Args) < 2 {
				return "Error: usage: lldp management-address <ip>"
			}
			state.LLDP.ManagementAddress = cmd.Args[1]
			return fmt.Sprintf("LLDP management-address set to %s", cmd.Args[1])
		}
		return "Error: invalid LLDP config"
	case "stp":
		// STP/RSTP/MSTP 命令族（P2 第四项，华为 VRP 课程 55/56/57）。
		// 全部经 DeviceConfig["stp:<field>"]（系统级）与
		// DeviceConfig["interface:<iface>:stp:<field>"]（接口级）单一事实源持久化
		// （方案 A，移除 state.STP）；side-effect 仅在此落地，show 经 EvaluateSTP 纯函数派生。
		return applySTP(state, cmd.Args)
	case "lacp":
		// P2 #5 改动点 13：lacp 命令族扩展（与既有 M-LAG 子命令共存不冲突）。
		//   非 m-lag 子命令 → applyLACPFeature（priority / preempt / timeout）
		//   m-lag 子命令   → 维持既有行为，零回归
		if len(cmd.Args) == 0 || !strings.EqualFold(cmd.Args[0], "m-lag") {
			return applyLACPFeature(state, cmd.Args)
		}
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 3 || strings.ToLower(cmd.Args[0]) != "m-lag" {
			return "Error: usage: lacp m-lag priority <prio>|system-id <mac>"
		}
		subCmd := strings.ToLower(cmd.Args[1])
		switch subCmd {
		case "priority":
			if len(cmd.Args) >= 3 {
				prio, err := parseNum(cmd.Args[2])
				if err == nil {
					state.DeviceConfig["lacp:m-lag:priority"] = fmt.Sprintf("%d", prio)
					return fmt.Sprintf("LACP M-LAG priority set to %d", prio)
				}
			}
			return "Error: usage: lacp m-lag priority <value>"
		case "system-id":
			if len(cmd.Args) >= 3 {
				state.DeviceConfig["lacp:m-lag:system-id"] = cmd.Args[2]
				return fmt.Sprintf("LACP M-LAG system-id set to %s", cmd.Args[2])
			}
			return "Error: usage: lacp m-lag system-id <mac>"
		}
		return "Error: invalid LACP M-LAG command"
	case "dhcp":
		// DHCP 池视图下的子命令处理
		if state.CurrentView == ViewDHCPPool {
			poolName := state.CurrentSub
			pool, ok := state.DHCP.Pools[poolName]
			if !ok {
				state.CurrentView = ViewSystem
				state.CurrentSub = ""
				return "Error: DHCP pool not found"
			}
			subCmd := strings.ToLower(cmd.Args[0])
			switch subCmd {
			case "network":
				if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[1]) != "mask" {
					return "Error: usage: network <ip> mask <mask>"
				}
				pool.Network = cmd.Args[0]
				pool.Mask = cmd.Args[2]
				// 计算总地址数
				if pool.Network != "" && pool.Mask != "" {
					prefix := subnetToPrefix(pool.Mask)
					total := 1 << (32 - prefix)
					if total > 2 {
						pool.Total = total - 2 // 减去网络地址和广播地址
					} else {
						pool.Total = total
					}
				}
				return fmt.Sprintf("Network %s mask %s configured", cmd.Args[0], cmd.Args[2])
			case "gateway-list":
				if len(cmd.Args) < 2 {
					return "Error: usage: gateway-list <ip> [<ip2>]"
				}
				pool.Gateway = cmd.Args[1]
				return fmt.Sprintf("Gateway %s configured", cmd.Args[1])
			case "dns-list":
				if len(cmd.Args) < 2 {
					return "Error: usage: dns-list <ip> [<ip2>]"
				}
				pool.DNSList = []string{cmd.Args[1]}
				if len(cmd.Args) >= 3 {
					pool.DNSList = append(pool.DNSList, cmd.Args[2])
				}
				return fmt.Sprintf("DNS list configured: %s", strings.Join(pool.DNSList, ", "))
			case "lease":
				// lease day <days> [hour <hours>]
				if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "day" {
					return "Error: usage: lease day <days> [hour <hours>]"
				}
				days := cmd.Args[1]
				hours := ""
				for i := 2; i < len(cmd.Args); i++ {
					if strings.ToLower(cmd.Args[i]) == "hour" && i+1 < len(cmd.Args) {
						hours = cmd.Args[i+1]
						break
					}
				}
				if hours != "" {
					pool.LeaseTime = fmt.Sprintf("%s days %s hours", days, hours)
				} else {
					pool.LeaseTime = fmt.Sprintf("%s days", days)
				}
				return fmt.Sprintf("Lease time configured: %s", pool.LeaseTime)
			case "excluded-ip-address":
				if len(cmd.Args) < 2 {
					return "Error: usage: excluded-ip-address <start-ip> [<end-ip>]"
				}
				startIP := cmd.Args[1]
				endIP := startIP
				if len(cmd.Args) >= 3 {
					endIP = cmd.Args[2]
				}
				excluded := fmt.Sprintf("%s-%s", startIP, endIP)
				pool.ExcludedIPs = append(pool.ExcludedIPs, excluded)
				return fmt.Sprintf("Excluded IP address: %s", excluded)
			default:
				return "Error: invalid DHCP pool command"
			}
		}
		// 接口视图下的 DHCP 命令（P2 #6 DHCP 中继）：
		// dhcp select global|interface|relay 与 dhcp relay <sub> 均在接口视图生效，
		// 统一由 applyDHCPInterfaceCmd 做三层守卫（视图/设备/relay 前置条件）并落
		// DeviceConfig 键（单一事实源）。二层交换机等不支持设备在此被拒绝。
		if state.CurrentView == ViewInterface {
			return applyDHCPInterfaceCmd(state, cmd.Args)
		}
		// 系统视图下的 DHCP 命令
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		subCmd := strings.ToLower(cmd.Args[0])
		switch subCmd {
		case "enable":
			if state.DHCP == nil {
				state.DHCP = &DHCPConfig{Pools: make(map[string]*DHCPPool)}
			}
			state.DHCP.Enabled = true
			return "DHCP enabled"
		case "disable":
			if state.DHCP != nil {
				state.DHCP.Enabled = false
			}
			return "DHCP disabled"
		case "pool":
			if len(cmd.Args) < 2 {
				return "Error: usage: dhcp pool <pool-name>"
			}
			poolName := cmd.Args[1]
			if state.DHCP == nil {
				state.DHCP = &DHCPConfig{Pools: make(map[string]*DHCPPool)}
			}
			if _, ok := state.DHCP.Pools[poolName]; !ok {
				state.DHCP.Pools[poolName] = &DHCPPool{Name: poolName}
			}
			state.CurrentView = ViewDHCPPool
			state.CurrentSub = poolName
			return fmt.Sprintf("Enter DHCP pool %s view", poolName)
		case "select":
			// P2 #6 / T0：dhcp select 已迁移到**接口视图**（对齐官方 VRP 课程 27），
			// 以 interface:<if>:dhcp-select 为单一事实源，支持 global|interface|relay 三态。
			// 系统视图旧用法一律报错引导（拍板 #2，原 state.DHCPSelectMode 死字段已删除）。
			return errDHCPSelectInterfaceView
		}
		return "Error: invalid DHCP command"
	case "ip pool":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: ip pool <pool-name>"
		}
		poolName := cmd.Args[0]
		if state.DHCP == nil {
			state.DHCP = &DHCPConfig{Pools: make(map[string]*DHCPPool)}
		}
		if _, ok := state.DHCP.Pools[poolName]; !ok {
			state.DHCP.Pools[poolName] = &DHCPPool{Name: poolName}
		}
		// 解析后续参数
		for i := 1; i < len(cmd.Args); i++ {
			switch strings.ToLower(cmd.Args[i]) {
			case "gateway-list":
				if i+1 < len(cmd.Args) {
					state.DHCP.Pools[poolName].Gateway = cmd.Args[i+1]
					i++
				}
			case "dns-list":
				for j := i + 1; j < len(cmd.Args); j++ {
					if strings.Contains(cmd.Args[j], ".") {
						state.DHCP.Pools[poolName].DNSList = append(state.DHCP.Pools[poolName].DNSList, cmd.Args[j])
					} else {
						break
					}
				}
				i += len(state.DHCP.Pools[poolName].DNSList)
			case "network":
				if i+1 < len(cmd.Args) {
					state.DHCP.Pools[poolName].Network = cmd.Args[i+1]
					i++
				}
			}
		}
		return fmt.Sprintf("IP pool %s configured", poolName)
	case "vrrp":
		// 华为 VRP 风格 VRRP 命令族（P2 第三项）：vrrp vrid <1-255> <subcommand>。
		// 全部经 DeviceConfig["interface:<iface>:vrrp:<vrid>:<field>"] 持久化
		// （单一事实源，save/reload 自动往返）。能力校验沿用 ExecuteCommandOn
		// 的 isCommandSupported（capabilities.go: "vrrp": l3Devices()）。
		return applyVRRP(state, cmd.Args)
	case "ipsec":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 6 {
			return "Error: need tunnel-id local-ip remote-ip mode encryption authentication"
		}
		tunnelID := cmd.Args[0]
		localIP := cmd.Args[1]
		remoteIP := cmd.Args[2]
		mode := cmd.Args[3]
		encryption := cmd.Args[4]
		auth := cmd.Args[5]
		state.IPsec[tunnelID] = &IPsecConfig{
			TunnelID:       tunnelID,
			LocalIP:        localIP,
			RemoteIP:       remoteIP,
			Mode:           mode,
			Encryption:     encryption,
			Authentication: auth,
		}
		return fmt.Sprintf("IPsec tunnel %s created", tunnelID)
	case "snmp":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 3 {
			return "Error: need version community [manager-ip]"
		}
		state.SNMP.Enabled = true
		state.SNMP.Version = cmd.Args[0]
		state.SNMP.Community = cmd.Args[1]
		if len(cmd.Args) >= 3 {
			state.SNMP.ManagerIP = cmd.Args[2]
		}
		return fmt.Sprintf("SNMP %s configured", state.SNMP.Version)
	case "syslog":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 2 {
			return "Error: need server-ip [port]"
		}
		state.Syslog.Enabled = true
		state.Syslog.ServerIP = cmd.Args[0]
		if len(cmd.Args) >= 2 {
			port, _ := parseNum(cmd.Args[1])
			state.Syslog.ServerPort = port
		}
		return fmt.Sprintf("Syslog server %s:%d configured", state.Syslog.ServerIP, state.Syslog.ServerPort)
	case "ntp":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 1 {
			return "Error: need server-ip"
		}
		state.NTP.Enabled = true
		state.NTP.ServerIP = cmd.Args[0]
		if len(cmd.Args) >= 2 {
			port, _ := parseNum(cmd.Args[1])
			state.NTP.ServerPort = port
		}
		return fmt.Sprintf("NTP server %s:%d configured", state.NTP.ServerIP, state.NTP.ServerPort)
	case "authentication-mode":
		// 🔴 设计 A2（本期最高危改动）：`authentication-mode` 在真机 VRP 中同时存在于
		// VTY 用户界面视图与 AAA 认证方案视图。Go 的 switch **不允许**两个同名 case，
		// 因此本 case 是全仓**唯一**的 authentication-mode 顶层入口，
		// 严禁为 AAA 另起一个同名 case（编译期 duplicate case 错误）。
		//
		// 改为按 CurrentView 分派；ViewVTY 分支逻辑与错误文案**逐字保持**既有实现，
		// 保证既有 VTY 用例零回归。注意 VRRP 的 authentication-mode 位于
		// `case "vrrp"` 的内层 switch，不受本改动影响（设计 §1.8 已复核）。
		switch state.CurrentView {
		case ViewAAA, ViewAAAAuthen:
			return applyAAAAuthenticationMode(state, cmd.Args)
		case ViewAAADomain:
			// OBS-1：域子视图下敲 authentication-mode 属误用（正确命令是
			// `authentication-scheme <name>` 绑定方案），引导到正确入口，
			// 不得回 VTY 文案（教学误导）。系统视图仍走下方 default → ErrMustBeInVTY。
			return ErrAuthSchemeFirst
		case ViewVTY:
			if len(cmd.Args) < 1 {
				return "Error: usage: authentication-mode aaa|password|none"
			}
			authMode := strings.ToLower(cmd.Args[0])
			if authMode != "aaa" && authMode != "password" && authMode != "none" {
				return "Error: usage: authentication-mode aaa|password|none"
			}
			state.VTY.AuthenticationMode = authMode
			return fmt.Sprintf("Authentication-mode set to %s", authMode)
		default:
			return ErrMustBeInVTY
		}
	case "user privilege":
		// VTY 视图下设置用户优先级
		if state.CurrentView != ViewVTY {
			return "Error: must be in VTY user interface view"
		}
		if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "level" {
			return "Error: usage: user privilege level <level>"
		}
		level, err := parseNum(cmd.Args[1])
		if err != nil || level < 0 || level > 15 {
			return "Error: privilege level must be between 0 and 15"
		}
		state.VTY.UserPrivilegeLevel = level
		return fmt.Sprintf("User privilege level set to %d", level)
	case "protocol":
		// VTY 视图下设置允许协议
		if state.CurrentView != ViewVTY {
			return "Error: must be in VTY user interface view"
		}
		if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "inbound" {
			return "Error: usage: protocol inbound ssh|telnet|all"
		}
		protocol := strings.ToLower(cmd.Args[1])
		if protocol != "ssh" && protocol != "telnet" && protocol != "all" {
			return "Error: usage: protocol inbound ssh|telnet|all"
		}
		state.VTY.ProtocolInbound = protocol
		return fmt.Sprintf("Protocol inbound set to %s", protocol)
	case "local-user":
		// P2 第八项 AAA（拍板 C1）：本地用户不再是系统视图的自造命令。
		// 旧实现在系统视图直接写 CLIState 的本地用户结构体 map（不落盘，save→reload 100% 丢失），
		// 且成功时回显自造的欢快文案（非 VRP 风格，真机为静默）——两者一并整改。
		// 现统一由 AAA 视图承载；非 AAA 视图一律返回 ErrAAAViewFirst 引导，且不写任何键。
		return applyAAALocalUser(state, cmd.Args)
	case "aaa":
		// 系统视图进入 AAA 视图（P0-1）。VRP 风格：成功静默。
		return applyAAAEnterView(state)
	case "authentication-scheme":
		// 双语义按视图分派：AAA 视图=建方案并进子视图；域子视图=绑定（引用完整性硬校验）。
		return applyAAAAuthenticationScheme(state, cmd.Args)
	case "authorization-scheme":
		return applyAAAAuthorizationScheme(state, cmd.Args)
	case "accounting-scheme":
		return applyAAAAccountingScheme(state, cmd.Args)
	case "authorization-mode":
		return applyAAAAuthorizationMode(state, cmd.Args)
	case "accounting-mode":
		return applyAAAAccountingMode(state, cmd.Args)
	case "domain":
		// 注：m-lag 的 `domain <id>` 位于 `case "m-lag"` 的**内层** switch（parser.go:1282），
		// 与本顶层 case 无命名冲突（设计 §1.8 已复核）。
		return applyAAADomain(state, cmd.Args)
	case "state":
		// AAA 域子视图：state { active | block }。
		return applyAAADomainState(state, cmd.Args)
	case "stelnet":
		// 系统视图下启用 STelnet 服务
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[0]) == "server" && strings.ToLower(cmd.Args[1]) == "enable" {
			state.SSH.STelnetEnabled = true
			return "STelnet server enabled"
		}
		return "Error: usage: stelnet server enable"
	case "rsa":
		// 系统视图下生成本地 RSA 密钥对
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[0]) == "local-key-pair" && strings.ToLower(cmd.Args[1]) == "create" {
			state.SSH.RSAGenDone = true
			return "RSA local key-pair has been generated"
		}
		return "Error: usage: rsa local-key-pair create"
	case "ssh user":
		// 系统视图下设置 SSH 用户认证类型
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 3 {
			return "Error: usage: ssh user <name> authentication-type password|rsa"
		}
		sshUserName := cmd.Args[0]
		if strings.ToLower(cmd.Args[1]) != "authentication-type" {
			return "Error: usage: ssh user <name> authentication-type password|rsa"
		}
		authType := strings.ToLower(cmd.Args[2])
		if authType != "password" && authType != "rsa" {
			return "Error: usage: ssh user <name> authentication-type password|rsa"
		}
		if state.SSH.Users == nil {
			state.SSH.Users = make(map[string]*SSHUser)
		}
		if _, ok := state.SSH.Users[sshUserName]; !ok {
			state.SSH.Users[sshUserName] = &SSHUser{Name: sshUserName}
		}
		state.SSH.Users[sshUserName].AuthType = authType
		return fmt.Sprintf("SSH user %s authentication-type set to %s", sshUserName, authType)
	case "ssh":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "disable" {
			state.SSH.Enabled = false
			return "SSH disabled"
		} else if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[0]) == "port" {
			port, err := parseNum(cmd.Args[1])
			if err == nil {
				state.SSH.Port = port
				return fmt.Sprintf("SSH port set to %d", port)
			}
		}
		return "Error: invalid SSH config"
	case "vxlan":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 3 {
			return "Error: need vni vtep-ip peer-vtep-ip [vrf]"
		}
		vni, err := parseNum(cmd.Args[0])
		if err != nil {
			return "Error: invalid VNI"
		}
		state.VXLAN.Enabled = true
		state.VXLAN.VNI = vni
		state.VXLAN.VTEPIP = cmd.Args[1]
		state.VXLAN.PeerVTEPIP = cmd.Args[2]
		if len(cmd.Args) >= 4 {
			state.VXLAN.VRFName = cmd.Args[3]
		}
		return fmt.Sprintf("VXLAN VNI %d configured", vni)
	case "vsi":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: vsi <name>"
		}
		vsiName := cmd.Args[0]
		if _, exists := state.VXLAN.VSIs[vsiName]; !exists {
			state.VXLAN.VSIs[vsiName] = &VSIConfig{
				Name:   vsiName,
				Status: "Active",
				VPNs:   []string{},
			}
			return fmt.Sprintf("VSI %s created", vsiName)
		}
		return fmt.Sprintf("VSI %s already exists", vsiName)
	case "vni":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: vni <vni-id>"
		}
		vni, err := parseNum(cmd.Args[0])
		if err != nil {
			return "Error: invalid VNI"
		}
		state.VXLAN.VNI = vni
		state.VXLAN.Enabled = true
		return fmt.Sprintf("VNI %d configured", vni)
	case "evpn-instance":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: evpn-instance <instance-id>"
		}
		instanceID := cmd.Args[0]
		state.VXLAN.EvpnEnabled = true
		state.DeviceConfig["vxlan:evpn-instance"] = instanceID
		return fmt.Sprintf("EVPN instance %s created", instanceID)
	case "route-distinguisher":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: route-distinguisher <auto|rd-value>"
		}
		rdVal := strings.Join(cmd.Args, " ")
		state.DeviceConfig["vxlan:route-distinguisher"] = rdVal
		return fmt.Sprintf("Route distinguisher set to %s", rdVal)
	case "vpn-target":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: vpn-target <auto|rt-value>"
		}
		rtVal := strings.Join(cmd.Args, " ")
		state.DeviceConfig["vxlan:vpn-target"] = rtVal
		return fmt.Sprintf("VPN target set to %s", rtVal)
	case "distributed-gateway":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		state.DeviceConfig["vxlan:distributed-gateway"] = "enabled"
		return "Distributed gateway enabled"
	case "vxlan-interface":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: vxlan-interface <interface-name>"
		}
		ifaceName := cmd.Args[0]
		if state.Interfaces == nil {
			state.Interfaces = make(map[string]*InterfaceConfig)
		}
		state.Interfaces[ifaceName] = &InterfaceConfig{
			Name:        ifaceName,
			Status:      "Up",
			Protocol:    "Up",
			Description: "VXLAN Tunnel Interface",
		}
		state.DeviceConfig[fmt.Sprintf("interface:%s:type", ifaceName)] = "VXLAN"
		return fmt.Sprintf("VXLAN interface %s created", ifaceName)
	case "vxlan-traffic-type":
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: vxlan-traffic-type control/data/all"
		}
		ttype := cmd.Args[0]
		state.DeviceConfig[fmt.Sprintf("interface:%s:vxlan-traffic-type", state.CurrentSub)] = ttype
		return fmt.Sprintf("VXLAN traffic type set to %s", ttype)
	case "remote-evpn-vtep":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 2 {
			return "Error: usage: remote-evpn-vtep <ip> vni <vni>"
		}
		remoteIP := cmd.Args[0]
		vniStr := cmd.Args[1]
		state.DeviceConfig[fmt.Sprintf("vxlan:remote-vtep:%s", remoteIP)] = vniStr
		return fmt.Sprintf("Remote EVPN VTEP %s added", remoteIP)
	case "bgp":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) < 1 {
			return "Error: need as-number"
		}
		asNumber, err := parseNum(cmd.Args[0])
		if err != nil {
			return "Error: invalid AS number"
		}
		state.BGP.Enabled = true
		state.BGP.ASNumber = asNumber
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "router-id" {
			state.BGP.RouterID = cmd.Args[2]
			state.DeviceConfig["bgp:router-id"] = state.BGP.RouterID
		}
		// 镜像到 DeviceConfig 以便随拓扑 save/reload 落盘（L2 修复，对齐 isis:* 键）。
		state.DeviceConfig["bgp:enabled"] = "true"
		state.DeviceConfig["bgp:as-number"] = fmt.Sprintf("%d", asNumber)
		state.CurrentView = ViewBGP
		state.CurrentSub = fmt.Sprintf("bgp-%d", asNumber)
		return fmt.Sprintf("Enter BGP view, AS %d", asNumber)
	case "peer":
		if state.CurrentView != ViewBGP || len(cmd.Args) < 2 {
			return "Error: must be in BGP view, need peer-ip and remote-as"
		}
		peerIP := cmd.Args[0]
		remoteAS, err := parseNum(cmd.Args[1])
		if err != nil {
			return "Error: invalid remote-as"
		}
		ebgp := remoteAS != state.BGP.ASNumber
		state.BGP.Neighbors[peerIP] = &BGPNeighbor{
			IPAddress: peerIP,
			RemoteAS:  remoteAS,
			EBGP:      ebgp,
		}
		// 镜像邻居到 DeviceConfig 以便随拓扑 save/reload 落盘（L2 修复，对齐 isis:* 键）。
		peerIPs := make([]string, 0, len(state.BGP.Neighbors))
		for ip := range state.BGP.Neighbors {
			peerIPs = append(peerIPs, ip)
		}
		state.DeviceConfig["bgp:peer-ips"] = strings.Join(peerIPs, ",")
		state.DeviceConfig["bgp:neighbor:"+peerIP+":remote-as"] = fmt.Sprintf("%d", remoteAS)
		state.DeviceConfig["bgp:neighbor:"+peerIP+":ebgp"] = fmt.Sprintf("%t", ebgp)
		return fmt.Sprintf("BGP neighbor %s configured", peerIP)
	case "rip":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 {
			processID, err := parseNum(cmd.Args[0])
			if err == nil {
				state.DeviceConfig["rip:process-id"] = fmt.Sprintf("%d", processID)
				state.DeviceConfig["rip:enabled"] = "true"
				return fmt.Sprintf("RIP process %d started", processID)
			}
		}
		return "Error: invalid RIP config"
	case "mpls":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 2 && strings.ToLower(cmd.Args[0]) == "lsr-id" {
			state.DeviceConfig["mpls:lsr-id"] = cmd.Args[1]
			return fmt.Sprintf("MPLS LSR ID set to %s", cmd.Args[1])
		}
		return "MPLS enabled"
	case "ppp":
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		state.DeviceConfig["interface:"+state.CurrentSub+":encapsulation"] = "ppp"
		return "PPP encapsulation enabled on " + state.CurrentSub
	case "pppoe":
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[0]) == "pppoe-client" && strings.ToLower(cmd.Args[1]) == "dial-bundle-number" {
			bundleID, err := parseNum(cmd.Args[2])
			if err == nil {
				state.DeviceConfig["interface:"+state.CurrentSub+":pppoe-bundle"] = fmt.Sprintf("%d", bundleID)
				return fmt.Sprintf("PPPoE client configured, bundle %d", bundleID)
			}
		}
		return "PPPoE enabled"
	case "ipv6":
		// 三态派发（P2 第九项，T02）：裸 ipv6 / ipv6 enable / ipv6 address /
		// ipv6 route-static，按「当前视图 + 一级子命令」精确路由到 ipv6_cmd.go 副作用出口。
		if state.CurrentView == ViewSystem {
			if len(cmd.Args) == 0 {
				return applyIPv6SystemEnable(state, cmd.Args)
			}
			switch strings.ToLower(strings.TrimSpace(cmd.Args[0])) {
			case "enable":
				// AC1③ / AC2⑤：系统视图 ipv6 enable 引导（A11），不写任何键。
				return ErrIPv6SystemViewEnableGuide
			case "address":
				// AC1④：系统视图 ipv6 address 须进接口视图。
				return ErrIPv6MustBeInterfaceView
			case "route-static":
				return applyIPv6RouteStatic(state, cmd.Args[1:])
			default:
				return ErrIPv6Unrecognized
			}
		} else if state.CurrentView == ViewInterface {
			if len(cmd.Args) == 0 {
				return ErrIPv6Unrecognized
			}
			switch strings.ToLower(strings.TrimSpace(cmd.Args[0])) {
			case "enable":
				return applyIPv6InterfaceEnable(state, cmd.Args[1:])
			case "address":
				return applyIPv6InterfaceAddress(state, cmd.Args[1:])
			default:
				return ErrIPv6Unrecognized
			}
		}
		// 用户视图（既非系统也非接口）：ipv6 enable 按 AC2⑤ 引导进接口视图。
		if len(cmd.Args) > 0 && strings.EqualFold(strings.TrimSpace(cmd.Args[0]), "enable") {
			return ErrIPv6MustBeInterfaceView
		}
		return ErrIPv6Unrecognized
	case "ripng":
		// RIPng：系统视图进进程 / 接口视图使能（P0-13，华为 VRP 真机形态）。
		// 接口视图仅接受 `ripng <pid> enable`；裸 / 缺 enable → unrecognized（§7.6）。
		if state.CurrentView == ViewInterface {
			if len(cmd.Args) == 2 && strings.EqualFold(strings.TrimSpace(cmd.Args[1]), "enable") {
				return applyRIPngInterface(state, cmd.Args)
			}
			return ErrIPv6Unrecognized
		}
		return applyRIPng(state, cmd.Args)
	case "ospfv3":
		// OSPFv3：系统视图进进程 / 接口视图绑区域（P0-14，华为 VRP 真机形态）。
		// 接口视图仅接受 `ospfv3 <pid> area <area>`；裸 / 缺 area → unrecognized（§7.6）。
		if state.CurrentView == ViewInterface {
			if len(cmd.Args) >= 3 && strings.EqualFold(strings.TrimSpace(cmd.Args[1]), "area") {
				return applyOSPFv3Interface(state, cmd.Args)
			}
			return ErrIPv6Unrecognized
		}
		return applyOSPFv3(state, cmd.Args)
	case "smtp":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "enable" {
			state.DeviceConfig["smtp:enabled"] = "true"
			return "SMTP service enabled"
		}
		return "SMTP configuration"

	// ===== P1-F 顶层命令补齐（一致性 9 条）=====

	case "isis":
		// IS-IS 协议视图：P0 最小启用（进视图） + P1 真实配置（network/import-route）。
		if state.CurrentView == ViewSystem {
			if len(cmd.Args) < 1 {
				return "Error: need process-id"
			}
			pid, err := parseNum(cmd.Args[0])
			if err != nil {
				return "Error: invalid IS-IS process-id"
			}
			state.ISIS.Enabled = true
			state.ISIS.ProcessID = pid
			state.DeviceConfig["isis:enabled"] = "true"
			state.DeviceConfig["isis:process-id"] = fmt.Sprintf("%d", pid)
			state.CurrentView = ViewISIS
			state.CurrentSub = fmt.Sprintf("isis-%d", pid)
			return fmt.Sprintf("Enter ISIS view, process %d", pid)
		}
		return "Error: must be in system view"

	case "network":
		// IS-IS 视图下的 network 命令（P1-F，T03）。
		// VRP 中进入 isis 视图后直接敲 `network <type>`，首个 token 为 network，
		// 故在顶层按 ViewISIS 守卫分发（非 isis 子参数）。
		if state.CurrentView != ViewISIS {
			return "Error: must be in ISIS view"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: network <level-1|level-2|level-1-2>"
		}
		nt := strings.ToLower(cmd.Args[0])
		switch nt {
		case "level-1", "level-2", "level-1-2":
			state.ISIS.NetworkType = nt
			state.DeviceConfig["isis:network-type"] = nt
			return fmt.Sprintf("IS-IS network type set to %s", nt)
		default:
			return "Error: usage: network <level-1|level-2|level-1-2>"
		}

	case "import-route":
		// IS-IS 视图下的 import-route 命令（P1-F，T03）。同上，顶层按 ViewISIS 守卫。
		if state.CurrentView != ViewISIS {
			return "Error: must be in ISIS view"
		}
		if len(cmd.Args) < 1 {
			return "Error: usage: import-route <protocol>"
		}
		proto := strings.ToLower(cmd.Args[0])
		found := false
		for _, r := range state.ISIS.ImportRoutes {
			if r == proto {
				found = true
				break
			}
		}
		if !found {
			state.ISIS.ImportRoutes = append(state.ISIS.ImportRoutes, proto)
		}
		state.DeviceConfig["isis:import-route"] = strings.Join(state.ISIS.ImportRoutes, ",")
		if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "route-policy" {
			state.DeviceConfig["isis:import-route:route-policy"] = cmd.Args[2]
			return fmt.Sprintf("IS-IS import-route %s route-policy %s added", proto, cmd.Args[2])
		}
		return fmt.Sprintf("IS-IS import-route %s added", proto)

	case "route-policy":
		// 路由策略节点视图入口（P0-2 路由策略补齐）。系统视图进入，建立节点上下文指针。
		return enterRoutePolicyView(state, cmd)
	case "if-match":
		// route-policy 节点视图内的匹配条件子句（P0-2）。顶层守卫 ViewRoutePolicy。
		return execRoutePolicyIfMatch(state, cmd)
	case "apply":
		// route-policy 节点视图内的 apply 动作子句（P0-2）。顶层守卫 ViewRoutePolicy。
		return execRoutePolicyApply(state, cmd)
	case "filter-policy":
		// 路由引入过滤（P0-2）：BGP/ISIS 视图内直接限定方向；系统视图需显式协议域。
		return execFilterPolicy(state, cmd)

	case "quit-cli":
		// 会话关闭提示（语义等同退出 CLI，前端透传）。
		return "Session closed."

	case "vlanif":
		// 引导分支（决策 #4 方案 A）：建 Vlanif 应走 interface Vlanif <id>。
		if len(cmd.Args) == 0 {
			return "Error: need vlan-id"
		}
		return fmt.Sprintf("Use 'interface Vlanif %s' to create the Layer 3 interface.", cmd.Args[0])

	case "port-security":
		// 顶层端口安全分支：委托既有 port security 逻辑（需接口视图）。
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: port-security <enable|disable|max-mac-num|mac-address>"
		}
		return applyPortSecurity(state, cmd.Args)

	case "simulate":
		// 模拟诊断命令：端口安全准入判定的唯一触发点（拍板 #2）。
		// 限定 switchDevices()（能力矩阵已注册，非交换机由能力校验回 not supported）；
		// 须处于接口视图，state.CurrentSub 为目标端口。
		if state.CurrentView != ViewInterface {
			return "Error: must be in interface view"
		}
		if len(cmd.Args) == 0 {
			return "Error: usage: simulate frame <src-mac> [vlan <id>]"
		}
		return handleSimulateFrame(state, cmd.Args)

	case "nslookup":
		// 终端 DNS 查询（复用 state.HostDNS）。
		if len(cmd.Args) == 0 {
			return "Error: need hostname"
		}
		return buildHostNslookup(state, cmd.Args[0])

	case "http", "https", "dns", "ftp":
		// 服务器应用层服务启用（对齐 smtp 回显风格）。
		return executeServerService(state, command, cmd.Args)

	case "bfd":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "enable" {
			state.BFD.Enabled = true
			return "BFD enabled"
		} else if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "disable" {
			state.BFD.Enabled = false
			return "BFD disabled"
		} else if len(cmd.Args) >= 4 && state.BFD.Enabled {
			peerIP := cmd.Args[0]
			localIP := cmd.Args[1]
			var warn strings.Builder
			minTx, err := parseNum(cmd.Args[2])
			if err != nil {
				warn.WriteString(fmt.Sprintf(" [warn: invalid min-tx %q, used 0]", cmd.Args[2]))
			}
			minRx, err := parseNum(cmd.Args[3])
			if err != nil {
				warn.WriteString(fmt.Sprintf(" [warn: invalid min-rx %q, used 0]", cmd.Args[3]))
			}
			detectMult := 3
			if len(cmd.Args) >= 5 {
				if n, e := parseNum(cmd.Args[4]); e == nil {
					detectMult = n
				} else {
					warn.WriteString(fmt.Sprintf(" [warn: invalid detect-mult %q, kept 3]", cmd.Args[4]))
				}
			}
			state.BFD.Sessions[peerIP] = &BFDSession{
				PeerIP:        peerIP,
				LocalIP:       localIP,
				MinTxInterval: minTx,
				MinRxInterval: minRx,
				DetectMult:    detectMult,
			}
			return fmt.Sprintf("BFD session %s created%s", peerIP, warn.String())
		}
		return "Error: invalid BFD config"
	case "policy-based-route", "pbr":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 4 {
			policyName := cmd.Args[0]
			ruleID := 0
			var warn strings.Builder
			if n, err := parseNum(cmd.Args[1]); err == nil {
				ruleID = n
			} else {
				warn.WriteString(fmt.Sprintf(" [warn: invalid rule-id %q, used 0]", cmd.Args[1]))
			}
			matchACL := cmd.Args[2]
			nextHop := cmd.Args[3]
			iface := ""
			if len(cmd.Args) >= 5 {
				iface = cmd.Args[4]
			}
			if _, ok := state.PBR[policyName]; !ok {
				state.PBR[policyName] = []*PBRRule{}
			}
			state.PBR[policyName] = append(state.PBR[policyName], &PBRRule{
				ID:        ruleID,
				MatchACL:  matchACL,
				NextHop:   nextHop,
				Interface: iface,
			})
			return fmt.Sprintf("PBR rule %d added to policy %s%s", ruleID, policyName, warn.String())
		}
		return "Error: invalid PBR config"
	case "tunnel-protocol", "source", "destination", "keepalive":
		// GRE 隧道配置命令族（P2 第七项，T2 改动点 #3）：全部为 Tunnel 接口视图命令。
		// 顶层合并 case → applyGREInterfaceCmd 分派；三态守卫（视图→设备→GRE 前置）在其内施加。
		return applyGREInterfaceCmd(state, strings.ToLower(cmd.Command), cmd.Args)
	case "gre":
		// GRE 隧道命令族（P2 第七项，华为 VRP 课程 69，T0 改动点 #1 / C1）。
		// 自造系统视图命令（旧 baseline）已废除：系统视图执行 → 报错引导，
		// 不写任何 DeviceConfig 键（AC3）；接口视图 → 分派到 applyGREInterfaceCmd
		// 处理 gre key / gre checksum（tunnel-protocol / source / destination / keepalive
		// 由顶层合并 case 分派）。
		if state.CurrentView == ViewInterface {
			return applyGREInterfaceCmd(state, "gre", cmd.Args)
		}
		if state.CurrentView == ViewSystem {
			return errGRESystemViewGuide
		}
		return errGREMustBeInterface
	case "qos":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "enable" {
			state.QoS.Enabled = true
			return "QoS enabled"
		} else if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "disable" {
			state.QoS.Enabled = false
			return "QoS disabled"
		} else if len(cmd.Args) >= 3 && state.QoS.Enabled {
			subCmd := strings.ToLower(cmd.Args[0])
			name := cmd.Args[1]
			switch subCmd {
			case "classifier":
				acl := ""
				dscp := 0
				var warn strings.Builder
				if len(cmd.Args) >= 3 {
					acl = cmd.Args[2]
				}
				if len(cmd.Args) >= 4 {
					if n, err := parseNum(cmd.Args[3]); err == nil {
						dscp = n
					} else {
						warn.WriteString(fmt.Sprintf(" [warn: invalid dscp %q, used 0]", cmd.Args[3]))
					}
				}
				state.QoS.Classifiers[name] = &QoSClassifier{
					Name: name,
					ACL:  acl,
					DSCP: dscp,
				}
				return fmt.Sprintf("QoS classifier %s created%s", name, warn.String())
			case "behavior":
				bandwidth := 0
				priority := 0
				queue := ""
				var warn strings.Builder
				if len(cmd.Args) >= 3 {
					if n, err := parseNum(cmd.Args[2]); err == nil {
						bandwidth = n
					} else {
						warn.WriteString(fmt.Sprintf(" [warn: invalid bandwidth %q, used 0]", cmd.Args[2]))
					}
				}
				if len(cmd.Args) >= 4 {
					if n, err := parseNum(cmd.Args[3]); err == nil {
						priority = n
					} else {
						warn.WriteString(fmt.Sprintf(" [warn: invalid priority %q, used 0]", cmd.Args[3]))
					}
				}
				if len(cmd.Args) >= 5 {
					queue = cmd.Args[4]
				}
				state.QoS.Behaviors[name] = &QoSBehavior{
					Name:      name,
					Bandwidth: bandwidth,
					Priority:  priority,
					Queue:     queue,
				}
				return fmt.Sprintf("QoS behavior %s created%s", name, warn.String())
			case "policy":
				classifier := ""
				behavior := ""
				if len(cmd.Args) >= 3 {
					classifier = cmd.Args[2]
				}
				if len(cmd.Args) >= 4 {
					behavior = cmd.Args[3]
				}
				state.QoS.Policies[name] = &QoSPolicy{
					Name:       name,
					Classifier: classifier,
					Behavior:   behavior,
				}
				return fmt.Sprintf("QoS policy %s created", name)
			}
		}
		return "Error: invalid QoS config"
	case "dot1x":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "enable" {
			state.Dot1x.Enabled = true
			return "802.1X enabled"
		} else if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "disable" {
			state.Dot1x.Enabled = false
			return "802.1X disabled"
		} else if len(cmd.Args) >= 2 && state.Dot1x.Enabled {
			portName := cmd.Args[0]
			authMethod := "eap"
			reauth := false
			quietTimer := 60
			var warn strings.Builder
			if len(cmd.Args) >= 2 {
				authMethod = cmd.Args[1]
			}
			if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[2]) == "reauth" {
				reauth = true
			}
			if len(cmd.Args) >= 4 {
				if n, err := parseNum(cmd.Args[3]); err == nil {
					quietTimer = n
				} else {
					warn.WriteString(fmt.Sprintf(" [warn: invalid quiet-timer %q, kept 60]", cmd.Args[3]))
				}
			}
			state.Dot1x.Ports[portName] = &Dot1xPort{
				Enabled:    true,
				AuthMethod: authMethod,
				Reauth:     reauth,
				QuietTimer: quietTimer,
			}
			return fmt.Sprintf("802.1X configured on port %s%s", portName, warn.String())
		}
		return "Error: invalid 802.1X config"
	case "radius":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 3 {
			state.RADIUS.Enabled = true
			state.RADIUS.PrimaryServer = cmd.Args[0]
			state.RADIUS.SharedSecret = cmd.Args[1]
			if len(cmd.Args) >= 3 {
				state.RADIUS.SecondaryServer = cmd.Args[2]
			}
			return fmt.Sprintf("RADIUS server %s configured", state.RADIUS.PrimaryServer)
		}
		return "Error: invalid RADIUS config"
	case "netflow":
		if state.CurrentView != ViewSystem {
			return "Error: must be in system view"
		}
		if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "enable" {
			state.NetFlow.Enabled = true
			return "NetFlow enabled"
		} else if len(cmd.Args) >= 1 && strings.ToLower(cmd.Args[0]) == "disable" {
			state.NetFlow.Enabled = false
			return "NetFlow disabled"
		} else if len(cmd.Args) >= 2 {
			state.NetFlow.Enabled = true
			state.NetFlow.Exporter = cmd.Args[0]
			if len(cmd.Args) >= 2 {
				port, _ := parseNum(cmd.Args[1])
				state.NetFlow.Port = port
			}
			return fmt.Sprintf("NetFlow exporter %s:%d configured", state.NetFlow.Exporter, state.NetFlow.Port)
		}
		return "Error: invalid NetFlow config"
	case "display", "dis":
		if len(cmd.Args) == 0 {
			return "Error: need args"
		}
		// Normalize args to handle multi-word commands like "ip routing-table"
		arg0 := strings.ToLower(cmd.Args[0])
		arg1 := ""
		if len(cmd.Args) > 1 {
			arg1 = strings.ToLower(cmd.Args[1])
		}
		// 子命令缩写映射（华为 VRP 常用缩写）
		arg0 = normalizeDisplaySubCmd(arg0)
		arg1 = normalizeDisplaySubCmd2(arg0, arg1)
		// v0.12.1：arg0 缩写歧义（多前缀命中，如 dis i / dis b）由
		// normalizeDisplaySubCmd 返回 "ambiguous"，此处输出 VRP 风格歧义文案。
		// 注意：不得复用 display ip 内层 case "ambiguous"（那是二级子命令专用）。
		if arg0 == "ambiguous" {
			return "Error: Ambiguous command found at '^' position."
		}

		// 设备类型检查：部分 display 子命令只对特定设备有意义
		isRouter := state.DeviceType == topology.DeviceRouter
		isL3Switch := state.DeviceType == topology.DeviceL3Switch
		isSwitch := state.DeviceType == topology.DeviceSwitch
		isFirewall := state.DeviceType == topology.DeviceFirewall
		isAC := state.DeviceType == topology.DeviceAC || state.DeviceType == topology.DeviceAP
		isHost := state.DeviceType == topology.DevicePC || state.DeviceType == topology.DeviceClient || state.DeviceType == topology.DeviceServer
		isCloudHub := state.DeviceType == topology.DeviceCloud || state.DeviceType == topology.DeviceHub

		switch arg0 {
		case "this":
			var out strings.Builder
			switch state.CurrentView {
			case ViewUser:
				// 用户视图显示所有配置
				out.WriteString("User View configuration:\n")
				sysname := state.DeviceConfig["sysname"]
				if sysname == "" {
					sysname = string(state.DeviceType)
				}
				out.WriteString(fmt.Sprintf("  sysname %s\n", sysname))
				if len(state.Routes) > 0 {
					out.WriteString("\nRouting table:\n")
					for _, r := range state.Routes {
						out.WriteString(fmt.Sprintf("  ip route-static %s %s %s\n", r.Destination, r.Mask, r.NextHop))
					}
				}
			case ViewSystem:
				// 系统视图显示系统配置
				out.WriteString("System View configuration:\n")
				if state.OSPF.Enabled {
					out.WriteString(fmt.Sprintf("  ospf %d\n", state.OSPF.ProcessID))
					out.WriteString(fmt.Sprintf("  area %d\n", state.OSPF.AreaID))
				}
				if state.BGP.Enabled {
					out.WriteString(fmt.Sprintf("  bgp %d\n", state.BGP.ASNumber))
					out.WriteString(fmt.Sprintf("  router-id %s\n", state.BGP.RouterID))
				}
				if isSTPEnabled(state) {
					out.WriteString(buildSavedSTPConfig(state))
				}
				if ifaces := vrrpInterfaces(state); len(ifaces) > 0 {
					for _, iname := range ifaces {
						if s := buildSavedVRRPConfig(state, iname); s != "" {
							out.WriteString(s)
						}
					}
				}
				if state.SNMP.Enabled {
					out.WriteString(fmt.Sprintf("  snmp-agent\n"))
				}
				if state.Syslog.Enabled {
					out.WriteString(fmt.Sprintf("  info-center loghost %s\n", state.Syslog.ServerIP))
				}
				if state.NTP.Enabled {
					out.WriteString(fmt.Sprintf("  ntp-server %s\n", state.NTP.ServerIP))
				}
				if state.SSH.Enabled {
					out.WriteString(fmt.Sprintf("  ssh user %s\n", state.SSH.Version))
				}
			case ViewInterface:
				// 接口视图显示接口配置
				ifaceName := state.CurrentSub
				out.WriteString(fmt.Sprintf("interface %s\n", ifaceName))
				if ip, ok := state.DeviceConfig[fmt.Sprintf("interface:%s:ip", ifaceName)]; ok && ip != "" {
					out.WriteString(fmt.Sprintf("  ip address %s\n", ip))
				}
				if desc, ok := state.DeviceConfig[fmt.Sprintf("interface:%s:description", ifaceName)]; ok && desc != "" {
					out.WriteString(fmt.Sprintf("  description %s\n", desc))
				}
				if state.Interfaces != nil {
					if iface, ok := state.Interfaces[ifaceName]; ok {
						if iface.Status != "" {
							out.WriteString(fmt.Sprintf("  %s\n", iface.Status))
						}
					}
				}
			default:
				out.WriteString("Error: Invalid view context\n")
			}
			return out.String()
		case "current-configuration":
			// 复用 VRP 风格配置快照生成器（与 display saved-configuration / save 一致），
			// 并追加 OSPF/BGP/ISIS 等协议启用摘要块，避免较旧版直排 key-value 而丢失协议信息
			//（P1-F，决策 #6 + 风险3）。
			return state.buildSavedConfigSnapshot() + formatProtocolBlocks(state)
		case "ipv6":
			// display ipv6 [interface [<if>|brief] | routing-table [<prefix>] | brief]
			// 渲染层严格复刻 gre_display 范式，只读且无能力守卫（AC11b）。
			return buildIPv6Display(state, cmd.Args[1:])
		case "ripng":
			// display ripng [<pid>]（AC13，诚实占位：配置态真实 / 运行态恒 "-"）。
			return buildRIPngDisplay(state, arg1)
		case "ospfv3":
			// display ospfv3 [<pid>]（AC13，诚实占位：配置态真实 / 运行态恒 "-"）。
			return buildOSPFv3Display(state, arg1)
		case "eth-trunk":
			// P2 #5 改动点 8（T03）：重写为 buildEthTrunkDisplay，唯一数据源 = EvaluateLAG。
			// 支持 display eth-trunk [<id>] [verbose | load-balance | interface <if>]；
			// 无参按 trunk-id 升序逐组完整块；成员自然序确定性（AC5）。
			return buildEthTrunkDisplay(state, cmd.Args[1:])
		case "link-aggregation":
			// P2 #5 改动点 9（T03）：display link-aggregation summary。
			// **已删除现状残桩的第二个重映射循环**——那是幽灵 Bridge-Aggregation
			// 编造数据的根因（P1-10 升级 P0，拍板 #4）；改按 agg-family 归类 + 确定性升序。
			if arg1 == "" || arg1 == "summary" {
				return buildLinkAggregationSummary(state)
			}
			return "Error: invalid link-aggregation command"
		case "port-vlan", "port":
			if !isSwitch && !isL3Switch {
				return fmt.Sprintf("Error: Port VLAN is not supported on %s", state.DeviceType)
			}
			// display port vlan
			var out strings.Builder
			out.WriteString("Port VLAN configuration:\n")
			out.WriteString("Interface            Link Type    PVID   Involved VLANs\n")
			out.WriteString("---------------------------------------------------------------\n")
			// 收集所有接口的端口配置
			portVLANMap := make(map[string]map[string]string)
			for k, v := range state.DeviceConfig {
				if !strings.HasPrefix(k, "interface:") {
					continue
				}
				parts := strings.SplitN(k, ":", 3)
				if len(parts) < 3 {
					continue
				}
				ifaceName := parts[1]
				key := parts[2]
				if !strings.HasPrefix(key, "port-") {
					continue
				}
				if _, ok := portVLANMap[ifaceName]; !ok {
					portVLANMap[ifaceName] = make(map[string]string)
				}
				portVLANMap[ifaceName][key] = v
			}
			if len(portVLANMap) == 0 {
				out.WriteString("No port VLAN configuration\n")
			} else {
				for iface, cfg := range portVLANMap {
					linkType := cfg["port-link-type"]
					if linkType == "" {
						linkType = "access"
					}
					pvid := cfg["port-default-vlan"]
					if pvid == "" {
						pvid = cfg["port-trunk-pvid"]
					}
					if pvid == "" {
						pvid = "1"
					}
					vlans := cfg["port-trunk-allow-vlan"]
					if vlans == "" {
						vlans = pvid
					} else {
						vlans = vlans + " " + pvid
					}
					out.WriteString(fmt.Sprintf("%-20s %-12s %-6s %s\n", iface, linkType, pvid, vlans))
				}
			}
			return out.String()
		case "ip":
			// display ip <sub> ...   sub ∈ {interface, pool, routing-table}
			// 二级子命令：normalizeDisplaySubCmd2("ip", arg1) 已把白名单缩写
			// （int/inter/rt/route）转成完整值；未命中的输入不静默展开——
			// 完整关键字放行、多前缀报 Ambiguous、其余（含唯一前缀如 r）
			// 报 unknown command 指向完整命令（与 dis aa 语义一致，v0.12.1）。
			ipSubKW := []string{"interface", "pool", "routing-table"}
			switch arg1 {
			case "interface", "pool", "routing-table":
				// 白名单/完整关键字放行
			case "":
				return "Error: Incomplete command found at '^' position."
			default:
				if _, _, subErr := resolveKeyword(arg1, ipSubKW); subErr == "ambiguous" {
					return "Error: Ambiguous command found at '^' position."
				}
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}

			// display ip pool / display ip pool interface vlanif <id>
			if arg1 == "pool" {
				// 检查是否是 display ip pool interface vlanif <id>
				if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[1]) == "interface" {
					// 查找 Vlanif 接口对应的地址池
					vlanifPrefixes := []string{"vlanif"}
					isVlanif := false
					for _, prefix := range vlanifPrefixes {
						if strings.HasPrefix(strings.ToLower(cmd.Args[2]), prefix) {
							isVlanif = true
							break
						}
					}
					if isVlanif && len(cmd.Args) >= 4 {
						// display ip pool interface vlanif <id>
						vlanID := cmd.Args[3]
						var out strings.Builder
						out.WriteString(fmt.Sprintf("Vlanif%s pool information:\n", vlanID))
						out.WriteString("----------------------------------------------------\n")
						// 查找关联到该 VLAN 的地址池
						for name, pool := range state.DHCP.Pools {
							// 检查是否有接口使用该 VLAN
							ifaceName := fmt.Sprintf("Vlanif%s", vlanID)
							boundKey := fmt.Sprintf("interface:%s:dhcp-pool", ifaceName)
							if state.DeviceConfig[boundKey] == name {
								out.WriteString(fmt.Sprintf("Pool name: %s\n", name))
								out.WriteString(fmt.Sprintf("  Network: %s %s\n", pool.Network, pool.Mask))
								out.WriteString(fmt.Sprintf("  Gateway: %s\n", pool.Gateway))
								out.WriteString(fmt.Sprintf("  DNS List: %s\n", strings.Join(pool.DNSList, ", ")))
								out.WriteString(fmt.Sprintf("  Lease: %s\n", pool.LeaseTime))
								out.WriteString(fmt.Sprintf("  Excluded IPs: %s\n", strings.Join(pool.ExcludedIPs, ", ")))
								out.WriteString(fmt.Sprintf("  Total: %d, Allocated: %d, Free: %d\n", pool.Total, pool.Allocated, pool.Total-pool.Allocated))
								return out.String()
							}
						}
						// 如果没找到绑定，显示所有池
						out.WriteString("No pool bound to this interface\n")
						if len(state.DHCP.Pools) > 0 {
							out.WriteString("\nAvailable pools:\n")
							for name, pool := range state.DHCP.Pools {
								out.WriteString(fmt.Sprintf("  %s: %s/%s\n", name, pool.Network, pool.Mask))
							}
						}
						return out.String()
					}
				}
				// display ip pool - 显示所有地址池概览
				var out strings.Builder
				out.WriteString("IP Pool information\n")
				out.WriteString("----------------------------------------------------\n")
				if state.DHCP == nil || len(state.DHCP.Pools) == 0 {
					out.WriteString("No DHCP pool configured\n")
					return out.String()
				}
				out.WriteString(fmt.Sprintf("%-15s %-18s %-10s %-10s %-10s\n", "PoolName", "Network", "Total", "Allocated", "Free"))
				out.WriteString("---------------------------------------------------------------\n")
				for _, pool := range state.DHCP.Pools {
					free := pool.Total - pool.Allocated
					if free < 0 {
						free = 0
					}
					network := pool.Network
					if pool.Mask != "" {
						network = fmt.Sprintf("%s/%s", pool.Network, pool.Mask)
					}
					out.WriteString(fmt.Sprintf("%-15s %-18s %-10d %-10d %-10d\n", pool.Name, network, pool.Total, pool.Allocated, free))
				}
				return out.String()
			}
			if arg1 == "routing-table" || arg1 == "route" {
				isVTEP := state.DeviceType == topology.DeviceVTEP
				if isSwitch && !isVTEP || isHost || isCloudHub || isAC {
					return fmt.Sprintf("Error: IP routing-table is not supported on %s", state.DeviceType)
				}
				targetIP := ""
				verbose := false
				if len(cmd.Args) >= 3 {
					if strings.ToLower(cmd.Args[2]) == "verbose" {
						verbose = true
					} else {
						targetIP = cmd.Args[2]
						if len(cmd.Args) >= 4 && strings.ToLower(cmd.Args[3]) == "verbose" {
							verbose = true
						}
					}
				}
				return formatRoutingTable(state, targetIP, verbose)
			}
			// display ip interface [interface-name] [brief]
			// 支持可选接口名参数（大小写不敏感，落到拓扑里的规范名），以及 brief 关键字；
			// 其余多余参数按华为 VRP 规则拒绝。
			//   dis ip int                -> 全部接口
			//   dis ip int brief          -> 全部接口简表
			//   dis ip int 10GE5/0/1      -> 指定接口
			//   dis ip int 10GE5/0/1 brief-> 指定接口简表
			if len(cmd.Args) > 4 {
				return "Error: Too many parameters found at '^' position."
			}
			brief := false
			ifName := ""
			for i := 2; i < len(cmd.Args); i++ {
				a := strings.ToLower(cmd.Args[i])
				// v0.12.1：brief 仅精确匹配完整关键字，不静默展开单字母缩写
				// （此前 b/br/bri 经 resolveKeyword 唯一前缀展开成 brief 静默执行，
				// 与 dis aa 语义不一致）；非 brief token 按接口名处理，非法则报错。
				if a == "brief" {
					brief = true
				} else {
					// 非 brief 关键字，按接口名处理（大小写不敏感）
					ifName = cmd.Args[i]
				}
			}
			if ifName != "" {
				if len(state.Interfaces) > 0 {
					canon, err := parseInterface(ifName, interfaceKeys(state.Interfaces))
					if err != nil {
						return fmt.Sprintf("Error: invalid interface '%s'", ifName)
					}
					ifName = canon
				}
			}
			return displayIPInterface(state, brief, ifName)
		case "routing-table":
			isVTEP := state.DeviceType == topology.DeviceVTEP
			if isSwitch && !isVTEP || isHost || isCloudHub || isAC {
				return fmt.Sprintf("Error: IP routing-table is not supported on %s", state.DeviceType)
			}
			targetIP := ""
			verbose := false
			if len(cmd.Args) >= 2 {
				if strings.ToLower(cmd.Args[1]) == "verbose" {
					verbose = true
				} else {
					targetIP = cmd.Args[1]
					if len(cmd.Args) >= 3 && strings.ToLower(cmd.Args[2]) == "verbose" {
						verbose = true
					}
				}
			}
			return formatRoutingTable(state, targetIP, verbose)
		case "arp":
			var out strings.Builder
			out.WriteString("IP Address      MAC Address     Interface           Type     Age\n")
			out.WriteString("------------------------------------------------------------------------\n")
			if len(state.ARPTable) == 0 {
				out.WriteString("No ARP entries found\n")
			} else {
				for _, entry := range state.ARPTable {
					age := entry.Age
					if age == "" {
						age = "0"
					}
					out.WriteString(fmt.Sprintf("%-15s %-17s %-20s %-8s %4s\n", entry.IP, entry.MAC, entry.Interface, entry.Type, age))
				}
			}
			return out.String()
		case "nat":
			if !isFirewall && !isRouter && !isL3Switch {
				return fmt.Sprintf("Error: NAT is not supported on %s", state.DeviceType)
			}
			// 检查是否是 display nat server 或 display nat address-group
			if len(cmd.Args) > 1 {
				subArg := strings.ToLower(cmd.Args[1])
				if subArg == "server" {
					// display nat server
					var out strings.Builder
					out.WriteString("NAT Server Configuration:\n")
					out.WriteString("----------------------------------------------------\n")
					if state.NAT != nil && len(state.NAT.Servers) > 0 {
						out.WriteString("Global IP       Global Port  Protocol  Inside IP       Inside Port\n")
						for _, server := range state.NAT.Servers {
							out.WriteString(fmt.Sprintf("%-15s %-12s %-8s %-15s %-12s\n", server.GlobalIP, server.GlobalPort, server.Protocol, server.InsideIP, server.InsidePort))
						}
					} else {
						out.WriteString("No NAT server configured\n")
					}
					return out.String()
				}
				if subArg == "address-group" {
					// display nat address-group
					var out strings.Builder
					out.WriteString("NAT Address Group Configuration:\n")
					out.WriteString("----------------------------------------------------\n")
					if state.NAT != nil && len(state.NAT.AddressPools) > 0 {
						out.WriteString("ID    Start IP        End IP\n")
						for _, pool := range state.NAT.AddressPools {
							out.WriteString(fmt.Sprintf("%-5d %-15s %-15s\n", pool.ID, pool.StartIP, pool.EndIP))
						}
					} else {
						out.WriteString("No NAT address-group configured\n")
					}
					return out.String()
				}
			}
			// display nat - 显示所有 NAT 表项
			var out strings.Builder
			out.WriteString("Protocol  Global IP       Global Port  Inside IP       Inside Port  Type\n")
			for _, entry := range state.NATTable {
				out.WriteString(fmt.Sprintf("%-9s %-15s %-12s %-15s %-12s %s\n", entry.Protocol, entry.GlobalIP, entry.GlobalPort, entry.InsideIP, entry.InsidePort, entry.Type))
			}
			return out.String()
		case "vlan":
			if !isSwitch && !isL3Switch {
				return fmt.Sprintf("Error: VLAN is not supported on %s", state.DeviceType)
			}
			// display vlan <id> - 显示指定 VLAN 信息
			if len(cmd.Args) > 1 {
				vlanID, err := parseNum(cmd.Args[1])
				if err == nil {
					if vlan, ok := state.VLANs[vlanID]; ok {
						var out strings.Builder
						out.WriteString(fmt.Sprintf("VLAN ID: %d\n", vlan.ID))
						out.WriteString(fmt.Sprintf("Name: %s\n", vlan.Name))
						out.WriteString(fmt.Sprintf("Status: %s\n", vlan.Status))
						out.WriteString(fmt.Sprintf("Ports: %s\n", strings.Join(vlan.Ports, ", ")))
						return out.String()
					}
					return fmt.Sprintf("Error: VLAN %d does not exist", vlanID)
				}
			}
			// display vlan - 显示所有 VLAN
			var out strings.Builder
			out.WriteString("VLAN ID  Name        Status  Ports\n")
			for _, vlan := range state.VLANs {
				ports := strings.Join(vlan.Ports, ", ")
				out.WriteString(fmt.Sprintf("%-8d %-12s %-7s %s\n", vlan.ID, vlan.Name, vlan.Status, ports))
			}
			return out.String()
		case "mac-address":
			if !isSwitch && !isL3Switch {
				return fmt.Sprintf("Error: MAC address is not supported on %s", state.DeviceType)
			}
			var out strings.Builder
			out.WriteString("MAC Address       VLAN  Interface            Type\n")
			for _, entry := range state.MACTable {
				out.WriteString(fmt.Sprintf("%-17s %-5d %-20s %s\n", entry.MAC, entry.VLAN, entry.Interface, entry.Type))
			}
			return out.String()
		case "port-security":
			// 端口安全状态展示（T04）。可选 interface 过滤：
			//   display port-security                → 全接口表
			//   display port-security interface <if> → 单端口详情
			filter := ""
			if arg1 == "interface" && len(cmd.Args) > 2 {
				filter = cmd.Args[2]
			}
			return buildPortSecurityDisplay(state, filter)
		case "interface":
			arg2 := ""
			if len(cmd.Args) > 2 {
				arg2 = strings.ToLower(cmd.Args[2])
			}
			isBrief := arg1 == "brief" || arg2 == "brief"
			// 收集所有配置的接口
			type ifaceInfo struct {
				Name        string
				Status      string
				Protocol    string
				Description string
				IP          string
				Mask        string
				Speed       string
				Duplex      string
				MTU         string
			}
			ifaceMap := make(map[string]*ifaceInfo)
			// 从 DeviceConfig 中读取接口信息
			for k, v := range state.DeviceConfig {
				if !strings.HasPrefix(k, "interface:") {
					continue
				}
				parts := strings.SplitN(k, ":", 3)
				if len(parts) < 2 {
					continue
				}
				ifaceName := parts[1]
				key := parts[2]
				if _, ok := ifaceMap[ifaceName]; !ok {
					ifaceMap[ifaceName] = &ifaceInfo{Name: ifaceName, Status: "up", Protocol: "up"}
				}
				switch key {
				case "status":
					ifaceMap[ifaceName].Status = v
				case "description":
					ifaceMap[ifaceName].Description = v
				case "ip":
					// config 键以 "IP MASK" 空格形态存储，拆出 IP 与 Mask 分别填充，
					// 避免 display 时把整串当 IP 后又补 "/Mask" 造成掩码重复渲染。
					ip, mask := splitInterfaceIPConfig(v)
					ifaceMap[ifaceName].IP = ip
					ifaceMap[ifaceName].Mask = mask
				case "speed":
					ifaceMap[ifaceName].Speed = v
				case "duplex":
					ifaceMap[ifaceName].Duplex = v
				case "mtu":
					ifaceMap[ifaceName].MTU = v
				}
			}
			// 从 state.Interfaces 合并信息
			for _, iface := range state.Interfaces {
				if _, ok := ifaceMap[iface.Name]; !ok {
					ifaceMap[iface.Name] = &ifaceInfo{Name: iface.Name}
				}
				if ifaceMap[iface.Name].Status == "up" && iface.Status != "" {
					ifaceMap[iface.Name].Status = iface.Status
				}
				if ifaceMap[iface.Name].Description == "" {
					ifaceMap[iface.Name].Description = iface.Description
				}
				if ifaceMap[iface.Name].IP == "" {
					ifaceMap[iface.Name].IP = iface.IP
				}
				if ifaceMap[iface.Name].Mask == "" {
					ifaceMap[iface.Name].Mask = iface.Mask
				}
			}
			var out strings.Builder
			// 检查是否指定了接口名
			specifiedIface := ""
			if !isBrief && len(cmd.Args) > 1 {
				specifiedIface = strings.ToLower(cmd.Args[1])
				if len(cmd.Args) > 2 && strings.ToLower(cmd.Args[2]) != "brief" {
					specifiedIface += cmd.Args[2]
				}
			}

			if isBrief {
				// 对齐真机华为 VRP：`display interface brief` 输出「图例块 + 表头 + 数据行」，
				// 既没有破折号分隔线，也没有 Rate / Description 两列（那是本项目早期自造的），
				// 真机列为 PHY / Protocol / InUti / OutUti / inErrors / outErrors。
				out.WriteString(interfaceBriefLegend)
				out.WriteString(interfaceBriefHeader)
				// 确定性排序：LoopBack → Vlanif → 其余物理口，同类内按**自然序**
				// （接口编号做数值比较，0/0/3 在 0/0/24 之前，对齐真机 VRP）。
				// 与 display ip interface brief 共用 sortInterfaceNames，口径完全一致，
				// 同时避免 map 迭代顺序随机导致输出抖动。
				briefNames := make([]string, 0, len(ifaceMap))
				for name := range ifaceMap {
					briefNames = append(briefNames, name)
				}
				sortInterfaceNames(briefNames)
				tunnelSeen := false
				for _, name := range briefNames {
					iface := ifaceMap[name]
					physical := "up"
					if iface.Status == "Down" || iface.Status == "down" {
						physical = "down"
					}
					protocol := physical
					// 改动点 #11：Tunnel 口协议态改为诚实短态（up*/down），非 Tunnel 口逐字不变。
					if isTunnelInterface(iface.Name) {
						protocol = greLineProtocolBrief(EvaluateGRE(state, iface.Name).Config)
						tunnelSeen = true
					}
					// 诚实占位：lite 引擎不做真实数据平面，无法统计真实利用率与错误计数，
					// 故一律输出零值（0% / 0），严禁编造随机数字。
					out.WriteString(fmt.Sprintf(interfaceBriefRowFormat,
						iface.Name, physical, protocol,
						interfaceBriefZeroUtil, interfaceBriefZeroUtil,
						interfaceBriefZeroCount, interfaceBriefZeroCount))
				}
				// 仅当输出中存在至少一个 Tunnel 口时才追加脚注；无 Tunnel 口时不输出（零回归）。
				if tunnelSeen {
					out.WriteString("* Tunnel protocol state is derived from local configuration only.\n")
				}
			} else if specifiedIface != "" {
				// 显示指定接口的详细信息
				var targetIface *ifaceInfo
				for _, iface := range ifaceMap {
					if strings.EqualFold(iface.Name, specifiedIface) ||
						strings.HasSuffix(strings.ToLower(iface.Name), specifiedIface) {
						targetIface = iface
						break
					}
				}
				if targetIface == nil {
					return fmt.Sprintf("Error: Interface %s does not exist", specifiedIface)
				}
				status := targetIface.Status
				if status == "" {
					status = "UP"
				}
				out.WriteString(fmt.Sprintf("%s current state : %s\n", targetIface.Name, status))
				// A11：Tunnel 口协议态 display 期诚实派生（C4），不写键；非 Tunnel 口逐字不变。
				if isTunnelInterface(targetIface.Name) {
					out.WriteString(fmt.Sprintf("Line protocol current state : %s\n", EvaluateGRE(state, targetIface.Name).LineProtocol))
				} else {
					out.WriteString("Line protocol current state : UP\n")
				}
				out.WriteString("Last line protocol up time : 0 days 0 hours 0 minutes\n")
				out.WriteString("\n")
				out.WriteString("Description : ")
				if targetIface.Description != "" {
					out.WriteString(targetIface.Description)
				} else {
					out.WriteString("-")
				}
				out.WriteString("\n")
				out.WriteString(fmt.Sprintf("Speed : %s\n", func() string {
					if targetIface.Speed != "" {
						return targetIface.Speed
					}
					if strings.Contains(targetIface.Name, "10GE") || strings.Contains(targetIface.Name, "10ge") {
						return "10Gbps"
					} else if strings.Contains(targetIface.Name, "GE") || strings.Contains(targetIface.Name, "ge") {
						return "1Gbps"
					}
					return "Auto"
				}()))
				out.WriteString(fmt.Sprintf("Duplex : %s\n", func() string {
					if targetIface.Duplex != "" {
						return targetIface.Duplex
					}
					return "Full"
				}()))
				out.WriteString(fmt.Sprintf("MTU : %s\n", func() string {
					if targetIface.MTU != "" {
						return targetIface.MTU
					}
					return "1500"
				}()))
				out.WriteString("\n")
				out.WriteString("Internet Address is ")
				if targetIface.IP != "" {
					out.WriteString(fmt.Sprintf("%s/%s\n", targetIface.IP, targetIface.Mask))
				} else {
					out.WriteString("not set\n")
				}
				// A11：Tunnel 口在 Internet Address 行后追加 GRE 详情块（含 --- Tunnel runtime
				// statistics --- 5 字段恒 - 与 greSimNote()）；非 Tunnel 口返回 ""，逐字不变。
				out.WriteString(buildGREInterfaceSection(state, targetIface.Name))
				out.WriteString("\n")
				out.WriteString("Statistics last cleared: Never\n")
				out.WriteString("\n")
				out.WriteString("    Input:  0 packets, 0 bytes\n")
				out.WriteString("             0 errors, 0 dropped\n")
				out.WriteString("    Output: 0 packets, 0 bytes\n")
				out.WriteString("             0 errors, 0 dropped\n")
			} else {
				out.WriteString("Interface                Status  Protocol  Description           IP Address\n")
				out.WriteString("------------------------------------------------------------------------\n")
				for _, iface := range ifaceMap {
					desc := iface.Description
					if desc == "" {
						desc = ""
					}
					ip := iface.IP
					if ip == "" {
						ip = "unassigned"
					}
					out.WriteString(fmt.Sprintf("%-25s %-8s %-9s %-21s %s\n", iface.Name, iface.Status, iface.Status, desc, ip))
				}
			}
			return out.String()
		case "ospf":
			if !isRouter && !isL3Switch && !isFirewall {
				return fmt.Sprintf("Error: OSPF is not supported on %s", state.DeviceType)
			}
			var out strings.Builder
			if state.OSPF.Enabled {
				out.WriteString(fmt.Sprintf("OSPF Process %d\n", state.OSPF.ProcessID))
				out.WriteString(fmt.Sprintf("  Area: %d\n", state.OSPF.AreaID))
				out.WriteString("  State: Running\n")
				out.WriteString("  Neighbors: 0\n")
			} else {
				out.WriteString("OSPF: Not configured\n")
			}
			return out.String()
		case "isis":
			if !isRouter && !isL3Switch && !isFirewall && state.DeviceType != topology.DeviceVTEP {
				return fmt.Sprintf("Error: ISIS is not supported on %s", state.DeviceType)
			}
			return buildIsisDisplay(state)
		case "acl":
			if isHost || isCloudHub || isAC {
				return fmt.Sprintf("Error: ACL is not supported on %s", state.DeviceType)
			}
			// 检查是否是 display acl configuration
			if len(cmd.Args) > 1 && strings.ToLower(cmd.Args[1]) == "configuration" {
				var out strings.Builder
				out.WriteString("ACL Configuration:\n")
				out.WriteString("----------------------------------------------------\n")
				for aclNum, rules := range state.ACLs {
					// 检查是否是命名 ACL（第一个规则的 Name 和 Type 字段）
					aclType := "basic"
					if len(rules) > 0 && rules[0] != nil {
						if rules[0].Type != "" {
							aclType = rules[0].Type
						}
					}
					out.WriteString(fmt.Sprintf("acl %s\n", aclNum))
					if rules[0] != nil && rules[0].Name != "" {
						out.WriteString(fmt.Sprintf("  description: ACL name %s, type %s\n", rules[0].Name, aclType))
					}
					for _, rule := range rules {
						if rule == nil {
							continue
						}
						// 跳过命名 ACL 的类型头规则
						if rule.Name != "" && rule.Action == "" && rule.Protocol == "" {
							continue
						}
						out.WriteString(fmt.Sprintf("  rule %d %s %s", rule.ID, rule.Action, rule.Protocol))
						if rule.SrcIP != "" {
							out.WriteString(fmt.Sprintf(" source %s", rule.SrcIP))
							if rule.SrcWildcard != "" {
								out.WriteString(fmt.Sprintf(" %s", rule.SrcWildcard))
							}
						}
						if rule.DstIP != "" {
							out.WriteString(fmt.Sprintf(" destination %s", rule.DstIP))
							if rule.DstWildcard != "" {
								out.WriteString(fmt.Sprintf(" %s", rule.DstWildcard))
							}
						}
						if rule.DstPort != "" {
							out.WriteString(fmt.Sprintf(" destination-port %s %s", rule.DstPortOp, rule.DstPort))
							if rule.DstPortEnd != "" {
								out.WriteString(fmt.Sprintf(" %s", rule.DstPortEnd))
							}
						}
						out.WriteString("\n")
					}
				}
				return out.String()
			}
			// display acl - 显示 ACL 摘要
			var out strings.Builder
			for aclNum, rules := range state.ACLs {
				out.WriteString(fmt.Sprintf("ACL %s:\n", aclNum))
				for _, rule := range rules {
					out.WriteString(fmt.Sprintf("  Rule %d: %s %s", rule.ID, rule.Action, rule.Protocol))
					if rule.SrcIP != "" {
						out.WriteString(fmt.Sprintf(" source %s", rule.SrcIP))
					}
					if rule.DstIP != "" {
						out.WriteString(fmt.Sprintf(" destination %s", rule.DstIP))
					}
					out.WriteString("\n")
				}
			}
			return out.String()
		case "m-lag", "mlag":
			if !isL3Switch {
				return fmt.Sprintf("Error: M-LAG is not supported on %s", state.DeviceType)
			}
			var out strings.Builder
			if state.MLAG.DomainID > 0 {
				out.WriteString(fmt.Sprintf("M-LAG Domain %d:\n", state.MLAG.DomainID))
				out.WriteString(fmt.Sprintf("  System Priority: %d\n", state.MLAG.SystemPriority))
				out.WriteString(fmt.Sprintf("  System MAC: %s\n", state.MLAG.SystemMAC))
				out.WriteString(fmt.Sprintf("  Peer IP: %s\n", state.MLAG.PeerIP))
				out.WriteString(fmt.Sprintf("  Peer Link: %s\n", state.MLAG.PeerLink))
				out.WriteString(fmt.Sprintf("  DFS Group: %d\n", state.MLAG.DFSGroupID))
				out.WriteString(fmt.Sprintf("  DFS Mode: %s\n", state.MLAG.DFSMode))
				out.WriteString("  M-LAG Interfaces:\n")
				for iface, config := range state.MLAGInterfaces {
					out.WriteString(fmt.Sprintf("    %s: %s\n", iface, config))
				}
			} else {
				out.WriteString("M-LAG: Not configured\n")
			}
			return out.String()
		case "lldp":
			// display lldp local-information / display lldp interface <type> <num> / display lldp neighbor
			// v0.12.1：二级子命令白名单校验——未知输入不静默回退到 LLDP 概览
			// （dis lldp xyz 此前显示 Enabled/Disabled 误导），报 unknown command 完整命令。
			switch arg1 {
			case "", "local-information", "interface", "neighbor":
			default:
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			var out strings.Builder
			if arg1 == "local-information" {
				// 显示本地 LLDP 信息
				sysname := state.DeviceConfig["sysname"]
				if sysname == "" {
					sysname = string(state.DeviceType)
				}
				chassisID := "00e0-fc12-3456" // 模拟 chassis ID
				portID := "GigabitEthernet0/0/1"
				out.WriteString("LLDP local information:\n")
				out.WriteString("----------------------------------------------------\n")
				out.WriteString(fmt.Sprintf("Chassis ID:       %s\n", chassisID))
				out.WriteString(fmt.Sprintf("Port ID:         %s\n", portID))
				out.WriteString(fmt.Sprintf("System Name:     %s\n", func() string {
					if state.LLDP.SystemName != "" {
						return state.LLDP.SystemName
					}
					return sysname
				}()))
				out.WriteString(fmt.Sprintf("System Description: %s\n", func() string {
					if state.LLDP.SystemDescription != "" {
						return state.LLDP.SystemDescription
					}
					return "Huawei Versatile Routing Platform Software"
				}()))
				out.WriteString(fmt.Sprintf("Management Address: %s\n", func() string {
					if state.LLDP.ManagementAddress != "" {
						return state.LLDP.ManagementAddress
					}
					return "Not configured"
				}()))
				out.WriteString(fmt.Sprintf("LLDP Enable:     %s\n", func() string {
					if state.LLDP.Enabled {
						return "Enabled"
					}
					return "Disabled"
				}()))
				out.WriteString("\nPort information:\n")
				if state.LLDP.PortConfig != nil && len(state.LLDP.PortConfig) > 0 {
					for ifaceName, enabled := range state.LLDP.PortConfig {
						status := "Disabled"
						if enabled {
							status = "Enabled"
						}
						out.WriteString(fmt.Sprintf("  %s: %s\n", ifaceName, status))
					}
				} else {
					out.WriteString("  No port-specific LLDP configuration\n")
				}
				return out.String()
			}
			if arg1 == "interface" {
				// display lldp interface <type> <num>
				if len(cmd.Args) < 4 {
					return "Error: usage: display lldp interface <type> <num>"
				}
				ifaceType := cmd.Args[2]
				ifaceNum := cmd.Args[3]
				ifaceName := ifaceType + ifaceNum
				out.WriteString(fmt.Sprintf("LLDP interface %s information:\n", ifaceName))
				out.WriteString("----------------------------------------------------\n")
				out.WriteString(fmt.Sprintf("Interface:       %s\n", ifaceName))
				out.WriteString(fmt.Sprintf("LLDP Status:     %s\n", func() string {
					if state.LLDP.PortConfig != nil {
						if enabled, ok := state.LLDP.PortConfig[ifaceName]; ok && enabled {
							return "Enabled"
						}
					}
					if state.LLDP.Enabled {
						return "Enabled (global)"
					}
					return "Disabled"
				}()))
				out.WriteString(fmt.Sprintf("Port ID:         %s\n", ifaceName))
				out.WriteString(fmt.Sprintf("Port Description: %s\n", func() string {
					if desc, ok := state.DeviceConfig[fmt.Sprintf("interface:%s:description", ifaceName)]; ok && desc != "" {
						return desc
					}
					return "Not configured"
				}()))
				return out.String()
			}
			if arg1 == "neighbor" {
				// display lldp neighbor
				out.WriteString("LLDP neighbor information:\n")
				out.WriteString("----------------------------------------------------\n")
				out.WriteString(fmt.Sprintf("%-20s %-20s %-15s %s\n", "Chassis ID", "Port ID", "System Name", "Management Address"))
				out.WriteString("-------------------------------------------------------------------------------\n")
				// 模拟邻居信息
				out.WriteString(fmt.Sprintf("%-20s %-20s %-15s %s\n", "00e0-fc12-789a", "GigabitEthernet0/0/2", "Switch-01", "192.168.1.1"))
				out.WriteString(fmt.Sprintf("%-20s %-20s %-15s %s\n", "00e0-fc12-789b", "GigabitEthernet0/0/3", "Router-01", "10.0.0.1"))
				return out.String()
			}
			// 默认 display lldp
			out.WriteString(fmt.Sprintf("LLDP: %s\n", func() string {
				if state.LLDP.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			return out.String()
		case "stp":
			// STP/RSTP/MSTP 显示（P2 第四项）：纯函数渲染，无副作用。
			// 单事实源 = DeviceConfig（stp:* / interface:*:stp:*），display 经 buildSTPDisplay 即时派生。
			// v0.12.1：二级子命令白名单校验——未知输入不静默回退 STP 概览。
			switch arg1 {
			case "", "brief", "interface", "region-configuration":
			default:
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			return buildSTPDisplay(state, arg1, cmd.Args)
		case "vrrp":
			// 忠实展示 VRRP 组（P2 第三项）：支持 brief / interface <if> / vrid <id> / 全接口。
			// 只读 collectVRRPGroups + EvaluateVRRP，无副作用；末尾附诚实占位注记。
			// v0.12.1：二级子命令白名单校验——未知输入不静默回退 VRRP 概览。
			switch arg1 {
			case "", "brief", "interface", "vrid":
			default:
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			return buildVRRPDisplay(state, arg1, cmd.Args)
		case "dhcp":
			// P2 #6（T3）：display dhcp relay [all | interface <if>]。
			// 只读渲染，唯一数据源 = EvaluateDHCPRelay；拍板 #5「display 任意设备可读」，
			// 故此处**不做设备类型守卫**（二层交换机读到的就是「无中继接口」，忠实呈现）。
			if strings.EqualFold(arg1, "relay") {
				return buildDHCPRelayDisplay(state, cmd.Args[2:])
			}
			// 其它 display dhcp <x> 本期未实现，明确拒绝而非静默返回空。
			return errUnrecognizedCommand
		case "ipsec":
			// v0.12.1：ipsec 二级子命令未实现——未知输入不静默忽略参数输出概览。
			if arg1 != "" {
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			var out strings.Builder
			if len(state.IPsec) > 0 {
				out.WriteString("IPsec Tunnels:\n")
				for _, tunnel := range state.IPsec {
					out.WriteString(fmt.Sprintf("  Tunnel %s:\n", tunnel.TunnelID))
					out.WriteString(fmt.Sprintf("    Local IP: %s\n", tunnel.LocalIP))
					out.WriteString(fmt.Sprintf("    Remote IP: %s\n", tunnel.RemoteIP))
					out.WriteString(fmt.Sprintf("    Mode: %s\n", tunnel.Mode))
					out.WriteString(fmt.Sprintf("    Encryption: %s\n", tunnel.Encryption))
					out.WriteString(fmt.Sprintf("    Authentication: %s\n", tunnel.Authentication))
				}
			} else {
				out.WriteString("IPsec: Not configured\n")
			}
			return out.String()
		case "snmp":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("SNMP: %s\n", func() string {
				if state.SNMP.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.SNMP.Enabled {
				out.WriteString(fmt.Sprintf("  Version: %s\n", state.SNMP.Version))
				out.WriteString(fmt.Sprintf("  Community: %s\n", state.SNMP.Community))
				out.WriteString(fmt.Sprintf("  Manager IP: %s\n", state.SNMP.ManagerIP))
			}
			return out.String()
		case "syslog":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("Syslog: %s\n", func() string {
				if state.Syslog.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.Syslog.Enabled {
				out.WriteString(fmt.Sprintf("  Server: %s:%d\n", state.Syslog.ServerIP, state.Syslog.ServerPort))
			}
			return out.String()
		case "ntp":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("NTP: %s\n", func() string {
				if state.NTP.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.NTP.Enabled {
				out.WriteString(fmt.Sprintf("  Server: %s:%d\n", state.NTP.ServerIP, state.NTP.ServerPort))
			}
			return out.String()
		case "ssh":
			// v0.12.1：二级子命令白名单校验——未知输入不静默回退到 SSH 概览。
			switch arg1 {
			case "", "server", "server-status":
			default:
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			// 检查是否是 ssh server status 子命令
			if arg1 == "server" || arg1 == "server-status" {
				var out strings.Builder
				out.WriteString("SSH Server Status:\n")
				out.WriteString("----------------------------------------------------\n")
				out.WriteString(fmt.Sprintf("SSH Server:               %s\n", func() string {
					if state.SSH.Enabled {
						return "Enable"
					}
					return "Disable"
				}()))
				out.WriteString(fmt.Sprintf("STelnet Server:          %s\n", func() string {
					if state.SSH.STelnetEnabled {
						return "Enable"
					}
					return "Disable"
				}()))
				out.WriteString(fmt.Sprintf("SSH Version:              %s\n", state.SSH.Version))
				out.WriteString(fmt.Sprintf("SSH Port:                 %d\n", state.SSH.Port))
				out.WriteString(fmt.Sprintf("RSA Key Pair:             %s\n", func() string {
					if state.SSH.RSAGenDone {
						return "Generated"
					}
					return "Not Generated"
				}()))
				out.WriteString(fmt.Sprintf("Authentication Mode:      %s\n", state.SSH.Authentication))
				out.WriteString(fmt.Sprintf("Max Sessions:             %d\n", state.SSH.MaxSessions))
				out.WriteString("\nVTY Configuration:\n")
				out.WriteString("----------------------------------------------------\n")
				if state.VTY != nil {
					out.WriteString(fmt.Sprintf("Authentication Mode:      %s\n", state.VTY.AuthenticationMode))
					out.WriteString(fmt.Sprintf("User Privilege Level:     %d\n", state.VTY.UserPrivilegeLevel))
					out.WriteString(fmt.Sprintf("Protocol Inbound:         %s\n", state.VTY.ProtocolInbound))
				}
				if len(state.SSH.Users) > 0 {
					out.WriteString("\nSSH Users:\n")
					out.WriteString("----------------------------------------------------\n")
					for _, user := range state.SSH.Users {
						out.WriteString(fmt.Sprintf("User: %s, Auth-Type: %s\n", user.Name, user.AuthType))
					}
				}
				// Local Users 段改读 AAA 新事实源（P2 第八项 T7 / A11）：
				// 旧实现随机遍历本地用户结构体 map 且 `Privilege: %d` 恒打印假 0，
				// 现由 fixSSHLocalUsersDisplay 统一输出（名称升序 + 口令脱敏 + 诚实注记）。
				out.WriteString(fixSSHLocalUsersDisplay(state))
				return out.String()
			}
			var out strings.Builder
			out.WriteString(fmt.Sprintf("SSH: %s\n", func() string {
				if state.SSH.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.SSH.Enabled {
				out.WriteString(fmt.Sprintf("  Version: %s\n", state.SSH.Version))
				out.WriteString(fmt.Sprintf("  Port: %d\n", state.SSH.Port))
				out.WriteString(fmt.Sprintf("  Authentication: %s\n", state.SSH.Authentication))
				out.WriteString(fmt.Sprintf("  Max Sessions: %d\n", state.SSH.MaxSessions))
			}
			return out.String()
		case "vxlan":
			// v0.12.1：二级子命令白名单校验——未知输入不静默回退到 VXLAN 概览。
			switch arg1 {
			case "", "tunnel":
			default:
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			if arg1 == "tunnel" {
				return buildVXLANTunnelDisplay(state)
			}
			var out strings.Builder
			if state.VXLAN.Enabled {
				out.WriteString("VXLAN Configuration:\n")
				out.WriteString(fmt.Sprintf("  VNI: %d\n", state.VXLAN.VNI))
				out.WriteString(fmt.Sprintf("  Local VTEP IP: %s\n", state.VXLAN.VTEPIP))
				out.WriteString(fmt.Sprintf("  Peer VTEP IP: %s\n", state.VXLAN.PeerVTEPIP))
				out.WriteString(fmt.Sprintf("  VRF: %s\n", state.VXLAN.VRFName))
				if state.VXLAN.EvpnEnabled {
					out.WriteString("  EVPN: Enabled\n")
					if rd := state.DeviceConfig["vxlan:route-distinguisher"]; rd != "" {
						out.WriteString(fmt.Sprintf("  Route Distinguisher: %s\n", rd))
					}
					if rt := state.DeviceConfig["vxlan:vpn-target"]; rt != "" {
						out.WriteString(fmt.Sprintf("  VPN Target: %s\n", rt))
					}
				}
				if len(state.VXLAN.VSIs) > 0 {
					out.WriteString("\nVSI List:\n")
					for name, vsi := range state.VXLAN.VSIs {
						out.WriteString(fmt.Sprintf("  %s: status=%s\n", name, vsi.Status))
					}
				}
			} else {
				out.WriteString("VXLAN: Not configured\n")
			}
			return out.String()
		case "bgp":
			// display bgp evpn [peer|routing-table|vni]：EVPN 地址族诚实占位（P2 / AC6）。
			// 此前内层 switch 的 bgp 分支漏接该子命令，仅回 "BGP: Not configured"；
			// 现补接（与 display_registry.go 的 regBgpDisplay arg1=="evpn" 分支同源）。
			if arg1 == "evpn" {
				return buildEVPNBGPDisplay(state)
			}
			// display bgp peer：逐邻居明细表（P1-F，T06）
			if arg1 == "peer" {
				return buildBGPPeerDisplay(state)
			}
			// v0.12.1：未知二级子命令不得静默回退到 BGP 概览（dis bgp e / dis bgp xyz
			// 此前输出 Not configured 误导）——报 unknown command 指向完整命令。
			if arg1 != "" {
				return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
			}
			var out strings.Builder
			if state.BGP.Enabled {
				out.WriteString(fmt.Sprintf("BGP Configuration:\n"))
				out.WriteString(fmt.Sprintf("  AS Number: %d\n", state.BGP.ASNumber))
				out.WriteString(fmt.Sprintf("  Router ID: %s\n", state.BGP.RouterID))
				out.WriteString("  Neighbors:\n")
				for _, neighbor := range state.BGP.Neighbors {
					out.WriteString(fmt.Sprintf("    %s: Remote AS %d, %s\n", neighbor.IPAddress, neighbor.RemoteAS, func() string {
						if neighbor.EBGP {
							return "EBGP"
						}
						return "IBGP"
					}()))
				}
			} else {
				out.WriteString("BGP: Not configured\n")
			}
			return out.String()
		case "diagnostic-information":
			// 单命令设备体检报告（P1-F，T06）
			return buildDiagnosticInfo(state)
		case "bfd":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("BFD: %s\n", func() string {
				if state.BFD.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.BFD.Enabled {
				out.WriteString("Sessions:\n")
				for _, session := range state.BFD.Sessions {
					out.WriteString(fmt.Sprintf("  Peer: %s, Local: %s\n", session.PeerIP, session.LocalIP))
					out.WriteString(fmt.Sprintf("    Min Tx/Rx: %d/%d ms\n", session.MinTxInterval, session.MinRxInterval))
					out.WriteString(fmt.Sprintf("    Detect Mult: %d\n", session.DetectMult))
				}
			}
			return out.String()
		case "vrf":
			var out strings.Builder
			if len(state.VRF) > 0 {
				out.WriteString("VRF Instances:\n")
				for name, vrf := range state.VRF {
					out.WriteString(fmt.Sprintf("  %s:\n", name))
					out.WriteString(fmt.Sprintf("    RD: %s\n", vrf.RD))
					out.WriteString(fmt.Sprintf("    Route Targets: %v\n", vrf.RouteTargets))
					out.WriteString(fmt.Sprintf("    Interfaces: %v\n", vrf.Interfaces))
				}
			} else {
				out.WriteString("VRF: Not configured\n")
			}
			return out.String()
		case "pbr":
			var out strings.Builder
			if len(state.PBR) > 0 {
				out.WriteString("Policy-Based Routing:\n")
				for name, rules := range state.PBR {
					out.WriteString(fmt.Sprintf("  Policy: %s\n", name))
					for _, rule := range rules {
						out.WriteString(fmt.Sprintf("    Rule %d: match acl %s -> next-hop %s\n", rule.ID, rule.MatchACL, rule.NextHop))
					}
				}
			} else {
				out.WriteString("PBR: Not configured\n")
			}
			return out.String()
		case "gre":
			// GRE 隧道汇总（P2 第七项，C6 重定向落点）：旧自造 display gre 重定向到
			// display gre tunnel 新实现（只读，无副作用，确定性升序，AC7）。
			return buildGREDisplay(state, cmd.Args[1:])
		case "aaa":
			// AAA 汇总 / 二级子命令（参数级补全落地，与 displayParamSpecs["aaa"] 严格一致；
			// arg1 为归一化二级子命令，见 normalizeDisplaySubCmd2）。委托 regAaaDisplay
			// 统一处理，使 displayRegistry 成为 display aaa 的单一事实源。
			return regAaaDisplay(state, cmd, arg0, arg1)
		case "local-user":
			// 本地用户表（P0-12/P0-13）：口令恒脱敏 ****，未配口令显示 -，
			// privilege 未配显示 - 而非假 0。
			return buildAAALocalUserDisplay(state)
		case "domain":
			// AAA 域汇总 / 详情（P1-7）。域名大小写敏感，故取原始 cmd.Args[1]
			// 而非已 ToLower 的 arg1。
			domainName := ""
			if len(cmd.Args) > 1 {
				domainName = cmd.Args[1]
			}
			return buildAAADomainDisplay(state, domainName)
		case "qos":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("QoS: %s\n", func() string {
				if state.QoS.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.QoS.Enabled {
				out.WriteString("  Classifiers:\n")
				for name, classifier := range state.QoS.Classifiers {
					out.WriteString(fmt.Sprintf("    %s: acl %s, dscp %d\n", name, classifier.ACL, classifier.DSCP))
				}
				out.WriteString("  Behaviors:\n")
				for name, behavior := range state.QoS.Behaviors {
					out.WriteString(fmt.Sprintf("    %s: bandwidth %d kbps, priority %d\n", name, behavior.Bandwidth, behavior.Priority))
				}
				out.WriteString("  Policies:\n")
				for name, policy := range state.QoS.Policies {
					out.WriteString(fmt.Sprintf("    %s: classifier %s -> behavior %s\n", name, policy.Classifier, policy.Behavior))
				}
			}
			return out.String()
		case "dot1x":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("802.1X: %s\n", func() string {
				if state.Dot1x.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.Dot1x.Enabled {
				out.WriteString("  Ports:\n")
				for name, port := range state.Dot1x.Ports {
					out.WriteString(fmt.Sprintf("    %s: auth %s, reauth %t\n", name, port.AuthMethod, port.Reauth))
				}
			}
			return out.String()
		case "radius":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("RADIUS: %s\n", func() string {
				if state.RADIUS.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.RADIUS.Enabled {
				out.WriteString(fmt.Sprintf("  Primary Server: %s:%d\n", state.RADIUS.PrimaryServer, state.RADIUS.AuthPort))
				out.WriteString(fmt.Sprintf("  Secondary Server: %s:%d\n", state.RADIUS.SecondaryServer, state.RADIUS.AuthPort))
				out.WriteString(fmt.Sprintf("  Accounting Port: %d\n", state.RADIUS.AcctPort))
				out.WriteString(fmt.Sprintf("  Timeout: %d, Retransmit: %d\n", state.RADIUS.Timeout, state.RADIUS.Retransmit))
			}
			return out.String()
		case "netflow":
			var out strings.Builder
			out.WriteString(fmt.Sprintf("NetFlow: %s\n", func() string {
				if state.NetFlow.Enabled {
					return "Enabled"
				}
				return "Disabled"
			}()))
			if state.NetFlow.Enabled {
				out.WriteString(fmt.Sprintf("  Exporter: %s:%d\n", state.NetFlow.Exporter, state.NetFlow.Port))
				out.WriteString(fmt.Sprintf("  Version: %s\n", state.NetFlow.Version))
				out.WriteString(fmt.Sprintf("  Sample Rate: %d\n", state.NetFlow.SampleRate))
				out.WriteString(fmt.Sprintf("  Active Timeout: %d sec\n", state.NetFlow.ActiveTime))
				out.WriteString(fmt.Sprintf("  Inactive Timeout: %d sec\n", state.NetFlow.InactiveTime))
			}
			return out.String()
		case "sysname":
			sysname := state.DeviceConfig["sysname"]
			if sysname == "" {
				sysname = string(state.DeviceType)
			}
			return fmt.Sprintf("System name: %s\n", sysname)
		case "version":
			var out strings.Builder
			deviceModel := ""
			switch state.DeviceType {
			case topology.DeviceRouter:
				deviceModel = "Huawei Router NE40E"
			case topology.DeviceL3Switch:
				deviceModel = "Huawei Switch S12700"
			case topology.DeviceSwitch:
				deviceModel = "Huawei Switch S5700"
			case topology.DeviceFirewall:
				deviceModel = "Huawei Firewall USG6000"
			case topology.DeviceAC:
				deviceModel = "Huawei AC6005"
			case topology.DeviceAP:
				deviceModel = "Huawei AP7030DN"
			case topology.DevicePC:
				deviceModel = "PC"
			case topology.DeviceServer:
				deviceModel = "Huawei Server RH2288H"
			case topology.DeviceClient:
				deviceModel = "Client PC"
			case topology.DeviceCloud:
				deviceModel = "Cloud"
			case topology.DeviceHub:
				deviceModel = "Hub"
			case topology.DeviceVTEP:
				deviceModel = "Huawei VTEP Switch"
			default:
				deviceModel = string(state.DeviceType)
			}
			out.WriteString("Huawei Versatile Routing Platform Software\n")
			out.WriteString("VRP (R) software, Version 5.170 (V300R005C00)\n")
			out.WriteString("Copyright (C) 2012 Huawei Technologies Co., Ltd.\n")
			out.WriteString("\n")
			out.WriteString(fmt.Sprintf("%s uptime is 0 days 0 hours 0 minutes\n", deviceModel))
			out.WriteString("\n")
			out.WriteString("System image version: V300R005C00\n")
			out.WriteString("Boot image version: V300R005C00\n")
			return out.String()
		case "memory":
			var out strings.Builder
			out.WriteString("Memory Usage:\n")
			out.WriteString("----------------------------------------------------\n")
			out.WriteString("Total      Used       Free      Shared    Buffers   Cached\n")
			out.WriteString("----------------------------------------------------\n")
			out.WriteString("8192MB     1234MB     6958MB    0MB       0MB       0MB\n")
			out.WriteString("\n")
			out.WriteString("Memory utilization percentage: 15%\n")
			return out.String()
		case "cpu-usage":
			var out strings.Builder
			out.WriteString("CPU Usage:\n")
			out.WriteString("----------------------------------------------------\n")
			out.WriteString("CPU Usage Stat. Cycle: 60 seconds\n")
			out.WriteString("\n")
			out.WriteString("CPU Usage:    5%\n")
			out.WriteString("CPU0:         5%\n")
			out.WriteString("CPU1:         5%\n")
			out.WriteString("CPU2:         5%\n")
			out.WriteString("CPU3:         5%\n")
			out.WriteString("\n")
			out.WriteString("CPU utilization for five seconds:  5%         busy percentage: 5%\n")
			out.WriteString("CPU utilization for one minute:    5%         busy percentage: 5%\n")
			out.WriteString("CPU utilization for five minutes:  5%         busy percentage: 5%\n")
			return out.String()
		case "users":
			var out strings.Builder
			out.WriteString("User-Intf    Delay Type Network Address  AuthenStatus    AuthorcmdFlag\n")
			out.WriteString("------------------------------------------------------------------------\n")
			out.WriteString(fmt.Sprintf("VTY 0        00:00:00  TEL  127.0.0.1         pass          No Privilege\n"))
			return out.String()
		case "device":
			var out strings.Builder
			out.WriteString("Device Type: ")
			switch state.DeviceType {
			case topology.DeviceRouter:
				out.WriteString("Huawei Router NE40E\n")
			case topology.DeviceL3Switch:
				out.WriteString("Huawei Switch S12700\n")
			case topology.DeviceSwitch:
				out.WriteString("Huawei Switch S5700\n")
			case topology.DeviceFirewall:
				out.WriteString("Huawei Firewall USG6000\n")
			case topology.DeviceAC:
				out.WriteString("Huawei AC6005\n")
			case topology.DeviceAP:
				out.WriteString("Huawei AP6050DN\n")
			default:
				out.WriteString(fmt.Sprintf("%s\n", state.DeviceType))
			}
			out.WriteString("Slots: 2\n")
			out.WriteString("Active Slot: 0\n")
			out.WriteString("Standby Slot: 1\n")
			return out.String()
		case "clock":
			return "2026-06-29 09:30:00\nTime zone: UTC+08:00"
		case "temperature":
			var out strings.Builder
			out.WriteString("Slot  Temperature  CPU Temperature\n")
			out.WriteString("----------------------------------\n")
			out.WriteString("0     45C          52C\n")
			out.WriteString("1     43C          50C\n")
			return out.String()
		case "startup":
			var out strings.Builder
			out.WriteString("Startup system software:        flash:/vrp.cc\n")
			out.WriteString("Next startup system software:  flash:/vrp.cc\n")
			out.WriteString("Startup saved-configuration:    flash:/vrpcfg.cfg\n")
			out.WriteString("Next startup saved-configuration: flash:/vrpcfg.cfg\n")
			out.WriteString("Startup license file:           flash:/license.dat\n")
			if state.Saved {
				out.WriteString(fmt.Sprintf("Configuration saved:           Yes (%s)\n", state.SaveTime))
			} else {
				out.WriteString("Configuration saved:           No\n")
			}
			return out.String()
		case "saved-configuration":
			if !state.Saved || state.SavedConfig == "" {
				return "No saved configuration found.\nPlease use the 'save' command to save the current configuration."
			}
			var out strings.Builder
			out.WriteString("Saved configuration:\n")
			out.WriteString(state.SavedConfig)
			out.WriteString(fmt.Sprintf("\nConfiguration saved at %s\n", state.SaveTime))
			return out.String()
		case "history-command":
			// display history-command [max-size]：展示最近执行的命令行。
			// 可选数字参数限定展示条数（如 display history-command 20）。
			maxSize := 0
			if len(cmd.Args) >= 2 {
				if n, err := strconv.Atoi(cmd.Args[1]); err == nil && n > 0 {
					maxSize = n
				}
			}
			return state.FormatHistoryCommand(maxSize)
		case "evpn":
			// EVPN 概览 / 子命令（vni/peer/routing-table）诚实占位（P2 / AC6）。
			// 已注册于 display_registry.go 的 regEvpnDisplay，此前内层 switch 漏接 case，
			// 落到了 unknown command 兜底；此处补接线（锁死用例 TestDisplayEVPN）。
			return regEvpnDisplay(state, cmd, arg0, arg1)
		case "ndp":
			// NDP 邻居表诚实占位（P2）：本端地址来自真实 IPv6 接口，邻居列恒 '-'。
			// 已注册于 display_registry.go 的 regNdpDisplay，此前内层 switch 漏接 case；
			// 此处补接线（锁死用例 TestDisplayNDP）。
			return regNdpDisplay(state, cmd, arg0, arg1)
		}
		// displayRegistry 兜底派发（v0.12）：上面的巨型 switch 是历史实现，
		// display_registry.go 虽自称「单一事实源」却长期只被 Tab 补全引用，
		// 导致「注册了但执行落到 unknown command」的孤儿命令（evpn/ndp 曾如此）。
		// 此处在 switch 全部未命中后再查注册表：对已有 case 零影响（永远先被 case 截获），
		// 新增 display 命令只需注册即可执行 + 补全同时生效，从根上消除漂移。
		// 锁死用例：TestDisplayRegistryNoOrphan。
		if handler, ok := displayRegistry[arg0]; ok {
			return handler(state, cmd, arg0, arg1)
		}
	}
	// v0.12.1：报错回显完整命令（cmd.Command + Args 重组），不再只显示首 token。
	// 历史问题：`dis aa` 报 `unknown command 'dis'` 误导——dis 明明存在，
	// 问题在子命令 aa；回显完整命令让排障直接定位。
	return fmt.Sprintf("Error: unknown command '%s'", fullCommandText(cmd))
}

// ---------------------------------------------------------------------------
// STP/RSTP/MSTP 命令处理器与展示（P2 第四项，华为 VRP 课程 55/56/57）
// 单一事实源 = DeviceConfig（stp:<field> 系统级 + interface:<iface>:stp:<field> 接口级）。
// side-effect 仅在本文件落地；选举/角色经 stp_eval.go 纯函数 EvaluateSTP 派生。
// ---------------------------------------------------------------------------

// applySTP 处理系统/接口/MST region 视图下的 stp 命令族（按 state.CurrentView 分支）。
func applySTP(state *CLIState, args []string) string {
	switch state.CurrentView {
	case ViewSystem:
		return applySTPInSystem(state, args)
	case ViewInterface:
		return applyInterfaceSTP(state, args)
	case ViewMSTRegion:
		return applySTPRegion(state, args)
	default:
		return "Error: must be in system view"
	}
}

// applySTPInSystem 系统视图下的 stp 子命令（enable/disable/mode/priority/root/pathcost-standard/
// bpdu/root/loop/tc-protection/bridge-address/instance/region-configuration 等）。
// 成功回显 VRP 静默风格（不回 "STP enabled (RSTP)" 等硬编码）。
func applySTPInSystem(state *CLIState, args []string) string {
	if len(args) == 0 {
		state.DeviceConfig[stpKey("enabled")] = "true"
		return ""
	}
	sub := strings.ToLower(args[0])
	// 接口视图命令误用在系统视图 → 明确提示迁接口视图（拍板 #1）。
	if sub == "cost" || sub == "port" || sub == "edged-port" {
		return "Error: must be in interface view"
	}
	switch sub {
	case "enable":
		state.DeviceConfig[stpKey("enabled")] = "true"
		return ""
	case "disable":
		state.DeviceConfig[stpKey("enabled")] = "false"
		return ""
	case "mode":
		if len(args) < 2 {
			return "Error: usage: stp mode {stp|rstp|mstp}"
		}
		m := strings.ToLower(args[1])
		if m != "stp" && m != "rstp" && m != "mstp" {
			return "Error: invalid STP mode (expect stp|rstp|mstp)"
		}
		state.DeviceConfig[stpKey("mode")] = m
		return ""
	case "priority":
		if len(args) < 2 {
			return "Error: usage: stp priority <0-61440, multiple of 4096>"
		}
		pri, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid priority value"
		}
		if ok, msg := validPriority(pri); !ok {
			return msg
		}
		state.DeviceConfig[stpKey("priority")] = strconv.Itoa(pri)
		return ""
	case "root":
		if len(args) < 2 {
			return "Error: usage: stp root primary|secondary"
		}
		rt := strings.ToLower(args[1])
		if rt == "primary" {
			state.DeviceConfig[stpKey("priority")] = "0"
		} else if rt == "secondary" {
			state.DeviceConfig[stpKey("priority")] = strconv.Itoa(stpPriStep)
		} else {
			return "Error: usage: stp root primary|secondary"
		}
		return ""
	case "v-stp":
		// 最小保留（O7）：仅配置态，不触发真实 V-STP 跨设备同步。
		if len(args) < 2 {
			return "Error: usage: stp v-stp enable|disable"
		}
		v := strings.ToLower(args[1])
		if v == "enable" {
			state.DeviceConfig[stpKey("v-stp")] = "enable"
		} else if v == "disable" {
			state.DeviceConfig[stpKey("v-stp")] = "disable"
		} else {
			return "Error: usage: stp v-stp enable|disable"
		}
		return ""
	case "bridge-address":
		if len(args) < 2 {
			return "Error: usage: stp bridge-address <mac>"
		}
		mac, ok := canonMAC(args[1])
		if !ok {
			return fmt.Sprintf("Error: invalid MAC address %q", args[1])
		}
		state.DeviceConfig[stpKey("bridge-address")] = mac
		return ""
	case "pathcost-standard":
		if len(args) < 2 {
			return "Error: usage: stp pathcost-standard {dot1d-1998|dot1t|legacy}"
		}
		std := strings.ToLower(args[1])
		if std != "dot1d-1998" && std != "dot1t" && std != "legacy" {
			return "Error: invalid pathcost-standard (expect dot1d-1998|dot1t|legacy)"
		}
		state.DeviceConfig[stpKey("pathcost-standard")] = std
		return ""
	case "bpdu-protection":
		state.DeviceConfig[stpKey("bpdu-protection")] = "enable"
		return ""
	case "root-protection":
		state.DeviceConfig[stpKey("root-protection")] = "enable"
		return ""
	case "loop-protection":
		state.DeviceConfig[stpKey("loop-protection")] = "enable"
		return ""
	case "tc-protection":
		if len(args) >= 3 && strings.ToLower(args[1]) == "interval" {
			iv, err := parseNum(args[2])
			if err != nil {
				return "Error: invalid tc-protection interval"
			}
			state.DeviceConfig[stpKey("tc-protection-interval")] = strconv.Itoa(iv)
		}
		state.DeviceConfig[stpKey("tc-protection")] = "enable"
		return ""
	case "region-configuration":
		// 进入 MSTP 域配置视图（拍板 #6）。
		state.CurrentView = ViewMSTRegion
		state.CurrentSub = ""
		return "Enter MSTP region configuration view"
	case "instance":
		// stp instance <id> vlan <list> | stp instance <id> priority <n> |
		// stp instance <id> root primary|secondary（P1-8）。
		if len(args) < 2 {
			return "Error: usage: stp instance <id> {vlan <list>|priority <n>|root primary|secondary}"
		}
		id, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid instance ID"
		}
		if ok, msg := validInstanceID(id); !ok {
			return msg
		}
		if len(args) < 3 {
			return "Error: usage: stp instance <id> {vlan|priority|root}"
		}
		switch strings.ToLower(args[2]) {
		case "vlan":
			if len(args) < 4 {
				return "Error: usage: stp instance <id> vlan <list>"
			}
			vlanSpec := strings.Join(args[3:], " ")
			if !validVLANList(vlanSpec) {
				return fmt.Sprintf("Error: invalid VLAN list %q", vlanSpec)
			}
			state.DeviceConfig[fmt.Sprintf("stp:instance:%d:vlans", id)] = vlanSpec
		case "priority":
			if len(args) < 4 {
				return "Error: usage: stp instance <id> priority <n>"
			}
			pri, err := parseNum(args[3])
			if err != nil {
				return "Error: invalid priority value"
			}
			if ok, msg := validPriority(pri); !ok {
				return msg
			}
			state.DeviceConfig[fmt.Sprintf("stp:instance:%d:priority", id)] = strconv.Itoa(pri)
		case "root":
			if len(args) < 4 {
				return "Error: usage: stp instance <id> root primary|secondary"
			}
			rt := strings.ToLower(args[3])
			if rt == "primary" {
				state.DeviceConfig[fmt.Sprintf("stp:instance:%d:priority", id)] = "0"
				state.DeviceConfig[fmt.Sprintf("stp:instance:%d:root", id)] = "primary"
			} else if rt == "secondary" {
				state.DeviceConfig[fmt.Sprintf("stp:instance:%d:priority", id)] = strconv.Itoa(stpPriStep)
				state.DeviceConfig[fmt.Sprintf("stp:instance:%d:root", id)] = "secondary"
			} else {
				return "Error: usage: stp instance <id> root primary|secondary"
			}
		default:
			return "Error: usage: stp instance <id> {vlan|priority|root}"
		}
		return ""
	case "region-name", "revision-level", "active":
		// 这些为 MST region 视图命令，需先进入 region-configuration。
		return "Error: please enter MST region configuration view first (stp region-configuration)"
	default:
		return "Error: invalid STP config"
	}
}

// applyInterfaceSTP 接口视图下的 stp 子命令（cost/port priority/edged-port，拍板 #1 迁接口视图）。
func applyInterfaceSTP(state *CLIState, args []string) string {
	if state.CurrentView != ViewInterface {
		return "Error: must be in interface view"
	}
	iface := state.CurrentSub
	if len(args) == 0 {
		return "Error: usage: stp <cost|port priority|edged-port>"
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "cost":
		if len(args) < 2 {
			return "Error: usage: stp cost <1-200000000>"
		}
		c, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid cost value"
		}
		std := stpPathCostStd(state)
		if ok, msg := validCost(c, std); !ok {
			return msg
		}
		state.DeviceConfig[stpIfaceKey(iface, "cost")] = strconv.Itoa(c)
		return ""
	case "port":
		if len(args) < 3 || strings.ToLower(args[1]) != "priority" {
			return "Error: usage: stp port priority <0-240, multiple of 16>"
		}
		pp, err := parseNum(args[2])
		if err != nil {
			return "Error: invalid port priority value"
		}
		if ok, msg := validPortPriority(pp); !ok {
			return msg
		}
		state.DeviceConfig[stpIfaceKey(iface, "port-priority")] = strconv.Itoa(pp)
		return ""
	case "edged-port":
		if len(args) < 2 {
			return "Error: usage: stp edged-port enable|disable"
		}
		e := strings.ToLower(args[1])
		if e == "enable" {
			state.DeviceConfig[stpIfaceKey(iface, "edged-port")] = "enable"
		} else if e == "disable" {
			state.DeviceConfig[stpIfaceKey(iface, "edged-port")] = "disable"
		} else {
			return "Error: usage: stp edged-port enable|disable"
		}
		return ""
	default:
		// 系统视图命令误用在接口视图。
		return "Error: must be in system view"
	}
}

// applySTPRegion MST region 视图下的 stp 子命令（region-name/revision-level/instance/active）。
func applySTPRegion(state *CLIState, args []string) string {
	if state.CurrentView != ViewMSTRegion {
		return "Error: must be in MST region configuration view"
	}
	if len(args) == 0 {
		return "Error: incomplete command"
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "region-name":
		if len(args) < 2 {
			return "Error: usage: region-name <name>"
		}
		state.DeviceConfig[stpKey("region-name")] = args[1]
		return ""
	case "revision-level":
		if len(args) < 2 {
			return "Error: usage: revision-level <level>"
		}
		lv, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid revision level"
		}
		state.DeviceConfig[stpKey("revision-level")] = strconv.Itoa(lv)
		return ""
	case "instance":
		if len(args) < 4 || strings.ToLower(args[2]) != "vlan" {
			return "Error: usage: instance <id> vlan <list>"
		}
		id, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid instance ID"
		}
		if ok, msg := validInstanceID(id); !ok {
			return msg
		}
		vlanSpec := strings.Join(args[3:], " ")
		if !validVLANList(vlanSpec) {
			return fmt.Sprintf("Error: invalid VLAN list %q", vlanSpec)
		}
		state.DeviceConfig[fmt.Sprintf("stp:instance:%d:vlans", id)] = vlanSpec
		return ""
	case "active":
		if len(args) < 2 || strings.ToLower(args[1]) != "region-configuration" {
			return "Error: usage: active region-configuration"
		}
		state.DeviceConfig[stpKey("region-active")] = "true"
		return ""
	default:
		return "Error: invalid MST region configuration command"
	}
}

// applyUndoSTP 处理 undo stp [root|instance <id> root|bpdu-protection|...|region-configuration]
// （清全部 stp:* 与 interface:*:stp:* 键并写 stp:enabled=false，方案 A 保活禁用态）。
func applyUndoSTP(state *CLIState, args []string) string {
	sub := []string{}
	if len(args) > 1 {
		sub = args[1:]
	}
	if len(sub) == 0 {
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "stp:") || (strings.HasPrefix(k, "interface:") && strings.Contains(k, ":stp:")) {
				delete(state.DeviceConfig, k)
			}
		}
		state.DeviceConfig[stpKey("enabled")] = "false"
		return "STP disabled"
	}
	switch strings.ToLower(sub[0]) {
	case "root":
		delete(state.DeviceConfig, stpKey("priority"))
		return "STP root role removed"
	case "instance":
		if len(sub) < 3 || strings.ToLower(sub[2]) != "root" {
			return "Error: usage: undo stp instance <id> root"
		}
		id, err := parseNum(sub[1])
		if err != nil {
			return "Error: invalid instance ID"
		}
		delete(state.DeviceConfig, fmt.Sprintf("stp:instance:%d:priority", id))
		delete(state.DeviceConfig, fmt.Sprintf("stp:instance:%d:root", id))
		return fmt.Sprintf("STP instance %d root role removed", id)
	case "bpdu-protection":
		delete(state.DeviceConfig, stpKey("bpdu-protection"))
		return "STP BPDU protection removed"
	case "root-protection":
		delete(state.DeviceConfig, stpKey("root-protection"))
		return "STP root protection removed"
	case "loop-protection":
		delete(state.DeviceConfig, stpKey("loop-protection"))
		return "STP loop protection removed"
	case "tc-protection":
		delete(state.DeviceConfig, stpKey("tc-protection"))
		delete(state.DeviceConfig, stpKey("tc-protection-interval"))
		return "STP TC protection removed"
	case "bridge-address":
		delete(state.DeviceConfig, stpKey("bridge-address"))
		return "STP bridge-address removed"
	case "region-configuration":
		delete(state.DeviceConfig, stpKey("region-name"))
		delete(state.DeviceConfig, stpKey("revision-level"))
		delete(state.DeviceConfig, stpKey("region-active"))
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "stp:instance:") {
				delete(state.DeviceConfig, k)
			}
		}
		return "STP region configuration removed"
	default:
		return fmt.Sprintf("Error: undo '%s' is not supported", strings.Join(sub, " "))
	}
}

// validVLANList 校验 VRP VLAN 列表（支持 "2 to 10" / "2-10" / "10 20 30" / "2,10" 形态）。
func validVLANList(spec string) bool {
	tmp := strings.NewReplacer("to", " ", "TO", " ", "-", " ", ",", " ").Replace(spec)
	fields := strings.Fields(tmp)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 4094 {
			return false
		}
	}
	return true
}

// buildSTPDisplay 渲染 display stp [brief|interface <if>|region-configuration]（P2 第四项）。
// 只读 EvaluateSTP / collectSTPInstances，无副作用；末尾附 stpSimNote() 诚实注记。
func buildSTPDisplay(state *CLIState, arg1 string, args []string) string {
	if !isSTPEnabled(state) {
		return "STP: Disabled"
	}
	switch arg1 {
	case "brief":
		return renderSTPBrief(state)
	case "interface":
		iface := ""
		if len(args) >= 3 {
			iface = args[2]
		}
		return renderSTPInterface(state, iface)
	case "region-configuration":
		return renderSTPRegion(state)
	default:
		return renderSTPDefault(state)
	}
}

// renderSTPDefault 渲染 display stp（CIST Global Info + 各端口 Role/State + 诚实注记）。
func renderSTPDefault(state *CLIState) string {
	cist := EvaluateSTP(state, 0)
	var out strings.Builder
	out.WriteString("-------[CIST Global Info]-------\n")
	out.WriteString(fmt.Sprintf(" Mode                : %s\n", stpMode(state)))
	out.WriteString(fmt.Sprintf(" CIST Bridge         : %s\n", formatBridgeID(cist.BridgePriority, cist.BridgeAddress)))
	out.WriteString(fmt.Sprintf(" Bridge Priority     : %d\n", cist.BridgePriority))
	out.WriteString(fmt.Sprintf(" Root Bridge         : %s   (本地假设: 本桥桥 ID 最小, 非真实 BPDU 选举)\n", formatBridgeID(cist.RootPriority, cist.RootAddress)))
	out.WriteString(fmt.Sprintf(" Root Path Cost      : %d\n", cist.RootPathCost))
	if v := state.DeviceConfig[stpKey("bridge-address")]; v != "" {
		out.WriteString(fmt.Sprintf(" Bridge Address      : %s\n", v))
	}
	if state.DeviceConfig[stpKey("v-stp")] == "enable" {
		out.WriteString(" V-STP               : Enabled\n")
	}
	out.WriteString("-------[Port Role/State]-------\n")
	for _, p := range cist.Ports {
		line := fmt.Sprintf("%-20s: %-7s %-11s", p.Interface, p.Role, p.State)
		if p.Note != "" {
			line += "   " + p.Note
		}
		out.WriteString(line + "\n")
	}
	out.WriteString("\n" + stpSimNote() + "\n")
	return out.String()
}

// renderSTPBrief 渲染 display stp brief（MSTID / Port / Role / State 摘要表，按实例分组）。
func renderSTPBrief(state *CLIState) string {
	var out strings.Builder
	out.WriteString(fmt.Sprintf("%-6s %-16s %-9s %s\n", "MSTID", "Port", "Role", "State"))
	for _, id := range collectSTPInstances(state) {
		inst := EvaluateSTP(state, id)
		for _, p := range inst.Ports {
			out.WriteString(fmt.Sprintf("%-6d %-16s %-9s %s\n", id, p.Interface, p.Role, p.State))
		}
	}
	out.WriteString("\n" + stpSimNote() + "\n")
	return out.String()
}

// renderSTPInterface 渲染 display stp interface <if>（单端口详情 + 诚实注记）。
func renderSTPInterface(state *CLIState, iface string) string {
	cist := EvaluateSTP(state, 0)
	var port *STPPortResult
	for i := range cist.Ports {
		if cist.Ports[i].Interface == iface {
			port = &cist.Ports[i]
			break
		}
	}
	bpdu := "Disabled"
	if state.DeviceConfig[stpKey("bpdu-protection")] == "enable" {
		bpdu = "Enabled"
	}
	rootPortID := "0.0"
	if port != nil && port.Role == "ROOT" {
		rootPortID = fmt.Sprintf("%d.2", port.PortPriority)
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Interface: %s\n", iface))
	out.WriteString("CIST Global Information:\n")
	out.WriteString(fmt.Sprintf(" Mode              : %s\n", stpMode(state)))
	out.WriteString(fmt.Sprintf(" CIST Bridge       : %s\n", formatBridgeID(cist.BridgePriority, cist.BridgeAddress)))
	out.WriteString(fmt.Sprintf(" CIST Root/ERPC    : %s / %d   (本地假设, 非真实 BPDU)\n", formatBridgeID(cist.RootPriority, cist.RootAddress), cist.RootPathCost))
	out.WriteString(fmt.Sprintf(" CIST RegRoot/IRPC : %s / %d\n", formatBridgeID(cist.BridgePriority, cist.BridgeAddress), 0))
	out.WriteString(fmt.Sprintf(" CIST RootPortId   : %s\n", rootPortID))
	out.WriteString(fmt.Sprintf(" BPDU-Protection   : %s\n", bpdu))
	out.WriteString(" TC or TCN received: 0\n")
	role, st, note := "--", "DOWN", ""
	if port != nil {
		role, st, note = port.Role, port.State, port.Note
	}
	line := fmt.Sprintf(" Port Role/State   : %s / %s", role, st)
	if note != "" {
		line += "   " + note
	}
	out.WriteString(line + "\n")
	out.WriteString("\n" + stpSimNote() + "\n")
	return out.String()
}

// renderSTPRegion 渲染 display stp region-configuration（Region name/Revision/Instance VLAN Mapped/Active）。
func renderSTPRegion(state *CLIState) string {
	name := state.DeviceConfig[stpKey("region-name")]
	if name == "" {
		name = "default"
	}
	rev := 0
	if v := state.DeviceConfig[stpKey("revision-level")]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rev = n
		}
	}
	active := state.DeviceConfig[stpKey("region-active")] == "true"
	instVlans := map[int]string{}
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "stp:instance:") || !strings.HasSuffix(k, ":vlans") {
			continue
		}
		rest := strings.TrimPrefix(k, "stp:instance:") // "<id>:vlans"
		ci := strings.Index(rest, ":")
		if ci <= 0 {
			continue
		}
		id, err := strconv.Atoi(rest[:ci])
		if err != nil || id <= 0 {
			continue
		}
		instVlans[id] = v
	}
	hasRegion := name != "default" || rev != 0 || len(instVlans) > 0 || active
	if !hasRegion {
		return "MSTP Region: not configured"
	}
	var out strings.Builder
	out.WriteString("Oper configuration\n")
	out.WriteString(" Format selector    : 0\n")
	out.WriteString(fmt.Sprintf(" Region name        : %s\n", name))
	out.WriteString(fmt.Sprintf(" Revision level     : %d\n", rev))
	out.WriteString(" Instance  VLAN Mapped\n")
	out.WriteString(fmt.Sprintf(" %-8d %s\n", 0, "1 to 4094"))
	ids := make([]int, 0, len(instVlans))
	for id := range instVlans {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		out.WriteString(fmt.Sprintf(" %-8d %s\n", id, instVlans[id]))
	}
	status := "Inactive"
	if active {
		status = "Active"
	}
	out.WriteString(fmt.Sprintf(" Configuration Status: %s\n", status))
	return out.String()
}

// buildSavedSTPConfig 输出系统级 STP 配置块（display current-configuration / display this 复用）。
// 仅差异值补行，保证 save→reload 后完整复现（AC2）。返回 "" 表示无系统级 STP 配置。
func buildSavedSTPConfig(state *CLIState) string {
	if !isSTPEnabled(state) {
		return ""
	}
	var b strings.Builder
	mode := stpMode(state)
	if mode != stpModeDefault {
		b.WriteString(fmt.Sprintf(" stp mode %s\n", mode))
	}
	switch pri := stpBridgePriority(state, 0); pri {
	case 0:
		b.WriteString(" stp root primary\n")
	case stpPriStep:
		b.WriteString(" stp root secondary\n")
	case stpPriDefault:
		// 默认，不输出
	default:
		b.WriteString(fmt.Sprintf(" stp priority %d\n", pri))
	}
	std := stpPathCostStd(state)
	if std != stpPCStdDefault {
		b.WriteString(fmt.Sprintf(" stp pathcost-standard %s\n", std))
	}
	if state.DeviceConfig[stpKey("bpdu-protection")] == "enable" {
		b.WriteString(" stp bpdu-protection\n")
	}
	if state.DeviceConfig[stpKey("root-protection")] == "enable" {
		b.WriteString(" stp root-protection\n")
	}
	if state.DeviceConfig[stpKey("loop-protection")] == "enable" {
		b.WriteString(" stp loop-protection\n")
	}
	if state.DeviceConfig[stpKey("tc-protection")] == "enable" {
		b.WriteString(" stp tc-protection\n")
		if v := state.DeviceConfig[stpKey("tc-protection-interval")]; v != "" && v != strconv.Itoa(stpTCIntervalDefault) {
			b.WriteString(fmt.Sprintf(" stp tc-protection interval %s\n", v))
		}
	}
	if v := state.DeviceConfig[stpKey("bridge-address")]; v != "" {
		b.WriteString(fmt.Sprintf(" stp bridge-address %s\n", v))
	}
	// MSTP 实例级 root / priority（P1-8）
	rootByInst := map[int]string{}
	priByInst := map[int]int{}
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "stp:instance:") {
			continue
		}
		rest := strings.TrimPrefix(k, "stp:instance:") // "<id>:root" or "<id>:priority"
		ci := strings.Index(rest, ":")
		if ci <= 0 {
			continue
		}
		id, err := strconv.Atoi(rest[:ci])
		if err != nil || id <= 0 {
			continue
		}
		switch rest[ci+1:] {
		case "root":
			rootByInst[id] = v
		case "priority":
			if n, e2 := strconv.Atoi(v); e2 == nil {
				priByInst[id] = n
			}
		}
	}
	instIDs := make([]int, 0)
	for id := range rootByInst {
		instIDs = append(instIDs, id)
	}
	for id := range priByInst {
		if _, ok := rootByInst[id]; !ok {
			instIDs = append(instIDs, id)
		}
	}
	sort.Ints(instIDs)
	for _, id := range instIDs {
		if r, ok := rootByInst[id]; ok {
			b.WriteString(fmt.Sprintf(" stp instance %d root %s\n", id, r))
		} else if p, ok := priByInst[id]; ok && p != stpPriDefault {
			b.WriteString(fmt.Sprintf(" stp instance %d priority %d\n", id, p))
		}
	}
	// region 块（stp region-configuration 子命令按 VRP 缩进嵌套 2 空格）
	regionName := state.DeviceConfig[stpKey("region-name")]
	rev := state.DeviceConfig[stpKey("revision-level")]
	active := state.DeviceConfig[stpKey("region-active")] == "true"
	instVlans := map[int]string{}
	for k, v := range state.DeviceConfig {
		if !strings.HasPrefix(k, "stp:instance:") || !strings.HasSuffix(k, ":vlans") {
			continue
		}
		rest := strings.TrimPrefix(k, "stp:instance:")
		ci := strings.Index(rest, ":")
		if ci <= 0 {
			continue
		}
		id, err := strconv.Atoi(rest[:ci])
		if err != nil || id <= 0 {
			continue
		}
		instVlans[id] = v
	}
	if regionName != "" || rev != "" || len(instVlans) > 0 {
		b.WriteString(" stp region-configuration\n")
		if regionName != "" {
			b.WriteString(fmt.Sprintf("  region-name %s\n", regionName))
		}
		if rev != "" {
			b.WriteString(fmt.Sprintf("  revision-level %s\n", rev))
		}
		vlanIDs := make([]int, 0, len(instVlans))
		for id := range instVlans {
			vlanIDs = append(vlanIDs, id)
		}
		sort.Ints(vlanIDs)
		for _, id := range vlanIDs {
			b.WriteString(fmt.Sprintf("  instance %d vlan %s\n", id, instVlans[id]))
		}
		if active {
			b.WriteString("  active region-configuration\n")
		}
	}
	return b.String()
}

// buildSavedSTPInterfaceConfig 输出单接口下 STP 接口级配置行（display current-configuration 接口块内）。
// 返回 "" 表示该接口无 STP 接口级配置。
func buildSavedSTPInterfaceConfig(state *CLIState, iface string) string {
	var b strings.Builder
	if v := state.DeviceConfig[stpIfaceKey(iface, "edged-port")]; v == "enable" {
		b.WriteString(" stp edged-port enable\n")
	} else if v == "disable" {
		b.WriteString(" stp edged-port disable\n")
	}
	if v := state.DeviceConfig[stpIfaceKey(iface, "cost")]; v != "" {
		b.WriteString(fmt.Sprintf(" stp cost %s\n", v))
	}
	if v := state.DeviceConfig[stpIfaceKey(iface, "port-priority")]; v != "" && v != strconv.Itoa(stpPortPriDefault) {
		b.WriteString(fmt.Sprintf(" stp port priority %s\n", v))
	}
	return b.String()
}

// applyPortSecurity 实现端口安全命令（enable/disable/max-mac-num/mac-address sticky）。
// 顶层 case "port-security" 与既有 "port security" 子命令共用，避免逻辑分裂。
// args[0] 为子命令（enable/disable/max-mac-num/mac-address），调用方需已确保接口视图。
func applyPortSecurity(state *CLIState, args []string) string {
	if len(args) < 1 {
		return "Error: usage: port-security <enable|disable|max-mac-num|mac-address>"
	}
	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "enable":
		state.DeviceConfig[fmt.Sprintf("interface:%s:port-security", state.CurrentSub)] = "enable"
		return "Port security enabled"
	case "disable":
		state.DeviceConfig[fmt.Sprintf("interface:%s:port-security", state.CurrentSub)] = "disable"
		return "Port security disabled"
	case "max-mac-num":
		if len(args) >= 2 {
			if maxNum, err := parseNum(args[1]); err == nil && maxNum >= psMaxMACMin && maxNum <= psMaxMACMax {
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-max-mac", state.CurrentSub)] = fmt.Sprintf("%d", maxNum)
				return fmt.Sprintf("Port security max-mac-num set to %d", maxNum)
			}
		}
		return fmt.Sprintf("Error: max-mac-num must be between %d and %d", psMaxMACMin, psMaxMACMax)
	case "protect-action":
		// 新增：protect-action {protect|restrict|shutdown}（拍板 #5，默认 restrict）。
		if len(args) >= 2 {
			act := strings.ToLower(args[1])
			switch act {
			case "protect", "restrict", "shutdown":
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-protect-action", state.CurrentSub)] = act
				return fmt.Sprintf("Port security protect-action set to %s", act)
			}
		}
		return "Error: invalid protect-action, must be one of protect|restrict|shutdown"
	case "aging-time":
		// 新增：aging-time <time>（合法范围 1–1440 分钟，拍板 #4）。
		if len(args) >= 2 {
			if n, err := parseNum(args[1]); err == nil && n >= psAgingMin && n <= psAgingMax {
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-aging-time", state.CurrentSub)] = fmt.Sprintf("%d", n)
				return fmt.Sprintf("Port security aging-time set to %d minutes", n)
			}
		}
		return fmt.Sprintf("Error: aging-time must be between %d and %d minutes", psAgingMin, psAgingMax)
	case "mac-address":
		if len(args) >= 2 && strings.ToLower(args[1]) == "sticky" {
			// 手动绑定形态：mac-address sticky <mac> vlan <id>（写 port-security-sticky-mac:<mac>）。
			if len(args) >= 5 && strings.EqualFold(strings.ToLower(args[3]), "vlan") {
				mac := args[2]
				canon, ok := canonMAC(mac)
				if !ok {
					return fmt.Sprintf("Error: invalid MAC address %q", mac)
				}
				vlan, err := parseNum(args[4])
				if err != nil || vlan <= 0 {
					return "Error: invalid vlan id"
				}
				state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-sticky-mac:%s", state.CurrentSub, canon)] = fmt.Sprintf("%d", vlan)
				return fmt.Sprintf("Port security sticky MAC %s bound to vlan %d", canon, vlan)
			}
			// 无参 → 自动粘滞标志（既有行为，保留）。
			state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-sticky", state.CurrentSub)] = "enable"
			return "Port security sticky MAC enabled"
		}
		return "Error: usage: port-security mac-address sticky [<mac> vlan <id>]"
	}
	return "Error: invalid port-security subcommand"
}

// applyVRRP 处理接口视图下的 VRRP 命令族（P2 第三项，华为 VRP 课程 60/61）。
//
// 解析 VRP 子命令并写对应 DeviceConfig 键（单一事实源）：
//
//	vrrp vrid <1-255> virtual-ip <ip>            → 写 :virtual-ip；先调 vrrpSameSubnet 校验，失败回 Error
//	vrrp vrid <1-255> priority <1-254>           → 写 :priority
//	vrrp vrid <1-255> preempt-mode disable       → 写 :preempt="disable"（enable 可省略，默认开启）
//	vrrp vrid <1-255> timer advertise <1-255>    → 写 :advertise
//	[P1] vrrp vrid <1-255> track interface <if> [reduced <1-255>]
//	                                               → 写 :track-iface / :track-reduced（reduced 缺省 10）
//	[P1] vrrp vrid <1-255> authentication-mode {simple|md5} <key>
//	                                               → 写 :auth-mode / :auth-key（仅存不显明文）
//
// 范围/格式非法 → "Error: ..."；非接口视图 → "Error: must be in interface view"；
// 能力校验沿用 ExecuteCommandOn 的 isCommandSupported（"vrrp": l3Devices()）。
//
// 副作用（写 DeviceConfig 键）在此落地；纯函数选举/校验见 vrrp_eval.go。
func applyVRRP(state *CLIState, args []string) string {
	if state.CurrentView != ViewInterface {
		return "Error: must be in interface view"
	}
	iface := state.CurrentSub
	if len(args) < 1 {
		return "Error: usage: vrrp vrid <1-255> {virtual-ip <ip> | priority <1-254> | preempt-mode disable | timer advertise <1-255> | track interface <iface> [reduced <1-255>] | authentication-mode {simple|md5} <key>}"
	}
	if strings.ToLower(args[0]) != "vrid" {
		return "Error: usage: vrrp vrid <1-255> ..."
	}
	if len(args) < 2 {
		return "Error: vrid number required"
	}
	vrid, err := parseNum(args[1])
	if err != nil || vrid < vrrpVRIDMin || vrid > vrrpVRIDMax {
		return fmt.Sprintf("Error: vrid must be between %d and %d", vrrpVRIDMin, vrrpVRIDMax)
	}
	if len(args) < 3 {
		return "Error: incomplete vrrp command"
	}
	sub := strings.ToLower(args[2])
	switch sub {
	case "virtual-ip":
		if len(args) < 4 {
			return "Error: virtual-ip address required"
		}
		vip := args[3]
		if net.ParseIP(vip) == nil {
			return fmt.Sprintf("Error: invalid virtual-ip %q", vip)
		}
		// 先做虚拟 IP 与接口 IP 同网段校验（拍板 #4，P0）。
		ok, _, errMsg := vrrpSameSubnet(state, iface, vip)
		if !ok {
			return errMsg
		}
		state.DeviceConfig[vrrpKey(iface, vrid, "virtual-ip")] = vip
		return fmt.Sprintf("VRRP vrid %d virtual-ip %s configured", vrid, vip)
	case "priority":
		if len(args) < 4 {
			return "Error: priority value required"
		}
		pri, err := parseNum(args[3])
		if err != nil || pri < vrrpPriMin || pri > vrrpPriMax {
			return fmt.Sprintf("Error: priority must be between %d and %d", vrrpPriMin, vrrpPriMax)
		}
		state.DeviceConfig[vrrpKey(iface, vrid, "priority")] = strconv.Itoa(pri)
		return fmt.Sprintf("VRRP vrid %d priority %d configured", vrid, pri)
	case "preempt-mode":
		if len(args) < 4 {
			return "Error: usage: vrrp vrid <id> preempt-mode disable"
		}
		mode := strings.ToLower(args[3])
		if mode != "disable" {
			return "Error: usage: vrrp vrid <id> preempt-mode disable"
		}
		state.DeviceConfig[vrrpKey(iface, vrid, "preempt")] = "disable"
		return fmt.Sprintf("VRRP vrid %d preempt-mode disable configured", vrid)
	case "timer":
		if len(args) < 5 || strings.ToLower(args[3]) != "advertise" {
			return "Error: usage: vrrp vrid <id> timer advertise <1-255>"
		}
		adv, err := parseNum(args[4])
		if err != nil || adv < vrrpAdvMin || adv > vrrpAdvMax {
			return fmt.Sprintf("Error: advertise interval must be between %d and %d", vrrpAdvMin, vrrpAdvMax)
		}
		state.DeviceConfig[vrrpKey(iface, vrid, "advertise")] = strconv.Itoa(adv)
		return fmt.Sprintf("VRRP vrid %d timer advertise %d configured", vrid, adv)
	case "track":
		// track interface <iface> [reduced <1-255>]
		if len(args) < 4 || strings.ToLower(args[3]) != "interface" {
			return "Error: usage: vrrp vrid <id> track interface <iface> [reduced <1-255>]"
		}
		if len(args) < 5 {
			return "Error: track interface name required"
		}
		trackIface := args[4]
		reduced := vrrpTrackReducedDefault
		if len(args) >= 6 && strings.ToLower(args[5]) == "reduced" {
			if len(args) < 7 {
				return "Error: reduced value required"
			}
			r, err := parseNum(args[6])
			if err != nil || r < vrrpTrackReducedMin || r > vrrpTrackReducedMax {
				return fmt.Sprintf("Error: reduced must be between %d and %d", vrrpTrackReducedMin, vrrpTrackReducedMax)
			}
			reduced = r
		}
		state.DeviceConfig[vrrpKey(iface, vrid, "track-iface")] = trackIface
		state.DeviceConfig[vrrpKey(iface, vrid, "track-reduced")] = strconv.Itoa(reduced)
		return fmt.Sprintf("VRRP vrid %d track interface %s reduced %d configured", vrid, trackIface, reduced)
	case "authentication-mode":
		if len(args) < 5 {
			return "Error: usage: vrrp vrid <id> authentication-mode {simple|md5} <key>"
		}
		mode := strings.ToLower(args[3])
		if mode != "simple" && mode != "md5" {
			return "Error: authentication-mode must be simple or md5"
		}
		key := args[4]
		state.DeviceConfig[vrrpKey(iface, vrid, "auth-mode")] = mode
		state.DeviceConfig[vrrpKey(iface, vrid, "auth-key")] = key
		return fmt.Sprintf("VRRP vrid %d authentication-mode %s configured", vrid, mode)
	default:
		return fmt.Sprintf("Error: unknown vrrp subcommand %q", sub)
	}
}

// applyUndoVRRP 处理接口视图下的 undo vrrp 命令（P1）。
//
//	undo vrrp vrid <1-255> [virtual-ip <ip>]
//
// 删除 interface:<iface>:vrrp:<vrid>:* 全部字段键（virtual-ip 为组存在标记，
// 删除后即该组消失）。副作用（删 DeviceConfig 键）在此落地。
func applyUndoVRRP(state *CLIState, args []string) string {
	if state.CurrentView != ViewInterface {
		return "Error: must be in interface view"
	}
	iface := state.CurrentSub
	if len(args) < 3 || strings.ToLower(args[0]) != "vrrp" || strings.ToLower(args[1]) != "vrid" {
		return "Error: usage: undo vrrp vrid <1-255> [virtual-ip <ip>]"
	}
	vrid, err := parseNum(args[2])
	if err != nil || vrid < vrrpVRIDMin || vrid > vrrpVRIDMax {
		return fmt.Sprintf("Error: vrid must be between %d and %d", vrrpVRIDMin, vrrpVRIDMax)
	}
	prefix := fmt.Sprintf("interface:%s:vrrp:%d:", iface, vrid)
	deleted := false
	for k := range state.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			delete(state.DeviceConfig, k)
			deleted = true
		}
	}
	if deleted {
		return fmt.Sprintf("VRRP vrid %d on %s deleted", vrid, iface)
	}
	return fmt.Sprintf("Error: VRRP vrid %d not configured on %s", vrid, iface)
}

// handleSimulateFrame 处理 simulate frame <src-mac> [vlan <id>]（T03，唯一触发点）。
//
// 解析参数 → 调用纯函数 EvaluatePortSecurity（只读 DeviceConfig + MACTable）→ 按返回结果
// 应用副作用（写 MACTable / error-down / violation 计数 / 粘滞持久化键）。与 NAT 的
// applyNAT / EvaluatePathACL 同构：纯函数不写 state，副作用在此落地。
func handleSimulateFrame(state *CLIState, args []string) string {
	if len(args) < 2 || strings.ToLower(args[0]) != "frame" {
		return "Error: usage: simulate frame <src-mac> [vlan <id>]"
	}
	srcRaw := args[1]
	canon, ok := canonMAC(srcRaw)
	if !ok {
		return fmt.Sprintf("Error: invalid MAC address %q", srcRaw)
	}
	vlan := 0
	if len(args) >= 4 && strings.EqualFold(strings.ToLower(args[2]), "vlan") {
		v, err := parseNum(args[3])
		if err != nil || v < 0 {
			return "Error: invalid vlan id"
		}
		vlan = v
	}
	iface := state.CurrentSub
	res := EvaluatePortSecurity(state, iface, Frame{SrcMAC: canon, VLAN: vlan})

	switch {
	case res.Admit && res.Learned != nil:
		// 准入且应学习：写入 MACTable；若粘滞则同时写持久化键。
		state.MACTable = append(state.MACTable, res.Learned)
		if res.Learned.Type == "sticky" {
			key := fmt.Sprintf("interface:%s:port-security-sticky-learned:%s", iface, res.Learned.MAC)
			state.DeviceConfig[key] = fmt.Sprintf("%d", vlan)
		}
		return fmt.Sprintf("Frame from %s on %s: ADMITTED (%s MAC learned) %s", canon, iface, res.Learned.Type, portSecSimNote())
	case res.Admit:
		// 授权 MAC：准入，不学习不计数。
		return fmt.Sprintf("Frame from %s on %s: ADMITTED %s", canon, iface, portSecSimNote())
	case res.Violation != nil && res.Violation.Action == "shutdown":
		// shutdown：端口 error-down 置位 + violation 计数 +1。
		state.DeviceConfig[fmt.Sprintf("interface:%s:port-security-error-down", iface)] = "true"
		state.incPortSecurityViolations(iface)
		return fmt.Sprintf("Frame from %s on %s: PORT ERROR-DOWN (protect-action=shutdown) %s", canon, iface, portSecSimNote())
	case res.Violation != nil && res.Violation.Action == "restrict":
		// restrict：丢弃 + violation 计数 +1。
		state.incPortSecurityViolations(iface)
		return fmt.Sprintf("Frame from %s on %s: DROPPED (protect-action=restrict, violation logged) %s", canon, iface, portSecSimNote())
	default:
		// protect：丢弃且不记录（无告警标志、不计数）。
		return fmt.Sprintf("Frame from %s on %s: DROPPED (protect-action=protect) %s", canon, iface, portSecSimNote())
	}
}

// buildPortSecurityDisplay 渲染 display port-security 的输出（T04）。
//
// filter==""：表格式列出所有接口（Interface/Status/Max MAC/Protect-Action/Sticky/Aging/Violations）。
// filter!=""：单端口详情，附「已学安全/粘滞 MAC 列表」与 error-down 状态。
// 只读 DeviceConfig 中 port-security 键与 MACTable，不写 state。
func buildPortSecurityDisplay(state *CLIState, ifaceFilter string) string {
	if state == nil {
		return ""
	}
	ifaceNames := make([]string, 0, len(state.Interfaces))
	for k := range state.Interfaces {
		ifaceNames = append(ifaceNames, k)
	}
	sort.Strings(ifaceNames)

	var b strings.Builder
	if ifaceFilter == "" {
		b.WriteString("Port Security Configuration\n")
		b.WriteString(fmt.Sprintf("%-18s %-8s %-8s %-15s %-7s %-11s %s\n",
			"Interface", "Status", "Max MAC", "Protect-Action", "Sticky", "Aging(min)", "Violations"))
		for _, iface := range ifaceNames {
			b.WriteString(psRow(state, iface))
		}
		return strings.TrimRight(b.String(), "\n")
	}

	// 单端口详情。
	if _, ok := state.Interfaces[ifaceFilter]; !ok {
		return fmt.Sprintf("Error: interface %s does not exist", ifaceFilter)
	}
	b.WriteString(fmt.Sprintf("Port Security Configuration: %s\n", ifaceFilter))
	b.WriteString(fmt.Sprintf("  Status                  : %s\n", psStatusStr(state, ifaceFilter)))
	b.WriteString(fmt.Sprintf("  Max MAC                 : %s\n", psMaxMACDisplay(state, ifaceFilter)))
	b.WriteString(fmt.Sprintf("  Protect-Action          : %s%s\n", psProtectAction(state, ifaceFilter), psProtectDefaultMark(state, ifaceFilter)))
	b.WriteString(fmt.Sprintf("  Sticky                  : %s\n", boolToYesNo(psIsSticky(state, ifaceFilter))))
	b.WriteString(fmt.Sprintf("  Aging(min)              : %s\n", psAgingDisplay(state, ifaceFilter)))
	b.WriteString(fmt.Sprintf("  Violations              : %s\n", psViolationsDisplay(state, ifaceFilter)))
	b.WriteString(fmt.Sprintf("  Error-Down              : %s\n", psErrorDownDisplay(state, ifaceFilter)))
	b.WriteString("  Learned Secure MACs:\n")
	found := false
	for _, e := range state.MACTable {
		if e == nil || e.Interface != ifaceFilter {
			continue
		}
		if e.Type == "sticky" || e.Type == "security" {
			found = true
			b.WriteString(fmt.Sprintf("    %s   VLAN %d   %s\n", e.MAC, e.VLAN, e.Type))
		}
	}
	if !found {
		b.WriteString("    (none)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// psRow 返回 display port-security 全接口表的单行（已排序 by 接口名）。
func psRow(state *CLIState, iface string) string {
	if !psIsEnabled(state, iface) {
		return fmt.Sprintf("%-18s %-8s %-8s %-15s %-7s %-11s %s\n",
			iface, "disable", "-", "-", "no", "-", "-")
	}
	status := "enable"
	maxStr := fmt.Sprintf("%d", psMaxMAC(state, iface))
	protect := psProtectAction(state, iface)
	sticky := boolToYesNo(psIsSticky(state, iface))
	aging := psAgingDisplay(state, iface)
	viol := psViolationsDisplay(state, iface)
	return fmt.Sprintf("%-18s %-8s %-8s %-15s %-7s %-11s %s\n",
		iface, status, maxStr, protect, sticky, aging, viol)
}

// boolToYesNo 把布尔转成 "yes"/"no"。
func boolToYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// psStatusStr 返回端口安全启用状态文案（enable/disable）。
func psStatusStr(state *CLIState, iface string) string {
	if psIsEnabled(state, iface) {
		return "enable"
	}
	return "disable"
}

// psMaxMACDisplay 返回 max-mac 展示值；未启用端口显示 "-"。
func psMaxMACDisplay(state *CLIState, iface string) string {
	if !psIsEnabled(state, iface) {
		return "-"
	}
	return fmt.Sprintf("%d", psMaxMAC(state, iface))
}

// psProtectDefaultMark 在 protect-action 为缺省（键未配置）时返回 " (default)" 标注。
func psProtectDefaultMark(state *CLIState, iface string) string {
	if _, ok := state.DeviceConfig[psKey(iface, psKeyProtect)]; !ok {
		return " (default)"
	}
	return ""
}

// psAgingDisplay 返回 aging-time 展示值；未配置显示 "-"。
func psAgingDisplay(state *CLIState, iface string) string {
	if v, ok := state.DeviceConfig[psKey(iface, psKeyAging)]; ok && v != "" {
		return v
	}
	return "-"
}

// psViolationsDisplay 返回违规计数展示值；缺省 "0"。
func psViolationsDisplay(state *CLIState, iface string) string {
	if v, ok := state.DeviceConfig[psKey(iface, psKeyViolations)]; ok && v != "" {
		return v
	}
	return "0"
}

// psErrorDownDisplay 返回 error-down 展示值（yes/no）。
func psErrorDownDisplay(state *CLIState, iface string) string {
	if state.DeviceConfig[psKey(iface, psKeyErrorDown)] == "true" {
		return "yes"
	}
	return "no"
}

// executeServerService 统一处理服务器应用层服务启用（http/https/dns/ftp）。
// 对齐 smtp 回显风格：系统视图下 "enable" 写 DeviceConfig["<proto>:enabled"]="true"
// 并返回 "<PROTO> service enabled"（如 "HTTP service enabled"）。
func executeServerService(state *CLIState, proto string, args []string) string {
	if state.CurrentView != ViewSystem {
		return "Error: must be in system view"
	}
	if len(args) >= 1 && strings.ToLower(args[0]) == "enable" {
		state.DeviceConfig[proto+":enabled"] = "true"
		return fmt.Sprintf("%s service enabled", strings.ToUpper(proto))
	}
	return fmt.Sprintf("%s configuration", strings.ToUpper(proto))
}

// buildIsisDisplay 渲染 display isis 的输出（参照 display ospf 风格）。
func buildIsisDisplay(state *CLIState) string {
	var b strings.Builder
	if state.ISIS == nil || !state.ISIS.Enabled {
		b.WriteString("ISIS: Not configured\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("ISIS Process %d\n", state.ISIS.ProcessID))
	b.WriteString(fmt.Sprintf("  Network Type: %s\n", state.ISIS.NetworkType))
	b.WriteString("  State: Running\n")
	b.WriteString("  Neighbors: 0\n")
	imports := "none"
	if len(state.ISIS.ImportRoutes) > 0 {
		imports = strings.Join(state.ISIS.ImportRoutes, ", ")
	}
	b.WriteString(fmt.Sprintf("  Import Routes: %s\n", imports))
	return b.String()
}

// buildBGPPeerDisplay 渲染 display bgp peer 的逐邻居明细表。
func buildBGPPeerDisplay(state *CLIState) string {
	var b strings.Builder
	if !state.BGP.Enabled {
		b.WriteString("BGP: Not configured\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("BGP Local Router ID : %s\n", state.BGP.RouterID))
	b.WriteString(fmt.Sprintf("Local AS number : %d\n", state.BGP.ASNumber))
	b.WriteString(fmt.Sprintf("Total number of peers : %d\n", len(state.BGP.Neighbors)))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%-15s %-12s %-12s %s\n", "Peer", "RemoteAS", "State", "Type"))
	for _, n := range state.BGP.Neighbors {
		peerState := "Established"
		peerType := "IBGP"
		if n.EBGP {
			peerType = "EBGP"
		}
		b.WriteString(fmt.Sprintf("%-15s %-12d %-12s %s\n", n.IPAddress, n.RemoteAS, peerState, peerType))
	}
	return b.String()
}

// buildDiagnosticInfo 聚合设备体检报告：version/device/cpu/memory + 关键协议状态小结。
func buildDiagnosticInfo(state *CLIState) string {
	var b strings.Builder
	b.WriteString("===== Device Diagnostic Information =====\n")
	deviceModel := "Unknown"
	switch state.DeviceType {
	case topology.DeviceRouter:
		deviceModel = "Huawei Router NE40E"
	case topology.DeviceL3Switch:
		deviceModel = "Huawei Switch S12700"
	case topology.DeviceSwitch:
		deviceModel = "Huawei Switch S5700"
	case topology.DeviceFirewall:
		deviceModel = "Huawei Firewall USG6000"
	case topology.DeviceVTEP:
		deviceModel = "Huawei VTEP Switch"
	case topology.DeviceServer:
		deviceModel = "Huawei Server RH2288H"
	case topology.DevicePC:
		deviceModel = "PC"
	case topology.DeviceClient:
		deviceModel = "Client PC"
	default:
		deviceModel = string(state.DeviceType)
	}
	b.WriteString(deviceModel + "\n")
	b.WriteString("VRP (R) software, Version 5.170 (V300R005C00)\n")
	b.WriteString("CPU Usage:    5%\n")
	b.WriteString("Memory utilization percentage: 15%\n")
	b.WriteString("----- Protocol Status -----\n")
	ospf := "Not configured"
	if state.OSPF.Enabled {
		ospf = "Running"
	}
	bgp := "Not configured"
	if state.BGP.Enabled {
		bgp = "Running"
	}
	isis := "Not configured"
	if state.ISIS != nil && state.ISIS.Enabled {
		isis = "Running"
	}
	stp := "Disabled"
	if isSTPEnabled(state) {
		stp = "Enabled"
	}
	dhcp := "Disabled"
	if state.DHCP != nil && state.DHCP.Enabled {
		dhcp = "Enabled"
	}
	b.WriteString(fmt.Sprintf("  OSPF: %s\n", ospf))
	b.WriteString(fmt.Sprintf("  BGP : %s\n", bgp))
	b.WriteString(fmt.Sprintf("  ISIS: %s\n", isis))
	b.WriteString(fmt.Sprintf("  STP : %s\n", stp))
	b.WriteString(fmt.Sprintf("  DHCP: %s\n", dhcp))
	b.WriteString("========================================\n")
	return b.String()
}

// formatProtocolBlocks 在 display current-configuration 的 VRP 快照后追加协议启用摘要块，
// 保证 OSPF/BGP/ISIS/STP/DHCP/IPv6/SMTP 等启用状态不随快照改造而"回退"丢失（P1，决策 #6 + 风险3）。
func formatProtocolBlocks(state *CLIState) string {
	var b strings.Builder
	b.WriteString("#\n")
	b.WriteString("protocol-status\n")
	if state.OSPF.Enabled {
		b.WriteString(fmt.Sprintf(" ospf %d\n", state.OSPF.ProcessID))
	}
	if state.ISIS != nil && state.ISIS.Enabled {
		b.WriteString(fmt.Sprintf(" isis %d\n", state.ISIS.ProcessID))
		b.WriteString(fmt.Sprintf("  network %s\n", state.ISIS.NetworkType))
	}
	if state.BGP.Enabled {
		b.WriteString(fmt.Sprintf(" bgp %d\n", state.BGP.ASNumber))
	}
	if isSTPEnabled(state) {
		b.WriteString(fmt.Sprintf(" stp mode %s\n", stpMode(state)))
	}
	if state.DHCP != nil && state.DHCP.Enabled {
		b.WriteString(" dhcp enable\n")
	}
	if v, ok := state.DeviceConfig["ipv6:enabled"]; ok && v == "true" {
		b.WriteString(" ipv6 enable\n")
	}
	if v, ok := state.DeviceConfig["smtp:enabled"]; ok && v == "true" {
		b.WriteString(" smtp enable\n")
	}
	// 路由策略（P0-2）：列出全部 route-policy 节点（诚实渲染配置态；lite 引擎不做实际选路过滤）。
	if rpBlock := buildRoutePolicySavedConfig(state); rpBlock != "" {
		b.WriteString(rpBlock)
	}
	b.WriteString("#\n")
	return b.String()
}

// applyUndoSystemFeature 处理系统视图下的 undo（反向清理协议/特性 state）。
// 支持 undo ospf [<id>] / undo vlan <id> / undo acl <num|name> / undo stp /
// undo dhcp / undo bgp [<as>] / undo ipv6；其它子命令返回 "not supported"。
func applyUndoSystemFeature(state *CLIState, args []string) string {
	if len(args) == 0 {
		return "Error: incomplete command"
	}
	feature := strings.ToLower(args[0])
	switch feature {
	case "aaa":
		// 🔴 P2 第八项 T5 / AC12：级联清理整个 aaa: 命名空间。
		// 实现内部使用**精确前缀** "aaa:"（含尾冒号），绝不误伤端口安全的
		// interface:...:port-security-sticky-learned:00e0-fc12-0aaa 等含 "aaa" 子串的异族键。
		msg, _ := applyUndoAAA(state, args)
		return msg
	case "local-user":
		// C1 一致性：本地用户已迁至 AAA 视图，系统视图下的 undo 同样引导而非静默处理。
		return ErrAAAViewFirst
	case "ospf":
		state.OSPF.Enabled = false
		state.OSPF.ProcessID = 0
		state.OSPF.AreaID = 0
		// 清理历史可能写盘的 ospf:* 键
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "ospf:") {
				delete(state.DeviceConfig, k)
			}
		}
		if len(args) >= 2 {
			return fmt.Sprintf("OSPF process %s removed", args[1])
		}
		return "OSPF process removed"
	case "vlan":
		if len(args) < 2 {
			return "Error: usage: undo vlan <id>"
		}
		vid, err := parseNum(args[1])
		if err != nil {
			return "Error: invalid VLAN id"
		}
		delete(state.VLANs, vid)
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "vlan:") {
				delete(state.DeviceConfig, k)
			}
		}
		return fmt.Sprintf("VLAN %d removed", vid)
	case "acl":
		if len(args) < 2 {
			return "Error: usage: undo acl <num|name>"
		}
		aclID := args[1]
		delete(state.ACLs, aclID)
		return fmt.Sprintf("ACL %s removed", aclID)
	case "stp":
		return applyUndoSTP(state, args)
	case "interface":
		// undo interface Tunnel<x>（P2 GRE，改动点 #8 / AC10③）：
		// 在调用 applyUndoInterfaceTrunk 之前拦截 Tunnel 口，lag_cmd.go 零改动 → Eth-Trunk 结构性零回归。
		if msg, handled := applyUndoInterfaceTunnel(state, args); handled {
			return msg
		}
		// undo interface Eth-Trunk <id>（P2 #5，T04 改动点 12 / AC11）：
		// 存在成员时必须拒绝，无成员才允许删除并清理 interface:Eth-Trunk<id>:* 全部键。
		return applyUndoInterfaceTrunk(state, args)
	case "lacp":
		// undo lacp priority|preempt|timeout（系统视图，P2 #5 T04 改动点 13）。
		return applyUndoLACPFeature(state, args[1:])
	case "dhcp":
		// P2 #6 / 设计 A8：undo dhcp select 已迁至接口视图，系统视图报错引导，
		// 避免用户在系统视图静默拿到 "DHCP disabled" 这种答非所问的回显。
		if len(args) >= 2 && strings.EqualFold(args[1], "select") {
			return errUndoDHCPSelectInterfaceView
		}
		if state.DHCP != nil {
			state.DHCP.Enabled = false
		}
		return "DHCP disabled"
	case "bgp":
		state.BGP.Enabled = false
		state.BGP.ASNumber = 0
		state.BGP.RouterID = ""
		state.BGP.Neighbors = make(map[string]*BGPNeighbor)
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "bgp:") {
				delete(state.DeviceConfig, k)
			}
		}
		if len(args) >= 2 {
			return fmt.Sprintf("BGP process %s removed", args[1])
		}
		return "BGP process removed"
	case "ipv6":
		// P2 第九项（T04）：系统视图 undo ipv6 族路由到精确 undo 函数。
		//   - undo ipv6                          → applyUndoIPv6System（清 ipv6: 精确前缀）
		//   - undo ipv6 route-static [<prefix>]  → applyUndoIPv6RouteStatic（A8/C2 级联）
		// 其余子命令交回既有逻辑（当前 ipv6 仅上述两类）。
		if len(args) >= 2 && strings.EqualFold(strings.TrimSpace(args[1]), "route-static") {
			if msg, handled := applyUndoIPv6RouteStatic(state, args); handled {
				return msg
			}
		}
		if msg, handled := applyUndoIPv6System(state, args); handled {
			return msg
		}
		return "IPv6 disabled"
	case "ripng":
		// undo ripng [<pid>]（P0-13）：清理 ipv6:ripng: 精确前缀 / 精确键。
		if msg, handled := applyUndoRIPng(state, args); handled {
			return msg
		}
		return "RIPng disabled"
	case "ospfv3":
		// undo ospfv3 [<pid>]（P0-14）：清理 ipv6:ospfv3: 精确前缀 / 精确键。
		if msg, handled := applyUndoOSPFv3(state, args); handled {
			return msg
		}
		return "OSPFv3 disabled"
	case "isis":
		// 反向清理 IS-IS 配置（P1-F 遗留 L1）。严格对齐 undo ospf/undo bgp 写法：
		// 复位结构化字段并清理历史写盘的 isis:* 键。
		state.ISIS.Enabled = false
		state.ISIS.ProcessID = 0
		state.ISIS.NetworkType = "level-2"
		state.ISIS.ImportRoutes = nil
		for k := range state.DeviceConfig {
			if strings.HasPrefix(k, "isis:") {
				delete(state.DeviceConfig, k)
			}
		}
		if len(args) >= 2 {
			return fmt.Sprintf("ISIS process %s removed", args[1])
		}
		return "ISIS process removed"
	case "route-policy":
		// P0-2 路由策略补齐：反向清理某 route-policy 的全部节点键（精确前缀）。
		msg, _ := undoRoutePolicy(state, args)
		return msg
	default:
		return fmt.Sprintf("Error: undo '%s' is not supported", feature)
	}
}

func GetPrompt(state *CLIState, deviceName string) string {
	if n, ok := state.DeviceConfig["sysname"]; ok {
		deviceName = n
	}
	if deviceName == "" {
		deviceName = "Router"
	}
	switch state.CurrentView {
	case ViewUser:
		return fmt.Sprintf("[%s]", deviceName)
	case ViewSystem:
		return fmt.Sprintf("[%s]", deviceName)
	case ViewInterface:
		return fmt.Sprintf("[%s-%s]", deviceName, state.CurrentSub)
	case ViewACL:
		return fmt.Sprintf("[%s-acl-%s]", deviceName, state.CurrentSub)
	case ViewMLAG:
		return fmt.Sprintf("[%s-m-lag-domain]", deviceName)
	case ViewBGP:
		return fmt.Sprintf("[%s-%s]", deviceName, state.CurrentSub)
	case ViewISIS:
		return fmt.Sprintf("[%s-%s]", deviceName, state.CurrentSub)
	case ViewVTY:
		return fmt.Sprintf("[%s-%s]", deviceName, state.CurrentSub)
	case ViewDHCPPool:
		return fmt.Sprintf("[%s-dhcp-pool-%s]", deviceName, state.CurrentSub)
	case ViewAAA:
		// [<dev>-aaa]
		return fmt.Sprintf("[%s-aaa]", deviceName)
	case ViewAAAAuthen:
		// CurrentSub 形如 "authen-sch1" / "author-x" / "acct-y"，
		// 故提示符恰为 [<dev>-aaa-authen-sch1]（PRD §4.1）。
		return fmt.Sprintf("[%s-aaa-%s]", deviceName, state.CurrentSub)
	case ViewAAADomain:
		return fmt.Sprintf("[%s-aaa-domain-%s]", deviceName, state.CurrentSub)
	case ViewRoutePolicy:
		return fmt.Sprintf("[%s-route-policy-%s]", deviceName, state.CurrentSub)
	}
	return fmt.Sprintf("[%s]", deviceName)
}

// SerializeToDeviceConfigData 将 CLIState 序列化为 DeviceConfigData
func (state *CLIState) SerializeToDeviceConfigData() *topology.DeviceConfigData {
	cfg := &topology.DeviceConfigData{
		DeviceName:     state.DeviceName,
		DefaultGateway: state.DefaultGateway,
		Interfaces:     make(map[string]string),
		Saved:          state.Saved,
		SavedConfig:    state.SavedConfig,
		SaveTime:       state.SaveTime,
		History:        state.History,
	}
	// 复制全部 DeviceConfig 键（含 interface:/isis:/ospf:/bgp: 等协议键），
	// 使 IS-IS/OSPF/BGP 等配置可随拓扑 save/reload 落盘（P1-F，风险2）。
	// 注：此前仅拷贝 interface: 前缀键，导致协议键在重载后丢失。
	for k, v := range state.DeviceConfig {
		cfg.Interfaces[k] = v
	}
	// 存储主机网络配置
	if state.HostIP != "" {
		// 注意：state.HostSubnet 是点分十进制掩码（如 255.255.255.0），
		// 必须用 ipToCIDR（内部走 subnetToPrefix）转换为 CIDR，切勿用
		// subnetFromCIDR（它期望前缀长度字符串，会把 "255.255.255.0" 贪婪
		// 解析成首段 255 → /32）。
		cfg.Interfaces["interface:Ethernet0:ip"] = ipToCIDR(state.HostIP, state.HostSubnet)
	}
	if state.HostDNS != "" {
		cfg.Interfaces["interface:Ethernet0:dns"] = state.HostDNS
	}
	return cfg
}

// LoadFromDeviceConfigData 从 DeviceConfigData 恢复 CLIState 配置
func (state *CLIState) LoadFromDeviceConfigData(cfg *topology.DeviceConfigData) {
	if cfg == nil {
		return
	}
	state.DeviceName = cfg.DeviceName
	state.DefaultGateway = cfg.DefaultGateway
	state.Saved = cfg.Saved
	state.SavedConfig = cfg.SavedConfig
	state.SaveTime = cfg.SaveTime
	if cfg.History != nil {
		state.History = cfg.History
	} else {
		state.History = []*topology.HistoryEntry{}
	}
	if cfg.Interfaces != nil {
		for k, v := range cfg.Interfaces {
			state.DeviceConfig[k] = v
			// 恢复主机网络配置
			if k == "interface:Ethernet0:ip" {
				ip, subnet := parseIPFormat(v)
				state.HostIP = ip
				state.HostSubnet = subnet
			} else if k == "interface:Ethernet0:dns" {
				state.HostDNS = v
			}
		}
	}

	// 从 ConfigData 恢复 VXLAN 配置，使 demo 预置拓扑后 display vxlan 即显示已配通
	if vni, ok := cfg.Interfaces["vxlan:vni"]; ok {
		if state.VXLAN == nil {
			state.VXLAN = &VXLANConfig{Enabled: true}
		} else {
			state.VXLAN.Enabled = true
		}
		if n, err := strconv.Atoi(vni); err == nil {
			state.VXLAN.VNI = n
		}
		state.VXLAN.VTEPIP = cfg.Interfaces["vxlan:source"]
		state.VXLAN.PeerVTEPIP = cfg.Interfaces["vxlan:peer"]
	}

	// 从 ConfigData 恢复 IS-IS 配置（P1-F，T01）。cfg.Interfaces 已包含 isis:* 键，
	// 上面循环已写回 state.DeviceConfig；此处重建 state.ISIS 结构化字段，
	// 保证 display isis / ISIS 视图状态在 reload 后保持一致。
	if enabled, ok := cfg.Interfaces["isis:enabled"]; ok && enabled == "true" {
		if state.ISIS == nil {
			state.ISIS = &ISISConfig{NetworkType: "level-2"}
		}
		state.ISIS.Enabled = true
		if pid, ok := cfg.Interfaces["isis:process-id"]; ok {
			if n, err := strconv.Atoi(pid); err == nil {
				state.ISIS.ProcessID = n
			}
		}
		if nt, ok := cfg.Interfaces["isis:network-type"]; ok && nt != "" {
			state.ISIS.NetworkType = nt
		}
		if ir, ok := cfg.Interfaces["isis:import-route"]; ok && ir != "" {
			state.ISIS.ImportRoutes = strings.Split(ir, ",")
		}
	}

	// 从 ConfigData 恢复 OSPF 配置（L2 修复，既有缺口）。cfg.Interfaces 已包含 ospf:* 键，
	// 上面循环已写回 state.DeviceConfig；此处重建 state.OSPF 结构化字段，
	// 保证 display ospf / OSPF 视图状态在 reload 后保持一致（对齐 isis 持久化做法）。
	if enabled, ok := cfg.Interfaces["ospf:enabled"]; ok && enabled == "true" {
		state.OSPF.Enabled = true
		if pid, ok := cfg.Interfaces["ospf:process-id"]; ok {
			if n, err := strconv.Atoi(pid); err == nil {
				state.OSPF.ProcessID = n
			}
		}
		if aid, ok := cfg.Interfaces["ospf:area-id"]; ok {
			if n, err := strconv.Atoi(aid); err == nil {
				state.OSPF.AreaID = n
			}
		}
	}

	// 从 ConfigData 恢复 BGP 配置（L2 修复，既有缺口）。对齐 isis 持久化做法。
	if enabled, ok := cfg.Interfaces["bgp:enabled"]; ok && enabled == "true" {
		state.BGP.Enabled = true
		if as, ok := cfg.Interfaces["bgp:as-number"]; ok {
			if n, err := strconv.Atoi(as); err == nil {
				state.BGP.ASNumber = n
			}
		}
		if rid, ok := cfg.Interfaces["bgp:router-id"]; ok {
			state.BGP.RouterID = rid
		}
		if state.BGP.Neighbors == nil {
			state.BGP.Neighbors = make(map[string]*BGPNeighbor)
		}
		if peerIPs, ok := cfg.Interfaces["bgp:peer-ips"]; ok && peerIPs != "" {
			for _, ip := range strings.Split(peerIPs, ",") {
				ip = strings.TrimSpace(ip)
				if ip == "" {
					continue
				}
				nb := &BGPNeighbor{IPAddress: ip}
				if ras, ok := cfg.Interfaces["bgp:neighbor:"+ip+":remote-as"]; ok {
					if n, err := strconv.Atoi(ras); err == nil {
						nb.RemoteAS = n
					}
				}
				if ebgp, ok := cfg.Interfaces["bgp:neighbor:"+ip+":ebgp"]; ok {
					nb.EBGP = ebgp == "true"
				}
				state.BGP.Neighbors[ip] = nb
			}
		}
	}

	// 链路聚合逻辑口重建（P2 #5，T04 改动点 11）：
	// reload 后 state.Interfaces 不含 Eth-Trunk / Bridge-Aggregation 逻辑口，
	// 会导致 display interface / display ip interface brief 丢失聚合口（P0-3）。
	// 此处按 collectLAGTrunks 的存在判据（§1.3）重建条目，
	// **Status 由 syncLAGTrunkIfaceStatus → EvaluateLAG 实时派生，绝不硬编码 Up**（P0-11）。
	if trunks := collectLAGTrunks(state); len(trunks) > 0 {
		if state.Interfaces == nil {
			state.Interfaces = make(map[string]*InterfaceConfig)
		}
		for _, id := range trunks {
			name := lagDisplayTrunkName(state, id)
			if _, ok := state.Interfaces[name]; !ok {
				state.Interfaces[name] = &InterfaceConfig{Name: name}
			}
			syncLAGTrunkIfaceStatus(state, id)
		}
	}

	// 端口安全粘滞 MAC 回填（B-lite，拍板 #3）：
	// 上述循环已把全部 DeviceConfig 键（含 port-security-*）写回 state.DeviceConfig。
	// 此处：① 清除运行态键 error-down / violations（reload 后运行态归零，见设计 §7 #3）；
	// ② 扫描 port-security-sticky-learned:<mac> 键回填 MACTable（Type=sticky，
	// 幂等去重：按 MAC+Interface 去重，避免 reload 重复追加）。
	// 注：CLI 为单 goroutine 交互模型，MACTable 无并发写竞争（O4）。
	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") {
			continue
		}
		if strings.HasSuffix(k, ":port-security-error-down") || strings.HasSuffix(k, ":port-security-violations") {
			delete(state.DeviceConfig, k)
		}
	}
	const learnSep = ":port-security-sticky-learned:"
	for k, v := range state.DeviceConfig {
		idx := strings.Index(k, learnSep)
		if idx < 0 {
			continue
		}
		iface := strings.TrimPrefix(k[:idx], "interface:")
		mac := k[idx+len(learnSep):]
		if iface == "" || mac == "" {
			continue
		}
		// 幂等去重：已存在相同 MAC+Interface 条目则跳过。
		dup := false
		for _, e := range state.MACTable {
			if e != nil && e.Interface == iface && e.MAC == mac {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		vlan := 0
		if n, err := strconv.Atoi(v); err == nil {
			vlan = n
		}
		state.MACTable = append(state.MACTable, &MACEntry{
			MAC:       mac,
			VLAN:      vlan,
			Interface: iface,
			Type:      "sticky",
		})
	}
}

// NewCLIStateFromDeviceConfig 从 DeviceConfigData 创建 CLIState
func NewCLIStateFromDeviceConfig(dt topology.DeviceType, cfg *topology.DeviceConfigData, deviceName string) *CLIState {
	state := newCLIStateWithType(dt)
	state.DeviceName = deviceName
	if cfg != nil {
		state.LoadFromDeviceConfigData(cfg)
	}
	return state
}

// doSave 将当前运行配置写入"启动配置"（贴近华为 eNSP 的 save 行为）。
// 生成 VRP 风格配置快照并写入 Saved/SavedConfig/SaveTime，供 display saved-configuration 展示，
// 并通过 SerializeToDeviceConfigData 持久化到拓扑，重启后依然保留。
func (state *CLIState) doSave() {
	state.Saved = true
	state.SaveTime = time.Now().Format("2006-01-02 15:04:05")
	state.SavedConfig = state.buildSavedConfigSnapshot()
}

// buildSavedConfigSnapshot 生成当前运行配置的 VRP 风格文本快照。
func (state *CLIState) buildSavedConfigSnapshot() string {
	var b strings.Builder
	name := state.DeviceConfig["sysname"]
	if name == "" {
		name = state.DeviceName
	}
	if name == "" {
		name = string(state.DeviceType)
	}
	b.WriteString(fmt.Sprintf("sysname %s\n", name))
	b.WriteString("#\n")

	// 系统级 STP 配置块（方案 A：单事实源 DeviceConfig，随快照完整复现，修 P0-1 丢配置）。
	if s := buildSavedSTPConfig(state); s != "" {
		b.WriteString(s)
	}

	// 系统级 AAA 配置块（P2 第八项 P1-5）：单事实源 DeviceConfig 的 "aaa:" 命名空间，
	// 位于接口块循环**之前**，与 STP 块同属系统级块区。
	// 🔴 口令行输出 `password cipher ****`（P0-13），该快照不可回灌。
	if s := buildSavedAAAConfig(state); s != "" {
		b.WriteString(s)
	}

	ifaceNames := make([]string, 0, len(state.Interfaces))
	for k := range state.Interfaces {
		ifaceNames = append(ifaceNames, k)
	}
	sort.Strings(ifaceNames)
	for _, k := range ifaceNames {
		ifc := state.Interfaces[k]
		if ifc == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("interface %s\n", ifc.Name))
		if ifc.IP != "" && ifc.Mask != "" {
			b.WriteString(fmt.Sprintf(" ip address %s %s\n", ifc.IP, ifc.Mask))
		} else if ifc.IP != "" {
			b.WriteString(fmt.Sprintf(" ip address %s\n", ifc.IP))
		}
		if ifc.Description != "" {
			b.WriteString(fmt.Sprintf(" description %s\n", ifc.Description))
		}
		// 聚合口（Eth-Trunk / Bridge-Aggregation）的 Status 是由成员**实时派生**的运行状态
		// （P2 #5，P0-11），并非管理态 shutdown；照抄会把 operate down 误写成用户配置。
		// 故聚合口仅在 DeviceConfig 明确记录管理性 shutdown 时才输出 shutdown 行。
		if isTrunkFamilyInterface(ifc.Name) {
			if strings.EqualFold(strings.TrimSpace(state.DeviceConfig[fmt.Sprintf("interface:%s:status", ifc.Name)]), "Down") {
				b.WriteString(" shutdown\n")
			}
		} else if strings.EqualFold(ifc.Status, "down") {
			b.WriteString(" shutdown\n")
		}
		// 端口安全：粘滞学习 MAC 持久化（B-lite，拍板 #3）。对每个
		// port-security-sticky-learned:<mac> 键追加 VRP 风格行，提升
		// display saved-configuration 保真度。按 mac 排序保证快照稳定。
		learnPrefix := fmt.Sprintf("interface:%s:port-security-sticky-learned:", ifc.Name)
		learned := make([]string, 0)
		for k, v := range state.DeviceConfig {
			if strings.HasPrefix(k, learnPrefix) {
				mac := strings.TrimPrefix(k, learnPrefix)
				learned = append(learned, fmt.Sprintf(" port-security mac-address sticky %s vlan %s", mac, v))
			}
		}
		sort.Strings(learned)
		for _, line := range learned {
			b.WriteString(line + "\n")
		}
		// VRRP（P2 第三项）：按 VRP 合规格式输出 vrrp vrid X virtual-ip（差异值才补 priority/preempt/advertise）。
		// 仅在该接口已在本循环（state.Interfaces）出现时，随接口块一起输出，避免重复 interface 标题。
		if _, ok := state.Interfaces[ifc.Name]; ok {
			if vrrpLines := buildSavedVRRPConfig(state, ifc.Name); vrrpLines != "" {
				b.WriteString(vrrpLines)
			}
			if stpLines := buildSavedSTPInterfaceConfig(state, ifc.Name); stpLines != "" {
				b.WriteString(stpLines)
			}
			// 链路聚合（P2 #5，T04 改动点 10）：聚合口输出 mode / load-balance /
			// least|max active-linknumber，成员口输出 eth-trunk <id> / lacp priority。
			if lagLines := buildSavedLAGInterfaceConfig(state, ifc.Name); lagLines != "" {
				b.WriteString(lagLines)
			}
			// DHCP 中继（P2 #6，T4）：输出 dhcp select <mode> 与 dhcp relay 差异值行
			// （strategy 为生效缺省 replace 时不输出，遵循 VRP「只落差异值」口径）。
			if dhcpLines := buildSavedDHCPRelayInterfaceConfig(state, ifc.Name); dhcpLines != "" {
				b.WriteString(dhcpLines)
			}
			// GRE 隧道（P2 第七项，T4 改动点 #9）：输出 tunnel-protocol / source / destination /
			// gre key / keepalive / gre checksum 差异值行（缺省值不冗余）。
			if greLines := buildSavedGREInterfaceConfig(state, ifc.Name); greLines != "" {
				b.WriteString(greLines)
			}
			// IPv6（P2 第九项，T4）：输出 ipv6 enable / ipv6 address 差异值行（缺省不冗余）。
			if ipv6Lines := buildSavedIPv6InterfaceConfig(state, ifc.Name); ipv6Lines != "" {
				b.WriteString(ipv6Lines)
			}
			// 链路质量（v0.12）：输出 delay / loss 差异值行（未配置不输出）。
			if lqLines := buildSavedLinkQualityInterfaceConfig(state, ifc.Name); lqLines != "" {
				b.WriteString(lqLines)
			}
		}
		b.WriteString("#\n")
	}
	// 独立 VRRP 输出通道：遍历 DeviceConfig vrrp 键（vrrpInterfaces），对任意「拥有 VRRP 配置、
	// 但 state.Interfaces 未重建」的接口（典型为 save→reload 后）补齐 interface 块与 VRRP 配置行，
	// 保证 display current-configuration 在 reload 后仍完整复现 VRRP（修掉残桩丢配置缺陷，AC2）。
	for _, iname := range vrrpInterfaces(state) {
		if _, ok := state.Interfaces[iname]; ok {
			continue
		}
		if vrrpLines := buildSavedVRRPConfig(state, iname); vrrpLines != "" {
			b.WriteString(fmt.Sprintf("interface %s\n", iname))
			b.WriteString(vrrpLines)
			b.WriteString("#\n")
		}
	}
	// 独立链路聚合输出通道（P2 #5，T04 改动点 10）：复用上面的 VRRP 范式，
	// 对「拥有 LAG 配置但 state.Interfaces 未重建」的聚合口 / 成员口补齐 interface 块，
	// 保证 save→reload 后 display current-configuration 完整复现聚合配置（AC2 ③）。
	if lagLines := buildSavedLAGConfig(state); lagLines != "" {
		b.WriteString(lagLines)
	}
	// 独立 DHCP 中继输出通道（P2 #6，T4）：同上范式，对「拥有 dhcp-select / dhcp-relay 键
	// 但 state.Interfaces 未重建」的接口补齐 interface 块，保证 save→reload 后
	// display current-configuration 完整复现中继配置（AC7 字节级一致）。
	if dhcpLines := buildSavedDHCPRelayConfig(state); dhcpLines != "" {
		b.WriteString(dhcpLines)
	}
	// 独立 GRE 隧道输出通道（P2 第七项，T4 改动点 #9）：对「拥有 GRE 配置但
	// state.Interfaces 未重建」的 Tunnel 口补齐 interface 块，保证 save→reload 后
	// display current-configuration 完整复现 GRE 配置（AC2 ③）。
	if greLines := buildSavedGREConfig(state); greLines != "" {
		b.WriteString(greLines)
	}
	// 独立链路质量输出通道（v0.12）：对「拥有 delay/loss 键但 state.Interfaces
	// 未重建」的接口补齐 interface 块，保证 save→reload 后完整复现链路质量配置。
	if lqLines := buildSavedLinkQualityConfig(state); lqLines != "" {
		b.WriteString(lqLines)
	}

	for _, r := range state.Routes {
		b.WriteString(fmt.Sprintf("ip route-static %s %s %s\n", r.Destination, r.Mask, r.NextHop))
	}

	// IPv6 静态路由（P2 第九项，T4）：输出 ipv6 route-static <prefix> <nexthop>
	//（确定性升序，AC8 ③ 字节级一致）。
	if ipv6RouteLines := buildSavedIPv6RouteConfig(state); ipv6RouteLines != "" {
		b.WriteString(ipv6RouteLines)
	}

	// VLAN 段按 vlan-id 升序输出。原实现直接 range map，导致
	// display current-configuration 每次调用 VLAN 块顺序随机（既有缺陷，P2 #6 T6 回归中发现）。
	// 与上方 ifaceNames 的 sort.Strings 同口径修正，保证快照字节级确定性（AC7）。
	vlanIDs := make([]int, 0, len(state.VLANs))
	for id := range state.VLANs {
		vlanIDs = append(vlanIDs, id)
	}
	sort.Ints(vlanIDs)
	for _, id := range vlanIDs {
		v := state.VLANs[id]
		if v == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("vlan %d\n", id))
		if v.Name != "" {
			b.WriteString(fmt.Sprintf(" description %s\n", v.Name))
		}
		b.WriteString("#\n")
	}

	if state.VXLAN != nil && state.VXLAN.Enabled {
		b.WriteString(fmt.Sprintf("vxlan vni %d\n", state.VXLAN.VNI))
		if state.VXLAN.VTEPIP != "" {
			b.WriteString(fmt.Sprintf(" source %s\n", state.VXLAN.VTEPIP))
		}
		if state.VXLAN.PeerVTEPIP != "" {
			b.WriteString(fmt.Sprintf(" peer %s\n", state.VXLAN.PeerVTEPIP))
		}
		b.WriteString("#\n")
	}

	b.WriteString(fmt.Sprintf("!configuration saved at %s\n", state.SaveTime))
	return b.String()
}

// buildVRRPDisplay 渲染 `display vrrp [brief|interface <if>|vrid <id>]`（P2 第三项）。
//
//	arg1==""         → 遍历所有接口所有组，逐组详情（含 EvaluateVRRP 角色 + 诚实注记）
//	arg1=="brief"    → 摘要表：VRID / Interface / Virtual IP / Priority / Role
//	arg1=="interface"→ 单接口所有组详情（args[2] 为目标接口名）
//	arg1=="vrid"     → 跨接口匹配该 vrid 的组详情（args[2] 为 vrid）
//
// 只读 collectVRRPGroups + EvaluateVRRP，无副作用；末尾附 vrrpSimNote() 诚实注记。
// 角色恒为 Master（拍板 #2(a) 本地静态假设）或 Initialize（未配组），绝不臆造 Backup（O2）。
func buildVRRPDisplay(state *CLIState, arg1 string, args []string) string {
	if state == nil {
		return "VRRP: Not configured"
	}
	// 决定要展示的 (iface, vrid) 组集合。
	var refs []vrrpGroupRef
	switch arg1 {
	case "interface":
		if len(args) < 3 {
			return "Error: usage: display vrrp interface <interface>"
		}
		iface := args[2]
		for _, g := range collectVRRPGroups(state, iface) {
			refs = append(refs, vrrpGroupRef{iface, g.VRID})
		}
	case "vrid":
		if len(args) < 3 {
			return "Error: usage: display vrrp vrid <id>"
		}
		vrid, err := parseNum(args[2])
		if err != nil {
			return "Error: invalid vrid"
		}
		for _, iface := range vrrpInterfaces(state) {
			for _, g := range collectVRRPGroups(state, iface) {
				if g.VRID == vrid {
					refs = append(refs, vrrpGroupRef{iface, g.VRID})
					break
				}
			}
		}
	case "brief":
		for _, iface := range vrrpInterfaces(state) {
			for _, g := range collectVRRPGroups(state, iface) {
				refs = append(refs, vrrpGroupRef{iface, g.VRID})
			}
		}
	default: // "" → 全接口
		for _, iface := range vrrpInterfaces(state) {
			for _, g := range collectVRRPGroups(state, iface) {
				refs = append(refs, vrrpGroupRef{iface, g.VRID})
			}
		}
	}

	if len(refs) == 0 {
		return "VRRP: Not configured"
	}
	if arg1 == "brief" {
		return renderVRRPBrief(state, refs)
	}
	return renderVRRPDetail(state, refs)
}

// renderVRRPBrief 渲染 display vrrp brief 的摘要表。
func renderVRRPBrief(state *CLIState, refs []vrrpGroupRef) string {
	var out strings.Builder
	// 列宽：VRID(5) Interface(22) Virtual IP(16) Priority(8) Role(8)
	out.WriteString(fmt.Sprintf("%-5s %-22s %-16s %-8s %s\n", "VRID", "Interface", "Virtual IP", "Priority", "Role"))
	for _, ref := range refs {
		res := EvaluateVRRP(state, ref.iface, ref.vrid)
		out.WriteString(fmt.Sprintf("%-5d %-22s %-16s %-8d %s\n", ref.vrid, ref.iface, res.VirtualIP, res.Priority, res.Role))
	}
	out.WriteString("\n" + vrrpSimNote() + "\n")
	return out.String()
}

// renderVRRPDetail 渲染 display vrrp（非 brief）的逐组详情。
func renderVRRPDetail(state *CLIState, refs []vrrpGroupRef) string {
	var out strings.Builder
	for i, ref := range refs {
		if i > 0 {
			out.WriteString("\n")
		}
		res := EvaluateVRRP(state, ref.iface, ref.vrid)
		g, _ := collectVRRPGroup(state, ref.iface, ref.vrid)
		out.WriteString(fmt.Sprintf("%s | Virtual Router %d\n", ref.iface, ref.vrid))
		out.WriteString(fmt.Sprintf("  %-15s: %s            %s\n", "State", res.Role, "（本地假设选举，非跨设备真实通告）"))
		out.WriteString(fmt.Sprintf("  %-15s: %s\n", "Virtual IP", res.VirtualIP))
		out.WriteString(fmt.Sprintf("  %-15s: %d\n", "Priority", res.Priority))
		preemptStr := "Enabled"
		if !res.Preempt {
			preemptStr = "Disabled"
		}
		out.WriteString(fmt.Sprintf("  %-15s: %s\n", "Preempt", preemptStr))
		out.WriteString(fmt.Sprintf("  %-15s: %d s\n", "Advertise Timer", res.Advertise))
		if g.TrackInterface != "" {
			out.WriteString(fmt.Sprintf("  %-15s: %s (reduced %d, Effective Priority %d)\n", "Track Interface", g.TrackInterface, g.TrackReduced, res.EffectivePriority))
		}
		if g.AuthMode != "" {
			// 仅显示认证模式，不显示明文 key（诚实边界 O3）。
			out.WriteString(fmt.Sprintf("  %-15s: %s\n", "Authentication", g.AuthMode))
		}
	}
	out.WriteString("\n" + vrrpSimNote() + "\n")
	return out.String()
}

// buildSavedVRRPConfig 输出指定接口下所有 VRRP 组的 VRP 合规配置行（已缩进，无 interface 包装）。
// 若接口无 VRRP 配置返回 ""。差异值才补行（priority!=100 / preempt 关闭 / advertise!=1），
// 修复旧 `vrrp vrid %d ip %s` 的非合规格式（拍板 #6，保真度约定）。
func buildSavedVRRPConfig(state *CLIState, iface string) string {
	groups := collectVRRPGroups(state, iface)
	if len(groups) == 0 {
		return ""
	}
	var b strings.Builder
	for _, g := range groups {
		b.WriteString(fmt.Sprintf(" vrrp vrid %d virtual-ip %s\n", g.VRID, g.VirtualIP))
		if g.Priority != vrrpPriDefault {
			b.WriteString(fmt.Sprintf(" vrrp vrid %d priority %d\n", g.VRID, g.Priority))
		}
		if !g.Preempt {
			b.WriteString(fmt.Sprintf(" vrrp vrid %d preempt-mode disable\n", g.VRID))
		}
		if g.Advertise != vrrpAdvDefault {
			b.WriteString(fmt.Sprintf(" vrrp vrid %d timer advertise %d\n", g.VRID, g.Advertise))
		}
		if g.TrackInterface != "" {
			b.WriteString(fmt.Sprintf(" vrrp vrid %d track interface %s", g.VRID, g.TrackInterface))
			if g.TrackReduced != vrrpTrackReducedDefault {
				b.WriteString(fmt.Sprintf(" reduced %d", g.TrackReduced))
			}
			b.WriteString("\n")
		}
		if g.AuthMode != "" {
			// 诚实边界（主理人裁定 §7.5 胜出，修复 T06 明文泄露）：current-configuration 的
			// VRRP 认证行仅输出「模式 + cipher」，不回显明文 key（display vrrp 详情亦仅显示模式）。
			// auth-key 仍持久化于 DeviceConfig（真实认证所需），仅 display 端脱敏。
			b.WriteString(fmt.Sprintf(" vrrp vrid %d authentication-mode %s cipher\n", g.VRID, g.AuthMode))
		}
		if g.PreemptDelay != 0 {
			b.WriteString(fmt.Sprintf(" vrrp vrid %d preempt timer delay %d\n", g.VRID, g.PreemptDelay))
		}
	}
	return b.String()
}

// displayIPInterface 渲染 `display ip interface [brief]` 的输出。
// brief=true 时输出华为 VRP 风格的简表（含 IP/Mask、Physical、Protocol），
// 否则输出含 Description 的明细表。输出格式与历史版本保持一致。
// —— display interface brief 的真机 VRP 输出规格 ——
//
// 真机（华为 AR/S 系列 VRP）`display interface brief` 的输出结构为：
//
//	<图例块>
//	Interface                   PHY   Protocol InUti OutUti   inErrors  outErrors
//	GigabitEthernet0/0/0        up    up          0%     0%          0          0
//
// 注意真机**没有**破折号分隔线，也**没有** Rate / Description 两列。
// 表头与数据行共用同一个格式串（interfaceBriefRowFormat），从而在编译期就保证
// 每一列的表头与取值严格对齐（列起始位：Interface=0 / PHY=28 / Protocol=34 /
// InUti=43 / OutUti=49 / inErrors=58 / outErrors=68）。
const (
	// interfaceBriefRowFormat 是 brief 表头与数据行共用的列格式串。
	interfaceBriefRowFormat = "%-27s %-5s %-8s %5s %6s %10s %10s\n"
	// interfaceBriefZeroUtil / interfaceBriefZeroCount 是诚实占位值：
	// Windows 侧 lite 引擎不实现真实数据平面，无法统计真实利用率与错误计数，
	// 因此统一输出零值，而不是编造随机数字。
	interfaceBriefZeroUtil  = "0%"
	interfaceBriefZeroCount = "0"
	// interfaceBriefLegend 是真机 brief 输出开头的图例块（逐行对齐真机文案）。
	interfaceBriefLegend = "PHY: Physical\n" +
		"*down: administratively down\n" +
		"^down: standby\n" +
		"(l): loopback\n" +
		"(s): spoofing\n" +
		"(b): BFD down\n" +
		"(e): ETHOAM down\n" +
		"(d): Dampening Suppressed\n" +
		"(p): port alarm down\n" +
		"(dl): DLDP down\n" +
		"InUti/OutUti: input utility rate/output utility rate\n"
)

// interfaceBriefHeader 由 interfaceBriefRowFormat 渲染表头 token 得到，
// 与数据行使用完全相同的列宽，因此不存在"表头与数据错位"的可能。
var interfaceBriefHeader = fmt.Sprintf(interfaceBriefRowFormat,
	"Interface", "PHY", "Protocol", "InUti", "OutUti", "inErrors", "outErrors")

// ifaceCategory 用于 display ip interface 的确定性排序：
// 0=LoopBack，1=Vlanif/Vlan，2=其余物理口。贴近华为 VRP 输出顺序。
func ifaceCategory(name string) int {
	switch {
	case strings.HasPrefix(name, "LoopBack"):
		return 0
	case strings.HasPrefix(name, "Vlan"):
		return 1
	default:
		return 2
	}
}

// naturalLess 对接口名做「自然序」比较（natural order / human order）。
//
// 真机华为 VRP 按接口编号的**数值**排序，而不是字符串字典序。若用字典序，
// GigabitEthernet0/0/24 会排在 GigabitEthernet0/0/3 之前（因为字符 '2' < '3'），
// 与真机不符，会误导照着课程练习的用户。
//
// 实现：把字符串切成「非数字段」与「数字段」交替的序列，逐段比较——
// 非数字段按字节串比较，数字段按数值比较（因此 3 < 10 < 24）。
// 数字段用 strings 逐字符扫描而不 strconv.Atoi，可天然容忍任意长度的编号
// （不会因超出 int64 而溢出），前导零也不影响数值比较。
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, bi := isASCIIDigit(a[i]), isASCIIDigit(b[j])
		if ai != bi {
			// 一边是数字一边不是：直接按字节比较，保证结果确定。
			return a[i] < b[j]
		}
		if !ai {
			// 都是非数字：逐字符比较。
			if a[i] != b[j] {
				return a[i] < b[j]
			}
			i++
			j++
			continue
		}
		// 都是数字：各自取完整数字段，先比有效长度（跳过前导零）再比字面。
		as, ae := i, i
		for ae < len(a) && isASCIIDigit(a[ae]) {
			ae++
		}
		bs, be := j, j
		for be < len(b) && isASCIIDigit(b[be]) {
			be++
		}
		// 跳过前导零后的有效数字串。
		for as < ae-1 && a[as] == '0' {
			as++
		}
		for bs < be-1 && b[bs] == '0' {
			bs++
		}
		aNum, bNum := a[as:ae], b[bs:be]
		if len(aNum) != len(bNum) {
			// 有效位数不同 → 位数少的数值小。
			return len(aNum) < len(bNum)
		}
		if aNum != bNum {
			// 位数相同 → 字典序即数值序。
			return aNum < bNum
		}
		i, j = ae, be
	}
	// 一方是另一方的前缀 → 短的在前。
	return len(a)-i < len(b)-j
}

// isASCIIDigit 判断字节是否为 ASCII 数字。
func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// sortInterfaceNames 就地按华为 VRP 口径排序接口名：
// 先按类别（LoopBack → Vlanif → 物理口），同类内按自然序（数字段做数值比较）。
//
// display interface brief 与 display ip interface brief 共用此函数，
// 保证两个命令的接口顺序完全一致。
func sortInterfaceNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		ci, cj := ifaceCategory(names[i]), ifaceCategory(names[j])
		if ci != cj {
			return ci < cj
		}
		return naturalLess(names[i], names[j])
	})
}

// parseInterface 在已有接口列表中做大小写不敏感匹配，返回规范（原大小写）的接口名。
// 例如用户输入 10Ge5/0/1，而拓扑中真实接口为 10GE5/0/1，则返回 10GE5/0/1，
// 使后续 ip address 等配置落到大小写一致的 key 上，避免 10Ge5/0/1 与 10GE5/0/1 分裂。
func parseInterface(input string, ifaceList []string) (string, error) {
	inputUpper := strings.ToUpper(input)
	for _, iface := range ifaceList {
		if strings.ToUpper(iface) == inputUpper {
			return iface, nil
		}
	}
	return "", fmt.Errorf("invalid interface '%s'", input)
}

// ParseInterfaceName 是 parseInterface 的导出版本，供 REST API 等非 CLI 上下文
// 复用同一套接口名大小写不敏感解析（返回拓扑中原始的规范接口名）。
func ParseInterfaceName(input string, ifaceList []string) (string, error) {
	return parseInterface(input, ifaceList)
}

// interfaceKeys 取出接口 map 的键列表，供 parseInterface 做匹配。
func interfaceKeys(m map[string]*InterfaceConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// displayIPInterface 渲染 `display ip interface [interface-name] [brief]` 的输出。
// brief=true 时输出华为 VRP 风格的简表（含 IP/Mask、Physical、Protocol），
// 否则输出含 Description 的明细表。输出格式与历史版本保持一致。
// filterIface 非空时只展示指定接口（规范名），其余接口忽略。
func displayIPInterface(state *CLIState, brief bool, filterIface string) string {
	type ifaceIPInfo struct {
		Name        string
		IP          string
		Status      string
		Description string
		IsLoopback  bool
	}
	ifaceIPMap := make(map[string]*ifaceIPInfo)

	isPCServer := state.DeviceType == topology.DevicePC || state.DeviceType == topology.DeviceServer
	// addIface 合并同一接口在 DeviceConfig / Interfaces 两处来源的信息；
	// 同时标记是否为 LoopBack（用于协议列追加 (s) 欺骗标志）。
	addIface := func(name, ip, status, desc string) {
		info, ok := ifaceIPMap[name]
		if !ok {
			info = &ifaceIPInfo{Name: name, Status: "up"}
			ifaceIPMap[name] = info
		}
		if ip != "" {
			info.IP = ip
		}
		if status != "" {
			info.Status = status
		}
		if desc != "" {
			info.Description = desc
		}
		info.IsLoopback = strings.HasPrefix(name, "LoopBack")
	}

	if isPCServer {
		hostIfaces := getHostInterfaces(state)
		for _, iface := range hostIfaces {
			name := iface["name"]
			ip := iface["ip"]
			status := strings.ToLower(iface["status"])
			if ip != "" {
				if strings.Contains(ip, "/") {
					ipParts := strings.Split(ip, "/")
					if len(ipParts) == 2 {
						prefix, _ := strconv.Atoi(ipParts[1])
						mask := prefixToSubnet(prefix)
						ip = fmt.Sprintf("%s %s", ipParts[0], mask)
					}
				}
			}
			addIface(name, ip, status, "")
		}
	} else {
		// 交换机/路由/VTEP 等网络设备：仅展示 10GE / LoopBack / Vlanif 等接口，
		// 不展示 Ethernet0（那是 PC/Server 的接口）。
		for k, v := range state.DeviceConfig {
			if !strings.HasPrefix(k, "interface:") {
				continue
			}
			parts := strings.SplitN(k, ":", 3)
			if len(parts) < 2 {
				continue
			}
			ifaceName := parts[1]
			if ifaceName == "Ethernet0" {
				continue
			}
			key := parts[2]
			addIface(ifaceName, "", "", "")
			switch key {
			case "ip":
				ifaceIPMap[ifaceName].IP = v
			case "status":
				ifaceIPMap[ifaceName].Status = strings.ToLower(v)
			}
		}
		// 从 Interfaces map 合并
		for name, iface := range state.Interfaces {
			if name == "Ethernet0" {
				continue
			}
			ip := ""
			if iface.IP != "" {
				if iface.Mask != "" {
					ip = fmt.Sprintf("%s %s", iface.IP, iface.Mask)
				} else {
					ip = iface.IP
				}
			}
			addIface(name, ip, strings.ToLower(iface.Status), iface.Description)
		}
	}

	// 过滤到指定接口（dis ip int <name>）：仅保留 filterIface。
	if filterIface != "" {
		info, ok := ifaceIPMap[filterIface]
		if !ok {
			return fmt.Sprintf("Interface %s does not exist or has no IP configuration", filterIface)
		}
		ifaceIPMap = map[string]*ifaceIPInfo{filterIface: info}
	}

	// 确定性排序：LoopBack → Vlanif → 其余物理口，同类内按**自然序**
	// （接口编号做数值比较，0/0/3 在 0/0/24 之前，对齐真机 VRP）。
	// 与 display interface brief 共用 sortInterfaceNames，口径完全一致。
	names := make([]string, 0, len(ifaceIPMap))
	for n := range ifaceIPMap {
		names = append(names, n)
	}
	sortInterfaceNames(names)

	var out strings.Builder
	if brief {
		out.WriteString("*down: administratively down\n")
		out.WriteString("^down: standby\n")
		out.WriteString("(l): loopback\n")
		out.WriteString("(s): spoofing\n")
		out.WriteString("(E): E-Trunk down\n")
		out.WriteString("\n")
		out.WriteString("Interface                         IP Address/Mask      Physical   Protocol  \n")
		out.WriteString("------------------------------------------------------------------------------\n")
		for _, name := range names {
			info := ifaceIPMap[name]
			ipDisplay := "unassigned"
			if info.IP != "" {
				ipAddr, subnet := parseIPFormat(info.IP)
				ipDisplay = fmt.Sprintf("%s/%d", ipAddr, subnetToPrefix(subnet))
			}
			physical := info.Status
			if physical == "" {
				physical = "up"
			}
			protocol := physical
			if info.IsLoopback {
				protocol = physical + "(s)"
			}
			out.WriteString(fmt.Sprintf("%-33s %-20s %-10s %-9s\n", info.Name, ipDisplay, physical, protocol))
		}
	} else {
		out.WriteString("Interface             IP Address      Physical   Protocol  Description\n")
		out.WriteString("------------------------------------------------------------------------------\n")
		for _, name := range names {
			info := ifaceIPMap[name]
			ipDisplay := "unassigned"
			if info.IP != "" {
				ipAddr, _ := parseIPFormat(info.IP)
				ipDisplay = ipAddr
			}
			physical := info.Status
			if physical == "" {
				physical = "up"
			}
			protocol := physical
			if info.IsLoopback {
				protocol = physical + "(s)"
			}
			desc := info.Description
			if desc == "" {
				desc = "-"
			}
			out.WriteString(fmt.Sprintf("%-21s %-15s %-10s %-9s %s\n", info.Name, ipDisplay, physical, protocol, desc))
		}
	}
	return out.String()
}
