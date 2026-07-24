//go:build !linux

package api

import (
	"testing"
)

func TestEngineIntegration(t *testing.T) {
	t.Skip("Engine integration test requires Linux")
}

func TestTopologyEngineIntegration(t *testing.T) {
	t.Skip("Topology engine integration test requires Linux")
}
