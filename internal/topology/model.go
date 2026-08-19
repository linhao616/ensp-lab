package topology

import (
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"
)

type DeviceType string

const (
	DeviceRouter   DeviceType = "router"
	DeviceSwitch   DeviceType = "switch"
	DeviceL3Switch DeviceType = "l3_switch"
	DeviceFirewall DeviceType = "firewall"
	DeviceAC       DeviceType = "ac"
	DeviceAP       DeviceType = "ap"
	DevicePC       DeviceType = "pc"
	DeviceClient   DeviceType = "client"
	DeviceServer   DeviceType = "server"
	DeviceCloud    DeviceType = "cloud"
	DeviceHub      DeviceType = "hub"
	DeviceVTEP     DeviceType = "vtep"
)

type DeviceStatus string

const (
	StatusPowerOff DeviceStatus = "power_off"
	StatusRunning  DeviceStatus = "running"
	StatusConfig   DeviceStatus = "configuring"
)

type PortType string

const (
	PortCopper     PortType = "copper"
	PortEthernet   PortType = "ethernet"
	PortFiber      PortType = "fiber"
	PortSerial     PortType = "serial"
	PortConsole    PortType = "console"
	PortManagement PortType = "management"
)

type LinkType string

const (
	LinkTypeBusiness LinkType = "business"
	LinkTypeOOB      LinkType = "oob"
	LinkTypeConsole  LinkType = "console"
	LinkTypePower    LinkType = "power"
	LinkTypeWireless LinkType = "wireless"
	LinkTypeUnderlay LinkType = "underlay" // 物理链路（实线，黑）
	LinkTypeVXLAN    LinkType = "vxlan"    // VXLAN 隧道（虚线，红）
	LinkTypeAccess   LinkType = "access"   // 接入链路（虚线，灰）
	LinkTypeVirtual  LinkType = "virtual"  // 虚拟接入（虚线，灰）
)

// deviceRole 将设备类型映射为连线约束矩阵使用的角色。
// 演示数据：spine 用 switch、leaf 用 vtep、server 用 server、vm/pc 用 pc。
func deviceRole(t DeviceType) string {
	switch t {
	case DeviceSwitch, DeviceRouter, DeviceL3Switch:
		return "Spine"
	case DeviceVTEP:
		return "Leaf"
	case DeviceServer:
		return "Server"
	case DevicePC, DeviceClient:
		return "PC"
	default:
		return "Unknown"
	}
}

// 无序角色对的允许连线类型。
var linkRuleMatrix = map[string]LinkType{
	"Leaf-Spine":   LinkTypeUnderlay, // 物理链路
	"Leaf-Leaf":    LinkTypeVXLAN,    // VXLAN 隧道
	"Leaf-PC":      LinkTypeAccess,   // 接入链路
	"Leaf-Server":  LinkTypeAccess,   // 接入链路
	"PC-Server":    LinkTypeVirtual,  // 虚拟接入（覆盖 Server-VM/Server-PC）
	"Spine-Spine":  LinkTypeUnderlay, // 物理链路
}

// 禁止的角色组合。
var linkForbidden = map[string]bool{
	"PC-Spine":     true, // 禁止 Spine-VM / Spine-PC
	"Server-Spine": true, // 禁止 Spine-Server
	"Server-Server": true, // 禁止 Server-Server
}

// AllowedLinkType 校验两设备间是否允许连线，返回允许的链路类型。
// 当 allowed=false 时，msg 为给前端的错误提示（中文）。
func AllowedLinkType(src, dst DeviceType) (LinkType, bool, string) {
	a, b := deviceRole(src), deviceRole(dst)
	if a == "Unknown" || b == "Unknown" {
		return LinkTypeUnderlay, true, ""
	}
	key := a + "-" + b
	if a > b {
		key = b + "-" + a
	}
	if linkForbidden[key] {
		return "", false, "不允许 " + a + " 与 " + b + " 直接连线"
	}
	if lt, ok := linkRuleMatrix[key]; ok {
		return lt, true, ""
	}
	// 未定义的组合默认放行为物理链路
	return LinkTypeUnderlay, true, ""
}

