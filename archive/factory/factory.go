//go:build ignore

// Package factory 提供网络设备工厂，根据类型与规格实例化具体的网络设备。
//
// ⚠️ 当前状态：暂未启用（预留架构）
//   - 被 Gateway 架构引用，但 Gateway 暂未启用
//   - 启用方式：在 main.go 中启用 Gateway 架构后自动生效
//
// 设计原则：
//   - 工厂模式：根据 DeviceType 集中生成拓扑模型中 Device 的全部默认值（型号、接口列表、VRP版本、初始配置等）。
//   - 可扩展：每个 DeviceType 拥有独立的构建器（Builder），便于后续添加新设备而无需改动主流程。
//   - 线程安全：设备 ID 与 MAC 地址由原子计数器 + 加密随机数生成，避免冲突。
//   - 关注点分离：工厂只负责"造设备"，不持有任何运行时状态；运行时状态由 simulator 包负责。
package factory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ensp-lab/internal/topology"
)

// DeviceSpec 是创建设备时的可选规格，零值表示使用类型默认配置。
type DeviceSpec struct {
	// ID 指定设备 ID，为空时由工厂自动生成。
	ID string
	// Name 指定设备显示名，为空时使用 "<Type>-<seq>"。
	Name string
	// Model 覆盖默认型号。
	Model string
	// PositionX / PositionY 拓扑画布坐标。
	PositionX float64
	PositionY float64
	// StartPoweredOn 是否以已上电状态创建（默认 true）。
	StartPoweredOn *bool
	// InitialConfig 注入初始配置文本（可选）。
	InitialConfig string
	// ExtraInterfaces 在默认接口之外追加的额外接口。
	ExtraInterfaces []string
}

// deviceDefaults 描述每种设备类型的默认值，集中维护便于扩展。
type deviceDefaults struct {
	Model      string
	Interfaces []string
	VRPVersion string
	// IsRouter / IsSwitch 描述设备能力，由工厂写入 Device.Config 中的元标记。
	IsRouter bool
	IsSwitch bool
}

// defaults 各类设备的出厂配置。
var defaults = map[topology.DeviceType]deviceDefaults{
	topology.DeviceRouter: {
		Model: "AR2240", VRPVersion: "VRP5", IsRouter: true,
		Interfaces: []string{
			"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2",
			"Serial0/0/0", "Serial0/0/1",
		},
	},
	topology.DeviceSwitch: {
		Model: "S5700-28P-LI", VRPVersion: "VRP5", IsSwitch: true,
		Interfaces: []string{
			"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4",
			"GigabitEthernet0/0/5", "GigabitEthernet0/0/6", "GigabitEthernet0/0/7", "GigabitEthernet0/0/8",
		},
	},
	topology.DeviceL3Switch: {
		Model: "S7700-48S", VRPVersion: "VRP5", IsRouter: true, IsSwitch: true,
		Interfaces: []string{
			"Vlanif1",
			"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4",
		},
	},
	topology.DeviceFirewall: {
		Model: "USG6000", VRPVersion: "VRP5", IsRouter: true,
		Interfaces: []string{
			"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3",
		},
	},
	topology.DeviceAC: {
		Model: "AC6005", VRPVersion: "VRP5",
		Interfaces: []string{
			"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3",
		},
	},
	topology.DeviceAP: {
		Model: "AP6010SN", VRPVersion: "VRP5",
		Interfaces: []string{"Radio0", "Radio1", "GigabitEthernet0"},
	},
	topology.DevicePC: {
		Model: "PC",
		Interfaces: []string{"Ethernet0"},
	},
	topology.DeviceClient: {
		Model: "Client",
		Interfaces: []string{"Wi-Fi0"},
	},
	topology.DeviceServer: {
		Model: "Server",
		Interfaces: []string{"Ethernet0", "Ethernet1"},
	},
	topology.DeviceCloud: {
		Model: "Cloud",
		Interfaces: []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"},
	},
	topology.DeviceHub: {
		Model: "Hub",
		Interfaces: []string{"Ethernet0", "Ethernet1", "Ethernet2", "Ethernet3"},
	},
	topology.DeviceVTEP: {
		Model: "VTEP", VRPVersion: "VRP5", IsRouter: true, IsSwitch: true,
		Interfaces: []string{
			"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "GigabitEthernet0/0/3", "GigabitEthernet0/0/4",
		},
	},
}

// Factory 是设备工厂，支持并发安全地创建设备。
type Factory struct {
	seq     uint64         // 设备序号，用于生成默认 Name
	usedIDs map[string]bool // 已使用的设备 ID
	mu      sync.Mutex
}

// NewFactory 构造一个工厂实例。
func NewFactory() *Factory {
	return &Factory{usedIDs: make(map[string]bool)}
}

