// display_registry.go 是 display/dis 子命令的集中注册表（v0.11.0 统一命令注册机制）。
// 由 parser.go 原 `case "display","dis":` 内的 `switch arg0` 逐字迁移：每个原 case 体
// 成为 buildXxxDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string，签名直接携带
// state/cmd/arg0/arg1，逻辑零改写。新增 display 命令：① 本文件加 buildXxxDisplay；② 注册归一化 key。
// 键碰撞红线：展示/补全键匹配走精确 helper，禁用 strings.Contains 模糊扫描。
package cli

import (
	"fmt"
	"strconv"
	"strings"
	"ensp-lab/internal/topology"
)

// DisplayHandler 渲染一条 display 子命令。state=当前状态；cmd=完整命令；
// arg0=归一化子命令（注册 key）；arg1=归一化二级子命令（可能空）。只读，禁写 DeviceConfig 键。
type DisplayHandler func(state *CLIState, cmd *Command, arg0, arg1 string) string

// displayRegistry 是 dis 子命令单一事实源。key = normalizeDisplaySubCmd 归一化结果。
var displayRegistry = map[string]DisplayHandler{
	"aaa": regAaaDisplay,
	"acl": regAclDisplay,
	"arp": regArpDisplay,
	"bfd": regBfdDisplay,
	"bgp": regBgpDisplay,
	"clock": regClockDisplay,
	"cpu-usage": regCpuUsageDisplay,
	"current-configuration": regCurrentConfigurationDisplay,
	"device": regDeviceDisplay,
	"dhcp": regDhcpDisplay,
	"diagnostic-information": regDiagnosticInformationDisplay,
	"domain": regDomainDisplay,
	"dot1x": regDot1xDisplay,
	"eth-trunk": regEthTrunkDisplay,
	"evpn": regEvpnDisplay,
	"gre": regGreDisplay,
	"history-command": regHistoryCommandDisplay,
	"interface": regInterfaceDisplay,
	"ip": regIpDisplay,
	"ipsec": regIpsecDisplay,
	"ipv6": regIpv6Display,
	"isis": regIsisDisplay,
	"link-aggregation": regLinkAggregationDisplay,
	"lldp": regLldpDisplay,
	"local-user": regLocalUserDisplay,
	"m-lag": regMLagDisplay,
	"mac-address": regMacAddressDisplay,
	"memory": regMemoryDisplay,
	"mlag": regMLagDisplay,
	"nat": regNatDisplay,
	"ndp": regNdpDisplay,
	"netflow": regNetflowDisplay,
	"ntp": regNtpDisplay,
	"ospf": regOspfDisplay,
	"ospfv3": regOspfv3Display,
	"pbr": regPbrDisplay,
	"port": regPortVlanDisplay,
	"port-security": regPortSecurityDisplay,
	"port-vlan": regPortVlanDisplay,
	"qos": regQosDisplay,
	"radius": regRadiusDisplay,
	"ripng": regRipngDisplay,
	"routing-table": regRoutingTableDisplay,
	"saved-configuration": regSavedConfigurationDisplay,
	"snmp": regSnmpDisplay,
	"ssh": regSshDisplay,
	"startup": regStartupDisplay,
	"stp": regStpDisplay,
	"syslog": regSyslogDisplay,
	"sysname": regSysnameDisplay,
	"temperature": regTemperatureDisplay,
	"this": regThisDisplay,
	"users": regUsersDisplay,
	"version": regVersionDisplay,
	"vlan": regVlanDisplay,
	"vrf": regVrfDisplay,
	"vrrp": regVrrpDisplay,
	"vxlan": regVxlanDisplay,
}

