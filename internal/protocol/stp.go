package protocol

import (
	"fmt"
	"net"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

// STPProtocol represents a Spanning Tree Protocol instance
type STPProtocol struct {
	BridgeID       string
	Priority       int
	RootBridgeID   string
	RootPathCost   int
	DesignatedPort string
	Interfaces     map[string]*STPInterface
	Enabled        bool
	mu             sync.RWMutex
}

// STPInterface represents an interface running STP
type STPInterface struct {
	Name               string
	PortID             string
	State              string // Disabled, Blocking, Listening, Learning, Forwarding, Broken
	Role               string // Root, Designated, Alternate, Backup, Disabled
	Priority           int
	Cost               int
	DesignatedBridgeID string
	DesignatedPortID   string
	DesignatedCost     int
	ForwardDelay       time.Duration
	MaxAge             time.Duration
	HelloTime          time.Duration
	TopologyChangeAck  bool
}

// BPDU represents a Bridge Protocol Data Unit
type BPDU struct {
	ProtocolID         uint16
	ProtocolVersionID  uint8
	BPDUType           uint8
	Flags              uint8
	RootBridgeID       string
	RootPathCost       int
	DesignatedBridgeID string
	DesignatedPortID   string
	MessageAge         uint16
	MaxAge             uint16
	HelloTime          uint16
	ForwardDelay       uint16
}

// NewSTPProtocol creates a new STP instance
func NewSTPProtocol(bridgeID string, priority int) *STPProtocol {
	return &STPProtocol{
		BridgeID:     bridgeID,
		Priority:     priority,
		RootBridgeID: bridgeID,
		RootPathCost: 0,
		Interfaces:   make(map[string]*STPInterface),
		Enabled:      false,
	}
}

// Enable starts STP
func (s *STPProtocol) Enable() {
	s.mu.Lock()
	s.Enabled = true
	s.mu.Unlock()
	go s.helloLoop()
}

// Disable stops STP
func (s *STPProtocol) Disable() {
	s.mu.Lock()
	s.Enabled = false
	s.mu.Unlock()
}

// AddInterface adds an interface to STP
func (s *STPProtocol) AddInterface(name string, priority int, cost int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Interfaces[name] = &STPInterface{
		Name:               name,
		PortID:             fmt.Sprintf("%04x", priority) + ":" + name,
		State:              "Blocking",
		Role:               "Designated",
		Priority:           priority,
		Cost:               cost,
		DesignatedBridgeID: s.BridgeID,
		DesignatedPortID:   fmt.Sprintf("%04x", priority) + ":" + name,
		DesignatedCost:     0,
		ForwardDelay:       15 * time.Second,
		MaxAge:             20 * time.Second,
		HelloTime:          2 * time.Second,
	}
}

// helloLoop sends BPDU periodically
func (s *STPProtocol) helloLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		s.mu.RLock()
		if !s.Enabled {
			s.mu.RUnlock()
			return
		}
		s.mu.RUnlock()

		s.sendBPDUs()
		<-ticker.C
	}
}

// sendBPDUs sends BPDUs on all interfaces
func (s *STPProtocol) sendBPDUs() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, iface := range s.Interfaces {
		if iface.State == "Disabled" || iface.State == "Blocking" {
			continue
		}

		bpdu := &BPDU{
			ProtocolID:         0,
			ProtocolVersionID:  0,
			BPDUType:           0,
			Flags:              0,
			RootBridgeID:       s.RootBridgeID,
			RootPathCost:       s.RootPathCost,
			DesignatedBridgeID: s.BridgeID,
			DesignatedPortID:   iface.PortID,
			MessageAge:         0,
			MaxAge:             uint16(s.Interfaces[iface.Name].MaxAge.Seconds()),
			HelloTime:          uint16(s.Interfaces[iface.Name].HelloTime.Seconds()),
			ForwardDelay:       uint16(s.Interfaces[iface.Name].ForwardDelay.Seconds()),
		}

		s.broadcastBPDU(iface, bpdu)
	}
}

