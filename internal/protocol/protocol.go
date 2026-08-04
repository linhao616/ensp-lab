// Package protocol 提供网络协议的模拟实现，包括 ARP、ICMP、BGP、OSPF、DHCP 等。
//
// ✅ 当前状态：已启用
//   - 被 router.go 中的 Router 结构体直接使用（protoSim 字段）
//   - ProtocolSimulator 用于协议级别的仿真和可达性检查
//   - 支持多种网络协议的模拟实现（ARP、ICMP、BGP、OSPF、DHCP、DNS 等）
package protocol

import (
	"ensp-lab/internal/topology"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProtocolSimulator struct {
	topology *topology.Topology
	routers  map[string]*RouterState
	mu       sync.RWMutex
}

type RouterState struct {
	DeviceID     string
	RoutingTable []RouteEntry
	ACLs         map[string][]*ACLRule
	OSPF         *OSPFState
	MLAG         *MLAGState
	LLDP         *LLDPSState
	STP          *STPState
	VRRP         *VRRPState
	IPsec        *IPsecState
	SNMP         *SNMPConfig
	Syslog       *SyslogConfig
	NTP          *NTPConfig
	SSH          *SSHConfig
	VXLAN        *VXLANConfig
	BGP          *BGPConfig
	BFD          *BFDState
	VRF          *VRFState
	PBR          *PBRState
	GRE          *GREState
	QoS          *QoSState
	Dot1x        *Dot1xState
	RADIUS       *RADIUSConfig
	NetFlow      *NetFlowState
	ICMP         *ICMPProtocol
	ARP          *ARPProtocol
	TCP          *TCPProtocol
	UDP          *UDPProtocol
	TLS          *TLSProtocol
	HTTP         *HTTPProtocol
	DNS          *DNSProtocol
	DHCP         *DHCPProtocol
	FTP          *FTPProtocol
	RIP          *RIPProtocol
	MPLS         *MPLSProtocol
	PPP          *PPPProtocol
	PPPoE        *PPPoEProtocol
	SMTP         *SMTPProtocol
	IPv6         *IPv6Protocol
	VXLANProto   *VXLANProtocol // Separate VXLAN instance
}

type RouteEntry struct {
	Destination string
	Mask        string
	NextHop     string
	Interface   string
	Protocol    string
	Metric      int
}

type OSPFState struct {
	Enabled   bool
	ProcessID int
	AreaID    int
	Neighbors []string
	LSDB      []LSA
}

type LSA struct {
	LSAType           int
	LinkStateID       string
	AdvertisingRouter string
	SequenceNumber    int
	Age               int
	Data              interface{}
}

type MLAGDomain struct {
	DomainID       int
	SystemPriority int
	SystemMAC      string
	PeerIP         string
	DFSGroupID     int
	DFSMode        string
	Interfaces     map[string]*MLAGInterface
	PeerLink       string
	Status         string
}

type MLAGInterface struct {
	InterfaceName string
	GroupID       int
	Mode          string
	Active        bool
	Backup        bool
}

type MLAGState struct {
	Enabled bool
	Domain  *MLAGDomain
}

type LLDPNeighbor struct {
	ChassisID    string
	PortID       string
	SystemName   string
	SystemDesc   string
	PortDesc     string
	ManagementIP string
	TTL          int
}

type LLDPSState struct {
	Enabled   bool
	Neighbors map[string][]LLDPNeighbor
}

type STPState struct {
	Enabled        bool
	Mode           string
	BridgePriority int
	RootBridgeID   string
	DesignatedRoot string
	RootCost       int
	Ports          map[string]*STPPort
}

type STPPort struct {
	PortName     string
	PortPriority int
	State        string
	Role         string
	Cost         int
}

type VRRPGroup struct {
	GroupID    int
	VirtualIP  string
	VirtualMAC string
	Priority   int
	Master     bool
	Preempt    bool
	Delay      int
	Status     string
}

type VRRPState struct {
	Enabled bool
	Groups  map[int]*VRRPGroup
}

type IPsecTunnel struct {
	TunnelID       string
	LocalIP        string
	RemoteIP       string
	Mode           string
	Encryption     string
	Authentication string
	Status         string
}

type IPsecState struct {
	Enabled bool
	Tunnels map[string]*IPsecTunnel
}

type SNMPConfig struct {
	Enabled    bool
	Version    string
	Community  string
	ManagerIP  string
	TrapEnable bool
	TrapServer string
}

type SyslogConfig struct {
	Enabled    bool
	ServerIP   string
	ServerPort int
	Severity   string
	Facility   string
}

type NTPConfig struct {
	Enabled    bool
	ServerIP   string
	ServerPort int
	Stratum    int
	SyncStatus string
}

type SSHConfig struct {
	Enabled        bool
	Port           int
	Version        string
	Authentication string
	MaxSessions    int
}

type VXLANConfig struct {
	Enabled    bool
	VNI        int
	VTEPIP     string
	PeerVTEPIP string
	VRFName    string
	Status     string
	// VSI (Virtual Switch Instance)
	VSI         string
	VPNs        []string
	GatewayVSI  string
	Distributed bool
	EVNPEncap   string // "vxlan" or "nve"
	EVPMRD      string // EVPN Route Distinguisher
	EVPMRT      string // EVPN Route Target
	// VXLAN tunnel endpoints
	RemoteVTEPs map[string]*RemoteVTEP
}

type RemoteVTEP struct {
	IP   string
	VNI  int
	Peer bool
}

type BGPConfig struct {
	Enabled       bool
	ASNumber      int
	RouterID      string
	Neighbors     map[string]*BGPNeighbor
	RoutingPolicy string
}

type BFDSession struct {
	PeerIP        string
	LocalIP       string
	State         string
	MinTxInterval int
	MinRxInterval int
	DetectMult    int
	DetectTime    int
	UpTime        string
}

type BFDState struct {
	Enabled  bool
	Sessions map[string]*BFDSession
}

type VRF struct {
	Name         string
	RD           string
	RouteTargets []string
	Interfaces   []string
	RoutingTable []RouteEntry
}

type VRFState struct {
	Enabled bool
	VRFs    map[string]*VRF
}

type PBRRule struct {
	ID        int
	MatchACL  string
	NextHop   string
	Interface string
}

type PBRState struct {
	Enabled bool
	Rules   map[string][]*PBRRule
}

type GRETunnel struct {
	Name      string
	SourceIP  string
	DestIP    string
	Key       int
	Keepalive bool
	Status    string
}

type GREState struct {
	Enabled bool
	Tunnels map[string]*GRETunnel
}

type QoSClassifier struct {
	Name     string
	ACL      string
	DSCP     int
	Protocol string
	SrcPort  string
	DstPort  string
}

type QoSBehavior struct {
	Name      string
	Bandwidth int
	Priority  int
	Queue     string
	Action    string
}

type QoSState struct {
	Enabled     bool
	Classifiers map[string]*QoSClassifier
	Behaviors   map[string]*QoSBehavior
	Policies    map[string]*QoSPolicy
}

type QoSPolicy struct {
	Name       string
	Classifier string
	Behavior   string
}

type Dot1xPort struct {
	PortName   string
	Enabled    bool
	AuthMethod string
	Reauth     bool
	QuietTimer int
}

type Dot1xState struct {
	Enabled bool
	Ports   map[string]*Dot1xPort
}

type RADIUSConfig struct {
	Enabled         bool
	PrimaryServer   string
	SecondaryServer string
	SharedSecret    string
	AuthPort        int
	AcctPort        int
	Timeout         int
	Retransmit      int
}

type NetFlowState struct {
	Enabled      bool
	Exporter     string
	Port         int
	Version      string
	SampleRate   int
	ActiveTime   int
	InactiveTime int
}

func NewProtocolSimulator(t *topology.Topology) *ProtocolSimulator {
	return &ProtocolSimulator{
		topology: t,
		routers:  make(map[string]*RouterState),
	}
}

func (p *ProtocolSimulator) InitRouter(deviceID string) *RouterState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.routers[deviceID]; !ok {
		p.routers[deviceID] = &RouterState{
			DeviceID:     deviceID,
			RoutingTable: []RouteEntry{},
			ACLs:         make(map[string][]*ACLRule),
			OSPF: &OSPFState{
				Enabled: false,
			},
			MLAG: &MLAGState{
				Enabled: false,
			},
			LLDP: &LLDPSState{
				Enabled:   false,
				Neighbors: make(map[string][]LLDPNeighbor),
			},
			STP: &STPState{
				Enabled:        false,
				Mode:           "rstp",
				BridgePriority: 32768,
				Ports:          make(map[string]*STPPort),
			},
			VRRP: &VRRPState{
				Enabled: false,
				Groups:  make(map[int]*VRRPGroup),
			},
			IPsec: &IPsecState{
				Enabled: false,
				Tunnels: make(map[string]*IPsecTunnel),
			},
			SNMP: &SNMPConfig{
				Enabled:   false,
				Version:   "v2c",
				Community: "public",
			},
			Syslog: &SyslogConfig{
				Enabled:    false,
				ServerPort: 514,
				Severity:   "informational",
				Facility:   "local0",
			},
			NTP: &NTPConfig{
				Enabled:    false,
				ServerPort: 123,
				Stratum:    15,
			},
			SSH: &SSHConfig{
				Enabled:        true,
				Port:           22,
				Version:        "2.0",
				Authentication: "password",
				MaxSessions:    5,
			},
			VXLAN: &VXLANConfig{
				Enabled:     false,
				RemoteVTEPs: make(map[string]*RemoteVTEP),
			},
			BGP: &BGPConfig{
				Enabled:   false,
				Neighbors: make(map[string]*BGPNeighbor),
			},
			BFD: &BFDState{
				Enabled:  false,
				Sessions: make(map[string]*BFDSession),
			},
			VRF: &VRFState{
				Enabled: false,
				VRFs:    make(map[string]*VRF),
			},
			PBR: &PBRState{
				Enabled: false,
				Rules:   make(map[string][]*PBRRule),
			},
			GRE: &GREState{
				Enabled: false,
				Tunnels: make(map[string]*GRETunnel),
			},
			QoS: &QoSState{
				Enabled:     false,
				Classifiers: make(map[string]*QoSClassifier),
				Behaviors:   make(map[string]*QoSBehavior),
				Policies:    make(map[string]*QoSPolicy),
			},
			Dot1x: &Dot1xState{
				Enabled: false,
				Ports:   make(map[string]*Dot1xPort),
			},
			RADIUS: &RADIUSConfig{
				Enabled:    false,
				AuthPort:   1812,
				AcctPort:   1813,
				Timeout:    5,
				Retransmit: 3,
			},
			NetFlow: &NetFlowState{
				Enabled:      false,
				Port:         9995,
				Version:      "v9",
				SampleRate:   100,
				ActiveTime:   1800,
				InactiveTime: 15,
			},
			ICMP:       NewICMPProtocol(deviceID),
			ARP:        NewARPProtocol(deviceID),
			TCP:        NewTCPProtocol(deviceID),
			UDP:        NewUDPProtocol(deviceID),
			TLS:        NewTLSProtocol(deviceID),
			HTTP:       NewHTTPProtocol(deviceID),
			DNS:        NewDNSProtocol(deviceID),
			DHCP:       NewDHCPProtocol(deviceID),
			FTP:        NewFTPProtocol(deviceID),
			RIP:        NewRIPProtocol(deviceID),
			MPLS:       NewMPLSProtocol(deviceID),
			PPP:        NewPPPProtocol(deviceID),
			PPPoE:      NewPPPoEProtocol(deviceID),
			SMTP:       NewSMTPProtocol(deviceID),
			IPv6:       NewIPv6Protocol(deviceID),
			VXLANProto: NewVXLANProtocol(),
		}
	}
	return p.routers[deviceID]
}

