//go:build ignore

// Package engine contains legacy code that has been superseded by internal/sim.
//
// SimpleSwitch is a standalone gont-based topology builder that was created
// as a proof-of-concept before the unified sim.Engine interface was established.
// It is NOT integrated with the API layer or the topology management system.
//
// For production use, refer to:
//   - internal/sim/engine.go      - Unified Engine interface
//   - internal/sim/gont_emulator.go - Linux gont implementation
//   - internal/sim/engine_nsx.go  - Cross-platform ns-x implementation
//   - internal/api/router.go      - API integration with sim.Engine
package engine

import (
	"fmt"

	"github.com/stv0g/gont/v2/pkg"
	"github.com/stv0g/gont/v2/pkg/options"
)

type SimpleSwitch struct {
	network *gont.Network
	ns1     *gont.Host
	ns2     *gont.Host
	sw      *gont.Switch
}

func NewSimpleSwitch() (*SimpleSwitch, error) {
	n, err := gont.NewNetwork("simple-switch")
	if err != nil {
		return nil, fmt.Errorf("create network: %w", err)
	}

	s := &SimpleSwitch{
		network: n,
	}

	if err := s.build(); err != nil {
		_ = n.Close()
		return nil, fmt.Errorf("build topology: %w", err)
	}

	return s, nil
}

func (s *SimpleSwitch) build() error {
	var err error

	s.ns1, err = s.network.AddHost("ns1")
	if err != nil {
		return fmt.Errorf("add ns1: %w", err)
	}

	s.ns2, err = s.network.AddHost("ns2")
	if err != nil {
		return fmt.Errorf("add ns2: %w", err)
	}

	s.sw, err = s.network.AddSwitch("switch1")
	if err != nil {
		return fmt.Errorf("add switch: %w", err)
	}

	_, err = s.network.AddLink(
		options.Interface("eth0", s.ns1, options.AddressIPv4(192, 168, 1, 1, 24)),
		options.Interface("eth0", s.sw),
	)
	if err != nil {
		return fmt.Errorf("add link ns1-switch: %w", err)
	}

	_, err = s.network.AddLink(
		options.Interface("eth0", s.ns2, options.AddressIPv4(192, 168, 1, 2, 24)),
		options.Interface("eth1", s.sw),
	)
	if err != nil {
		return fmt.Errorf("add link ns2-switch: %w", err)
	}

	return nil
}

func (s *SimpleSwitch) PingTest() (string, error) {
	result, err := s.ns1.Ping(s.ns2)
	if err != nil {
		return "", fmt.Errorf("ping failed: %w", err)
	}
	return result.String(), nil
}

func (s *SimpleSwitch) Close() error {
	return s.network.Close()
}