// broadcastBPDU broadcasts a BPDU
func (s *STPProtocol) broadcastBPDU(iface *STPInterface, bpdu *BPDU) {
	fmt.Printf("[STP] %s: Sending BPDU on %s, Root: %s, Cost: %d\n",
		s.BridgeID, iface.Name, bpdu.RootBridgeID, bpdu.RootPathCost)
}

// ReceiveBPDU processes a received BPDU
func (s *STPProtocol) ReceiveBPDU(ifaceName string, bpdu *BPDU, sourceIP net.IP) {
	s.mu.Lock()
	defer s.mu.Unlock()

	iface, ok := s.Interfaces[ifaceName]
	if !ok {
		return
	}

	// Compare root bridge IDs
	if bpdu.RootBridgeID < s.RootBridgeID {
		// New root found
		s.RootBridgeID = bpdu.RootBridgeID
		s.RootPathCost = bpdu.RootPathCost + iface.Cost

		// Update designated info
		iface.DesignatedBridgeID = bpdu.DesignatedBridgeID
		iface.DesignatedPortID = bpdu.DesignatedPortID
		iface.DesignatedCost = bpdu.RootPathCost

		// This port becomes root port
		iface.Role = "Root"
		s.DesignatedPort = ifaceName

		fmt.Printf("[STP] %s: New root %s via %s, path cost %d\n",
			s.BridgeID, s.RootBridgeID, ifaceName, s.RootPathCost)
	} else if bpdu.RootBridgeID == s.RootBridgeID {
		// Same root, check path cost
		newCost := bpdu.RootPathCost + iface.Cost
		if newCost < s.RootPathCost {
			s.RootPathCost = newCost
			iface.Role = "Root"
			s.DesignatedPort = ifaceName
		}
	}

	// Determine port state based on role
	if iface.Role == "Root" {
		// Root port should forward
		if iface.State != "Forwarding" {
			iface.State = "Listening"
			go s.transitionToForwarding(ifaceName)
		}
	} else if iface.Role == "Designated" {
		// Designated port should forward
		if iface.State != "Forwarding" {
			iface.State = "Listening"
			go s.transitionToForwarding(ifaceName)
		}
	} else {
		// Alternate/Backup port should block
		iface.State = "Blocking"
	}
}

// transitionToForwarding handles port state transitions
func (s *STPProtocol) transitionToForwarding(ifaceName string) {
	s.mu.Lock()
	iface, ok := s.Interfaces[ifaceName]
	if !ok {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Listening state
	time.Sleep(iface.ForwardDelay)

	s.mu.Lock()
	iface, ok = s.Interfaces[ifaceName]
	if !ok || iface.State != "Listening" {
		s.mu.Unlock()
		return
	}
	iface.State = "Learning"
	s.mu.Unlock()

	// Learning state
	time.Sleep(iface.ForwardDelay)

	s.mu.Lock()
	iface, ok = s.Interfaces[ifaceName]
	if !ok || iface.State != "Learning" {
		s.mu.Unlock()
		return
	}
	iface.State = "Forwarding"
	s.mu.Unlock()

	fmt.Printf("[STP] %s: Port %s transitioned to Forwarding\n", s.BridgeID, ifaceName)
}

// IsRootBridge checks if this is the root bridge
func (s *STPProtocol) IsRootBridge() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RootBridgeID == s.BridgeID
}

// GetInterfaces returns STP interfaces
func (s *STPProtocol) GetInterfaces() []*STPInterface {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []*STPInterface{}
	for _, iface := range s.Interfaces {
		result = append(result, iface)
	}
	return result
}

// GetRootInfo returns root bridge information
func (s *STPProtocol) GetRootInfo() (string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RootBridgeID, s.RootPathCost
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (s *STPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that STPProtocol satisfies Handler.
var _ Handler = (*STPProtocol)(nil)