// getRouter 在 RLock 下读取 routers map，返回 (指针, 是否存在)。
// 返回指针在 RUnlock 释放后仍有效（Go GC 不会回收被引用的对象），
// 调用方据此读取/修改 RouterState 字段是安全的；map 本身的读写则由 mu 保护。
func (p *ProtocolSimulator) getRouter(deviceID string) (*RouterState, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rs, ok := p.routers[deviceID]
	return rs, ok
}

func (p *ProtocolSimulator) AddRoute(deviceID, dest, mask, nextHop, iface, protocol string, metric int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).RoutingTable = append(p.InitRouter(deviceID).RoutingTable, RouteEntry{
		Destination: dest,
		Mask:        mask,
		NextHop:     nextHop,
		Interface:   iface,
		Protocol:    protocol,
		Metric:      metric,
	})
}

func (p *ProtocolSimulator) AddACL(deviceID, aclNum string, rules []*ACLRule) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).ACLs[aclNum] = rules
}

func (p *ProtocolSimulator) MatchACL(deviceID, aclNum, srcIP, dstIP, protocol string, dstPort int) bool {
	router, ok := p.getRouter(deviceID)
	if !ok {
		return true
	}
	rules, ok := router.ACLs[aclNum]
	if !ok {
		return true
	}

	for _, rule := range rules {
		if p.matchRule(rule, srcIP, dstIP, protocol, dstPort) {
			return rule.Action == "permit"
		}
	}
	return true
}

