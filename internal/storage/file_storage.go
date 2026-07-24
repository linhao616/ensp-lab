package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ensp-lab/internal/logging"
	"ensp-lab/internal/topology"

	"go.uber.org/zap"
)

const defaultStorageDir = "./data"

type FileStorage struct {
	mu         sync.RWMutex
	storageDir string
	topologies map[string]*topology.Topology
	events     map[string][]*PacketEventRecord
}

func NewFileStorage(storageDir string) *FileStorage {
	if storageDir == "" {
		storageDir = defaultStorageDir
	}

	if err := os.MkdirAll(storageDir, 0755); err != nil {
		logging.Error("FileStorage: failed to create storage directory",
			zap.String("dir", storageDir),
			zap.Error(err),
		)
	}

	fs := &FileStorage{
		storageDir: storageDir,
		topologies: make(map[string]*topology.Topology),
		events:     make(map[string][]*PacketEventRecord),
	}

	if err := fs.loadAll(); err != nil {
		logging.Warn("FileStorage: failed to load from disk, starting fresh",
			zap.String("dir", storageDir),
			zap.Error(err),
		)
	}

	return fs
}

func (s *FileStorage) GetTopology(id string) (*topology.Topology, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.topologies[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (s *FileStorage) ListTopologies() ([]*topology.Topology, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []*topology.Topology{}
	for _, t := range s.topologies {
		result = append(result, t)
	}
	return result, nil
}

func (s *FileStorage) CreateTopology(t *topology.Topology) error {
	// 前置校验：拒绝含非法 IP 配置的拓扑，避免脏数据在运行时 Ping 时才暴露为 HTTP 500。
	if errs := topology.ValidateIPConfig(t); len(errs) > 0 {
		return fmt.Errorf("%w: topology %q: %v", topology.ErrInvalidIPConfig, t.ID, errs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topologies[t.ID] = t
	return s.saveTopology(t)
}

func (s *FileStorage) UpdateTopology(t *topology.Topology) error {
	// 与 CreateTopology 对称：更新拓扑时也拒绝非法 IP 配置。
	if errs := topology.ValidateIPConfig(t); len(errs) > 0 {
		return fmt.Errorf("%w: topology %q: %v", topology.ErrInvalidIPConfig, t.ID, errs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topologies[t.ID] = t
	return s.saveTopology(t)
}

func (s *FileStorage) DeleteTopology(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.topologies, id)
	delete(s.events, id)
	return s.deleteTopologyFile(id)
}

func (s *FileStorage) AddPacketEvent(topologyID string, event *PacketEventRecord) error {
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

func (s *FileStorage) ListPacketEvents(topologyID string, limit int) ([]*PacketEventRecord, error) {
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

func (s *FileStorage) ClearPacketEvents(topologyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, topologyID)
	return nil
}

func (s *FileStorage) loadAll() error {
	files, err := filepath.Glob(filepath.Join(s.storageDir, "*.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			logging.Warn("FileStorage: failed to read topology file",
				zap.String("file", file),
				zap.Error(err),
			)
			continue
		}

		var t *topology.Topology
		if err := json.Unmarshal(data, &t); err != nil {
			logging.Warn("FileStorage: failed to unmarshal topology",
				zap.String("file", file),
				zap.Error(err),
			)
			continue
		}

		if t.ID != "" {
			// 向后兼容：历史脏数据仅告警、不阻断启动；新建拓扑由 CreateTopology 硬拒。
			if errs := topology.ValidateIPConfig(t); len(errs) > 0 {
				logging.Warn("FileStorage: topology loaded with invalid IP configuration",
					zap.String("id", t.ID),
					zap.Any("errors", errs),
				)
			}
			s.topologies[t.ID] = t
			logging.Info("FileStorage: loaded topology",
				zap.String("id", t.ID),
				zap.String("name", t.Name),
			)
		}
	}

	return nil
}

func (s *FileStorage) saveTopology(t *topology.Topology) error {
	if err := os.MkdirAll(s.storageDir, 0755); err != nil {
		return fmt.Errorf("storage: failed to create dir: %w", err)
	}

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: failed to marshal topology: %w", err)
	}

	filePath := filepath.Join(s.storageDir, t.ID+".json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("storage: failed to write topology: %w", err)
	}

	return nil
}

func (s *FileStorage) deleteTopologyFile(id string) error {
	filePath := filepath.Join(s.storageDir, id+".json")
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("storage: failed to delete topology file: %w", err)
	}
	return nil
}

func (s *FileStorage) StorageDir() string {
	return s.storageDir
}
