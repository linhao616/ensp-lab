//go:build ignore

// Package api 接收前端所有操作指令（创建节点、连接链路、执行命令、查询状态）
// 并分发给内部的 TopologyManager / Factory / CLI / Simulator。
//
// ⚠️ 当前状态：Gateway 架构暂未启用（预留架构）
//   - 主流程使用 router.go 中的 Router 结构体直接处理请求
//   - Gateway 作为备用架构，提供更完整的分层设计
//   - 包含：设备工厂(Factory)、仿真调度器(Scheduler)、CLI状态管理
//   - 启用方式：在 main.go 中替换 router.NewRouter 为 NewGateway + 适配层
//
// 架构设计：
//   - Gateway 是核心聚合器，把"前端 HTTP 请求"翻译为内部组件的方法调用；
//   - Router 是在 Gateway 之上暴露的 Gin 处理器，保持 HTTP 边界薄；
//   - 任何对状态的修改都通过 Gateway → Manager 的链路，避免直接绕过 Manager。
package api

import (
	"context"
	"fmt"
	"sync"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/factory"
	"ensp-lab/internal/simulator"
	"ensp-lab/internal/topology"
)

// Gateway 是 API 网关层的核心：聚合 TopologyManager、Factory、CLI 状态、Simulator。
type Gateway struct {
	manager   *topology.Manager
	factory   *factory.Factory
	scheduler *simulator.Scheduler

	cliMu sync.Mutex
	clis  map[string]*cli.CLIState

	captureMu sync.Mutex
	capture   map[string]*simulator.PacketEvent // 设备ID → 最近一次包事件
}

// NewGateway 构造一个 Gateway。scheduler 可为 nil（不在此网关中触发事件循环）。
func NewGateway(manager *topology.Manager, sched *simulator.Scheduler) *Gateway {
	g := &Gateway{
		manager:   manager,
		factory:   factory.NewFactory(),
		scheduler: sched,
		clis:      make(map[string]*cli.CLIState),
		capture:   make(map[string]*simulator.PacketEvent),
	}
	if sched != nil {
		sched.Subscribe(g.onSimEvent)
	}
	return g
}

// Factory 返回底层设备工厂，供测试 / 高级功能复用。
func (g *Gateway) Factory() *factory.Factory { return g.factory }

// Manager 返回底层拓扑管理器。
func (g *Gateway) Manager() *topology.Manager { return g.manager }

// Scheduler 返回底层调度器（可能为 nil）。
func (g *Gateway) Scheduler() *simulator.Scheduler { return g.scheduler }

// ---------------- 拓扑操作 ----------------

// CreateTopology 创建一份新拓扑。
func (g *Gateway) CreateTopology(t *topology.Topology) error {
	return g.manager.CreateTopology(t)
}

// GetTopology 获取一份拓扑。
func (g *Gateway) GetTopology(id string) (*topology.Topology, error) {
	return g.manager.GetTopology(id)
}

// ListTopologies 列出所有拓扑 id。
func (g *Gateway) ListTopologies() []string {
	return g.manager.ListTopologies()
}

// DeleteTopology 删除拓扑。
func (g *Gateway) DeleteTopology(id string) bool {
	return g.manager.DeleteTopology(id)
}

// CreateDevice 在指定拓扑中创建设备。
// typ 与 spec 由前端传入；ID 为空时由工厂自动生成。
func (g *Gateway) CreateDevice(topoID string, typ topology.DeviceType, spec factory.DeviceSpec) (*topology.Device, error) {
	dev, err := g.factory.Create(typ, spec)
	if err != nil {
		return nil, err
	}
	if err := g.manager.AddDevice(topoID, dev); err != nil {
		// 回滚工厂状态，避免 ID 泄漏。
		g.factory.Release(dev.ID)
		return nil, err
	}
	return dev, nil
}

// UpdateDevice 更新一个已存在的设备。
func (g *Gateway) UpdateDevice(topoID string, dev *topology.Device) error {
	return g.manager.UpdateDevice(topoID, dev)
}

// DeleteDevice 删除一个设备。
func (g *Gateway) DeleteDevice(topoID, deviceID string) error {
	return g.manager.RemoveDevice(topoID, deviceID)
}

// CreateLink 创建一条链路。
func (g *Gateway) CreateLink(topoID string, link *topology.Link) error {
	return g.manager.AddLink(topoID, link)
}

// DeleteLink 删除一条链路。
func (g *Gateway) DeleteLink(topoID, linkID string) error {
	return g.manager.RemoveLink(topoID, linkID)
}

// ---------------- 设备交互 ----------------

