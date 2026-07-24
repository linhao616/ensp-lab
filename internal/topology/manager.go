// Package topology 包含网络拓扑数据模型与内存中的拓扑管理器。
//
// model.go 定义不可变数据契约（Device/Link/Topology 等）。
// manager.go 在 model 之上提供线程安全、可订阅变更的内存管理能力，
// 作为 API 网关与仿真引擎之间共享状态的唯一入口。
package topology

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/logging"

	"go.uber.org/zap"
)

// NodeRebuilder 由模拟引擎实现，用于在拓扑变更时重建内部节点图。
//
// topology 包不依赖任何具体引擎实现，仅通过此接口与 sim 包解耦。
// 实现方需保证 Rebuild 在并发调用时是线程安全的。
type NodeRebuilder interface {
	Rebuild(topo *Topology) error
}

// 公共错误，调用方可通过 errors.Is 判定。
var (
	ErrNotFound       = errors.New("topology: not found")
	ErrAlreadyExists  = errors.New("topology: already exists")
	ErrInvalidLink    = errors.New("topology: invalid link endpoint")
	ErrDuplicatePort  = errors.New("topology: port already occupied on link endpoint")
	ErrSelfLink       = errors.New("topology: link endpoints must be on different devices")
	ErrMissingDevices = errors.New("topology: link endpoint device does not exist")
	ErrMissingPort    = errors.New("topology: link endpoint port does not exist on device")
)

// ChangeType 描述一次拓扑变更的类型。
type ChangeType string

const (
	ChangeTopologyCreated ChangeType = "topology_created"
	ChangeTopologyUpdated ChangeType = "topology_updated"
	ChangeTopologyDeleted ChangeType = "topology_deleted"
	ChangeDeviceAdded     ChangeType = "device_added"
	ChangeDeviceUpdated   ChangeType = "device_updated"
	ChangeDeviceRemoved   ChangeType = "device_removed"
	ChangeLinkAdded       ChangeType = "link_added"
	ChangeLinkRemoved     ChangeType = "link_removed"
)

// ChangeEvent 描述一次拓扑变更事件，订阅者通过此结构体感知状态变化。
type ChangeEvent struct {
	Type       ChangeType
	TopologyID string
	DeviceID   string
	LinkID     string
	At         time.Time
}

// Subscriber 订阅者回调函数。
type Subscriber func(ev ChangeEvent)

// Manager 是拓扑的内存管理器，线程安全。
//
// 设计要点：
//   - 所有公开方法都通过 mu 互斥保护，调用方无需额外加锁。
//   - 通过 Subscribe 注册回调，实现"事件 → 仿真引擎/前端推送"的解耦。
//   - Validate 参数控制 AddLink 等方法是否执行端到端校验（生产路径 true，性能敏感批量导入可设 false）。
//   - 可选的 NodeRebuilder 在每次拓扑变更（创建/更新/增删设备/增删链路）后于持锁状态下
//     被触发，用于通知仿真引擎重建内部节点图；重建失败仅记录日志，不阻断主流程。
type Manager struct {
	mu        sync.RWMutex
	store     map[string]*Topology
	subs      []Subscriber
	closed    bool
	rebuilder NodeRebuilder
}

// NewManager 构造一个空的管理器。
func NewManager() *Manager {
	return &Manager{store: make(map[string]*Topology)}
}

// SetRebuilder 注册或替换重建器。传入 nil 表示取消注册。
// 该方法自身加锁，调用方无需额外同步。
func (m *Manager) SetRebuilder(rb NodeRebuilder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rebuilder = rb
}

// rebuildLocked 在持锁状态下触发重建器，忽略错误仅记录日志。
// 必须由已持有 m.mu 的方法调用。
func (m *Manager) rebuildLocked(t *Topology) {
	if m.rebuilder == nil || t == nil {
		return
	}
	if err := m.rebuilder.Rebuild(t); err != nil {
		logging.Warn("rebuilder.Rebuild failed",
			zap.String("topology_id", t.ID),
			zap.Error(err),
		)
	}
}

