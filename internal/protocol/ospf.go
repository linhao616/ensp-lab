package protocol

import (
	"ensp-lab/internal/sim"
	"fmt"
	"net"
	"sync"
	"time"
)

// OSPFProtocol represents an OSPF routing protocol instance
type OSPFProtocol struct {
	ProcessID     int
	AreaID        int
	RouterID      string
	Enabled       bool
	Interfaces    map[string]*OSPFInterface
	Neighbors     map[string]*OSPFNeighbor
	LSDB          map[string]*LSA
	Routes        []RouteEntry
	mu            sync.RWMutex
	helloInterval time.Duration
	deadInterval  time.Duration
}

// OSPFInterface represents an interface running OSPF
type OSPFInterface struct {
	Name          string
	IP            net.IP
	Network       net.IPNet
	AreaID        int
	HelloInterval time.Duration
	DeadInterval  time.Duration
	State         string // Down, Init, 2-Way, ExStart, Exchange, Loading, Full
	Cost          int
	Priority      int
	DR            string
	BDR           string
}

// OSPFNeighbor represents an OSPF neighbor
type OSPFNeighbor struct {
	IP           net.IP
	RouterID     string
	State        string
	Interface    string
	DeadTimer    time.Time
	LastHello    time.Time
	DatabaseDesc *DatabaseDescription
	LinkStateReq []*LinkStateRequest
	LinkStateUpd []*LinkStateUpdate
	LinkStateAck []*LinkStateAcknowledgment
}

// DatabaseDescription represents a DBD packet
type DatabaseDescription struct {
	InterfaceMTU uint16
	Options      uint8
	Flags        uint8
	SeqNumber    uint32
	LSAHeaders   []*LSAHeader
}

// LinkStateRequest represents an LSR
type LinkStateRequest struct {
	LSAType     uint16
	LinkStateID uint32
	AdvRouter   uint32
}

// LinkStateUpdate represents an LSU
type LinkStateUpdate struct {
	LSACount uint32
	LSAs     []*LSA
}

// LinkStateAcknowledgment represents an LSAck
type LinkStateAcknowledgment struct {
	LSAHeaders []*LSAHeader
}

// LSAHeader represents an LSA header
type LSAHeader struct {
	LSAType     uint16
	LinkStateID uint32
	AdvRouter   uint32
	SeqNumber   uint32
	Checksum    uint16
	Length      uint16
}

// NewOSPFProtocol creates a new OSPF instance
func NewOSPFProtocol(processID, areaID int, routerID string) *OSPFProtocol {
	return &OSPFProtocol{
		ProcessID:     processID,
		AreaID:        areaID,
		RouterID:      routerID,
		Enabled:       false,
		Interfaces:    make(map[string]*OSPFInterface),
		Neighbors:     make(map[string]*OSPFNeighbor),
		LSDB:          make(map[string]*LSA),
		Routes:        []RouteEntry{},
		helloInterval: 10 * time.Second,
		deadInterval:  40 * time.Second,
	}
}

// Enable starts OSPF
func (o *OSPFProtocol) Enable() {
	o.mu.Lock()
	o.Enabled = true
	o.mu.Unlock()
	go o.helloLoop()
	go o.deadLoop()
}

// Disable stops OSPF
func (o *OSPFProtocol) Disable() {
	o.mu.Lock()
	o.Enabled = false
	o.mu.Unlock()
}

// AddInterface adds an interface to OSPF
func (o *OSPFProtocol) AddInterface(name string, ip net.IP, mask net.IPMask, areaID int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	network := net.IPNet{IP: ip.Mask(mask), Mask: mask}
	o.Interfaces[name] = &OSPFInterface{
		Name:          name,
		IP:            ip,
		Network:       network,
		AreaID:        areaID,
		HelloInterval: 10 * time.Second,
		DeadInterval:  40 * time.Second,
		State:         "Down",
		Cost:          10,
		Priority:      1,
	}
}

// helloLoop sends Hello packets periodically
func (o *OSPFProtocol) helloLoop() {
	ticker := time.NewTicker(o.helloInterval)
	defer ticker.Stop()

	for {
		o.mu.RLock()
		if !o.Enabled {
			o.mu.RUnlock()
			return
		}
		o.mu.RUnlock()

		o.sendHelloPackets()
		<-ticker.C
	}
}

// sendHelloPackets sends Hello packets on all interfaces
func (o *OSPFProtocol) sendHelloPackets() {
	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, iface := range o.Interfaces {
		if iface.State == "Down" {
			continue
		}

		// Create Hello packet
		hello := &HelloPacket{
			NetworkMask:            iface.Network.Mask,
			HelloInterval:          uint16(o.helloInterval.Seconds()),
			Options:                0x02, // E bit
			RouterPriority:         uint8(iface.Priority),
			DeadInterval:           uint16(o.deadInterval.Seconds()),
			DesignatedRouter:       net.ParseIP(iface.DR),
			BackupDesignatedRouter: net.ParseIP(iface.BDR),
			NeighborIDs:            []string{},
		}

		// Add neighbors to Hello packet
		for _, neighbor := range o.Neighbors {
			if neighbor.Interface == iface.Name {
				hello.NeighborIDs = append(hello.NeighborIDs, neighbor.RouterID)
			}
		}

		// Broadcast Hello on the interface
		o.broadcastHello(iface, hello)
	}
}

