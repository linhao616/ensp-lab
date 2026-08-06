// Package cli 提供网络设备的命令行接口模拟，支持命令解析、执行和状态管理。
//
// ✅ 当前状态：已启用
//   - 被 router.go 中的 Router 结构体直接使用
//   - 支持设备命令执行、接口查询、可达性检查等功能
//   - 与 protocol 包配合实现协议模拟
//
// capabilities.go 定义命令能力矩阵，描述每个命令在不同设备类型上的可用性。
package cli

import "ensp-lab/internal/topology"

// capabilities 描述每个顶层命令在每种设备类型上是否可用。
//
// 设计原则：
//   - true 表示支持，false 表示应拒绝并提示 "Error: command not supported on <type>"。
//   - 视图切换（system-view/quit/interface）和查询类（display/ping/?）在所有设备上通用。
//   - 二层能力矩阵：命令 × 设备类型，O(M*N) 查找 O(1)。
//   - 后续添加新命令时只需补一行，不会扩散到 ExecuteCommand 主逻辑。
type capabilityMatrix map[string]map[topology.DeviceType]bool

// capabilities 静态能力矩阵，按设备类型描述可用的顶层命令。
var capabilities = capabilityMatrix{
	// 通用：所有设备均可用
	"system-view": allDevices(),
	"sys":         allDevices(),
	"quit":        allDevices(),
	"q":           allDevices(),
	"interface":   allDevices(),
	"int":         allDevices(),
	"sysname":     allDevices(),
	"display":     allDevices(),
	"dis":         allDevices(),
	"ping":        allDevices(),
	"?":           allDevices(),
	"help":        allDevices(),
	"save":        allDevices(),
	"reboot":      allDevices(),
	"quit-cli":    allDevices(),
	"return":      allDevices(),
	"reset":       allDevices(),
	"undo":        allDevices(),

	// 用户界面与认证类：路由器、三层交换机、防火墙
	"user-interface": l3Devices(),
	"local-user":     l3Devices(),
	"stelnet":        l3Devices(),
	"rsa":            l3Devices(),
	"ssh":            l3Devices(),

	// L3 / 路由类：路由器、三层交换机、防火墙
	"ip":                 hostsAndL3(),
	"ospf":               l3Devices(),
	"isis":               l3Devices(),
	"bgp":                l3Devices(),
	"rip":                routerDevices(),
	"vrrp":               l3Devices(),
	"bfd":                l3Devices(),
	"policy-based-route": l3Devices(),
	"pbr":                l3Devices(),
	"gre":                l3Devices(),
	"ipsec":              l3Devices(),
	"nat":                l3Devices(),
	"dhcp":               switchAndL3(),
	"dfs-group":          l3SwitchOnly(),

	// 二层 / 交换类：交换机、三层交换机
	"vlan":          switchDevices(),
	"vlanif":        l3SwitchOnly(),
	"stp":           switchDevices(),
	"lldp":          switchDevices(),
	"m-lag":         l3SwitchOnly(),
	"mlag":          l3SwitchOnly(),
	"vxlan":         l3SwitchOnly(),
	"dot1x":         switchDevices(),
	"radius":        switchDevices(),
	"qos":           switchDevices(),
	"lacp":          switchDevices(),
	"port-security": switchDevices(),

	// 端口安全诊断命令：simulate frame（仅交换机类，拍板 #6）。非交换机执行回显 not supported。
	"simulate": switchDevices(),

	// VTEP / EVPN-VXLAN 类：VTEP 设备、三层交换机
	"vsi":                 l3SwitchOnly(),
	"evpn-instance":       l3SwitchOnly(),
	"route-distinguisher": l3SwitchOnly(),
	"vpn-target":          l3SwitchOnly(),
	"distributed-gateway": l3SwitchOnly(),
	"vxlan-interface":     l3SwitchOnly(),
	"vxlan-traffic-type":  l3SwitchOnly(),
	"remote-evpn-vtep":    l3SwitchOnly(),

	// ACL / 安全：路由器、三层交换机、防火墙
	"acl":            l3Devices(),
	"rule":           l3Devices(),
	"traffic-filter": l3Devices(),

	// 管理类：所有 L2/L3 设备
	"snmp":    switchAndL3(),
	"syslog":  switchAndL3(),
	"ntp":     switchAndL3(),
	"netflow": l3Devices(),

	// 主机/终端类：PC、Client、Server
	"ipconfig":   hostDevices(),
	"ifconfig":   hostDevices(),
	"netsh":      hostDevices(),
	"tracert":    hostDevices(),
	"traceroute": hostDevices(),
	"arp":        hostDevices(),
	"netstat":    hostDevices(),
	"nslookup":   hostDevices(),

	// MPLS / PPP / PPPoE：路由器支持
	"mpls":  routerDevices(),
	"ppp":   routerDevices(),
	"pppoe": routerDevices(),

	// IPv6：路由器、三层交换机、防火墙、服务器支持
	"ipv6": hostsAndL3(),

	// 应用层服务：Server 设备
	"http":  serverDevices(),
	"https": serverDevices(),
	"dns":   serverDevices(),
	"ftp":   serverDevices(),
	"smtp":  serverDevices(),
}

