package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/logging"
	"ensp-lab/internal/topology"

	"go.uber.org/zap"
)

const defaultStorageDir = "./data"

// ErrInvalidTopoID 表示拓扑 ID 非法（含路径分隔符或 ".."），可能被用于路径穿越写文件。
var ErrInvalidTopoID = errors.New("storage: invalid topology id")

// topoFilePath 将拓扑 ID 安全地映射到存储目录下的文件名。
// 拒绝任何可能跳出存储目录的 ID（路径分隔符或 ".."），避免路径穿越写文件。
func topoFilePath(dir, id string) (string, error) {
	if id == "" {
		return "", ErrInvalidTopoID
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return "", ErrInvalidTopoID
	}
	return filepath.Join(dir, id+".json"), nil
}

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

	// 校验拓扑 ID 合法性，防止路径穿越写文件。
	filePath, err := topoFilePath(s.storageDir, t.ID)
	if err != nil {
		return err
	}
	// 原子写入：先写同目录临时文件，再 rename 覆盖。rename 在同一文件系统内是
	// 原子操作，避免进程崩溃/掉电时留下半截 JSON，导致下次启动 loadAll 静默跳过
	// 该拓扑（数据丢失）。临时文件与目标文件同目录，保证 rename 不跨卷。
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("storage: failed to write temp topology: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath) // 清理残留临时文件，避免留下 .tmp 垃圾
		return fmt.Errorf("storage: failed to commit topology: %w", err)
	}

	return nil
}

func (s *FileStorage) deleteTopologyFile(id string) error {
	filePath, err := topoFilePath(s.storageDir, id)
	if err != nil {
		return err
	}
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

// Flush 将内存中所有拓扑原子写盘。即使单个拓扑写盘失败也会尽力保存其余拓扑，
// 并最终返回首个错误。异常退出（panic/信号）时由 main 的 recover / 信号处理调用。
//
// 安全性：先持 RLock 拷贝出各拓扑的深拷贝快照，立即释放读锁，再在锁外序列化与写盘，
// 避免持锁做 I/O。panic 路径调用同样安全——Go 在 panic 展开栈时会执行各层 defer，
// 触发 panic 的函数所持有的锁会被其 defer 释放，到达 main 的 recover 时锁已空闲，
// 因此这里的 RLock 不会死锁。
func (s *FileStorage) Flush() error {
	s.mu.RLock()
	snaps := make([]*topology.Topology, 0, len(s.topologies))
	for _, t := range s.topologies {
		if t != nil {
			snaps = append(snaps, t.Clone())
		}
	}
	s.mu.RUnlock()

	var firstErr error
	for _, t := range snaps {
		if err := s.saveTopology(t); err != nil {
			logging.Error("FileStorage: flush topology failed",
				zap.String("id", t.ID), zap.Error(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// StartAutoSave 启动后台定时刷盘，直到 ctx 取消。用于兜底崩溃/掉电：
// 即便某次 in-place 修改未走 UpdateTopology，定时快照也能将内存态落盘。
// 刷盘失败仅记录日志，不中断循环。
func (s *FileStorage) StartAutoSave(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Flush(); err != nil {
					logging.Warn("FileStorage: auto-save failed", zap.Error(err))
				}
			}
		}
	}()
}