func (p *ProtocolSimulator) matchRule(rule *ACLRule, srcIP, dstIP, protocol string, dstPort int) bool {
	if rule.Protocol != "" && !strings.EqualFold(rule.Protocol, protocol) {
		return false
	}

	// 使用 firewall.go 的 ACLRule 字段名
	if rule.SourceIP != "" && !p.matchIP(srcIP, rule.SourceIP, rule.SourceMask) {
		return false
	}

	if rule.DestIP != "" && !p.matchIP(dstIP, rule.DestIP, rule.DestMask) {
		return false
	}

	if rule.DestPort != "" {
		portStr := fmt.Sprintf("%d", dstPort)
		if rule.DestPort != portStr {
			return false
		}
	}

	return true
}

func (p *ProtocolSimulator) matchIP(ip, ruleIP, wildcard string) bool {
	ipNet := parseIPWithWildcard(ruleIP, wildcard)
	ipAddr := net.ParseIP(ip)
	return ipNet.Contains(ipAddr)
}

func parseIPWithWildcard(ip, wildcard string) *net.IPNet {
	if wildcard == "" {
		wildcard = "255.255.255.255"
	}
	mask := wildcardToMask(wildcard)
	ipAddr := net.ParseIP(ip)
	return &net.IPNet{
		IP:   ipAddr,
		Mask: net.CIDRMask(mask, 32),
	}
}

func wildcardToMask(wildcard string) int {
	mask := 0
	parts := strings.Split(wildcard, ".")
	for _, part := range parts {
		// 显式解析并校验每个八位：非法通配符按 0 处理（与原始 Sscanf 静默语义一致），
		// 但不再忽略解析错误，避免把脏数据误当成合法掩码位参与匹配。
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			n = 0
		}
		for i := 0; i < 8; i++ {
			if (n & (1 << i)) == 0 {
				mask++
			}
		}
	}
	return mask
}

func (p *ProtocolSimulator) FindRoute(deviceID, destIP string) *RouteEntry {
	router, ok := p.getRouter(deviceID)
	if !ok {
		return nil
	}

	var bestRoute *RouteEntry
	bestMaskLen := 0

	for _, route := range router.RoutingTable {
		ipNet := parseIPWithWildcard(route.Destination, route.Mask)
		ipAddr := net.ParseIP(destIP)
		if ipNet.Contains(ipAddr) {
			maskLen, _ := ipNet.Mask.Size()
			// bestRoute==nil 时（首条命中路由为 /0 默认路由）跳过 Metric 比较，避免空指针解引用。
			if maskLen > bestMaskLen || (bestRoute != nil && maskLen == bestMaskLen && route.Metric < bestRoute.Metric) {
				bestRoute = &route
				bestMaskLen = maskLen
			}
		}
	}

	return bestRoute
}

// CheckReachability 基于拓扑链路图做无向 BFS，判断 srcDevice 与 dstDevice
// 是否在网络层可达（经交换机/集线器桥接视为同一广播域，可达）。
//
// 修复：此前为 `return true` 桩，任何调用都恒为真，会向诊断/可达性判定
// 返回错误结果（例如把本不连通的两台设备判为可达）。现改为真实图遍历，
// 并处理参数/边界：空/未知设备、同设备、空拓扑均返回确定结果。
func (p *ProtocolSimulator) CheckReachability(srcDevice, dstDevice, srcIP, dstIP string, topo *topology.Topology) bool {
	if topo == nil {
		return false
	}
	if srcDevice == "" || dstDevice == "" {
		return false
	}
	if _, ok := topo.GetDevice(srcDevice); !ok {
		return false
	}
	if _, ok := topo.GetDevice(dstDevice); !ok {
		return false
	}
	if srcDevice == dstDevice {
		return true
	}

	adj := make(map[string][]string)
	for _, l := range topo.GetLinks() {
		adj[l.SourceDevice] = append(adj[l.SourceDevice], l.TargetDevice)
		adj[l.TargetDevice] = append(adj[l.TargetDevice], l.SourceDevice)
	}

	visited := map[string]bool{srcDevice: true}
	queue := []string{srcDevice}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if nb == dstDevice {
				return true
			}
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return false
}

func (p *ProtocolSimulator) StartOSPF(deviceID string, processID, areaID int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).OSPF.Enabled = true
	p.InitRouter(deviceID).OSPF.ProcessID = processID
	p.InitRouter(deviceID).OSPF.AreaID = areaID
}

func (p *ProtocolSimulator) StopOSPF(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.OSPF.Enabled = false
	}
}

func (p *ProtocolSimulator) UpdateOSPFNeighbors(deviceID string, neighbors []string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.OSPF.Neighbors = neighbors
	}
}

func (p *ProtocolSimulator) StartMLAG(deviceID string, domainID, priority int, systemMAC, peerIP string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).MLAG.Enabled = true
	p.InitRouter(deviceID).MLAG.Domain = &MLAGDomain{
		DomainID:       domainID,
		SystemPriority: priority,
		SystemMAC:      systemMAC,
		PeerIP:         peerIP,
		DFSGroupID:     1,
		DFSMode:        "all-active",
		Interfaces:     make(map[string]*MLAGInterface),
		Status:         "peer-link-down",
	}
}

func (p *ProtocolSimulator) StopMLAG(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.MLAG.Enabled = false
		router.MLAG.Domain = nil
	}
}

func (p *ProtocolSimulator) AddMLAGInterface(deviceID, ifaceName string, groupID int, mode string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.MLAG.Domain != nil {
		router.MLAG.Domain.Interfaces[ifaceName] = &MLAGInterface{
			InterfaceName: ifaceName,
			GroupID:       groupID,
			Mode:          mode,
			Active:        false,
			Backup:        false,
		}
	}
}

func (p *ProtocolSimulator) SetMLAGPeerLink(deviceID, peerLink string) {
	if router, ok := p.getRouter(deviceID); ok && router.MLAG.Domain != nil {
		router.MLAG.Domain.PeerLink = peerLink
		router.MLAG.Domain.Status = "peer-link-up"
	}
}

