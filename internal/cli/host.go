// host.go 实现「终端设备」（PC / Client / Server）的 ifconfig / ip 命令渲染：
//   - GetHostInterfaces  导出版，供 api 包使用
//   - parseIPFormat      支持 CIDR / 双参数 / 纯 IP 多种写法
//   - buildHost*         渲染 ifconfig / ip addr / ip link / ip route / arp / netstat
//
// 与「路由器/交换机」的命令渲染完全分开，是因为终端走的是 Linux 风格输出，
// 而路由器/交换机走的是华为 VRP 风格，二者不会混用。
package cli

import (
	"fmt"
	"strings"
)

func GetHostInterfaces(state *CLIState) []map[string]string {
	return getHostInterfaces(state)
}

func getHostInterfaces(state *CLIState) []map[string]string {
	var ifaces []map[string]string
	seen := map[string]bool{}
	key := state.DeviceID
	if key == "" {
		key = state.DeviceName
	}
	ipSuffix := 100 + (hashString(key) % 50)

	if state.HostIP != "" {
		subnet := state.HostSubnet
		if subnet == "" {
			subnet = "255.255.255.0"
		}
		mac := fmt.Sprintf("00-0C-29-%02X-%02X-%02X", ipSuffix%256, (ipSuffix+1)%256, (ipSuffix+2)%256)
		return []map[string]string{
			{
				"name":   "Ethernet0",
				"ip":     state.HostIP + "/" + cidrFromSubnet(subnet),
				"mac":    mac,
				"status": "Up",
			},
		}
	}

	for k := range state.DeviceConfig {
		if !strings.HasPrefix(k, "interface:") {
			continue
		}
		parts := strings.SplitN(k, ":", 3)
		if len(parts) < 2 {
			continue
		}
		name := parts[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		ip := state.DeviceConfig["interface:"+name+":ip"]
		mac := state.DeviceConfig["interface:"+name+":mac"]
		if mac == "" {
			mac = fmt.Sprintf("00-0C-29-%02X-%02X-%02X", ipSuffix%256, (ipSuffix+1)%256, (ipSuffix+2)%256)
		}
		if ip == "" && name == "Ethernet0" {
			ip = fmt.Sprintf("192.168.1.%d/24", ipSuffix)
		}
		status := "Up"
		if s, ok := state.DeviceConfig["interface:"+name+":status"]; ok {
			status = s
		}
		ifaces = append(ifaces, map[string]string{
			"name":   name,
			"ip":     ip,
			"mac":    mac,
			"status": status,
		})
	}
	if len(ifaces) == 0 {
		ifaces = append(ifaces, map[string]string{
			"name":   "Ethernet0",
			"ip":     fmt.Sprintf("192.168.1.%d/24", ipSuffix),
			"mac":    fmt.Sprintf("00-0C-29-%02X-%02X-%02X", ipSuffix%256, (ipSuffix+1)%256, (ipSuffix+2)%256),
			"status": "Up",
		})
	}
	return ifaces
}

func parseIPFormat(ipStr string) (ipAddr string, subnet string) {
	if ipStr == "" {
		return "", "255.255.255.0"
	}
	// CIDR 格式: 192.168.1.100/24
	if idx := strings.Index(ipStr, "/"); idx > 0 {
		ipAddr = ipStr[:idx]
		prefixLen := ipStr[idx+1:]
		if n, err := parseNum(prefixLen); err == nil {
			subnet = prefixToSubnet(n)
		} else {
			subnet = "255.255.255.0"
		}
		return
	}
	// 双参数格式: 192.168.1.100 255.255.255.0
	parts := strings.Fields(ipStr)
	if len(parts) >= 2 {
		ipAddr = parts[0]
		subnet = parts[1]
		return
	}
	// 只有 IP，没有掩码
	return ipStr, "255.255.255.0"
}

func buildHostIfconfig(state *CLIState) string {
	ifaces := getHostInterfaces(state)
	var lines []string
	for i, iface := range ifaces {
		if i > 0 {
			lines = append(lines, "")
		}
		ip := iface["ip"]
		ipAddr, subnet := parseIPFormat(ip)
		gateway := state.DefaultGateway
		if gateway == "" {
			gateway = state.DeviceConfig["ip:default-gateway"]
		}
		dns := state.HostDNS
		if dns == "" {
			dns = state.DeviceConfig["ip:dns"]
		}
		lines = append(lines, fmt.Sprintf("%-20s", iface["name"]))
		lines = append(lines, fmt.Sprintf("  Connection-specific DNS Suffix  . : "))
		lines = append(lines, fmt.Sprintf("  Description . . . . . . . . . . . : %s Adapter", iface["name"]))
		lines = append(lines, fmt.Sprintf("  Physical Address. . . . . . . . . : %s", iface["mac"]))
		if iface["status"] == "Up" {
			lines = append(lines, fmt.Sprintf("  IPv4 Address. . . . . . . . . . . : %s", ipAddr))
			lines = append(lines, fmt.Sprintf("  Subnet Mask . . . . . . . . . . . : %s", subnet))
			if gateway != "" {
				lines = append(lines, fmt.Sprintf("  Default Gateway . . . . . . . . . : %s", gateway))
			}
			if dns != "" {
				lines = append(lines, fmt.Sprintf("  DNS Servers . . . . . . . . . . . : %s", dns))
			}
		} else {
			lines = append(lines, fmt.Sprintf("  Media State . . . . . . . . . . . : Media disconnected"))
		}
	}
	return strings.Join(lines, "\n")
}

func buildHostIPAddr(state *CLIState) string {
	ifaces := getHostInterfaces(state)
	var lines []string
	for _, iface := range ifaces {
		ip := iface["ip"]
		stateStr := "UP"
		if iface["status"] != "Up" {
			stateStr = "DOWN"
		}
		lines = append(lines, fmt.Sprintf("2: %s: <%s,BROADCAST,RUNNING,MULTICAST> mtu 1500 qdisc fq_codel state UP group default qlen 1000", iface["name"], stateStr))
		lines = append(lines, fmt.Sprintf("    link/ether %s brd ff:ff:ff:ff:ff:ff", strings.ReplaceAll(iface["mac"], "-", ":")))
		if ip != "" && iface["status"] == "Up" {
			// 统一格式化为 CIDR 显示
			ipAddr, subnet := parseIPFormat(ip)
			cidr := ipToCIDR(ipAddr, subnet)
			lines = append(lines, fmt.Sprintf("    inet %s scope global %s", cidr, iface["name"]))
			lines = append(lines, fmt.Sprintf("       valid_lft forever preferred_lft forever"))
		}
		lines = append(lines, fmt.Sprintf("    inet6 fe80::%02x%02x:%02xff:fe%02x:%02x%02x/64 scope link",
			len(iface["name"]), len(iface["name"])*2, len(iface["name"]),
			len(iface["name"])*2, len(iface["name"])*3, len(iface["name"])*4))
		lines = append(lines, fmt.Sprintf("       valid_lft forever preferred_lft forever"))
	}
	return strings.Join(lines, "\n")
}

func buildHostIPLink(state *CLIState) string {
	ifaces := getHostInterfaces(state)
	var lines []string
	for i, iface := range ifaces {
		stateStr := "UP"
		if iface["status"] != "Up" {
			stateStr = "DOWN"
		}
		lines = append(lines, fmt.Sprintf("%d: %s: <%s,BROADCAST,MULTICAST,%s> mtu 1500 qdisc fq_codel state %s mode DEFAULT group default qlen 1000",
			i+1, iface["name"], stateStr, stateStr, stateStr))
		lines = append(lines, fmt.Sprintf("    link/ether %s brd ff:ff:ff:ff:ff:ff", strings.ReplaceAll(iface["mac"], "-", ":")))
	}
	return strings.Join(lines, "\n")
}

func buildHostIPRoute(state *CLIState) string {
	var lines []string
	ifaces := getHostInterfaces(state)
	if len(ifaces) > 0 {
		iface := ifaces[0]
		ip := iface["ip"]
		gateway := ""
		if state.DefaultGateway != "" {
			gateway = state.DefaultGateway
		}
		subnet := "192.168.1.0/24"
		if idx := strings.Index(ip, "/"); idx > 0 {
			ipAddr := ip[:idx]
			prefixLen := ip[idx+1:]
			parts := strings.Split(ipAddr, ".")
			if len(parts) == 4 && prefixLen == "24" {
				subnet = fmt.Sprintf("%s.%s.%s.0/24", parts[0], parts[1], parts[2])
			}
		}
		lines = append(lines, fmt.Sprintf("default via %s dev %s proto dhcp src %s metric 100",
			gateway, iface["name"], strings.Split(ip, "/")[0]))
		lines = append(lines, fmt.Sprintf("%s dev %s proto kernel scope link src %s",
			subnet, iface["name"], strings.Split(ip, "/")[0]))
	}
	return strings.Join(lines, "\n")
}

func buildHostARPTable(state *CLIState) string {
	ifaces := getHostInterfaces(state)
	var lines []string
	lines = append(lines, "Interface: 192.168.1.100 --- 0x2")
	lines = append(lines, "  Internet Address      Physical Address      Type")
	lines = append(lines, "  192.168.1.1           00-0C-29-AA-BB-CC     dynamic")
	lines = append(lines, "  192.168.1.255         FF-FF-FF-FF-FF-FF     static")
	_ = ifaces
	return strings.Join(lines, "\n")
}

func buildHostNetstat(state *CLIState) string {
	var lines []string
	lines = append(lines, "Active Connections")
	lines = append(lines, "")
	lines = append(lines, "  Proto  Local Address          Foreign Address        State")
	lines = append(lines, "  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING")
	lines = append(lines, "  TCP    0.0.0.0:445            0.0.0.0:0              LISTENING")
	lines = append(lines, "  TCP    192.168.1.100:49664    192.168.1.1:443        ESTABLISHED")
	lines = append(lines, "  UDP    0.0.0.0:500            *:*")
	lines = append(lines, "  UDP    0.0.0.0:4500           *:*")
	_ = state
	return strings.Join(lines, "\n")
}

// unquote 去掉字符串两端引号（netsh 接口名常带引号，如 "Ethernet0"）。
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// gwNote 生成网关说明后缀，便于回显时带出默认网关信息。
func gwNote(gw string) string {
	if gw == "" {
		return ""
	}
	return "，网关 " + gw
}

// normalizeMask 将用户输入的掩码统一为点分十进制字符串。
//   - 含 "."（如 255.255.255.0）视为点分掩码，直接返回；
//   - 纯数字（如 24）视为前缀长度，转为点分掩码；
//   - 其余原样返回。
// 注意：不能直接用 parseNum 解析点分掩码，parseNum 会贪婪取首段数字（如把
// "255.255.255.0" 解析成 255），导致 prefixToSubnet(255) 错误地生成 /32。
func normalizeMask(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return ""
	}
	if strings.Contains(m, ".") {
		return m
	}
	if n, err := parseNum(m); err == nil && n >= 0 && n <= 32 {
		return prefixToSubnet(n)
	}
	return m
}