// regThisDisplay 由原 parser.go `case this:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regThisDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regCurrentConfigurationDisplay 由原 parser.go `case current-configuration:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regCurrentConfigurationDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// 复用 VRP 风格配置快照生成器（与 display saved-configuration / save 一致），
			// 并追加 OSPF/BGP/ISIS 等协议启用摘要块，避免较旧版直排 key-value 而丢失协议信息
			//（P1-F，决策 #6 + 风险3）。
			return state.buildSavedConfigSnapshot() + formatProtocolBlocks(state)
}
// regEthTrunkDisplay 由原 parser.go `case eth-trunk:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regEthTrunkDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// P2 #5 改动点 8（T03）：重写为 buildEthTrunkDisplay，唯一数据源 = EvaluateLAG。
			// 支持 display eth-trunk [<id>] [verbose | load-balance | interface <if>]；
			// 无参按 trunk-id 升序逐组完整块；成员自然序确定性（AC5）。
			return buildEthTrunkDisplay(state, cmd.Args[1:])
}
// regLinkAggregationDisplay 由原 parser.go `case link-aggregation:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regLinkAggregationDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// P2 #5 改动点 9（T03）：display link-aggregation summary。
			// **已删除现状残桩的第二个重映射循环**——那是幽灵 Bridge-Aggregation
			// 编造数据的根因（P1-10 升级 P0，拍板 #4）；改按 agg-family 归类 + 确定性升序。
			if arg1 == "" || arg1 == "summary" {
				return buildLinkAggregationSummary(state)
			}
			return "Error: invalid link-aggregation command"
}
// regPortVlanDisplay 由原 parser.go `case port-vlan/port:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regPortVlanDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isSwitch := state.DeviceType == topology.DeviceSwitch

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
}
// regIpDisplay 由原 parser.go `case ip:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regIpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isSwitch := state.DeviceType == topology.DeviceSwitch
	isAC := state.DeviceType == topology.DeviceAC || state.DeviceType == topology.DeviceAP
	isHost := state.DeviceType == topology.DevicePC || state.DeviceType == topology.DeviceClient || state.DeviceType == topology.DeviceServer
	isCloudHub := state.DeviceType == topology.DeviceCloud || state.DeviceType == topology.DeviceHub

			// display ip <sub> ...   sub ∈ {interface, pool, routing-table}
			// 二级子命令缩写与合法性校验（华为 VRP 规则：必须是关键字的前缀）
			ipSubKW := []string{"interface", "pool", "routing-table"}
			resolvedSub, subOK, subErr := resolveKeyword(arg1, ipSubKW)
			if !subOK {
				switch subErr {
				case "incomplete":
					return "Error: Incomplete command found at '^' position."
				case "ambiguous":
					return "Error: Ambiguous command found at '^' position."
				default:
					return "Error: Wrong parameter found at '^' position."
				}
			}
			arg1 = resolvedSub

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
				if res, ok, err := resolveKeyword(a, []string{"brief"}); ok {
					brief = res == "brief"
				} else if err == "incomplete" {
					return "Error: Incomplete command found at '^' position."
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
}
// regIpv6Display 由原 parser.go `case ipv6:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regIpv6Display(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display ipv6 ...（P0-5/6/9，A13 无参等价 brief，只读任意设备可读 AC11b）。
			//   display ipv6 [brief]                       → 接口简表
			//   display ipv6 interface [<if>|brief]        → 详情 / 简表
			//   display ipv6 routing-table [<prefix>]      → 路由表（P1-1 过滤）
			return buildIPv6Display(state, cmd.Args[1:])
}
// regRipngDisplay 由原 parser.go `case ripng:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regRipngDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display ripng [<pid>]（P0-13，诚实占位：配置态真实 + 运行态恒 "-" + 注记）。
			pid := ""
			if len(cmd.Args) >= 2 {
				pid = cmd.Args[1]
			}
			return buildRIPngDisplay(state, pid)
}
// regOspfv3Display 由原 parser.go `case ospfv3:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regOspfv3Display(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display ospfv3 [<pid>]（P0-14，诚实占位：配置态真实 + 运行态恒 "-" + 注记）。
			pid := ""
			if len(cmd.Args) >= 2 {
				pid = cmd.Args[1]
			}
			return buildOSPFv3Display(state, pid)
}
// regRoutingTableDisplay 由原 parser.go `case routing-table:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regRoutingTableDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isSwitch := state.DeviceType == topology.DeviceSwitch
	isAC := state.DeviceType == topology.DeviceAC || state.DeviceType == topology.DeviceAP
	isHost := state.DeviceType == topology.DevicePC || state.DeviceType == topology.DeviceClient || state.DeviceType == topology.DeviceServer
	isCloudHub := state.DeviceType == topology.DeviceCloud || state.DeviceType == topology.DeviceHub

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
}
// regArpDisplay 由原 parser.go `case arp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regArpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regNatDisplay 由原 parser.go `case nat:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regNatDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isRouter := state.DeviceType == topology.DeviceRouter
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isFirewall := state.DeviceType == topology.DeviceFirewall

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
}
// regVlanDisplay 由原 parser.go `case vlan:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regVlanDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isSwitch := state.DeviceType == topology.DeviceSwitch

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
}
// regMacAddressDisplay 由原 parser.go `case mac-address:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regMacAddressDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isSwitch := state.DeviceType == topology.DeviceSwitch

			if !isSwitch && !isL3Switch {
				return fmt.Sprintf("Error: MAC address is not supported on %s", state.DeviceType)
			}
			var out strings.Builder
			out.WriteString("MAC Address       VLAN  Interface            Type\n")
			for _, entry := range state.MACTable {
				out.WriteString(fmt.Sprintf("%-17s %-5d %-20s %s\n", entry.MAC, entry.VLAN, entry.Interface, entry.Type))
			}
			return out.String()
}
// regPortSecurityDisplay 由原 parser.go `case port-security:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regPortSecurityDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// 端口安全状态展示（T04）。可选 interface 过滤：
			//   display port-security                → 全接口表
			//   display port-security interface <if> → 单端口详情
			filter := ""
			if arg1 == "interface" && len(cmd.Args) > 2 {
				filter = cmd.Args[2]
			}
			return buildPortSecurityDisplay(state, filter)
}
// regInterfaceDisplay 由原 parser.go `case interface:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regInterfaceDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
					ifaceMap[ifaceName].IP = v
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
					ip := iface.IP
					if ip == "" {
						ip = "unassigned"
					}
					// 口径对齐 brief：模型口默认 up，仅显式 Down 才显示 down；
					// Protocol 列取真实协议态（与物理态一致），严禁 Status 误填 Protocol。
					physical := "up"
					if iface.Status == "Down" || iface.Status == "down" {
						physical = "down"
					}
					protocol := physical
					if isTunnelInterface(iface.Name) {
						protocol = greLineProtocolBrief(EvaluateGRE(state, iface.Name).Config)
					}
					out.WriteString(fmt.Sprintf("%-25s %-8s %-9s %-21s %s\n", iface.Name, physical, protocol, desc, ip))
				}
			}
			return out.String()
}
// regOspfDisplay 由原 parser.go `case ospf:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regOspfDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isRouter := state.DeviceType == topology.DeviceRouter
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isFirewall := state.DeviceType == topology.DeviceFirewall

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
}
// regIsisDisplay 由原 parser.go `case isis:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regIsisDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isRouter := state.DeviceType == topology.DeviceRouter
	isL3Switch := state.DeviceType == topology.DeviceL3Switch
	isFirewall := state.DeviceType == topology.DeviceFirewall

			if !isRouter && !isL3Switch && !isFirewall && state.DeviceType != topology.DeviceVTEP {
				return fmt.Sprintf("Error: ISIS is not supported on %s", state.DeviceType)
			}
			return buildIsisDisplay(state)
}
// regAclDisplay 由原 parser.go `case acl:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regAclDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isAC := state.DeviceType == topology.DeviceAC || state.DeviceType == topology.DeviceAP
	isHost := state.DeviceType == topology.DevicePC || state.DeviceType == topology.DeviceClient || state.DeviceType == topology.DeviceServer
	isCloudHub := state.DeviceType == topology.DeviceCloud || state.DeviceType == topology.DeviceHub

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
}
// regMLagDisplay 由原 parser.go `case m-lag/mlag:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regMLagDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {
	isL3Switch := state.DeviceType == topology.DeviceL3Switch

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
}
// regLldpDisplay 由原 parser.go `case lldp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regLldpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display lldp local-information / display lldp interface <type> <num> / display lldp neighbor
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
}
// regStpDisplay 由原 parser.go `case stp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regStpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// STP/RSTP/MSTP 显示（P2 第四项）：纯函数渲染，无副作用。
			// 单事实源 = DeviceConfig（stp:* / interface:*:stp:*），display 经 buildSTPDisplay 即时派生。
			return buildSTPDisplay(state, arg1, cmd.Args)
}
// regVrrpDisplay 由原 parser.go `case vrrp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regVrrpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// 忠实展示 VRRP 组（P2 第三项）：支持 brief / interface <if> / vrid <id> / 全接口。
			// 只读 collectVRRPGroups + EvaluateVRRP，无副作用；末尾附诚实占位注记。
			return buildVRRPDisplay(state, arg1, cmd.Args)
}
// regDhcpDisplay 由原 parser.go `case dhcp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regDhcpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// P2 #6（T3）：display dhcp relay [all | interface <if>]。
			// 只读渲染，唯一数据源 = EvaluateDHCPRelay；拍板 #5「display 任意设备可读」，
			// 故此处**不做设备类型守卫**（二层交换机读到的就是「无中继接口」，忠实呈现）。
			if strings.EqualFold(arg1, "relay") {
				return buildDHCPRelayDisplay(state, cmd.Args[2:])
			}
			// 其它 display dhcp <x> 本期未实现，明确拒绝而非静默返回空。
			return errUnrecognizedCommand
}
// regIpsecDisplay 由原 parser.go `case ipsec:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regIpsecDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regSnmpDisplay 由原 parser.go `case snmp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regSnmpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regSyslogDisplay 由原 parser.go `case syslog:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regSyslogDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regNtpDisplay 由原 parser.go `case ntp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regNtpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regSshDisplay 由原 parser.go `case ssh:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regSshDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regVxlanDisplay 由原 parser.go `case vxlan:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regVxlanDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regBgpDisplay 由原 parser.go `case bgp:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regBgpDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display bgp peer：逐邻居明细表（P1-F，T06）
			if arg1 == "peer" {
				return buildBGPPeerDisplay(state)
			}
			// display bgp evpn [peer|routing-table|vni]：EVPN 地址族诚实占位（P2 / AC6）
			if arg1 == "evpn" {
				return buildEVPNBGPDisplay(state)
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
}
// regDiagnosticInformationDisplay 由原 parser.go `case diagnostic-information:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regDiagnosticInformationDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// 单命令设备体检报告（P1-F，T06）
			return buildDiagnosticInfo(state)
}
// regBfdDisplay 由原 parser.go `case bfd:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regBfdDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regVrfDisplay 由原 parser.go `case vrf:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regVrfDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regPbrDisplay 由原 parser.go `case pbr:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regPbrDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regGreDisplay 由原 parser.go `case gre:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regGreDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// GRE 隧道汇总（P2 第七项，C6 重定向落点）：旧自造 display gre 重定向到
			// display gre tunnel 新实现（只读，无副作用，确定性升序，AC7）。
			return buildGREDisplay(state, cmd.Args[1:])
}
// regAaaDisplay 由原 parser.go `case aaa:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regAaaDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// AAA 汇总（P2 第八项，P0-12）：只读、名称升序、末尾附诚实注记。
			return buildAAADisplay(state)
}
// regLocalUserDisplay 由原 parser.go `case local-user:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regLocalUserDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// 本地用户表（P0-12/P0-13）：口令恒脱敏 ****，未配口令显示 -，
			// privilege 未配显示 - 而非假 0。
			return buildAAALocalUserDisplay(state)
}
// regDomainDisplay 由原 parser.go `case domain:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regDomainDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// AAA 域汇总 / 详情（P1-7）。域名大小写敏感，故取原始 cmd.Args[1]
			// 而非已 ToLower 的 arg1。
			domainName := ""
			if len(cmd.Args) > 1 {
				domainName = cmd.Args[1]
			}
			return buildAAADomainDisplay(state, domainName)
}
// regQosDisplay 由原 parser.go `case qos:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regQosDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regDot1xDisplay 由原 parser.go `case dot1x:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regDot1xDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regRadiusDisplay 由原 parser.go `case radius:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regRadiusDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regNetflowDisplay 由原 parser.go `case netflow:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regNetflowDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regSysnameDisplay 由原 parser.go `case sysname:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regSysnameDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			sysname := state.DeviceConfig["sysname"]
			if sysname == "" {
				sysname = string(state.DeviceType)
			}
			return fmt.Sprintf("System name: %s\n", sysname)
}
// regVersionDisplay 由原 parser.go `case version:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regVersionDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regMemoryDisplay 由原 parser.go `case memory:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regMemoryDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			var out strings.Builder
			out.WriteString("Memory Usage:\n")
			out.WriteString("----------------------------------------------------\n")
			out.WriteString("Total      Used       Free      Shared    Buffers   Cached\n")
			out.WriteString("----------------------------------------------------\n")
			out.WriteString("8192MB     1234MB     6958MB    0MB       0MB       0MB\n")
			out.WriteString("\n")
			out.WriteString("Memory utilization percentage: 15%\n")
			return out.String()
}
// regCpuUsageDisplay 由原 parser.go `case cpu-usage:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regCpuUsageDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regUsersDisplay 由原 parser.go `case users:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regUsersDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			var out strings.Builder
			out.WriteString("User-Intf    Delay Type Network Address  AuthenStatus    AuthorcmdFlag\n")
			out.WriteString("------------------------------------------------------------------------\n")
			out.WriteString(fmt.Sprintf("VTY 0        00:00:00  TEL  127.0.0.1         pass          No Privilege\n"))
			return out.String()
}
// regDeviceDisplay 由原 parser.go `case device:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regDeviceDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regClockDisplay 由原 parser.go `case clock:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regClockDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			return "2026-06-29 09:30:00\nTime zone: UTC+08:00"
}
// regTemperatureDisplay 由原 parser.go `case temperature:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regTemperatureDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			var out strings.Builder
			out.WriteString("Slot  Temperature  CPU Temperature\n")
			out.WriteString("----------------------------------\n")
			out.WriteString("0     45C          52C\n")
			out.WriteString("1     43C          50C\n")
			return out.String()
}
// regStartupDisplay 由原 parser.go `case startup:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regStartupDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

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
}
// regSavedConfigurationDisplay 由原 parser.go `case saved-configuration:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regSavedConfigurationDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			if !state.Saved || state.SavedConfig == "" {
				return "No saved configuration found.\nPlease use the 'save' command to save the current configuration."
			}
			var out strings.Builder
			out.WriteString("Saved configuration:\n")
			out.WriteString(state.SavedConfig)
			out.WriteString(fmt.Sprintf("\nConfiguration saved at %s\n", state.SaveTime))
			return out.String()
}
// regHistoryCommandDisplay 由原 parser.go `case history-command:` 逐字迁移（逻辑零变化；委托既有 build* 函数者保持委托）。
func regHistoryCommandDisplay(state *CLIState, cmd *Command, arg0, arg1 string) string {

			// display history-command [max-size]：展示最近执行的命令行。
			// 可选数字参数限定展示条数（如 display history-command 20）。
			maxSize := 0
			if len(cmd.Args) >= 2 {
				if n, err := strconv.Atoi(cmd.Args[1]); err == nil && n > 0 {
					maxSize = n
				}
			}
			return state.FormatHistoryCommand(maxSize)
}