func (p *ProtocolSimulator) CheckMLAGStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.MLAG.Enabled {
		if router.MLAG.Domain == nil {
			return "M-LAG: Enabled, Domain not configured"
		}
		var out strings.Builder
		out.WriteString(fmt.Sprintf("M-LAG Domain %d:\n", router.MLAG.Domain.DomainID))
		out.WriteString(fmt.Sprintf("  System Priority: %d\n", router.MLAG.Domain.SystemPriority))
		out.WriteString(fmt.Sprintf("  System MAC: %s\n", router.MLAG.Domain.SystemMAC))
		out.WriteString(fmt.Sprintf("  Peer IP: %s\n", router.MLAG.Domain.PeerIP))
		out.WriteString(fmt.Sprintf("  Peer Link: %s\n", router.MLAG.Domain.PeerLink))
		out.WriteString(fmt.Sprintf("  DFS Group: %d\n", router.MLAG.Domain.DFSGroupID))
		out.WriteString(fmt.Sprintf("  DFS Mode: %s\n", router.MLAG.Domain.DFSMode))
		out.WriteString(fmt.Sprintf("  Status: %s\n", router.MLAG.Domain.Status))
		out.WriteString("  M-LAG Interfaces:\n")
		for _, iface := range router.MLAG.Domain.Interfaces {
			status := "down"
			if iface.Active {
				status = "active"
			} else if iface.Backup {
				status = "backup"
			}
			out.WriteString(fmt.Sprintf("    %s (Group %d, Mode %s, Status %s)\n",
				iface.InterfaceName, iface.GroupID, iface.Mode, status))
		}
		return out.String()
	}
	return "M-LAG: Not configured"
}

func (p *ProtocolSimulator) SimulateMLAGFailover(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok && router.MLAG.Enabled && router.MLAG.Domain != nil {
		for _, iface := range router.MLAG.Domain.Interfaces {
			if iface.Active {
				iface.Active = false
				iface.Backup = true
			} else if iface.Backup {
				iface.Backup = false
				iface.Active = true
			}
		}
		router.MLAG.Domain.Status = "failover-completed"
	}
}

func (p *ProtocolSimulator) StartLLDP(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).LLDP.Enabled = true
}

func (p *ProtocolSimulator) StopLLDP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.LLDP.Enabled = false
	}
}

func (p *ProtocolSimulator) AddLLDPNeighbor(deviceID, portName string, neighbor LLDPNeighbor) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).LLDP.Neighbors[portName] = append(p.InitRouter(deviceID).LLDP.Neighbors[portName], neighbor)
}

func (p *ProtocolSimulator) GetLLDPNeighbors(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.LLDP.Enabled {
		var out strings.Builder
		out.WriteString("LLDP Neighbors:\n")
		for port, neighbors := range router.LLDP.Neighbors {
			out.WriteString(fmt.Sprintf("  Port %s:\n", port))
			for _, n := range neighbors {
				out.WriteString(fmt.Sprintf("    System Name: %s\n", n.SystemName))
				out.WriteString(fmt.Sprintf("    Chassis ID: %s\n", n.ChassisID))
				out.WriteString(fmt.Sprintf("    Port ID: %s\n", n.PortID))
				out.WriteString(fmt.Sprintf("    Management IP: %s\n", n.ManagementIP))
				out.WriteString(fmt.Sprintf("    System Description: %s\n", n.SystemDesc))
			}
		}
		return out.String()
	}
	return "LLDP: Not enabled"
}

func (p *ProtocolSimulator) StartSTP(deviceID string, mode string) {
	router := p.InitRouter(deviceID)
	router.STP.Enabled = true
	router.STP.Mode = mode
	// deviceID 可能短于 12 字符，先做安全切片避免越界 panic。
	shortID := deviceID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	router.STP.RootBridgeID = fmt.Sprintf("%d.%s", router.STP.BridgePriority, shortID)
	router.STP.DesignatedRoot = router.STP.RootBridgeID
	router.STP.RootCost = 0
}

func (p *ProtocolSimulator) StopSTP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.STP.Enabled = false
	}
}

func (p *ProtocolSimulator) ConfigureSTPPort(deviceID, portName string, priority, cost int) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.STP.Enabled {
		router.STP.Ports[portName] = &STPPort{
			PortName:     portName,
			PortPriority: priority,
			Cost:         cost,
			State:        "forwarding",
			Role:         "designated",
		}
	}
}

func (p *ProtocolSimulator) GetSTPStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.STP.Enabled {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("STP Mode: %s\n", router.STP.Mode))
		out.WriteString(fmt.Sprintf("Bridge Priority: %d\n", router.STP.BridgePriority))
		out.WriteString(fmt.Sprintf("Root Bridge ID: %s\n", router.STP.RootBridgeID))
		out.WriteString(fmt.Sprintf("Designated Root: %s\n", router.STP.DesignatedRoot))
		out.WriteString(fmt.Sprintf("Root Cost: %d\n", router.STP.RootCost))
		out.WriteString("STP Ports:\n")
		for _, port := range router.STP.Ports {
			out.WriteString(fmt.Sprintf("  %s: Role=%s, State=%s, Priority=%d, Cost=%d\n",
				port.PortName, port.Role, port.State, port.PortPriority, port.Cost))
		}
		return out.String()
	}
	return "STP: Not enabled"
}

func (p *ProtocolSimulator) SimulateSTPConvergence(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok && router.STP.Enabled {
		for _, port := range router.STP.Ports {
			if port.State == "forwarding" && port.Role == "designated" {
				port.State = "forwarding"
			}
		}
	}
}

func (p *ProtocolSimulator) StartVRRP(deviceID string, groupID int, virtualIP string, priority int, preempt bool, delay int) {
	p.InitRouter(deviceID)
	virtualMAC := fmt.Sprintf("00-00-5E-00-01-%02X", groupID)
	p.InitRouter(deviceID).VRRP.Enabled = true
	p.InitRouter(deviceID).VRRP.Groups[groupID] = &VRRPGroup{
		GroupID:    groupID,
		VirtualIP:  virtualIP,
		VirtualMAC: virtualMAC,
		Priority:   priority,
		Master:     priority >= 100,
		Preempt:    preempt,
		Delay:      delay,
		Status:     "master",
	}
}

func (p *ProtocolSimulator) StopVRRP(deviceID string, groupID int) {
	if router, ok := p.getRouter(deviceID); ok {
		delete(router.VRRP.Groups, groupID)
		if len(router.VRRP.Groups) == 0 {
			router.VRRP.Enabled = false
		}
	}
}

func (p *ProtocolSimulator) GetVRRPStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.VRRP.Enabled {
		var out strings.Builder
		out.WriteString("VRRP Groups:\n")
		for _, group := range router.VRRP.Groups {
			out.WriteString(fmt.Sprintf("  Group %d:\n", group.GroupID))
			out.WriteString(fmt.Sprintf("    Virtual IP: %s\n", group.VirtualIP))
			out.WriteString(fmt.Sprintf("    Virtual MAC: %s\n", group.VirtualMAC))
			out.WriteString(fmt.Sprintf("    Priority: %d\n", group.Priority))
			out.WriteString(fmt.Sprintf("    Role: %s\n", func() string {
				if group.Master {
					return "Master"
				}
				return "Backup"
			}()))
			out.WriteString(fmt.Sprintf("    Preempt: %s\n", func() string {
				if group.Preempt {
					return "Enabled"
				}
				return "Disabled"
			}()))
			out.WriteString(fmt.Sprintf("    Delay: %ds\n", group.Delay))
			out.WriteString(fmt.Sprintf("    Status: %s\n", group.Status))
		}
		return out.String()
	}
	return "VRRP: Not enabled"
}