// ExecuteCLI 在指定设备上执行 CLI 命令，返回输出与新的视图状态。
func (g *Gateway) ExecuteCLI(topoID, deviceID, command string) (output, view, sub string, err error) {
	g.cliMu.Lock()
	state, ok := g.clis[deviceID]
	if !ok {
		// 绑定设备类型以启用命令能力校验
		dt := g.lookupDeviceType(topoID, deviceID)
		state = cli.NewCLIStateWithType(dt)
		g.clis[deviceID] = state
	}
	g.cliMu.Unlock()

	cmd := cli.ParseCommand(command)
	output = cli.ExecuteCommandOn(state, cmd, state.DeviceType)
	return output, string(state.CurrentView), state.CurrentSub, nil
}

// lookupDeviceType 从拓扑中查询设备类型；查询失败时返回空字符串。
func (g *Gateway) lookupDeviceType(topoID, deviceID string) topology.DeviceType {
	t, err := g.manager.GetTopology(topoID)
	if err != nil {
		return ""
	}
	for _, d := range t.Devices {
		if d.ID == deviceID {
			return d.Type
		}
	}
	return ""
}

// ResetCLI 丢弃指定设备的 CLI 会话状态。
func (g *Gateway) ResetCLI(deviceID string) {
	g.cliMu.Lock()
	delete(g.clis, deviceID)
	g.cliMu.Unlock()
}

// PowerOn 打开设备电源。
func (g *Gateway) PowerOn(topoID, deviceID string) error {
	return g.setDevicePower(topoID, deviceID, topology.StatusRunning)
}

// PowerOff 关闭设备电源。
func (g *Gateway) PowerOff(topoID, deviceID string) error {
	return g.setDevicePower(topoID, deviceID, topology.StatusPowerOff)
}

func (g *Gateway) setDevicePower(topoID, deviceID string, status topology.DeviceStatus) error {
	t, err := g.manager.GetTopology(topoID)
	if err != nil {
		return err
	}
	dev, ok := t.Devices[deviceID]
	if !ok {
		return topology.ErrNotFound
	}
	dev.Status = status
	return g.manager.UpdateDevice(topoID, dev)
}

// ---------------- 仿真事件接入 ----------------

// onSimEvent 处理来自仿真调度器的事件，将最近一次包事件缓存供前端查询。
func (g *Gateway) onSimEvent(ev simulator.Event) {
	if ev.Kind != simulator.EventPacketArrive &&
		ev.Kind != simulator.EventPacketDrop &&
		ev.Kind != simulator.EventPacketSent {
		return
	}
	pe, ok := ev.Payload.(*simulator.PacketEvent)
	if !ok || pe == nil {
		return
	}
	g.captureMu.Lock()
	g.capture[pe.DeviceID] = pe
	g.captureMu.Unlock()
}

// LastPacketEvent 返回指定设备的最近一次包事件（用于前端可视化）。
func (g *Gateway) LastPacketEvent(deviceID string) (*simulator.PacketEvent, bool) {
	g.captureMu.Lock()
	defer g.captureMu.Unlock()
	pe, ok := g.capture[deviceID]
	return pe, ok
}

// StartScheduler 启动调度器循环（如果存在）。通常在 main() 中调用。
func (g *Gateway) StartScheduler(ctx context.Context) {
	if g.scheduler == nil {
		return
	}
	go g.scheduler.Run(ctx)
}

// ---------------- 类型信息 ----------------

// DeviceTypeInfo 描述前端可下拉选择的设备类型。
type DeviceTypeInfo struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// DeviceTypes 返回所有支持的设备类型，供前端动态渲染工具栏。
func (g *Gateway) DeviceTypes() []DeviceTypeInfo {
	all := []struct {
		Type, Name string
	}{
		{string(topology.DeviceRouter), "Router"},
		{string(topology.DeviceSwitch), "Switch"},
		{string(topology.DeviceL3Switch), "L3 Switch"},
		{string(topology.DeviceFirewall), "Firewall"},
		{string(topology.DeviceAC), "AC"},
		{string(topology.DeviceAP), "AP"},
		{string(topology.DevicePC), "PC"},
		{string(topology.DeviceClient), "Client"},
		{string(topology.DeviceServer), "Server"},
		{string(topology.DeviceCloud), "Cloud"},
		{string(topology.DeviceHub), "Hub"},
	}
	out := make([]DeviceTypeInfo, 0, len(all))
	for _, x := range all {
		out = append(out, DeviceTypeInfo{Type: x.Type, Name: x.Name})
	}
	return out
}

// HealthCheck 简单的健康检查。
func (g *Gateway) HealthCheck() error {
	if g.manager == nil {
		return fmt.Errorf("gateway: manager is nil")
	}
	return nil
}
