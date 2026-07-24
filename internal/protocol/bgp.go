package protocol

import (
	"ensp-lab/internal/sim"
	"fmt"
	"net"
	"sync"
	"time"
)

// BGPProtocol represents a BGP routing protocol instance
type BGPProtocol struct {
	ASN            int
	RouterID       string
	Enabled        bool
	Neighbors      map[string]*BGPNeighbor
	Routes         []RouteEntry
	LocalPref      int
	ASPath         []int
	mu             sync.RWMutex
	keepaliveTimer *time.Ticker
}

// BGPNeighbor represents a BGP neighbor
type BGPNeighbor struct {
	IP            net.IP
	ASN           int
	State         string // Idle, Connect, Active, OpenSent, OpenConfirm, Established
	RemoteASN     int
	LocalAddress  net.IP
	HoldTime      time.Duration
	KeepAliveTime time.Duration
	LastUpdate    time.Time
	Prefixes      []string
	PeerGroup     string
}

// BGPOpenMessage represents a BGP Open message
type BGPOpenMessage struct {
	Version     uint8
	MyAS        uint16
	HoldTime    uint16
	BGPID       uint32
	OptionalLen uint8
	Optional    []byte
}

// BGPUpdateMessage represents a BGP Update message
type BGPUpdateMessage struct {
	UnfeasibleRoutes uint16
	WithdrawnRoutes  []string
	TotalPathAttrLen uint16
	PathAttributes   []*PathAttribute
	NLRI             []string
}

// PathAttribute represents a BGP path attribute
type PathAttribute struct {
	Flags  uint8
	Type   uint8
	Length uint8
	Value  []byte
}

// NewBGPProtocol creates a new BGP instance
func NewBGPProtocol(asn int, routerID string) *BGPProtocol {
	return &BGPProtocol{
		ASN:       asn,
		RouterID:  routerID,
		Enabled:   false,
		Neighbors: make(map[string]*BGPNeighbor),
		Routes:    []RouteEntry{},
		LocalPref: 100,
		ASPath:    []int{asn},
	}
}

// Enable starts BGP
func (b *BGPProtocol) Enable() {
	b.mu.Lock()
	b.Enabled = true
	b.mu.Unlock()
	go b.keepaliveLoop()
}

// Disable stops BGP
func (b *BGPProtocol) Disable() {
	b.mu.Lock()
	b.Enabled = false
	b.mu.Unlock()
	if b.keepaliveTimer != nil {
		b.keepaliveTimer.Stop()
	}
}

// AddNeighbor adds a BGP neighbor
func (b *BGPProtocol) AddNeighbor(ip net.IP, remoteASN int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Neighbors[ip.String()] = &BGPNeighbor{
		IP:            ip,
		ASN:           b.ASN,
		State:         "Idle",
		RemoteASN:     remoteASN,
		HoldTime:      180 * time.Second,
		KeepAliveTime: 60 * time.Second,
		LastUpdate:    time.Now(),
		Prefixes:      []string{},
	}

	// Start connection attempt
	go b.connectToNeighbor(ip.String())
}

// connectToNeighbor attempts to establish BGP connection
func (b *BGPProtocol) connectToNeighbor(ipStr string) {
	b.mu.Lock()
	neighbor, ok := b.Neighbors[ipStr]
	if !ok {
		b.mu.Unlock()
		return
	}

	if neighbor.State != "Idle" {
		b.mu.Unlock()
		return
	}

	neighbor.State = "Connect"
	b.mu.Unlock()

	// Simulate TCP connection and Open message exchange
	time.Sleep(1 * time.Second)

	b.mu.Lock()
	neighbor.State = "OpenSent"
	b.mu.Unlock()

	time.Sleep(500 * time.Millisecond)

	b.mu.Lock()
	neighbor.State = "OpenConfirm"
	b.mu.Unlock()

	time.Sleep(500 * time.Millisecond)

	b.mu.Lock()
	neighbor.State = "Established"
	neighbor.LastUpdate = time.Now()
	b.mu.Unlock()

	fmt.Printf("[BGP] %s: Neighbor %s established (AS %d)\n", b.RouterID, ipStr, neighbor.RemoteASN)
}

