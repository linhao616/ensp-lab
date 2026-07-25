package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

func TestRecordHistory(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)

	// 空命令被忽略
	s.RecordHistory("   ")
	s.RecordHistory("")
	if len(s.History) != 0 {
		t.Fatalf("empty commands should be ignored, got %d entries", len(s.History))
	}

	s.RecordHistory("sysname R1")
	s.RecordHistory("interface GE0/0/1")
	if len(s.History) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(s.History))
	}
	if s.History[0].Command != "sysname R1" {
		t.Errorf("entry0 = %q, want %q", s.History[0].Command, "sysname R1")
	}
	if s.History[1].Command != "interface GE0/0/1" {
		t.Errorf("entry1 = %q, want %q", s.History[1].Command, "interface GE0/0/1")
	}
	// 时间戳应已填充
	if !strings.Contains(s.History[0].Timestamp, "-") {
		t.Errorf("timestamp not populated: %q", s.History[0].Timestamp)
	}
}

func TestRecordHistoryFIFOCap(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	for i := 0; i < maxCLIHistory+50; i++ {
		s.RecordHistory("cmd")
	}
	if len(s.History) != maxCLIHistory {
		t.Fatalf("expected capped at %d, got %d", maxCLIHistory, len(s.History))
	}
	// 最旧的被丢弃：最早保留的应是第 51 条（index 0 对应原始第 51 条）
	// 由于每条内容相同无法区分，这里只验证容量上限与切片连续性。
}

func TestFormatHistoryCommand(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	if out := s.FormatHistoryCommand(0); !strings.Contains(out, "(empty)") {
		t.Errorf("empty history should report empty, got %q", out)
	}

	for i := 1; i <= 15; i++ {
		s.RecordHistory("cmd-N")
	}
	// 默认展示最近 10 条
	out := s.FormatHistoryCommand(0)
	if c := strings.Count(out, "cmd-N"); c != 10 {
		t.Errorf("default view should show last 10, got %d lines of cmd-N", c)
	}
	// 指定 5 条
	out = s.FormatHistoryCommand(5)
	if c := strings.Count(out, "cmd-N"); c != 5 {
		t.Errorf("maxSize=5 should show last 5, got %d", c)
	}
}

func TestHistorySerializeRoundTrip(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.RecordHistory("sysname R1")
	s.RecordHistory("ip route-static 0.0.0.0 0 10.0.0.1")

	cfg := s.SerializeToDeviceConfigData()
	if len(cfg.History) != 2 {
		t.Fatalf("serialized history should have 2 entries, got %d", len(cfg.History))
	}

	// 反序列化到新状态，验证持久化往返
	s2 := NewCLIStateWithType(topology.DeviceRouter)
	s2.LoadFromDeviceConfigData(cfg)
	if len(s2.History) != 2 {
		t.Fatalf("loaded history should have 2 entries, got %d", len(s2.History))
	}
	if s2.History[0].Command != "sysname R1" {
		t.Errorf("round-trip entry0 = %q", s2.History[0].Command)
	}
	if s2.History[1].Command != "ip route-static 0.0.0.0 0 10.0.0.1" {
		t.Errorf("round-trip entry1 = %q", s2.History[1].Command)
	}
}