// NormalizeMask 是 normalizeMask 的导出版本，供 REST API 等非 CLI 上下文
// 复用同一套掩码归一化逻辑（点分十进制 / 前缀长度 / CIDR 统一为点分十进制）。
func NormalizeMask(m string) string {
	return normalizeMask(m)
}

// executeNetsh 处理 Windows netsh 风格的终端网络配置命令（仅 PC/Client/Server 可用）。
// 支持：
//
//	netsh interface ip set address "Ethernet0" static <ip> <mask> [gw]
//	netsh interface ip set address "Ethernet0" dhcp
//	netsh interface ip set dns "Ethernet0" static <dns>
//	netsh interface ip set dns "Ethernet0" dhcp
//	netsh interface ip show addresses | show config
func executeNetsh(state *CLIState, args []string) string {
	if len(args) < 2 || strings.ToLower(args[0]) != "interface" || strings.ToLower(args[1]) != "ip" {
		return "Error: only 'netsh interface ip' is supported"
	}
	if len(args) < 3 {
		return "Usage: netsh interface ip <set|show> ..."
	}
	sub := strings.ToLower(args[2])
	switch sub {
	case "set":
		if len(args) < 5 {
			return "Usage: netsh interface ip set address <name> static <ip> <mask> [gw]"
		}
		obj := strings.ToLower(args[3])
		iface := unquote(args[4])
		method := strings.ToLower(args[5])
		if obj == "address" {
			if method == "dhcp" {
				state.HostIP = ""
				state.HostSubnet = ""
				return fmt.Sprintf("接口 \"%s\": 已切换到 DHCP，IP 地址已释放。", iface)
			}
			if method != "static" || len(args) < 7 {
				return "Usage: netsh interface ip set address <name> static <ip> <mask> [gw]"
			}
			ipTok := args[6]
			ip, mask := parseIPFormat(ipTok)
			state.HostIP = ip
			if strings.Contains(ipTok, "/") {
				// CIDR 形式：掩码已由 parseIPFormat 解析
				state.HostSubnet = mask
				if len(args) >= 8 {
					state.DefaultGateway = args[7]
				}
			} else {
				if len(args) >= 8 {
					state.HostSubnet = normalizeMask(args[7])
				}
				if len(args) >= 9 {
					state.DefaultGateway = args[8]
				}
			}
			return fmt.Sprintf("Ok.\n\n接口 \"%s\" 的 IP 地址已设置为 %s/%s%s", iface, state.HostIP, state.HostSubnet, gwNote(state.DefaultGateway))
		}
		if obj == "dns" {
			if method == "dhcp" {
				state.HostDNS = ""
				return fmt.Sprintf("Ok.\n\n接口 \"%s\" 的 DNS 已设为 DHCP。", iface)
			}
			if method != "static" || len(args) < 7 {
				return "Usage: netsh interface ip set dns <name> static <dns>"
			}
			state.HostDNS = args[6]
			return fmt.Sprintf("Ok.\n\n接口 \"%s\" 的 DNS 已设置为 %s。", iface, state.HostDNS)
		}
		return "Error: unsupported netsh object '" + obj + "'"
	case "show":
		what := "config"
		if len(args) >= 4 {
			what = strings.ToLower(args[3])
		}
		if what == "addresses" {
			return buildHostIPAddr(state)
		}
		return buildHostIfconfig(state)
	}
	return "Error: unsupported netsh subcommand '" + sub + "'"
}

