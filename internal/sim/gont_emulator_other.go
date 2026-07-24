//go:build !linux

// Package sim: gont emulator fallback for non-Linux platforms.
//
// gont depends on Linux network namespaces (netlink, netns, nftables)
// and cannot be compiled on Windows or macOS. This file provides the
// same constructor surface but returns ErrPlatformNotSupported so
// that callers can fall back to the ns-x pure simulation engine.

package sim

import (
	"fmt"

	"ensp-lab/internal/topology"
)

// ErrPlatformNotSupported is returned by NewGontEngine when the
// current platform does not support gont (i.e. anything other than
// Linux). The variable is declared here, on the non-Linux build,
// so that callers always see a stable error regardless of the target
// platform.
var ErrPlatformNotSupported = fmt.Errorf("sim: gont emulator requires Linux")

// NewGontEngine is the non-Linux stub for the gont-backed Engine.
//
// On Windows/macOS the gont library cannot be imported; this stub
// returns ErrPlatformNotSupported so that the caller (typically
// cmd/server/main.go) can fall back to NewNSxEngine.
func NewGontEngine(topo *topology.Topology) (Engine, error) {
	return nil, ErrPlatformNotSupported
}
