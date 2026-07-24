package protocol

import (
	"fmt"
	"strings"
	"sync"

	"ensp-lab/internal/sim"
)

type MPLSProtocol struct {
	Enabled    bool
	DeviceID   string
	LSRID      string
	LSPs       map[string]*LSPTunnel
	LabelTable map[int]*LabelEntry
	mu         sync.RWMutex
}

type LSPTunnel struct {
	Name        string
	Destination string
	NextHop     string
	OutLabel    int
	InLabel     int
	Status      string
	Path        []string
}

type LabelEntry struct {
	Label       int
	Destination string
	NextHop     string
	Interface   string
	OutLabel    int
	Status      string
}

func NewMPLSProtocol(deviceID string) *MPLSProtocol {
	return &MPLSProtocol{
		DeviceID:   deviceID,
		Enabled:    false,
		LSRID:      "",
		LSPs:       make(map[string]*LSPTunnel),
		LabelTable: make(map[int]*LabelEntry),
	}
}

func (m *MPLSProtocol) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Enabled = true
}

func (m *MPLSProtocol) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Enabled = false
}

func (m *MPLSProtocol) SetLSRID(lsrID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LSRID = lsrID
}

func (m *MPLSProtocol) CreateLSP(name, destination, nextHop string, outLabel, inLabel int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LSPs[name] = &LSPTunnel{
		Name:        name,
		Destination: destination,
		NextHop:     nextHop,
		OutLabel:    outLabel,
		InLabel:     inLabel,
		Status:      "up",
		Path:        []string{},
	}
}

func (m *MPLSProtocol) DeleteLSP(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.LSPs, name)
}

func (m *MPLSProtocol) AddLabel(label int, destination, nextHop, iface string, outLabel int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LabelTable[label] = &LabelEntry{
		Label:       label,
		Destination: destination,
		NextHop:     nextHop,
		Interface:   iface,
		OutLabel:    outLabel,
		Status:      "active",
	}
}

func (m *MPLSProtocol) GetStatus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString("MPLS Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[m.Enabled]))
	sb.WriteString(fmt.Sprintf("  LSR ID: %s\n", m.LSRID))
	if len(m.LSPs) > 0 {
		sb.WriteString("  LSP Tunnels:\n")
		for name, lsp := range m.LSPs {
			sb.WriteString(fmt.Sprintf("    %s -> %s (OutLabel: %d, InLabel: %d, Status: %s)\n",
				name, lsp.Destination, lsp.OutLabel, lsp.InLabel, lsp.Status))
		}
	}
	if len(m.LabelTable) > 0 {
		sb.WriteString("  Label Table:\n")
		for label, entry := range m.LabelTable {
			sb.WriteString(fmt.Sprintf("    Label %d -> %s (NextHop: %s, OutLabel: %d)\n",
				label, entry.Destination, entry.NextHop, entry.OutLabel))
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
func (m *MPLSProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that MPLSProtocol satisfies Handler.
var _ Handler = (*MPLSProtocol)(nil)