// isCommandSupported 判断指定设备类型是否支持某顶层命令。
// topCommand 是命令的第一个 token（已 ToLower）。
func isCommandSupported(topCommand string, dt topology.DeviceType) bool {
	row, ok := capabilities[topCommand]
	if !ok {
		// 未在矩阵中显式声明的命令默认允许（保持扩展性，但已知的危险命令应列入 false）
		return true
	}
	supported, ok := row[dt]
	if !ok {
		// 设备类型未在行中声明则视为不支持
		return false
	}
	return supported
}

// allDevices 列出全部已知设备类型。
func allDevices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceSwitch:   true,
		topology.DeviceFirewall: true,
		topology.DeviceAC:       true,
		topology.DeviceAP:       true,
		topology.DevicePC:       true,
		topology.DeviceClient:   true,
		topology.DeviceServer:   true,
		topology.DeviceCloud:    true,
		topology.DeviceHub:      true,
		topology.DeviceVTEP:     true,
	}
}

// l3Devices 路由类设备：路由器、三层交换机、防火墙。
func l3Devices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceFirewall: true,
		topology.DeviceVTEP:     true,
	}
}

// hostsAndL3 路由器 / 三层交换机 / 防火墙 / 终端 / AC / AP：均可配置 IP 地址。
func hostsAndL3() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceFirewall: true,
		topology.DevicePC:       true,
		topology.DeviceServer:   true,
		topology.DeviceClient:   true,
		topology.DeviceAC:       true,
		topology.DeviceAP:       true,
		topology.DeviceVTEP:     true,
	}
}

// switchDevices 二层/三层交换机均支持。
func switchDevices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceSwitch:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceVTEP:     true,
	}
}

// l3SwitchOnly 仅三层交换机支持。
func l3SwitchOnly() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceL3Switch: true,
		topology.DeviceVTEP:     true,
	}
}

// l3AndSwitch 路由器 / 三层交换机 / 二层交换机 / 防火墙。
func l3AndSwitch() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceSwitch:   true,
		topology.DeviceFirewall: true,
		topology.DeviceVTEP:     true,
	}
}

// switchAndL3 交换机 / 三层交换机 / 路由器 / 防火墙（管理平面功能）。
func switchAndL3() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter:   true,
		topology.DeviceL3Switch: true,
		topology.DeviceSwitch:   true,
		topology.DeviceFirewall: true,
		topology.DeviceAC:       true,
		topology.DeviceAP:       true,
		topology.DeviceVTEP:     true,
	}
}

// hostDevices 主机/终端设备：PC、Client、Server。
func hostDevices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DevicePC:     true,
		topology.DeviceClient: true,
		topology.DeviceServer: true,
	}
}

// serverDevices 服务器设备：仅 Server。
func serverDevices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceServer: true,
	}
}

// routerDevices 路由器设备：仅 Router。
func routerDevices() map[topology.DeviceType]bool {
	return map[topology.DeviceType]bool{
		topology.DeviceRouter: true,
	}
}