type Interface struct {
	Name       string   `json:"name"`
	IPAddress  string   `json:"ip_address"`
	SubnetMask string   `json:"subnet_mask"`
	Gateway    string   `json:"gateway"`
	DNS        string   `json:"dns"`
	MAC        string   `json:"mac"`
	Status      string   `json:"status"`
	VLAN        int      `json:"vlan"`
	PortType    PortType `json:"port_type"`
	Bandwidth   int      `json:"bandwidth"`
	Delay       int      `json:"delay"`
	Loss        float64  `json:"loss,omitempty"` // 单向丢包率（百分比 0-100）：配置载体，仿真读 Link.Loss
	Description string   `json:"description"`
}

type Device struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Type       DeviceType            `json:"type"`
	Model      string                `json:"model"`
	Status     DeviceStatus          `json:"status"`
	PositionX  float64               `json:"position_x"`
	PositionY  float64               `json:"position_y"`
	Interfaces map[string]*Interface `json:"interfaces"`
	Config     string                `json:"config"`      // 文本配置（华为 VRP 风格）
	ConfigData *DeviceConfigData     `json:"config_data"` // 结构化配置数据
	VRPVersion string                `json:"vrp_version"`
	CreatedAt  time.Time             `json:"created_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
}

// HistoryEntry 表示一条已录入的 CLI 命令历史（贴近华为 VRP 的
// display history-command 体验）。定义在 topology 包内以避免 cli 反向
// 依赖 topology 形成 import 环。
type HistoryEntry struct {
	Command   string `json:"command"`   // 原始命令行
	Timestamp string `json:"timestamp"` // 录入时间（2006-01-02 15:04:05）
}

// DeviceConfigData 存储设备的结构化配置数据
type DeviceConfigData struct {
	DeviceName     string            `json:"device_name"`     // 设备名称
	Interfaces     map[string]string `json:"interfaces"`      // 接口配置 (key: "interface:GE0/0/0:ip")
	DefaultGateway string            `json:"default_gateway"` // 默认网关
	// save 命令结果：当前配置已写入"启动配置"（startup-configuration）。
	// 与 eNSP 一致，save 后配置在重启后依然保留。
	Saved      bool   `json:"saved"`        // 是否已 save
	SavedConfig string `json:"saved_config"` // 已保存配置的快照（VRP 风格文本）
	SaveTime   string `json:"save_time"`    // 最近一次 save 的时间
	// CLI 命令历史：每条执行成功的命令行均记录，随拓扑持久化，重启不丢。
	History []*HistoryEntry `json:"history,omitempty"` // 命令历史（FIFO，上限见 cli 包 maxCLIHistory）
}

type Link struct {
	ID            string    `json:"id"`
	SourceDevice  string    `json:"source_device"`
	SourcePort    string    `json:"source_port"`
	TargetDevice  string    `json:"target_device"`
	TargetPort    string    `json:"target_port"`
	LinkType      LinkType  `json:"link_type"`
	CableType     PortType  `json:"cable_type"`
	Bandwidth     int       `json:"bandwidth"`
	Delay         int       `json:"delay"`
	Loss          float64   `json:"loss,omitempty"` // 单向丢包率（百分比 0-100）；端到端按路径各段累积，见 internal/sim/link_quality.go
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	VXLANVNI      int       `json:"vxlan_vni,omitempty"`
	VXLANPeerList []string  `json:"vxlan_peer_list,omitempty"`
	VLAN          int       `json:"vlan,omitempty"`
	// 端口标签偏移（世界坐标，单位 px），用于前端拖拽端口名标签，避免被设备遮挡。
	SourceLabelDX float64 `json:"source_label_dx,omitempty"`
	SourceLabelDY float64 `json:"source_label_dy,omitempty"`
	TargetLabelDX float64 `json:"target_label_dx,omitempty"`
	TargetLabelDY float64 `json:"target_label_dy,omitempty"`
}

type TextAnnotation struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	PositionX float64   `json:"position_x"`
	PositionY float64   `json:"position_y"`
	// 样式字段（前端标注设置面板控制）
	FontSize    int     `json:"font_size,omitempty"`    // 0 = 默认(12)
	FontFamily  string  `json:"font_family,omitempty"`  // 字体族，如 "sans-serif"/"monospace"
	TextAlign   string  `json:"text_align,omitempty"`   // left | center | right
	BorderStyle string  `json:"border_style,omitempty"` // solid | dashed | hidden
	Background  string  `json:"background,omitempty"`   // 背景色，如 "#fffbe6"
	Width       float64 `json:"width,omitempty"`        // 显式宽度（px），0 = 自适应
	Height      float64 `json:"height,omitempty"`       // 显式高度（px），0 = 自适应（显示全部内容）
	CreatedAt   time.Time `json:"created_at"`
}

type Topology struct {
	mu            sync.RWMutex
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Devices       map[string]*Device `json:"devices"`
	Links         []*Link            `json:"links"`
	Annotations   []*TextAnnotation  `json:"annotations"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	CanvasScale   float64            `json:"canvas_scale"`
	CanvasOffsetX float64            `json:"canvas_offset_x"`
	CanvasOffsetY float64            `json:"canvas_offset_y"`
}