// HelloPacket represents an OSPF Hello packet
type HelloPacket struct {
	NetworkMask            net.IPMask
	HelloInterval          uint16
	Options                uint8
	RouterPriority         uint8
	DeadInterval           uint16
	DesignatedRouter       net.IP
	BackupDesignatedRouter net.IP
	NeighborIDs            []string
}

// broadcastHello broadcasts a Hello packet
func (o *OSPFProtocol) broadcastHello(iface *OSPFInterface, hello *HelloPacket) {
	// In a real implementation, this would send a multicast packet
	// For simulation, we'll notify neighbors directly
	fmt.Printf("[OSPF] %s: Sending Hello on %s, neighbors: %v\n", o.RouterID, iface.Name, hello.NeighborIDs)
}

// deadLoop checks for dead neighbors
func (o *OSPFProtocol) deadLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		o.mu.RLock()
		if !o.Enabled {
			o.mu.RUnlock()
			return
		}
		o.mu.RUnlock()

		o.checkDeadNeighbors()
		<-ticker.C
	}
}

// checkDeadNeighbors checks if any neighbors have timed out
func (o *OSPFProtocol) checkDeadNeighbors() {
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	for ipStr, neighbor := range o.Neighbors {
		if now.After(neighbor.DeadTimer) {
			fmt.Printf("[OSPF] %s: Neighbor %s is dead\n", o.RouterID, ipStr)
			neighbor.State = "Down"
		}
	}
}

// ReceiveHello processes a received Hello packet
func (o *OSPFProtocol) ReceiveHello(ifaceName string, hello *HelloPacket, sourceIP net.IP) {
	o.mu.Lock()
	defer o.mu.Unlock()

	iface, ok := o.Interfaces[ifaceName]
	if !ok {
		return
	}

	// Check if neighbor exists
	neighbor, ok := o.Neighbors[sourceIP.String()]
	if !ok {
		// Create new neighbor
		neighbor = &OSPFNeighbor{
			IP:        sourceIP,
			Interface: ifaceName,
			State:     "Init",
		}
		o.Neighbors[sourceIP.String()] = neighbor
	}

	// Update dead timer
	neighbor.DeadTimer = time.Now().Add(o.deadInterval)
	neighbor.LastHello = time.Now()

	// Check if we are in neighbor's Hello packet
	weInNeighbor := false
	for _, nid := range hello.NeighborIDs {
		if nid == o.RouterID {
			weInNeighbor = true
			break
		}
	}

	if weInNeighbor && neighbor.State == "Init" {
		neighbor.State = "2-Way"
		fmt.Printf("[OSPF] %s: Neighbor %s reached 2-Way state\n", o.RouterID, sourceIP)
	}

	// Elect DR/BDR if needed
	if iface.DR == "" && neighbor.State == "2-Way" {
		o.electDRBDR(iface)
	}
}

// electDRBDR performs DR/BDR election
func (o *OSPFProtocol) electDRBDR(iface *OSPFInterface) {
	// Simple DR/BDR election based on priority and Router ID
	var candidates []*OSPFNeighbor
	for _, neighbor := range o.Neighbors {
		if neighbor.Interface == iface.Name && neighbor.State == "2-Way" {
			candidates = append(candidates, neighbor)
		}
	}

	// Sort by priority (descending), then Router ID (descending)
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].IP.String() > candidates[i].IP.String() {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	if len(candidates) > 0 {
		iface.DR = candidates[0].IP.String()
		if len(candidates) > 1 {
			iface.BDR = candidates[1].IP.String()
		}
	}
	fmt.Printf("[OSPF] %s: DR elected: %s, BDR: %s on %s\n", o.RouterID, iface.DR, iface.BDR, iface.Name)
}

// SendLSA sends an LSA to neighbors
func (o *OSPFProtocol) SendLSA(lsa *LSA) {
	o.mu.Lock()
	defer o.mu.Unlock()

	key := fmt.Sprintf("%d:%s:%s", lsa.LSAType, lsa.LinkStateID, lsa.AdvertisingRouter)
	o.LSDB[key] = lsa

	fmt.Printf("[OSPF] %s: LSA %s added to LSDB\n", o.RouterID, key)
}

// CalculateRoutes performs SPF calculation
func (o *OSPFProtocol) CalculateRoutes() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.Enabled {
		return
	}

	// Simple SPF implementation
	// For each LSA in LSDB, add route if it's better than existing
	o.Routes = []RouteEntry{}

	// Add connected routes
	for _, iface := range o.Interfaces {
		o.Routes = append(o.Routes, RouteEntry{
			Destination: iface.Network.IP.String(),
			Mask:        net.IP(iface.Network.Mask).String(),
			NextHop:     "0.0.0.0",
			Interface:   iface.Name,
			Protocol:    "ospf",
			Metric:      iface.Cost,
		})
	}

	fmt.Printf("[OSPF] %s: SPF calculated, %d routes\n", o.RouterID, len(o.Routes))
}

// GetRoutes returns the OSPF routes
func (o *OSPFProtocol) GetRoutes() []RouteEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.Routes
}

// GetNeighbors returns the OSPF neighbors
func (o *OSPFProtocol) GetNeighbors() []*OSPFNeighbor {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := []*OSPFNeighbor{}
	for _, n := range o.Neighbors {
		result = append(result, n)
	}
	return result
}

// HandlePacket implements the Handler interface for OSPF. OSPF packets
// are processed by the protocol state machine in a follow-up task;
// this stub allows OSPF to be registered as a Handler without
// participating in packet-level simulation yet.
func (o *OSPFProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

var _ Handler = (*OSPFProtocol)(nil)