func (p *ProtocolSimulator) SimulateVRRPFailover(deviceID string, groupID int) {
	if router, ok := p.getRouter(deviceID); ok && router.VRRP.Enabled {
		if group, ok := router.VRRP.Groups[groupID]; ok {
			group.Master = !group.Master
			if group.Master {
				group.Status = "master"
			} else {
				group.Status = "backup"
			}
		}
	}
}

func (p *ProtocolSimulator) AddIPsecTunnel(deviceID, tunnelID, localIP, remoteIP, mode, encryption, auth string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).IPsec.Enabled = true
	p.InitRouter(deviceID).IPsec.Tunnels[tunnelID] = &IPsecTunnel{
		TunnelID:       tunnelID,
		LocalIP:        localIP,
		RemoteIP:       remoteIP,
		Mode:           mode,
		Encryption:     encryption,
		Authentication: auth,
		Status:         "up",
	}
}

func (p *ProtocolSimulator) RemoveIPsecTunnel(deviceID, tunnelID string) {
	if router, ok := p.getRouter(deviceID); ok {
		delete(router.IPsec.Tunnels, tunnelID)
		if len(router.IPsec.Tunnels) == 0 {
			router.IPsec.Enabled = false
		}
	}
}

func (p *ProtocolSimulator) GetIPsecStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.IPsec.Enabled {
		var out strings.Builder
		out.WriteString("IPsec Tunnels:\n")
		for _, tunnel := range router.IPsec.Tunnels {
			out.WriteString(fmt.Sprintf("  Tunnel %s:\n", tunnel.TunnelID))
			out.WriteString(fmt.Sprintf("    Local IP: %s\n", tunnel.LocalIP))
			out.WriteString(fmt.Sprintf("    Remote IP: %s\n", tunnel.RemoteIP))
			out.WriteString(fmt.Sprintf("    Mode: %s\n", tunnel.Mode))
			out.WriteString(fmt.Sprintf("    Encryption: %s\n", tunnel.Encryption))
			out.WriteString(fmt.Sprintf("    Authentication: %s\n", tunnel.Authentication))
			out.WriteString(fmt.Sprintf("    Status: %s\n", tunnel.Status))
		}
		return out.String()
	}
	return "IPsec: Not enabled"
}

func (p *ProtocolSimulator) ConfigureSNMP(deviceID, version, community, managerIP string, trapEnable bool, trapServer string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).SNMP.Enabled = true
	p.InitRouter(deviceID).SNMP.Version = version
	p.InitRouter(deviceID).SNMP.Community = community
	p.InitRouter(deviceID).SNMP.ManagerIP = managerIP
	p.InitRouter(deviceID).SNMP.TrapEnable = trapEnable
	p.InitRouter(deviceID).SNMP.TrapServer = trapServer
}

func (p *ProtocolSimulator) DisableSNMP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.SNMP.Enabled = false
	}
}

func (p *ProtocolSimulator) GetSNMPConfig(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("SNMP: %s\n", func() string {
			if router.SNMP.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.SNMP.Enabled {
			out.WriteString(fmt.Sprintf("  Version: %s\n", router.SNMP.Version))
			out.WriteString(fmt.Sprintf("  Community: %s\n", router.SNMP.Community))
			out.WriteString(fmt.Sprintf("  Manager IP: %s\n", router.SNMP.ManagerIP))
			out.WriteString(fmt.Sprintf("  Trap: %s\n", func() string {
				if router.SNMP.TrapEnable {
					return "Enabled"
				}
				return "Disabled"
			}()))
			out.WriteString(fmt.Sprintf("  Trap Server: %s\n", router.SNMP.TrapServer))
		}
		return out.String()
	}
	return "SNMP: Not configured"
}

func (p *ProtocolSimulator) ConfigureSyslog(deviceID, serverIP string, serverPort int, severity, facility string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).Syslog.Enabled = true
	p.InitRouter(deviceID).Syslog.ServerIP = serverIP
	p.InitRouter(deviceID).Syslog.ServerPort = serverPort
	p.InitRouter(deviceID).Syslog.Severity = severity
	p.InitRouter(deviceID).Syslog.Facility = facility
}

func (p *ProtocolSimulator) DisableSyslog(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.Syslog.Enabled = false
	}
}

func (p *ProtocolSimulator) GetSyslogConfig(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("Syslog: %s\n", func() string {
			if router.Syslog.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.Syslog.Enabled {
			out.WriteString(fmt.Sprintf("  Server: %s:%d\n", router.Syslog.ServerIP, router.Syslog.ServerPort))
			out.WriteString(fmt.Sprintf("  Severity: %s\n", router.Syslog.Severity))
			out.WriteString(fmt.Sprintf("  Facility: %s\n", router.Syslog.Facility))
		}
		return out.String()
	}
	return "Syslog: Not configured"
}

func (p *ProtocolSimulator) ConfigureNTP(deviceID, serverIP string, serverPort int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).NTP.Enabled = true
	p.InitRouter(deviceID).NTP.ServerIP = serverIP
	p.InitRouter(deviceID).NTP.ServerPort = serverPort
	p.InitRouter(deviceID).NTP.SyncStatus = "synchronized"
	if serverIP != "" {
		p.InitRouter(deviceID).NTP.Stratum = 4
	}
}

func (p *ProtocolSimulator) DisableNTP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.NTP.Enabled = false
		router.NTP.SyncStatus = "not synchronized"
		router.NTP.Stratum = 15
	}
}

func (p *ProtocolSimulator) GetNTPStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("NTP: %s\n", func() string {
			if router.NTP.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.NTP.Enabled {
			out.WriteString(fmt.Sprintf("  Server: %s:%d\n", router.NTP.ServerIP, router.NTP.ServerPort))
			out.WriteString(fmt.Sprintf("  Stratum: %d\n", router.NTP.Stratum))
			out.WriteString(fmt.Sprintf("  Sync Status: %s\n", router.NTP.SyncStatus))
		}
		return out.String()
	}
	return "NTP: Not configured"
}

func (p *ProtocolSimulator) ConfigureSSH(deviceID, version, auth string, port, maxSessions int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).SSH.Enabled = true
	p.InitRouter(deviceID).SSH.Version = version
	p.InitRouter(deviceID).SSH.Authentication = auth
	p.InitRouter(deviceID).SSH.Port = port
	p.InitRouter(deviceID).SSH.MaxSessions = maxSessions
}

