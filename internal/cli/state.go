// state.go 集中定义 CLIState 及所有相关协议/特性的配置类型与构造函数。
//
// 任何「协议配置」「会话视图」「命令行解析」相关的 struct 都收在这里。
// 真正的命令解析/分发逻辑见 parser.go，纯工具函数见 tools.go。
package cli

import (
	"fmt"
	"strings"
	"time"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// ViewType 表示 CLI 当前所在的视图层级。
type ViewType string

const (
	ViewUser      ViewType = "user"
	ViewSystem    ViewType = "system"
	ViewInterface ViewType = "interface"
	ViewACL       ViewType = "acl"
	ViewMLAG      ViewType = "mlag"
	ViewBGP       ViewType = "bgp"
	ViewVTY       ViewType = "vty"
	ViewDHCPPool  ViewType = "dhcp-pool"
	ViewISIS      ViewType = "isis"       // IS-IS 协议视图（P1-F）
	ViewMSTRegion ViewType = "mst-region" // MSTP 域配置视图（P2 第四项 STP）

	// —— AAA 视图三档（P2 第八项 AAA 本地认证，课程 71，设计 T1 / P0-1）——
	//
	// 真机 VRP 层级：[R1] → aaa → [R1-aaa] → authentication-scheme sch1
	// → [R1-aaa-authen-sch1]；域同理 domain huawei → [R1-aaa-domain-huawei]。
	//
	// 🔴 quit 链红线（设计 A3 / AC1③）：子视图 quit 必须回 ViewAAA（**不是** ViewSystem），
	// ViewAAA quit 才回 ViewSystem。parser.go 的 quit if-else 链末尾 else 会兜底弹回
	// ViewSystem，因此这三档**必须**在链中显式列出分支，否则子视图会越级弹回。
	ViewAAA       ViewType = "aaa"        // AAA 视图 [<dev>-aaa]
	ViewAAAAuthen ViewType = "aaa-authen" // AAA 方案子视图 [<dev>-aaa-authen-<name>] 等
	ViewAAADomain ViewType = "aaa-domain" // AAA 域子视图 [<dev>-aaa-domain-<name>]

	// —— 网闸（GAP）视图三档（安全隔离网闸：内外网物理隔离 + 协议摆渡）——
	ViewGAP        ViewType = "gap"         // 网闸配置视图 [<dev>-gap]
	ViewGAPChannel ViewType = "gap-channel" // 摆渡通道子视图 [<dev>-gap-channel-<n>]
	ViewGAPPolicy  ViewType = "gap-policy"  // 摆渡策略子视图 [<dev>-gap-policy-<n>]

	// —— 路由策略（route-policy）节点子视图（P0-2 路由策略补齐）——
	// 进入命令：系统视图 route-policy <NAME> permit|deny node <N>
	// 视图内：if-match / apply 子句；配置以 DeviceConfig 单一事实源持久化。
	ViewRoutePolicy ViewType = "route-policy" // 路由策略节点视图 [<dev>-route-policy-<NAME>-<N>]
)

// Command 表示一条已解析的 CLI 命令。
type Command struct {
	Raw     string
	Command string
	Args    []string
}

// CLIState 保存一台网络设备的全部运行状态（含协议配置、接口、路由、转发表等）。
type CLIState struct {
	CurrentView    ViewType
	CurrentSub     string
	// route-policy 子视图上下文指针（仅视图层，配置本身以 DeviceConfig 单一事实源持久化）。
	// 用独立字段而非 CurrentSub 拼接，避免策略名含连字符时解析错位。
	RoutePolicyName string
	RoutePolicyNode int
	DeviceType     topology.DeviceType
	DeviceName     string             // 设备名称
	DeviceID       string             // 设备ID
	Topology       *topology.Topology // 拓扑引用（dis vxlan tunnel 等需读取拓扑链路时由 api 层注入；可为 nil）
	DeviceConfig   map[string]string  // 设备配置键值对
	DefaultGateway string
	HostIP         string // PC 主机 IP 地址
	HostSubnet     string // PC 主机子网掩码
	HostDNS        string // PC 主机 DNS 服务器
	ACLs           map[string][]*ACLRule
	Routes         []*RouteEntry
	OSPF           *OSPFConfig
	MLAG           *MLAGConfig
	MLAGInterfaces map[string]map[string]string
	LLDP           *LLDPConfig
	IPRouting      bool // 三层路由功能启用标志
	IPsec          map[string]*IPsecConfig
	SNMP           *SNMPConfig
	Syslog         *SyslogConfig
	NTP            *NTPConfig
	SSH            *SSHConfig
	VTY            *VTYConfig
	// 注：AAA 本地用户 / 认证方案 / 域配置已迁移至 DeviceConfig 的 "aaa:" 命名空间，
	// 单一事实源为 DeviceConfig["aaa:local-user:<name>:*"] / ["aaa:authen-scheme:<name>:mode"]
	// / ["aaa:domain:<name>:*"]（P2 第八项 AAA，save/reload 自动往返）。
	// ⚠️ 架构铁律：本结构体严禁新增任何 AAA / 本地用户 / 域 / 方案 的内嵌结构体或字段
	// （设计 §7.1 / P0-2）。旧的本地用户结构体字段与类型已彻底删除，其名字禁止复用；
	// 相关只读派生视图一律定义在 aaa_eval.go，并且只从 DeviceConfig 即时派生、不缓存。
	VXLAN *VXLANConfig
	BGP   *BGPConfig
	ISIS  *ISISConfig // IS-IS 配置（P1-F，最小启用 + 真实 network/import-route）
	BFD   *BFDConfig
	VRF   map[string]*VRFConfig
	PBR   map[string][]*PBRRule
	// 注：GRE 隧道配置已迁移至接口视图，单一事实源为
	// DeviceConfig["interface:<if>:tunnel-protocol"] 与 "interface:<if>:gre-*"（P2 GRE，save/reload 自动往返）。
	// ⚠️ 架构铁律：本结构体严禁新增任何 GRE / Tunnel 内嵌结构体或字段（设计 §3.1 / AC12）。
	QoS        *QoSConfig
	Dot1x      *Dot1xConfig
	RADIUS     *RADIUSConfig
	NetFlow    *NetFlowConfig
	ARPTable   []*ARPEntry
	NATTable   []*NATEntry
	NAT        *NATConfig
	VLANs      map[int]*VLANConfig
	MACTable   []*MACEntry
	Interfaces map[string]*InterfaceConfig
	DHCP       *DHCPConfig
	// 注：接口 DHCP 模式（dhcp select）已迁移至接口视图，单一事实源为
	// DeviceConfig["interface:<iface>:dhcp-select"]（P2 #6，save/reload 自动往返）。
	// ⚠️ 架构铁律：本结构体严禁新增任何 DHCP 中继内嵌结构体或模式字段。
	History []*topology.HistoryEntry // CLI 命令历史（FIFO，上限见 maxCLIHistory）

	// save 命令相关（贴近华为 eNSP 体验）
	Saved       bool   `json:"saved"`        // 是否已执行 save（写入启动配置）
	SaveTime    string `json:"save_time"`    // 最近一次 save 时间
	SavedConfig string `json:"saved_config"` // 已保存配置的 VRP 风格快照
	PendingSave bool   `json:"pending_save"` // save  awaiting Y/N 确认

	// ResolveTraceroute 是可选的真实引擎解析钩子（P1-F，风险1）。
	// 直连 ExecuteCommandOn 执行 tracert/traceroute 时若已注入，则通过它走
	// sim.Engine 真实路径；为 nil 时 parser 返回合理的"无引擎"提示，不 panic、
	// 不硬编码假路径。由 api 层在构造 CLIState 时注入，parser 不直接 import 引擎实例。
	ResolveTraceroute func(target string) *sim.TracerouteResult
}

type ARPEntry struct {
	IP        string
	MAC       string
	Interface string
	Type      string
	Age       string
}

type NATEntry struct {
	Protocol   string
	GlobalIP   string
	GlobalPort string
	InsideIP   string
	InsidePort string
	Type       string
}

type VLANConfig struct {
	ID     int
	Name   string
	Status string
	Ports  []string
}

type MACEntry struct {
	MAC       string
	VLAN      int
	Interface string
	Type      string
}

type InterfaceConfig struct {
	Name        string
	Status      string
	Protocol    string
	Description string
	IP          string
	Mask        string
}

type DHCPConfig struct {
	Enabled bool
	Pools   map[string]*DHCPPool
}

type DHCPPool struct {
	Name        string
	Network     string
	Mask        string
	Gateway     string
	DNSList     []string
	ExcludedIPs []string
	LeaseTime   string
	Allocated   int
	Total       int
}

type ACLRule struct {
	ID          int
	Name        string // 命名 ACL 名称
	Type        string // basic, advanced
	Action      string
	Protocol    string
	SrcIP       string
	SrcWildcard string
	DstIP       string
	DstWildcard string
	DstPort     string
	DstPortOp   string // eq, neq, gt, lt, range
	DstPortEnd  string // range 时的结束端口
	SourcePort  string
}

type RouteEntry struct {
	Destination string
	Mask        string
	MaskLength  int
	Protocol    string
	Pre         int
	Cost        int
	Flags       string
	NextHop     string
	Interface   string
}

type NATConfig struct {
	Enabled      bool
	Servers      []NATServer
	AddressPools []NATAddressPool
	Outbounds    []NATOutbound
}

type NATServer struct {
	GlobalIP   string
	InsideIP   string
	Protocol   string
	GlobalPort string
	InsidePort string
}

type NATAddressPool struct {
	ID      int
	StartIP string
	EndIP   string
}

type NATOutbound struct {
	ACLNum      int
	ACLName     string
	AddressPool int
	Type        string // "easy-ip" 或 "address-group"
}

type OSPFConfig struct {
	Enabled   bool
	ProcessID int
	AreaID    int
}

// ISISConfig 描述 IS-IS 协议配置（P1-F）。
// P0 仅置 Enabled/ProcessID 完成"最小启用"（进视图），P1 真实化补充
// NetworkType 与 ImportRoutes。同时镜像到 state.DeviceConfig 的 isis:* 键，
// 以便随拓扑 save/reload 落盘（见 SerializeToDeviceConfigData/LoadFromDeviceConfigData）。
type ISISConfig struct {
	Enabled      bool
	ProcessID    int
	NetworkType  string   // "level-1" / "level-2" / "level-1-2"，默认 "level-2"
	ImportRoutes []string // import-route 引入的协议列表，如 ["static","ospf"]
}

type MLAGConfig struct {
	DomainID       int
	SystemPriority int
	SystemMAC      string
	SystemNumber   int
	KeepaliveDest  string
	KeepaliveSrc   string
	MADExclude     []string
	PeerIP         string
	PeerLink       string
	DFSGroupID     int
	DFSMode        string
	DFSGroup       map[int]*DFSGroupConfig
}

type DFSGroupConfig struct {
	ID             int
	MLAGID         int
	Priority       int
	SourceIP       string
	PeerIP         string
	Authentication string
	Password       string
	Enabled        bool
}

type IPsecConfig struct {
	TunnelID       string
	LocalIP        string
	RemoteIP       string
	Mode           string
	Encryption     string
	Authentication string
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
}

type SSHConfig struct {
	Enabled        bool
	Port           int
	Version        string
	Authentication string
	MaxSessions    int
	STelnetEnabled bool
	RSAGenDone     bool
	Users          map[string]*SSHUser
}

type SSHUser struct {
	Name           string
	AuthType       string // password, rsa
	Password       string
	ServiceType    string // ssh, telnet, http
	PrivilegeLevel int
}

type VTYConfig struct {
	AuthenticationMode string // aaa, password, none
	UserPrivilegeLevel int
	ProtocolInbound    string // ssh, telnet, all
}

type VXLANConfig struct {
	Enabled     bool
	VNI         int
	VTEPIP      string
	PeerVTEPIP  string
	VRFName     string
	VSIs        map[string]*VSIConfig
	EvpnEnabled bool
}

type VSIConfig struct {
	Name        string
	VNI         int
	VPNs        []string
	Gateway     string
	Distributed bool
	EvpnEncap   string
	Status      string
}

type BGPConfig struct {
	Enabled   bool
	ASNumber  int
	RouterID  string
	Neighbors map[string]*BGPNeighbor
}

type BGPNeighbor struct {
	IPAddress string
	RemoteAS  int
	EBGP      bool
}

type BFDSession struct {
	PeerIP        string
	LocalIP       string
	MinTxInterval int
	MinRxInterval int
	DetectMult    int
}

type BFDConfig struct {
	Enabled  bool
	Sessions map[string]*BFDSession
}

type VRFConfig struct {
	RD           string
	RouteTargets []string
	Interfaces   []string
}

type PBRRule struct {
	ID        int
	MatchACL  string
	NextHop   string
	Interface string
}

type QoSClassifier struct {
	Name string
	ACL  string
	DSCP int
}

type QoSBehavior struct {
	Name      string
	Bandwidth int
	Priority  int
	Queue     string
	Action    string
}

type QoSPolicy struct {
	Name       string
	Classifier string
	Behavior   string
}

type QoSConfig struct {
	Enabled     bool
	Classifiers map[string]*QoSClassifier
	Behaviors   map[string]*QoSBehavior
	Policies    map[string]*QoSPolicy
}

type Dot1xPort struct {
	Enabled    bool
	AuthMethod string
	Reauth     bool
	QuietTimer int
}

type Dot1xConfig struct {
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

type LLDPConfig struct {
	Enabled           bool
	SystemName        string
	SystemDescription string
	ManagementAddress string
	PortConfig        map[string]bool // 接口名 -> 是否启用 LLDP
}

type NetFlowConfig struct {
	Enabled      bool
	Exporter     string
	Port         int
	Version      string
	SampleRate   int
	ActiveTime   int
	InactiveTime int
}

func NewCLIState() *CLIState {
	return newCLIStateWithType("")
}

// NewCLIStateWithType 构造一个绑定到指定设备类型的 CLI 会话状态。
// 传入空字符串将得到一个未绑定类型的状态（命令能力校验会跳过）。
func NewCLIStateWithType(dt topology.DeviceType) *CLIState {
	return newCLIStateWithType(dt)
}

func newCLIStateWithType(dt topology.DeviceType) *CLIState {
	return &CLIState{
		CurrentView:  ViewUser,
		DeviceType:   dt,
		DeviceConfig: make(map[string]string),
		ACLs:         make(map[string][]*ACLRule),
		Routes:       []*RouteEntry{},
		OSPF: &OSPFConfig{
			Enabled: false,
		},
		MLAG: &MLAGConfig{
			DFSMode: "all-active",
		},
		MLAGInterfaces: make(map[string]map[string]string),
		LLDP: &LLDPConfig{
			PortConfig: make(map[string]bool),
		},
		IPsec:  make(map[string]*IPsecConfig),
		SNMP:   &SNMPConfig{},
		Syslog: &SyslogConfig{},
		NTP:    &NTPConfig{},
		SSH: &SSHConfig{
			Enabled:        true,
			Port:           22,
			Version:        "2.0",
			Authentication: "password",
			MaxSessions:    5,
			STelnetEnabled: false,
			RSAGenDone:     false,
			Users:          make(map[string]*SSHUser),
		},
		VTY: &VTYConfig{
			AuthenticationMode: "password",
			UserPrivilegeLevel: 0,
			ProtocolInbound:    "all",
		},
		VXLAN: &VXLANConfig{
			VSIs:        make(map[string]*VSIConfig),
			EvpnEnabled: false,
		},
		BGP: &BGPConfig{
			Neighbors: make(map[string]*BGPNeighbor),
		},
		ISIS: &ISISConfig{
			Enabled:     false,
			NetworkType: "level-2",
		},
		BFD: &BFDConfig{
			Enabled:  false,
			Sessions: make(map[string]*BFDSession),
		},
		VRF: make(map[string]*VRFConfig),
		PBR: make(map[string][]*PBRRule),
		QoS: &QoSConfig{
			Enabled:     false,
			Classifiers: make(map[string]*QoSClassifier),
			Behaviors:   make(map[string]*QoSBehavior),
			Policies:    make(map[string]*QoSPolicy),
		},
		Dot1x: &Dot1xConfig{
			Enabled: false,
			Ports:   make(map[string]*Dot1xPort),
		},
		RADIUS: &RADIUSConfig{
			AuthPort:   1812,
			AcctPort:   1813,
			Timeout:    5,
			Retransmit: 3,
		},
		NetFlow: &NetFlowConfig{
			Port:         9995,
			Version:      "v9",
			SampleRate:   100,
			ActiveTime:   1800,
			InactiveTime: 15,
		},
		ARPTable: []*ARPEntry{
			{IP: "192.168.1.1", MAC: "00e0-fc12-3456", Interface: "GigabitEthernet0/0/1", Type: "Dynamic", Age: "00:05:23"},
			{IP: "192.168.1.2", MAC: "00e0-fc12-3457", Interface: "GigabitEthernet0/0/1", Type: "Dynamic", Age: "00:03:12"},
			{IP: "192.168.1.10", MAC: "00e0-fc12-3460", Interface: "GigabitEthernet0/0/2", Type: "Static", Age: "-"},
			{IP: "10.0.0.1", MAC: "00e0-fc12-3461", Interface: "GigabitEthernet0/0/3", Type: "Dynamic", Age: "00:01:45"},
		},
		NATTable: []*NATEntry{
			{Protocol: "TCP", GlobalIP: "203.0.113.1", GlobalPort: "80", InsideIP: "192.168.1.100", InsidePort: "80", Type: "NAT Server"},
			{Protocol: "UDP", GlobalIP: "203.0.113.1", GlobalPort: "53", InsideIP: "192.168.1.101", InsidePort: "53", Type: "NAT Server"},
			{Protocol: "TCP", GlobalIP: "203.0.113.2", GlobalPort: "1024-65535", InsideIP: "192.168.1.0", InsidePort: "-", Type: "Easy IP"},
		},
		NAT: &NATConfig{
			Enabled:      false,
			Servers:      []NATServer{},
			AddressPools: []NATAddressPool{},
			Outbounds:    []NATOutbound{},
		},
		VLANs: map[int]*VLANConfig{
			1:   {ID: 1, Name: "VLAN1", Status: "Up", Ports: []string{"GE0/0/1", "GE0/0/2", "GE0/0/3"}},
			10:  {ID: 10, Name: "VLAN10", Status: "Up", Ports: []string{"GE0/0/1", "GE0/0/4"}},
			20:  {ID: 20, Name: "VLAN20", Status: "Up", Ports: []string{"GE0/0/2", "GE0/0/5"}},
			100: {ID: 100, Name: "Management", Status: "Up", Ports: []string{"GE0/0/24"}},
		},
		MACTable: []*MACEntry{
			{MAC: "00e0-fc12-3456", VLAN: 10, Interface: "GigabitEthernet0/0/1", Type: "dynamic"},
			{MAC: "00e0-fc12-3457", VLAN: 20, Interface: "GigabitEthernet0/0/2", Type: "dynamic"},
			{MAC: "00e0-fc12-3460", VLAN: 100, Interface: "GigabitEthernet0/0/24", Type: "static"},
			{MAC: "00e0-fc12-3461", VLAN: 1, Interface: "GigabitEthernet0/0/3", Type: "dynamic"},
		},
		Interfaces: map[string]*InterfaceConfig{
			"GigabitEthernet0/0/1":  {Name: "GigabitEthernet0/0/1", Status: "Up", Protocol: "Up", Description: "Link to Core", IP: "192.168.1.1", Mask: "255.255.255.0"},
			"GigabitEthernet0/0/2":  {Name: "GigabitEthernet0/0/2", Status: "Up", Protocol: "Up", Description: "Link to Server", IP: "10.0.0.1", Mask: "255.255.255.0"},
			"GigabitEthernet0/0/3":  {Name: "GigabitEthernet0/0/3", Status: "Down", Protocol: "Down", Description: "", IP: "", Mask: ""},
			"GigabitEthernet0/0/24": {Name: "GigabitEthernet0/0/24", Status: "Up", Protocol: "Up", Description: "Management", IP: "172.16.0.1", Mask: "255.255.255.0"},
			"LoopBack0":             {Name: "LoopBack0", Status: "Up", Protocol: "Up", Description: "Router ID", IP: "1.1.1.1", Mask: "255.255.255.255"},
		},
		DHCP: &DHCPConfig{
			Enabled: false,
			Pools:   make(map[string]*DHCPPool),
		},
		History: []*topology.HistoryEntry{},
	}
}

// maxCLIHistory 限制单机命令历史长度，超过后 FIFO 滚动丢弃最旧的条目，
// 避免长会话无界增长内存。与华为 VRP 默认行为（上限 10，可配至 256）取向一致。
const maxCLIHistory = 256

// RecordHistory 记录一条命令到历史（FIFO）。空命令被忽略；超过上限时丢弃最旧。
// 调用方应保证同一设备的 CLIState 串行访问（见 api.Router 的 per-device 锁），
// 因此本方法本身不做额外并发保护。
func (s *CLIState) RecordHistory(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	s.History = append(s.History, &topology.HistoryEntry{
		Command:   command,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	})
	if len(s.History) > maxCLIHistory {
		s.History = s.History[len(s.History)-maxCLIHistory:]
	}
}

// FormatHistoryCommand 生成 display history-command 的输出。maxSize<=0 时默认
// 展示最近 10 条；否则展示最近 maxSize 条（不超过实际条数）。
func (s *CLIState) FormatHistoryCommand(maxSize int) string {
	n := len(s.History)
	if n == 0 {
		return "  History Command Record:\n  (empty)"
	}
	start := 0
	limit := 10
	if maxSize > 0 {
		limit = maxSize
	}
	if limit < n {
		start = n - limit
	}
	var b strings.Builder
	b.WriteString("  History Command Record:\n")
	for i := start; i < n; i++ {
		b.WriteString(fmt.Sprintf("   %3d  %s\n", i+1, s.History[i].Command))
	}
	return strings.TrimRight(b.String(), "\n")
}