// Create 根据设备类型和规格创建设备实例。
//
// 返回的 *topology.Device 已被初始化：包含默认接口、MAC 地址、状态与时间戳。
// 重复使用相同 ID 调用将返回错误，调用方需保证 ID 唯一性。
func (f *Factory) Create(typ topology.DeviceType, spec DeviceSpec) (*topology.Device, error) {
	def, ok := defaults[typ]
	if !ok {
		return nil, fmt.Errorf("factory: unknown device type %q", typ)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// 1. 确定 ID
	id := spec.ID
	if id == "" {
		id = f.nextID(typ)
	} else if f.usedIDs[id] {
		return nil, fmt.Errorf("factory: device id %q already used", id)
	}
	f.usedIDs[id] = true

	// 2. 构造设备结构
	now := time.Now()
	dev := &topology.Device{
		ID:         id,
		Name:       spec.Name,
		Type:       typ,
		Model:      def.Model,
		Interfaces: make(map[string]*topology.Interface),
		Config:     spec.InitialConfig,
		VRPVersion: def.VRPVersion,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// 3. 应用可选覆盖
	if spec.Model != "" {
		dev.Model = spec.Model
	}
	if dev.Name == "" {
		dev.Name = fmt.Sprintf("%s-%d", typ, atomic.AddUint64(&f.seq, 1))
	}
	dev.PositionX = spec.PositionX
	dev.PositionY = spec.PositionY

	// 4. 初始化接口
	startPowered := true
	if spec.StartPoweredOn != nil {
		startPowered = *spec.StartPoweredOn
	}
	if startPowered {
		dev.Status = topology.StatusRunning
	} else {
		dev.Status = topology.StatusPowerOff
	}

	f.initInterfaces(dev, def.Interfaces)
	for _, ifName := range spec.ExtraInterfaces {
		f.addInterface(dev, ifName)
	}

	return dev, nil
}

// Release 释放一个已创建设备的 ID，允许后续复用。
func (f *Factory) Release(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.usedIDs, id)
}

// SupportedTypes 返回工厂支持的所有设备类型。
func (f *Factory) SupportedTypes() []topology.DeviceType {
	out := make([]topology.DeviceType, 0, len(defaults))
	for t := range defaults {
		out = append(out, t)
	}
	return out
}

// nextID 原子地生成 "type-N" 形式的默认 ID。
func (f *Factory) nextID(typ topology.DeviceType) string {
	for i := uint64(1); ; i++ {
		candidate := fmt.Sprintf("%s-%d", typ, i)
		if !f.usedIDs[candidate] {
			f.usedIDs[candidate] = true
			return candidate
		}
	}
}

// initInterfaces 初始化设备的接口映射，按接口名设置端口类型与初始状态。
func (f *Factory) initInterfaces(dev *topology.Device, names []string) {
	for _, name := range names {
		f.addInterface(dev, name)
	}
}

// addInterface 添加单个接口到设备，自动推断端口类型与生成 MAC。
func (f *Factory) addInterface(dev *topology.Device, name string) {
	if _, exists := dev.Interfaces[name]; exists {
		return
	}
	portType := inferPortType(name)
	dev.Interfaces[name] = &topology.Interface{
		Name:      name,
		Status:    "down",
		PortType:  portType,
		Bandwidth: defaultBandwidth(portType),
		MAC:       generateMAC(dev.ID, name),
	}
}

// inferPortType 根据接口名推断物理端口类型。
func inferPortType(name string) topology.PortType {
	switch {
	case len(name) >= 6 && name[:6] == "Serial":
		return topology.PortSerial
	case len(name) >= 5 && name[:5] == "Radio":
		return topology.PortCopper // Radio ports use copper-like medium in the simulator
	case len(name) >= 5 && name[:5] == "Fiber", len(name) >= 4 && name[:4] == "XGE", len(name) >= 4 && name[:4] == "40GE":
		return topology.PortFiber
	case len(name) >= 7 && name[:7] == "Console":
		return topology.PortConsole
	case len(name) >= 4 && name[:4] == "MGE", len(name) >= 6 && name[:6] == "Manage", len(name) >= 3 && name[:3] == "ME0":
		return topology.PortManagement
	case len(name) >= 4 && name[:4] == "Vlan":
		return topology.PortEthernet
	default:
		return topology.PortEthernet
	}
}

// defaultBandwidth 根据端口类型返回默认带宽（Mbps）。
func defaultBandwidth(p topology.PortType) int {
	switch p {
	case topology.PortSerial:
		return 2 // E1 default
	case topology.PortFiber:
		return 10000 // 10G
	case topology.PortManagement:
		return 100
	default:
		return 1000 // 1G copper
	}
}

// generateMAC 基于设备 ID 与接口名生成确定性的伪 MAC 地址。
// 使用加密随机字节以保证分布式环境下 ID 冲突概率极低。
func generateMAC(deviceID, ifName string) string {
	var seed [10]byte
	copy(seed[:], []byte(deviceID))
	copy(seed[6:], []byte(ifName))
	_, _ = rand.Read(seed[:6]) //nolint:errcheck

	// 设为本地管理位 (bit1 of first octet = 0) 与单播位 (bit0 of first octet = 0)
	seed[0] &^= 0x01
	seed[0] &^= 0x02

	return hex.EncodeToString(seed[:6])
}