func (p *ProtocolSimulator) DisableSSH(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.SSH.Enabled = false
	}
}

func (p *ProtocolSimulator) GetSSHConfig(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("SSH: %s\n", func() string {
			if router.SSH.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.SSH.Enabled {
			out.WriteString(fmt.Sprintf("  Version: %s\n", router.SSH.Version))
			out.WriteString(fmt.Sprintf("  Port: %d\n", router.SSH.Port))
			out.WriteString(fmt.Sprintf("  Authentication: %s\n", router.SSH.Authentication))
			out.WriteString(fmt.Sprintf("  Max Sessions: %d\n", router.SSH.MaxSessions))
		}
		return out.String()
	}
	return "SSH: Not configured"
}

func (p *ProtocolSimulator) ConfigureVXLAN(deviceID string, vni int, vtepIP, peerVTEPIP, vrfName string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).VXLAN.Enabled = true
	p.InitRouter(deviceID).VXLAN.VNI = vni
	p.InitRouter(deviceID).VXLAN.VTEPIP = vtepIP
	p.InitRouter(deviceID).VXLAN.PeerVTEPIP = peerVTEPIP
	p.InitRouter(deviceID).VXLAN.VRFName = vrfName
	p.InitRouter(deviceID).VXLAN.Status = "up"
}

func (p *ProtocolSimulator) DisableVXLAN(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.VXLAN.Enabled = false
		router.VXLAN.Status = "down"
	}
}

func (p *ProtocolSimulator) GetVXLANStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.VXLAN.Enabled {
		var out strings.Builder
		out.WriteString("VXLAN Configuration:\n")
		out.WriteString(fmt.Sprintf("  VNI: %d\n", router.VXLAN.VNI))
		out.WriteString(fmt.Sprintf("  Local VTEP IP: %s\n", router.VXLAN.VTEPIP))
		out.WriteString(fmt.Sprintf("  Peer VTEP IP: %s\n", router.VXLAN.PeerVTEPIP))
		out.WriteString(fmt.Sprintf("  VRF: %s\n", router.VXLAN.VRFName))
		out.WriteString(fmt.Sprintf("  Status: %s\n", router.VXLAN.Status))
		return out.String()
	}
	return "VXLAN: Not enabled"
}

func (p *ProtocolSimulator) ConfigureBGP(deviceID string, asNumber int, routerID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).BGP.Enabled = true
	p.InitRouter(deviceID).BGP.ASNumber = asNumber
	p.InitRouter(deviceID).BGP.RouterID = routerID
}

func (p *ProtocolSimulator) DisableBGP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.BGP.Enabled = false
	}
}

func (p *ProtocolSimulator) AddBGPNeighbor(deviceID, neighborIP string, remoteAS int, ebgp bool) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.BGP.Enabled {
		router.BGP.Neighbors[neighborIP] = &BGPNeighbor{
			IP:            net.ParseIP(neighborIP),
			ASN:           router.BGP.ASNumber,
			State:         "Established",
			RemoteASN:     remoteAS,
			HoldTime:      180 * 1000000000, // 180 秒转换为纳秒
			KeepAliveTime: 60 * 1000000000,  // 60 秒转换为纳秒
			LastUpdate:    time.Now(),
			Prefixes:      []string{},
		}
	}
}

func (p *ProtocolSimulator) RemoveBGPNeighbor(deviceID, neighborIP string) {
	if router, ok := p.getRouter(deviceID); ok && router.BGP.Enabled {
		delete(router.BGP.Neighbors, neighborIP)
	}
}

func (p *ProtocolSimulator) GetBGPStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.BGP.Enabled {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("BGP Configuration:\n"))
		out.WriteString(fmt.Sprintf("  AS Number: %d\n", router.BGP.ASNumber))
		out.WriteString(fmt.Sprintf("  Router ID: %s\n", router.BGP.RouterID))
		out.WriteString("  Neighbors:\n")
		for _, neighbor := range router.BGP.Neighbors {
			// 使用 bgp.go 的 BGPNeighbor 字段
			out.WriteString(fmt.Sprintf("    %s:\n", neighbor.IP.String()))
			out.WriteString(fmt.Sprintf("      Remote AS: %d\n", neighbor.RemoteASN))
			out.WriteString(fmt.Sprintf("      Type: %s\n", func() string {
				if neighbor.RemoteASN != router.BGP.ASNumber {
					return "EBGP"
				}
				return "IBGP"
			}()))
			out.WriteString(fmt.Sprintf("      State: %s\n", neighbor.State))
			out.WriteString(fmt.Sprintf("      Up Time: %s\n", func() string {
				uptime := time.Since(neighbor.LastUpdate)
				hours := int(uptime.Hours())
				minutes := int(uptime.Minutes()) % 60
				seconds := int(uptime.Seconds()) % 60
				return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
			}()))
			out.WriteString(fmt.Sprintf("      Received Routes: %d\n", len(neighbor.Prefixes)))
			out.WriteString(fmt.Sprintf("      Advertised Routes: %d\n", len(neighbor.Prefixes)))
		}
		return out.String()
	}
	return "BGP: Not enabled"
}

func (p *ProtocolSimulator) EnableBFD(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).BFD.Enabled = true
}

func (p *ProtocolSimulator) DisableBFD(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.BFD.Enabled = false
	}
}

func (p *ProtocolSimulator) AddBFDSession(deviceID, peerIP, localIP string, minTx, minRx, detectMult int) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.BFD.Enabled {
		router.BFD.Sessions[peerIP] = &BFDSession{
			PeerIP:        peerIP,
			LocalIP:       localIP,
			State:         "up",
			MinTxInterval: minTx,
			MinRxInterval: minRx,
			DetectMult:    detectMult,
			DetectTime:    minRx * detectMult,
			UpTime:        "00:00:00",
		}
	}
}

func (p *ProtocolSimulator) GetBFDStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.BFD.Enabled {
		var out strings.Builder
		out.WriteString("BFD Sessions:\n")
		for _, session := range router.BFD.Sessions {
			out.WriteString(fmt.Sprintf("  Local: %s, Peer: %s\n", session.LocalIP, session.PeerIP))
			out.WriteString(fmt.Sprintf("    State: %s\n", session.State))
			out.WriteString(fmt.Sprintf("    Min Tx/Rx: %d/%d ms\n", session.MinTxInterval, session.MinRxInterval))
			out.WriteString(fmt.Sprintf("    Detect Mult: %d\n", session.DetectMult))
			out.WriteString(fmt.Sprintf("    Detect Time: %d ms\n", session.DetectTime))
			out.WriteString(fmt.Sprintf("    Up Time: %s\n", session.UpTime))
		}
		return out.String()
	}
	return "BFD: Not enabled"
}

