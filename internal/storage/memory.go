package storage

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/topology"
)

// PacketEventType 镜像 sim.PacketEventType，避免 storage 包直接依赖 sim 包。
//
// 保留字符串底层类型以便 API 层在 sim.PacketEventType 与本类型之间无损转换。
type PacketEventType string

// PacketEventRecord 镜像 sim.PacketEvent 的字段集合，作为 storage 层
// 持久化包事件历史的本地表示。由 API 层负责 sim.PacketEvent 与本结构体
// 之间的相互转换，从而保持 storage 包对 sim 包的零依赖。
type PacketEventRecord struct {
	PacketID    string
	Type        PacketEventType
	DeviceID    string
	Interface   string
	Timestamp   time.Time
	Description string
	Path        []string
}

// maxPacketEventsPerTopology 限制单个拓扑缓存的包事件条数，
// 超过后按 FIFO 滚动丢弃最早记录，避免内存无界增长。
const maxPacketEventsPerTopology = 1000

type Storage interface {
	GetTopology(id string) (*topology.Topology, error)
	ListTopologies() ([]*topology.Topology, error)
	CreateTopology(t *topology.Topology) error
	UpdateTopology(t *topology.Topology) error
	DeleteTopology(id string) error

	// AddPacketEvent 追加一个包事件到指定拓扑的历史记录。
	AddPacketEvent(topologyID string, event *PacketEventRecord) error
	// ListPacketEvents 返回指定拓扑的包事件历史，limit<=0 表示返回全部。
	ListPacketEvents(topologyID string, limit int) ([]*PacketEventRecord, error)
	// ClearPacketEvents 清空指定拓扑的包事件历史。
	ClearPacketEvents(topologyID string) error
}

type MemoryStorage struct {
	mu         sync.RWMutex
	topologies map[string]*topology.Topology
	events     map[string][]*PacketEventRecord
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		topologies: make(map[string]*topology.Topology),
		events:     make(map[string][]*PacketEventRecord),
	}
}

func (s *MemoryStorage) GetTopology(id string) (*topology.Topology, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.topologies[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (s *MemoryStorage) ListTopologies() ([]*topology.Topology, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []*topology.Topology{}
	for _, t := range s.topologies {
		result = append(result, t)
	}
	return result, nil
}

func (s *MemoryStorage) CreateTopology(t *topology.Topology) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topologies[t.ID] = t
	return nil
}

func (s *MemoryStorage) UpdateTopology(t *topology.Topology) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topologies[t.ID] = t
	return nil
}

func (s *MemoryStorage) DeleteTopology(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.topologies, id)
	delete(s.events, id)
	return nil
}

// AddPacketEvent 追加一条包事件记录到指定拓扑的历史，超过上限后 FIFO 滚动。
func (s *MemoryStorage) AddPacketEvent(topologyID string, event *PacketEventRecord) error {
	if event == nil {
		return fmt.Errorf("storage: nil packet event")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := s.events[topologyID]
	buf = append(buf, event)
	if len(buf) > maxPacketEventsPerTopology {
		buf = buf[len(buf)-maxPacketEventsPerTopology:]
	}
	s.events[topologyID] = buf
	return nil
}

// ListPacketEvents 返回指定拓扑的包事件历史。
// limit <= 0 表示返回全部；否则返回最近 limit 条（按时间顺序）。
func (s *MemoryStorage) ListPacketEvents(topologyID string, limit int) ([]*PacketEventRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	buf := s.events[topologyID]
	if len(buf) == 0 {
		return []*PacketEventRecord{}, nil
	}
	if limit <= 0 || limit >= len(buf) {
		out := make([]*PacketEventRecord, len(buf))
		copy(out, buf)
		return out, nil
	}
	start := len(buf) - limit
	out := make([]*PacketEventRecord, limit)
	copy(out, buf[start:])
	return out, nil
}

// ClearPacketEvents 清空指定拓扑的包事件历史。
func (s *MemoryStorage) ClearPacketEvents(topologyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, topologyID)
	return nil
}