func NewTopology(id, name string) *Topology {
	now := time.Now()
	return &Topology{
		ID:            id,
		Name:          name,
		Devices:       make(map[string]*Device),
		Links:         []*Link{},
		Annotations:   []*TextAnnotation{},
		CreatedAt:     now,
		UpdatedAt:     now,
		CanvasScale:   1.0,
		CanvasOffsetX: 0,
		CanvasOffsetY: 0,
	}
}

// Clone 返回拓扑的一份深拷贝。
//
// 深拷贝是「仿真引擎与 API 共享状态解耦」的关键：引擎持有 Clone 的结果而非
// 共享的 *Topology 指针，API 层后续对原拓扑的就地修改不会影响已加载的引擎视图，
// 从根本上消除并发读写竞争。返回的拓扑带一把新的 RWMutex，不复制原锁状态。
func (t *Topology) Clone() *Topology {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := &Topology{
		ID:            t.ID,
		Name:          t.Name,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		CanvasScale:   t.CanvasScale,
		CanvasOffsetX: t.CanvasOffsetX,
		CanvasOffsetY: t.CanvasOffsetY,
		Devices:       make(map[string]*Device, len(t.Devices)),
		Links:         make([]*Link, len(t.Links)),
		Annotations:   make([]*TextAnnotation, len(t.Annotations)),
	}
	for id, d := range t.Devices {
		out.Devices[id] = cloneDevice(d)
	}
	for i, l := range t.Links {
		out.Links[i] = cloneLink(l)
	}
	for i, a := range t.Annotations {
		ca := *a
		out.Annotations[i] = &ca
	}
	return out
}

// cloneDevice 深拷贝单个设备（含接口表与结构化配置）。
func cloneDevice(src *Device) *Device {
	dst := *src
	dst.Interfaces = make(map[string]*Interface, len(src.Interfaces))
	for name, iface := range src.Interfaces {
		ci := *iface
		dst.Interfaces[name] = &ci
	}
	if src.ConfigData != nil {
		cd := *src.ConfigData
		if src.ConfigData.Interfaces != nil {
			cd.Interfaces = make(map[string]string, len(src.ConfigData.Interfaces))
			for k, v := range src.ConfigData.Interfaces {
				cd.Interfaces[k] = v
			}
		}
		if src.ConfigData.History != nil {
			cd.History = make([]*HistoryEntry, len(src.ConfigData.History))
			for i, h := range src.ConfigData.History {
				hh := *h
				cd.History[i] = &hh
			}
		}
		dst.ConfigData = &cd
	}
	return &dst
}

// cloneLink 深拷贝单条链路（含切片类字段）。
func cloneLink(src *Link) *Link {
	dst := *src
	if src.VXLANPeerList != nil {
		dst.VXLANPeerList = make([]string, len(src.VXLANPeerList))
		copy(dst.VXLANPeerList, src.VXLANPeerList)
	}
	return &dst
}