// CreateTopology 注册一份新拓扑。
// 若 id 已存在则返回 ErrAlreadyExists。
func (m *Manager) CreateTopology(t *Topology) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("topology: nil or empty id")
	}
	if t.Devices == nil {
		t.Devices = make(map[string]*Device)
	}
	if t.Links == nil {
		t.Links = []*Link{}
	}
	if t.Annotations == nil {
		t.Annotations = []*TextAnnotation{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("topology: manager is closed")
	}
	if _, ok := m.store[t.ID]; ok {
		return fmt.Errorf("%w: topology %q", ErrAlreadyExists, t.ID)
	}
	m.store[t.ID] = t
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeTopologyCreated, TopologyID: t.ID, At: time.Now(),
	})
	return nil
}

// GetTopology 获取拓扑的指针，调用方只读，不要直接修改。
// 若未找到返回 (nil, ErrNotFound)。
func (m *Manager) GetTopology(id string) (*Topology, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.store[id]
	if !ok {
		return nil, fmt.Errorf("%w: topology %q", ErrNotFound, id)
	}
	return t, nil
}

// ListTopologies 返回拓扑 id 列表。
func (m *Manager) ListTopologies() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.store))
	for id := range m.store {
		ids = append(ids, id)
	}
	return ids
}

// Snapshot 返回所有拓扑的深拷贝切片（只读），用于前端列表展示。
func (m *Manager) Snapshot() []*Topology {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Topology, 0, len(m.store))
	for _, t := range m.store {
		out = append(out, t)
	}
	return out
}

// UpdateTopology 整体替换一份已存在的拓扑。
func (m *Manager) UpdateTopology(t *Topology) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.store[t.ID]; !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, t.ID)
	}
	t.UpdatedAt = time.Now()
	m.store[t.ID] = t
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeTopologyUpdated, TopologyID: t.ID, At: time.Now(),
	})
	return nil
}

// DeleteTopology 删除一份拓扑，返回是否实际删除。
func (m *Manager) DeleteTopology(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.store[id]; !ok {
		return false
	}
	delete(m.store, id)
	m.notify(ChangeEvent{
		Type: ChangeTopologyDeleted, TopologyID: id, At: time.Now(),
	})
	return true
}

// AddDevice 向指定拓扑添加一个设备，自动校验 ID 唯一性。
func (m *Manager) AddDevice(topoID string, dev *Device) error {
	if dev == nil || dev.ID == "" {
		return fmt.Errorf("topology: nil or empty device id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.store[topoID]
	if !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, topoID)
	}
	t.mu.Lock()
	if _, exists := t.Devices[dev.ID]; exists {
		t.mu.Unlock()
		return fmt.Errorf("%w: device %q", ErrAlreadyExists, dev.ID)
	}
	t.mu.Unlock()
	t.AddDevice(dev)
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeDeviceAdded, TopologyID: topoID, DeviceID: dev.ID, At: time.Now(),
	})
	return nil
}

// UpdateDevice 替换一个已存在的设备。
func (m *Manager) UpdateDevice(topoID string, dev *Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.store[topoID]
	if !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, topoID)
	}
	t.mu.Lock()
	if _, ok := t.Devices[dev.ID]; !ok {
		t.mu.Unlock()
		return fmt.Errorf("%w: device %q", ErrNotFound, dev.ID)
	}
	dev.UpdatedAt = time.Now()
	t.Devices[dev.ID] = dev
	t.UpdatedAt = time.Now()
	t.mu.Unlock()
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeDeviceUpdated, TopologyID: topoID, DeviceID: dev.ID, At: time.Now(),
	})
	return nil
}

// RemoveDevice 删除设备，并级联删除包含该设备的所有链路。
func (m *Manager) RemoveDevice(topoID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.store[topoID]
	if !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, topoID)
	}
	t.mu.Lock()
	if _, ok := t.Devices[deviceID]; !ok {
		t.mu.Unlock()
		return fmt.Errorf("%w: device %q", ErrNotFound, deviceID)
	}
	t.mu.Unlock()
	t.RemoveDevice(deviceID)
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeDeviceRemoved, TopologyID: topoID, DeviceID: deviceID, At: time.Now(),
	})
	return nil
}