func (p *ProtocolSimulator) CreateVRF(deviceID, name, rd string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok {
		router.VRF.Enabled = true
		router.VRF.VRFs[name] = &VRF{
			Name:         name,
			RD:           rd,
			RouteTargets: []string{},
			Interfaces:   []string{},
			RoutingTable: []RouteEntry{},
		}
	}
}

func (p *ProtocolSimulator) AddVRFRouteTarget(deviceID, vrfName, rt string) {
	if router, ok := p.getRouter(deviceID); ok {
		if vrf, ok := router.VRF.VRFs[vrfName]; ok {
			vrf.RouteTargets = append(vrf.RouteTargets, rt)
		}
	}
}

func (p *ProtocolSimulator) BindVRFToInterface(deviceID, vrfName, iface string) {
	if router, ok := p.getRouter(deviceID); ok {
		if vrf, ok := router.VRF.VRFs[vrfName]; ok {
			vrf.Interfaces = append(vrf.Interfaces, iface)
		}
	}
}

func (p *ProtocolSimulator) GetVRFStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.VRF.Enabled {
		var out strings.Builder
		out.WriteString("VRF Instances:\n")
		for name, vrf := range router.VRF.VRFs {
			out.WriteString(fmt.Sprintf("  %s:\n", name))
			out.WriteString(fmt.Sprintf("    RD: %s\n", vrf.RD))
			out.WriteString(fmt.Sprintf("    Route Targets: %v\n", vrf.RouteTargets))
			out.WriteString(fmt.Sprintf("    Interfaces: %v\n", vrf.Interfaces))
			out.WriteString(fmt.Sprintf("    Routes: %d\n", len(vrf.RoutingTable)))
		}
		return out.String()
	}
	return "VRF: Not configured"
}

func (p *ProtocolSimulator) EnablePBR(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).PBR.Enabled = true
}

func (p *ProtocolSimulator) DisablePBR(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.PBR.Enabled = false
	}
}

func (p *ProtocolSimulator) AddPBRRule(deviceID, policyName string, ruleID int, matchACL, nextHop, iface string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.PBR.Enabled {
		if _, ok := router.PBR.Rules[policyName]; !ok {
			router.PBR.Rules[policyName] = []*PBRRule{}
		}
		router.PBR.Rules[policyName] = append(router.PBR.Rules[policyName], &PBRRule{
			ID:        ruleID,
			MatchACL:  matchACL,
			NextHop:   nextHop,
			Interface: iface,
		})
	}
}

func (p *ProtocolSimulator) GetPBRStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.PBR.Enabled {
		var out strings.Builder
		out.WriteString("Policy-Based Routing:\n")
		for name, rules := range router.PBR.Rules {
			out.WriteString(fmt.Sprintf("  Policy: %s\n", name))
			for _, rule := range rules {
				out.WriteString(fmt.Sprintf("    Rule %d: match acl %s -> next-hop %s interface %s\n",
					rule.ID, rule.MatchACL, rule.NextHop, rule.Interface))
			}
		}
		return out.String()
	}
	return "PBR: Not enabled"
}

func (p *ProtocolSimulator) EnableGRE(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).GRE.Enabled = true
}

func (p *ProtocolSimulator) DisableGRE(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.GRE.Enabled = false
	}
}

func (p *ProtocolSimulator) AddGRETunnel(deviceID, tunnelName, srcIP, destIP string, key int, keepalive bool) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.GRE.Enabled {
		router.GRE.Tunnels[tunnelName] = &GRETunnel{
			Name:      tunnelName,
			SourceIP:  srcIP,
			DestIP:    destIP,
			Key:       key,
			Keepalive: keepalive,
			Status:    "up",
		}
	}
}

func (p *ProtocolSimulator) GetGREStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.GRE.Enabled {
		var out strings.Builder
		out.WriteString("GRE Tunnels:\n")
		for name, tunnel := range router.GRE.Tunnels {
			out.WriteString(fmt.Sprintf("  %s:\n", name))
			out.WriteString(fmt.Sprintf("    Source: %s\n", tunnel.SourceIP))
			out.WriteString(fmt.Sprintf("    Destination: %s\n", tunnel.DestIP))
			out.WriteString(fmt.Sprintf("    Key: %d\n", tunnel.Key))
			out.WriteString(fmt.Sprintf("    Keepalive: %t\n", tunnel.Keepalive))
			out.WriteString(fmt.Sprintf("    Status: %s\n", tunnel.Status))
		}
		return out.String()
	}
	return "GRE: Not enabled"
}

func (p *ProtocolSimulator) EnableQoS(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).QoS.Enabled = true
}

func (p *ProtocolSimulator) DisableQoS(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.QoS.Enabled = false
	}
}

func (p *ProtocolSimulator) AddQoSClassifier(deviceID, name, acl string, dscp int) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.QoS.Enabled {
		router.QoS.Classifiers[name] = &QoSClassifier{
			Name:     name,
			ACL:      acl,
			DSCP:     dscp,
			Protocol: "",
			SrcPort:  "",
			DstPort:  "",
		}
	}
}

func (p *ProtocolSimulator) AddQoSBehavior(deviceID, name string, bandwidth, priority int, queue, action string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.QoS.Enabled {
		router.QoS.Behaviors[name] = &QoSBehavior{
			Name:      name,
			Bandwidth: bandwidth,
			Priority:  priority,
			Queue:     queue,
			Action:    action,
		}
	}
}

func (p *ProtocolSimulator) AddQoSPolicy(deviceID, name, classifier, behavior string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.QoS.Enabled {
		router.QoS.Policies[name] = &QoSPolicy{
			Name:       name,
			Classifier: classifier,
			Behavior:   behavior,
		}
	}
}

func (p *ProtocolSimulator) GetQoSStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.QoS.Enabled {
		var out strings.Builder
		out.WriteString("QoS Configuration:\n")
		out.WriteString("  Classifiers:\n")
		for name, classifier := range router.QoS.Classifiers {
			out.WriteString(fmt.Sprintf("    %s: acl %s, dscp %d\n", name, classifier.ACL, classifier.DSCP))
		}
		out.WriteString("  Behaviors:\n")
		for name, behavior := range router.QoS.Behaviors {
			out.WriteString(fmt.Sprintf("    %s: bandwidth %d kbps, priority %d, queue %s, action %s\n",
				name, behavior.Bandwidth, behavior.Priority, behavior.Queue, behavior.Action))
		}
		out.WriteString("  Policies:\n")
		for name, policy := range router.QoS.Policies {
			out.WriteString(fmt.Sprintf("    %s: classifier %s -> behavior %s\n",
				name, policy.Classifier, policy.Behavior))
		}
		return out.String()
	}
	return "QoS: Not enabled"
}

