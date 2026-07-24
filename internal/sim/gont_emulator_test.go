package sim

import (
	"testing"

	"ensp-lab/internal/testutil"
	"ensp-lab/internal/topology"
)

func TestNewGontEngine_NilTopology(t *testing.T) {
	t.Parallel()
	_, err := NewGontEngine(nil)
	if err == nil {
		t.Fatal("expected error for nil topology, got nil")
	}
}

func TestNewGontEngine_PlatformCheck(t *testing.T) {
	t.Parallel()

	testutil.CheckNetNSLimit(t)

	topo := topology.NewTopology("t", "T")
	eng, err := NewGontEngine(topo)
	if err == nil && eng != nil {
		testutil.EnsureEngineCleanup(t, eng)
	}
}

func TestNewEngine_FallbackOnNonLinux(t *testing.T) {
	t.Parallel()

	testutil.CheckNetNSLimit(t)

	topo := topology.NewTopology("fallback", "Fallback")
	eng, err := NewEngine(topo)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if eng == nil {
		t.Fatal("engine is nil")
	}
	testutil.EnsureEngineCleanup(t, eng)
}