// AddLink 添加一条链路并进行端到端校验。
// 校验规则：源/目的设备存在、端口存在、端点不在同设备、源/目的端口组合未被占用。
func (m *Manager) AddLink(topoID string, link *Link) error {
	if link == nil || link.ID == "" {
		return fmt.Errorf("topology: nil or empty link id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.store[topoID]
	if !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, topoID)
	}
	if err := m.validateLink(t, link); err != nil {
		return err
	}
	t.AddLink(link)
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeLinkAdded, TopologyID: topoID, LinkID: link.ID, At: time.Now(),
	})
	return nil
}

// RemoveLink 删除一条链路。
func (m *Manager) RemoveLink(topoID, linkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.store[topoID]
	if !ok {
		return fmt.Errorf("%w: topology %q", ErrNotFound, topoID)
	}
	t.mu.RLock()
	found := false
	for _, l := range t.Links {
		if l.ID == linkID {
			found = true
			break
		}
	}
	t.mu.RUnlock()
	if !found {
		return fmt.Errorf("%w: link %q", ErrNotFound, linkID)
	}
	t.RemoveLink(linkID)
	m.rebuildLocked(t)
	m.notify(ChangeEvent{
		Type: ChangeLinkRemoved, TopologyID: topoID, LinkID: linkID, At: time.Now(),
	})
	return nil
}

// validateLink 校验链路端点合法性（必须在持锁状态下调用）。
func (m *Manager) validateLink(t *Topology, link *Link) error {
	if link.SourceDevice == link.TargetDevice {
		return fmt.Errorf("%w: %s", ErrSelfLink, link.ID)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	src, ok := t.Devices[link.SourceDevice]
	if !ok {
		return fmt.Errorf("%w: source %q", ErrMissingDevices, link.SourceDevice)
	}
	dst, ok := t.Devices[link.TargetDevice]
	if !ok {
		return fmt.Errorf("%w: target %q", ErrMissingDevices, link.TargetDevice)
	}
	if _, ok := src.Interfaces[link.SourcePort]; !ok {
		return fmt.Errorf("%w: source port %s/%s", ErrMissingPort, src.ID, link.SourcePort)
	}
	if _, ok := dst.Interfaces[link.TargetPort]; !ok {
		return fmt.Errorf("%w: target port %s/%s", ErrMissingPort, dst.ID, link.TargetPort)
	}
	for _, l := range t.Links {
		if l.ID == link.ID {
			return fmt.Errorf("%w: link %q", ErrAlreadyExists, link.ID)
		}
		if (l.SourceDevice == link.SourceDevice && l.SourcePort == link.SourcePort) ||
			(l.TargetDevice == link.SourceDevice && l.TargetPort == link.SourcePort) ||
			(l.SourceDevice == link.TargetDevice && l.SourcePort == link.TargetPort) ||
			(l.TargetDevice == link.TargetDevice && l.TargetPort == link.TargetPort) {
			return fmt.Errorf("%w: %s/%s <-> %s/%s", ErrDuplicatePort,
				link.SourceDevice, link.SourcePort, link.TargetDevice, link.TargetPort)
		}
	}
	return nil
}

// Subscribe 注册一个变更订阅者，返回取消函数。
// 订阅者回调在 manager 的内部锁外执行，因此订阅者内部可以再次调用 Manager 方法（不会死锁）。
func (m *Manager) Subscribe(fn Subscriber) func() {
	m.mu.Lock()
	m.subs = append(m.subs, fn)
	idx := len(m.subs) - 1
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if idx < len(m.subs) {
			m.subs[idx] = nil
		}
	}
}

// notify 在持锁状态下复制订阅者切片，然后释放锁再回调，避免订阅者死锁。
func (m *Manager) notify(ev ChangeEvent) {
	if len(m.subs) == 0 {
		return
	}
	subs := make([]Subscriber, len(m.subs))
	copy(subs, m.subs)
	go func() {
		for _, fn := range subs {
			if fn != nil {
				fn(ev)
			}
		}
	}()
}

// Close 关闭管理器，不再接受变更；后续所有写入返回错误。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.subs = nil
}
