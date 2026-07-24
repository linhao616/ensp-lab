package protocol

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type RIPProtocol struct {
	ProcessID   int
	Version     int
	Enabled     bool
	DeviceID    string
	Networks    []string
	Neighbors   map[string]*RIPNeighbor
	Routes      []RouteEntry
	UpdateTimer time.Duration
	ExpireTimer time.Duration
	FlushTimer  time.Duration
	mu          sync.RWMutex
}

type RIPNeighbor struct {
	IP         string
	LastUpdate time.Time
	Version    int
	Metric     int
}

type RIPRouteEntry struct {
	Destination string
	NextHop     string
	Metric      int
	Interface   string
	ExpireTime  time.Time
}

func NewRIPProtocol(deviceID string) *RIPProtocol {
	return &RIPProtocol{
		DeviceID:    deviceID,
		Version:     2,
		Enabled:     false,
		Networks:    []string{},
		Neighbors:   make(map[string]*RIPNeighbor),
		Routes:      []RouteEntry{},
		UpdateTimer: 30 * time.Second,
		ExpireTimer: 180 * time.Second,
		FlushTimer:  240 * time.Second,
	}
}

func (r *RIPProtocol) Enable(processID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ProcessID = processID
	r.Enabled = true
}

func (r *RIPProtocol) Disable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Enabled = false
}

func (r *RIPProtocol) AddNetwork(network string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.Networks {
		if n == network {
			return
		}
	}
	r.Networks = append(r.Networks, network)
}

func (r *RIPProtocol) RemoveNetwork(network string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, n := range r.Networks {
		if n == network {
			r.Networks = append(r.Networks[:i], r.Networks[i+1:]...)
			return
		}
	}
}

func (r *RIPProtocol) SetVersion(version int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if version == 1 || version == 2 {
		r.Version = version
	}
}

func (r *RIPProtocol) SetTimers(update, expire, flush time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.UpdateTimer = update
	r.ExpireTimer = expire
	r.FlushTimer = flush
}

func (r *RIPProtocol) AddNeighbor(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.Neighbors[ip]; !ok {
		r.Neighbors[ip] = &RIPNeighbor{
			IP:         ip,
			LastUpdate: time.Now(),
			Version:    r.Version,
			Metric:     1,
		}
	}
}

func (r *RIPProtocol) GetStatus() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("RIP Process %d\n", r.ProcessID))
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[r.Enabled]))
	sb.WriteString(fmt.Sprintf("  Version: RIPv%d\n", r.Version))
	sb.WriteString(fmt.Sprintf("  Update Timer: %ds\n", int(r.UpdateTimer.Seconds())))
	sb.WriteString(fmt.Sprintf("  Expire Timer: %ds\n", int(r.ExpireTimer.Seconds())))
	sb.WriteString(fmt.Sprintf("  Flush Timer: %ds\n", int(r.FlushTimer.Seconds())))
	if len(r.Networks) > 0 {
		sb.WriteString("  Networks:\n")
		for _, n := range r.Networks {
			sb.WriteString(fmt.Sprintf("    %s\n", n))
		}
	}
	if len(r.Neighbors) > 0 {
		sb.WriteString("  Neighbors:\n")
		for ip, n := range r.Neighbors {
			sb.WriteString(fmt.Sprintf("    %s (Metric: %d)\n", ip, n.Metric))
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
func (r *RIPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that RIPProtocol satisfies Handler.
var _ Handler = (*RIPProtocol)(nil)