func (p *ProtocolSimulator) EnableDot1x(deviceID string) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).Dot1x.Enabled = true
}

func (p *ProtocolSimulator) DisableDot1x(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.Dot1x.Enabled = false
	}
}

func (p *ProtocolSimulator) ConfigureDot1xPort(deviceID, portName, authMethod string, reauth bool, quietTimer int) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.Dot1x.Enabled {
		router.Dot1x.Ports[portName] = &Dot1xPort{
			PortName:   portName,
			Enabled:    true,
			AuthMethod: authMethod,
			Reauth:     reauth,
			QuietTimer: quietTimer,
		}
	}
}

func (p *ProtocolSimulator) GetDot1xStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok && router.Dot1x.Enabled {
		var out strings.Builder
		out.WriteString("802.1X Configuration:\n")
		for name, port := range router.Dot1x.Ports {
			out.WriteString(fmt.Sprintf("  Port %s:\n", name))
			out.WriteString(fmt.Sprintf("    Enabled: %t\n", port.Enabled))
			out.WriteString(fmt.Sprintf("    Auth Method: %s\n", port.AuthMethod))
			out.WriteString(fmt.Sprintf("    Reauthentication: %t\n", port.Reauth))
			out.WriteString(fmt.Sprintf("    Quiet Timer: %d\n", port.QuietTimer))
		}
		return out.String()
	}
	return "802.1X: Not enabled"
}

func (p *ProtocolSimulator) ConfigureRADIUS(deviceID, primary, secondary, secret string, authPort, acctPort, timeout, retransmit int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).RADIUS.Enabled = true
	p.InitRouter(deviceID).RADIUS.PrimaryServer = primary
	p.InitRouter(deviceID).RADIUS.SecondaryServer = secondary
	p.InitRouter(deviceID).RADIUS.SharedSecret = secret
	p.InitRouter(deviceID).RADIUS.AuthPort = authPort
	p.InitRouter(deviceID).RADIUS.AcctPort = acctPort
	p.InitRouter(deviceID).RADIUS.Timeout = timeout
	p.InitRouter(deviceID).RADIUS.Retransmit = retransmit
}

func (p *ProtocolSimulator) DisableRADIUS(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.RADIUS.Enabled = false
	}
}

func (p *ProtocolSimulator) GetRADIUSStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("RADIUS: %s\n", func() string {
			if router.RADIUS.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.RADIUS.Enabled {
			out.WriteString(fmt.Sprintf("  Primary Server: %s:%d\n", router.RADIUS.PrimaryServer, router.RADIUS.AuthPort))
			out.WriteString(fmt.Sprintf("  Secondary Server: %s:%d\n", router.RADIUS.SecondaryServer, router.RADIUS.AuthPort))
			out.WriteString(fmt.Sprintf("  Accounting Port: %d\n", router.RADIUS.AcctPort))
			out.WriteString(fmt.Sprintf("  Timeout: %d\n", router.RADIUS.Timeout))
			out.WriteString(fmt.Sprintf("  Retransmit: %d\n", router.RADIUS.Retransmit))
		}
		return out.String()
	}
	return "RADIUS: Not configured"
}

func (p *ProtocolSimulator) ConfigureNetFlow(deviceID, exporter string, port int, version string, sampleRate, activeTime, inactiveTime int) {
	p.InitRouter(deviceID)
	p.InitRouter(deviceID).NetFlow.Enabled = true
	p.InitRouter(deviceID).NetFlow.Exporter = exporter
	p.InitRouter(deviceID).NetFlow.Port = port
	p.InitRouter(deviceID).NetFlow.Version = version
	p.InitRouter(deviceID).NetFlow.SampleRate = sampleRate
	p.InitRouter(deviceID).NetFlow.ActiveTime = activeTime
	p.InitRouter(deviceID).NetFlow.InactiveTime = inactiveTime
}

func (p *ProtocolSimulator) DisableNetFlow(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok {
		router.NetFlow.Enabled = false
	}
}

func (p *ProtocolSimulator) GetNetFlowStatus(deviceID string) string {
	if router, ok := p.getRouter(deviceID); ok {
		var out strings.Builder
		out.WriteString(fmt.Sprintf("NetFlow: %s\n", func() string {
			if router.NetFlow.Enabled {
				return "Enabled"
			}
			return "Disabled"
		}()))
		if router.NetFlow.Enabled {
			out.WriteString(fmt.Sprintf("  Exporter: %s:%d\n", router.NetFlow.Exporter, router.NetFlow.Port))
			out.WriteString(fmt.Sprintf("  Version: %s\n", router.NetFlow.Version))
			out.WriteString(fmt.Sprintf("  Sample Rate: %d\n", router.NetFlow.SampleRate))
			out.WriteString(fmt.Sprintf("  Active Timeout: %d sec\n", router.NetFlow.ActiveTime))
			out.WriteString(fmt.Sprintf("  Inactive Timeout: %d sec\n", router.NetFlow.InactiveTime))
		}
		return out.String()
	}
	return "NetFlow: Not configured"
}

func (p *ProtocolSimulator) Ping(deviceID, targetIP string, timeout time.Duration, count, size int, checker ReachabilityChecker) []ICMPResult {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.ICMP != nil {
		return router.ICMP.Ping(targetIP, timeout, count, size, checker)
	}
	return []ICMPResult{}
}

func (p *ProtocolSimulator) GetICMPStats(deviceID string) ICMPStats {
	if router, ok := p.getRouter(deviceID); ok && router.ICMP != nil {
		return router.ICMP.GetStats()
	}
	return ICMPStats{}
}

func (p *ProtocolSimulator) ResetICMPStats(deviceID string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.ICMP != nil {
		router.ICMP.ResetStats()
	}
}

func (p *ProtocolSimulator) EnableICMP(deviceID string) {
	p.InitRouter(deviceID)
	if router, ok := p.getRouter(deviceID); ok && router.ICMP != nil {
		router.ICMP.Enable()
	}
}

func (p *ProtocolSimulator) DisableICMP(deviceID string) {
	if router, ok := p.getRouter(deviceID); ok && router.ICMP != nil {
		router.ICMP.Disable()
	}
}

func (p *ProtocolSimulator) GetRouter(deviceID string) (*RouterState, bool) {
	return p.getRouter(deviceID)
}

func (p *ProtocolSimulator) RemoveRouter(deviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routers, deviceID)
}
