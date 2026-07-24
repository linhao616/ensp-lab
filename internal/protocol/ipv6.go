package protocol

import (
	"fmt"
	"strings"
	"sync"

	"ensp-lab/internal/sim"
)

type IPv6Protocol struct {
	Enabled    bool
	DeviceID   string
	Interfaces map[string]*IPv6Interface
	Routes     []*IPv6RouteEntry
	Forwarding bool
	mu         sync.RWMutex
}

type IPv6Interface struct {
	Name      string
	Enabled   bool
	Addresses []string
	Status    string
}

type IPv6RouteEntry struct {
	Destination string
	NextHop     string
	Interface   string
	Metric      int
	Protocol    string
}

func NewIPv6Protocol(deviceID string) *IPv6Protocol {
	return &IPv6Protocol{
		DeviceID:   deviceID,
		Enabled:    false,
		Interfaces: make(map[string]*IPv6Interface),
		Routes:     []*IPv6RouteEntry{},
		Forwarding: false,
	}
}

func (i *IPv6Protocol) Enable() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Enabled = true
}

func (i *IPv6Protocol) Disable() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Enabled = false
}

func (i *IPv6Protocol) EnableForwarding() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Forwarding = true
}

func (i *IPv6Protocol) DisableForwarding() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Forwarding = false
}

func (i *IPv6Protocol) AddInterface(ifaceName string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.Interfaces[ifaceName]; !ok {
		i.Interfaces[ifaceName] = &IPv6Interface{
			Name:      ifaceName,
			Enabled:   true,
			Addresses: []string{},
			Status:    "up",
		}
	}
}

func (i *IPv6Protocol) AddAddress(ifaceName, address string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if iface, ok := i.Interfaces[ifaceName]; ok {
		for _, addr := range iface.Addresses {
			if addr == address {
				return
			}
		}
		iface.Addresses = append(iface.Addresses, address)
	}
}

func (i *IPv6Protocol) AddRoute(destination, nextHop, iface string, metric int, protocol string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Routes = append(i.Routes, &IPv6RouteEntry{
		Destination: destination,
		NextHop:     nextHop,
		Interface:   iface,
		Metric:      metric,
		Protocol:    protocol,
	})
}

func (i *IPv6Protocol) GetStatus() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString("IPv6 Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[i.Enabled]))
	sb.WriteString(fmt.Sprintf("  Forwarding: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[i.Forwarding]))
	if len(i.Interfaces) > 0 {
		sb.WriteString("  Interfaces:\n")
		for name, iface := range i.Interfaces {
			sb.WriteString(fmt.Sprintf("    %s: Status=%s\n", name, iface.Status))
			for _, addr := range iface.Addresses {
				sb.WriteString(fmt.Sprintf("         %s\n", addr))
			}
		}
	}
	if len(i.Routes) > 0 {
		sb.WriteString("  Routes:\n")
		for _, route := range i.Routes {
			sb.WriteString(fmt.Sprintf("    %s -> %s (Interface: %s, Metric: %d, Protocol: %s)\n",
				route.Destination, route.NextHop, route.Interface, route.Metric, route.Protocol))
		}
	}
	return sb.String()
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (i *IPv6Protocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that IPv6Protocol satisfies Handler.
var _ Handler = (*IPv6Protocol)(nil)