// keepaliveLoop sends KeepAlive messages
func (b *BGPProtocol) keepaliveLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		b.mu.RLock()
		if !b.Enabled {
			b.mu.RUnlock()
			return
		}
		b.mu.RUnlock()

		b.sendKeepalives()
		<-ticker.C
	}
}

// sendKeepalives sends KeepAlive to established neighbors
func (b *BGPProtocol) sendKeepalives() {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ipStr, neighbor := range b.Neighbors {
		if neighbor.State == "Established" {
			neighbor.LastUpdate = time.Now()
			fmt.Printf("[BGP] %s: Sending KeepAlive to %s\n", b.RouterID, ipStr)
		}
	}
}

// SendUpdate sends routing update to neighbors
func (b *BGPProtocol) SendUpdate(prefix string, nextHop string, asPath []int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	update := &BGPUpdateMessage{
		NLRI: []string{prefix},
		PathAttributes: []*PathAttribute{
			{Flags: 0x40, Type: 1, Length: 4, Value: []byte{0, 0, 0, byte(len(asPath))}},
			{Flags: 0x40, Type: 5, Length: 4, Value: []byte{0, 0, 0, 1}},
		},
	}
	_ = update // 变量暂时未使用，但保留以备后续实现完整的 BGP 消息序列化

	for ipStr, neighbor := range b.Neighbors {
		if neighbor.State == "Established" {
			neighbor.Prefixes = append(neighbor.Prefixes, prefix)
			fmt.Printf("[BGP] %s: Sending Update to %s: %s -> %s\n", b.RouterID, ipStr, prefix, nextHop)
		}
	}

	// Add to local routes
	b.Routes = append(b.Routes, RouteEntry{
		Destination: prefix,
		NextHop:     nextHop,
		Protocol:    "bgp",
		Metric:      len(asPath),
	})
}

// ReceiveUpdate processes a received Update message
func (b *BGPProtocol) ReceiveUpdate(prefix string, nextHop string, asPath []int, sourceIP net.IP) {
	b.mu.Lock()
	defer b.mu.Unlock()

	neighbor, ok := b.Neighbors[sourceIP.String()]
	if !ok {
		return
	}

	// Check if route is already known
	for _, route := range b.Routes {
		if route.Destination == prefix {
			return
		}
	}

	// Add route
	b.Routes = append(b.Routes, RouteEntry{
		Destination: prefix,
		NextHop:     nextHop,
		Interface:   neighbor.LocalAddress.String(),
		Protocol:    "bgp",
		Metric:      len(asPath),
	})

	fmt.Printf("[BGP] %s: Received Update from %s: %s -> %s\n", b.RouterID, sourceIP, prefix, nextHop)
}

// GetRoutes returns BGP routes
func (b *BGPProtocol) GetRoutes() []RouteEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Routes
}

// GetNeighbors returns BGP neighbors
func (b *BGPProtocol) GetNeighbors() []*BGPNeighbor {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := []*BGPNeighbor{}
	for _, n := range b.Neighbors {
		result = append(result, n)
	}
	return result
}

// WithdrawRoute withdraws a route
func (b *BGPProtocol) WithdrawRoute(prefix string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Remove from local routes
	for i, route := range b.Routes {
		if route.Destination == prefix {
			b.Routes = append(b.Routes[:i], b.Routes[i+1:]...)
			break
		}
	}

	// Send withdraw to neighbors
	for ipStr, neighbor := range b.Neighbors {
		if neighbor.State == "Established" {
			for i, p := range neighbor.Prefixes {
				if p == prefix {
					neighbor.Prefixes = append(neighbor.Prefixes[:i], neighbor.Prefixes[i+1:]...)
					break
				}
			}
			fmt.Printf("[BGP] %s: Withdrawing %s from %s\n", b.RouterID, prefix, ipStr)
		}
	}
}

// HandlePacket implements the Handler interface for BGP. BGP packets
// are processed by the protocol state machine in a follow-up task;
// this stub allows BGP to be registered as a Handler without
// participating in packet-level simulation yet.
func (b *BGPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

var _ Handler = (*BGPProtocol)(nil)