func (t *Topology) AddDevice(device *Device) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Devices[device.ID] = device
	t.UpdatedAt = time.Now()
}

func (t *Topology) RemoveDevice(deviceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.Devices, deviceID)
	t.Links = filterLinks(t.Links, deviceID)
	t.UpdatedAt = time.Now()
}

func (t *Topology) AddLink(link *Link) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Links = append(t.Links, link)
	t.UpdatedAt = time.Now()
}

func (t *Topology) RemoveLink(linkID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Links = filterLinksByID(t.Links, linkID)
	t.UpdatedAt = time.Now()
}

func (t *Topology) GetDevice(deviceID string) (*Device, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	d, ok := t.Devices[deviceID]
	return d, ok
}

func (t *Topology) GetDeviceIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ids := make([]string, 0, len(t.Devices))
	for id := range t.Devices {
		ids = append(ids, id)
	}
	return ids
}

func (t *Topology) GetLinks() []*Link {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*Link, len(t.Links))
	copy(out, t.Links)
	return out
}

func (t *Topology) GetLinkByID(linkID string) (*Link, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, l := range t.Links {
		if l.ID == linkID {
			return l, true
		}
	}
	return nil, false
}

func (t *Topology) DeviceCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.Devices)
}

func (t *Topology) LinkCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.Links)
}

func filterLinks(links []*Link, deviceID string) []*Link {
	result := []*Link{}
	for _, link := range links {
		if link.SourceDevice != deviceID && link.TargetDevice != deviceID {
			result = append(result, link)
		}
	}
	return result
}

func filterLinksByID(links []*Link, linkID string) []*Link {
	result := []*Link{}
	for _, link := range links {
		if link.ID != linkID {
			result = append(result, link)
		}
	}
	return result
}