// buildHostNslookup 渲染终端 nslookup 的输出（P1-F，T02）。
// 复用 state.HostDNS：已配置 DNS 时返回模拟解析（RFC 5737 TEST-NET-1 段地址，
// 避免与现实网络冲突）；未配置时返回 "DNS server not configured" 提示。
func buildHostNslookup(state *CLIState, host string) string {
	dns := state.HostDNS
	if dns == "" {
		dns = state.DeviceConfig["ip:dns"]
	}
	if dns == "" {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("*** Can't find server name for address ... : DNS server not configured.\n"))
		b.WriteString("Server:  Unknown\n")
		b.WriteString("Address:  ::1#53\n\n")
		b.WriteString(fmt.Sprintf("*** %s: Non-existent domain\n", host))
		return b.String()
	}
	// 模拟解析：根据主机名哈希出一个确定的 A 记录地址（TEST-NET-1 段）。
	ip := fmt.Sprintf("192.0.2.%d", (hashString(host)%250)+1)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s?\n", host))
	b.WriteString(fmt.Sprintf("Server:  %s\n", dns))
	b.WriteString(fmt.Sprintf("Address:  %s#53\n", dns))
	b.WriteString("\n")
	b.WriteString("Non-authoritative answer:\n")
	b.WriteString(fmt.Sprintf("Name:    %s\n", host))
	b.WriteString(fmt.Sprintf("Address:  %s\n", ip))
	return b.String()
}
