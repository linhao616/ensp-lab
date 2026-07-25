package metrics

import (
	"testing"
	"time"
)

func TestCountersIncrement(t *testing.T) {
	c := NewCollector()
	c.Start()

	c.IncrPacket()
	c.IncrPacket()
	if got := c.Snapshot().PacketsProcessed; got != 2 {
		t.Fatalf("PacketsProcessed = %d, want 2", got)
	}

	c.AddPingsActive(1)
	c.AddPingsActive(1)
	if got := c.Snapshot().PingsActive; got != 2 {
		t.Fatalf("PingsActive = %d, want 2", got)
	}
	c.AddPingsActive(-1)
	if got := c.Snapshot().PingsActive; got != 1 {
		t.Fatalf("PingsActive = %d, want 1", got)
	}

	c.IncrPing(false)
	c.IncrPing(true)
	s := c.Snapshot()
	if s.PingsTotal != 2 {
		t.Fatalf("PingsTotal = %d, want 2", s.PingsTotal)
	}
	if s.PingsTimeout != 1 {
		t.Fatalf("PingsTimeout = %d, want 1", s.PingsTimeout)
	}

	c.IncrTopoMutation()
	c.RecordRebuild(5 * time.Millisecond)
	s = c.Snapshot()
	if s.TopoMutations != 1 {
		t.Fatalf("TopoMutations = %d, want 1", s.TopoMutations)
	}
	if s.RebuildsTotal != 1 {
		t.Fatalf("RebuildsTotal = %d, want 1", s.RebuildsTotal)
	}
	if s.RebuildLastMs <= 0 {
		t.Fatalf("RebuildLastMs = %v, want > 0", s.RebuildLastMs)
	}
	if s.RebuildBusyMs <= 0 {
		t.Fatalf("RebuildBusyMs = %v, want > 0", s.RebuildBusyMs)
	}
}

func TestPendingMaxIsMonotonic(t *testing.T) {
	c := NewCollector()
	c.NotePending(3)
	c.NotePending(1) // 更小，不应回退
	if got := c.Snapshot().PendingEventMax; got != 3 {
		t.Fatalf("PendingEventMax = %d, want 3", got)
	}
	c.NotePending(10)
	if got := c.Snapshot().PendingEventMax; got != 10 {
		t.Fatalf("PendingEventMax = %d, want 10", got)
	}
}

func TestDiagnoseFlagsRebuildBurst(t *testing.T) {
	c := NewCollector()
	now := time.Now()
	c.windowMu.Lock()
	for i := 0; i < 5; i++ {
		c.rebuildWindow = append(c.rebuildWindow, now.UnixMilli()-int64(i*100)) // 近 0.5s 内 5 次
	}
	c.windowMu.Unlock()

	s := c.Snapshot()
	found := false
	for _, d := range s.Diagnosis {
		if containsStr(d, "拓扑编辑突发") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rebuild-burst diagnosis, got %v", s.Diagnosis)
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