var deviceDefaults = map[DeviceType]struct {
	Model      string
	Interfaces []string
	VRPVersion string
}{
	DeviceRouter: {
		Model:      "AR2240",
		Interfaces: []string{"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "Serial0/0/0", "Serial0/0/1"},
		VRPVersion: "VRP5",
	},
	DeviceSwitch: {
		Model:      "S5700-28P-LI",
		Interfaces: []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4", "GigabitEthernet0/0/5", "GigabitEthernet0/0/6", "GigabitEthernet0/0/7", "GigabitEthernet0/0/8"},
		VRPVersion: "VRP5",
	},
	DeviceL3Switch: {
		Model:      "S7700-48S",
		Interfaces: []string{"Vlanif1", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4"},
		VRPVersion: "VRP5",
	},
	DeviceFirewall: {
		Model:      "USG6000",
		Interfaces: []string{"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3"},
		VRPVersion: "VRP5",
	},
	DeviceAC: {
		Model:      "AC6005",
		Interfaces: []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3"},
		VRPVersion: "VRP5",
	},
	DeviceAP: {
		Model:      "AP6010SN",
		Interfaces: []string{"Radio0", "Radio1", "GigabitEthernet0"},
		VRPVersion: "VRP5",
	},
	DevicePC: {
		Model:      "PC",
		Interfaces: []string{"Ethernet0"},
		VRPVersion: "",
	},
	DeviceClient: {
		Model:      "Client",
		Interfaces: []string{"Wi-Fi0"},
		VRPVersion: "",
	},
	DeviceServer: {
		Model:      "Server",
		Interfaces: []string{"Ethernet0"},
		VRPVersion: "",
	},
	DeviceCloud: {
		Model:      "Cloud",
		Interfaces: []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"},
		VRPVersion: "",
	},
	DeviceHub: {
		Model:      "Hub",
		Interfaces: []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"},
		VRPVersion: "",
	},
	DeviceVTEP: {
		Model:      "VTEP",
		Interfaces: []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4"},
		VRPVersion: "VRP5",
	},
}

func (d *Device) InitializeDefaults() {
	if d.Model == "" {
		d.Model = deviceDefaults[d.Type].Model
	}
	if d.VRPVersion == "" {
		d.VRPVersion = deviceDefaults[d.Type].VRPVersion
	}
	if d.Interfaces == nil {
		d.Interfaces = make(map[string]*Interface)
		for _, ifName := range deviceDefaults[d.Type].Interfaces {
			portType := PortEthernet
			if len(ifName) >= 6 && ifName[:6] == "Serial" {
				portType = PortSerial
			} else if len(ifName) >= 5 && ifName[:5] == "Radio" {
				portType = PortCopper
			} else if len(ifName) >= 6 && ifName[:6] == "Vlanif" {
				portType = PortCopper
			} else if len(ifName) >= 4 && ifName[:4] == "Wi-Fi" {
				portType = PortCopper
			}
			d.Interfaces[ifName] = &Interface{
				Name:      ifName,
				Status:    "down",
				PortType:  portType,
				Bandwidth: 1000,
			}
		}
	}
}

// NeedsAutoLayout reports whether this topology's devices are stacked at the
// origin (0,0) — i.e. they were never given real coordinates. Such topologies
// render as a single overlapping clump in the canvas and should be laid out.
func (t *Topology) NeedsAutoLayout() bool {
	if len(t.Devices) == 0 {
		return false
	}
	for _, d := range t.Devices {
		if d == nil {
			continue
		}
		if math.Abs(d.PositionX) > 1e-6 || math.Abs(d.PositionY) > 1e-6 {
			return false
		}
	}
	return true
}

// AutoLayout assigns non-overlapping, evenly distributed coordinates to every
// device using a deterministic force-directed simulation (Fruchterman-Reingold
// style). Connected devices attract along their links; every device pair
// repels, which spreads the graph out and prevents overlap or clustering. The
// result is centered at (500, 320) in canvas coordinates, matching the
// hand-authored VXLAN reference topology. No randomness is used, so layouts
// are fully reproducible.
func (t *Topology) AutoLayout() {
	ids := make([]string, 0, len(t.Devices))
	for id := range t.Devices {
		ids = append(ids, id)
	}
	n := len(ids)
	if n == 0 {
		return
	}
	sort.Strings(ids)

	// Ideal edge length. Slightly larger for bigger graphs to keep density
	// readable; tuned to the ~200px spacing seen in the VXLAN reference.
	k := 200.0
	switch {
	case n > 12:
		k = 240.0
	case n > 6:
		k = 220.0
	}
	repulse := k * k

	// Deterministic circular initialization (sorted ids => stable layout).
	pos := make(map[string]*[2]float64, n)
	radius := k * math.Sqrt(float64(n)) * 1.3
	for i, id := range ids {
		ang := 2*math.Pi*float64(i)/float64(n) - math.Pi/2
		pos[id] = &[2]float64{radius * math.Cos(ang), radius * math.Sin(ang)}
	}

	const (
		iterations = 400
		cooling    = 0.97
	)
	temp := 2.0 * k
	for step := 0; step < iterations; step++ {
		disp := make(map[string]*[2]float64, n)
		for _, id := range ids {
			disp[id] = &[2]float64{0, 0}
		}
		// Repulsive force between every pair.
		for i := 0; i < n; i++ {
			a := ids[i]
			for j := i + 1; j < n; j++ {
				b := ids[j]
				dx := pos[a][0] - pos[b][0]
				dy := pos[a][1] - pos[b][1]
				dist := math.Hypot(dx, dy)
				if dist < 0.01 {
					// Deterministic symmetry break based on indices.
					dx = 0.01 * (float64((i*7+j)%5) - 2)
					dy = 0.01 * (float64((i*3+j)%5) - 2)
					dist = math.Hypot(dx, dy)
					if dist < 0.01 {
						dist = 0.01
					}
				}
				f := repulse / dist
				ux, uy := dx/dist, dy/dist
				disp[a][0] += ux * f
				disp[a][1] += uy * f
				disp[b][0] -= ux * f
				disp[b][1] -= uy * f
			}
		}
		// Attractive force along edges.
		for _, l := range t.Links {
			a, b := l.SourceDevice, l.TargetDevice
			pa, okA := pos[a]
			pb, okB := pos[b]
			if !okA || !okB {
				continue
			}
			dx := pa[0] - pb[0]
			dy := pa[1] - pb[1]
			dist := math.Hypot(dx, dy)
			if dist < 0.01 {
				dist = 0.01
			}
			f := (dist * dist) / k
			ux, uy := dx/dist, dy/dist
			disp[a][0] -= ux * f
			disp[a][1] -= uy * f
			disp[b][0] += ux * f
			disp[b][1] += uy * f
		}
		// Apply displacement capped by temperature, then cool.
		for _, id := range ids {
			d := math.Hypot(disp[id][0], disp[id][1])
			if d > temp {
				disp[id][0] = disp[id][0] / d * temp
				disp[id][1] = disp[id][1] / d * temp
			}
			pos[id][0] += disp[id][0]
			pos[id][1] += disp[id][1]
		}
		temp *= cooling
	}

	// Center the result at (500, 320).
	const centerX, centerY = 500.0, 320.0
	cx, cy := 0.0, 0.0
	for _, id := range ids {
		cx += pos[id][0]
		cy += pos[id][1]
	}
	cx /= float64(n)
	cy /= float64(n)
	for _, id := range ids {
		dev := t.Devices[id]
		if dev == nil {
			continue
		}
		dev.PositionX = pos[id][0] - cx + centerX
		dev.PositionY = pos[id][1] - cy + centerY
	}
}

func (t *Topology) MarshalJSON() ([]byte, error) {
	type Alias Topology
	return json.Marshal(&struct {
		*Alias
		DeviceCount int `json:"device_count"`
		LinkCount   int `json:"link_count"`
	}{
		Alias:       (*Alias)(t),
		DeviceCount: len(t.Devices),
		LinkCount:   len(t.Links),
	})
}

func (t *Topology) ToGraphJson() ([]byte, error) {
	graph := &GraphData{}

	for _, device := range t.Devices {
		node := GraphNode{
			ID:     device.ID,
			Label:  device.Name,
			Type:   string(device.Type),
			Status: string(device.Status),
			Model:  device.Model,
			X:      device.PositionX,
			Y:      device.PositionY,
			Data: map[string]interface{}{
				"model":       device.Model,
				"vrp_version": device.VRPVersion,
				"interfaces":  len(device.Interfaces),
			},
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	for _, link := range t.Links {
		edge := GraphEdge{
			ID:     link.ID,
			Source: link.SourceDevice,
			Target: link.TargetDevice,
			Label:  string(link.LinkType),
			Type:   string(link.LinkType),
			Data: map[string]interface{}{
				"source_port": link.SourcePort,
				"target_port": link.TargetPort,
				"bandwidth":   link.Bandwidth,
				"delay":       link.Delay,
				"loss":        link.Loss,
				"cable_type":  string(link.CableType),
			},
		}
		graph.Edges = append(graph.Edges, edge)
	}

	return graph.ToCytoscapeJSON()
}

func (t *Topology) ToGraphJsonG6() ([]byte, error) {
	graph := &GraphData{}

	for _, device := range t.Devices {
		node := GraphNode{
			ID:     device.ID,
			Label:  device.Name,
			Type:   string(device.Type),
			Status: string(device.Status),
			Model:  device.Model,
			X:      device.PositionX,
			Y:      device.PositionY,
			Data: map[string]interface{}{
				"model":       device.Model,
				"vrp_version": device.VRPVersion,
				"interfaces":  len(device.Interfaces),
			},
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	for _, link := range t.Links {
		edge := GraphEdge{
			ID:     link.ID,
			Source: link.SourceDevice,
			Target: link.TargetDevice,
			Label:  string(link.LinkType),
			Type:   string(link.LinkType),
			Data: map[string]interface{}{
				"source_port": link.SourcePort,
				"target_port": link.TargetPort,
				"bandwidth":   link.Bandwidth,
				"delay":       link.Delay,
				"loss":        link.Loss,
				"cable_type":  string(link.CableType),
			},
		}
		graph.Edges = append(graph.Edges, edge)
	}

	return graph.ToG6JSON()
}
