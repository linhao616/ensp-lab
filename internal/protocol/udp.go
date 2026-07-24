package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type UDPServer struct {
	Port            int
	Enabled         bool
	PacketsReceived int
	BytesReceived   int
	LastReceived    time.Time
}

type UDPPacket struct {
	SourceIP   string
	SourcePort int
	DestIP     string
	DestPort   int
	Length     int
	Payload    []byte
	ReceivedAt time.Time
}

type UDPProtocol struct {
	Enabled     bool
	DeviceID    string
	Servers     map[int]*UDPServer
	Packets     []*UDPPacket
	PacketStats map[int]*UDPPacketStats
	mu          sync.RWMutex
}

type UDPPacketStats struct {
	Port            int
	PacketsReceived int
	BytesReceived   int
	PacketsSent     int
	BytesSent       int
}

func NewUDPProtocol(deviceID string) *UDPProtocol {
	return &UDPProtocol{
		Enabled:     true,
		DeviceID:    deviceID,
		Servers:     make(map[int]*UDPServer),
		Packets:     []*UDPPacket{},
		PacketStats: make(map[int]*UDPPacketStats),
	}
}

func (u *UDPProtocol) Enable() {
	u.mu.Lock()
	u.Enabled = true
	u.mu.Unlock()
}

func (u *UDPProtocol) Disable() {
	u.mu.Lock()
	u.Enabled = false
	u.mu.Unlock()
}

func (u *UDPProtocol) AddServer(port int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, exists := u.Servers[port]; !exists {
		u.Servers[port] = &UDPServer{
			Port:            port,
			Enabled:         true,
			PacketsReceived: 0,
			BytesReceived:   0,
		}
	}

	if _, exists := u.PacketStats[port]; !exists {
		u.PacketStats[port] = &UDPPacketStats{
			Port:            port,
			PacketsReceived: 0,
			BytesReceived:   0,
			PacketsSent:     0,
			BytesSent:       0,
		}
	}

	fmt.Printf("[UDP] %s: Server started on port %d\n", u.DeviceID, port)
}

func (u *UDPProtocol) RemoveServer(port int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	delete(u.Servers, port)
	fmt.Printf("[UDP] %s: Server stopped on port %d\n", u.DeviceID, port)
}

func (u *UDPProtocol) IsServerRunning(port int) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()

	server, exists := u.Servers[port]
	return exists && server.Enabled
}

func (u *UDPProtocol) ReceivePacket(sourceIP string, sourcePort, destPort, length int, payload []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.Enabled {
		return
	}

	packet := &UDPPacket{
		SourceIP:   sourceIP,
		SourcePort: sourcePort,
		DestIP:     "0.0.0.0",
		DestPort:   destPort,
		Length:     length,
		Payload:    payload,
		ReceivedAt: time.Now(),
	}

	u.Packets = append(u.Packets, packet)

	if server, exists := u.Servers[destPort]; exists && server.Enabled {
		server.PacketsReceived++
		server.BytesReceived += length
		server.LastReceived = time.Now()
	}

	if stats, exists := u.PacketStats[destPort]; exists {
		stats.PacketsReceived++
		stats.BytesReceived += length
	}

	fmt.Printf("[UDP] %s: Received packet from %s:%d to port %d (%d bytes)\n",
		u.DeviceID, sourceIP, sourcePort, destPort, length)
}

func (u *UDPProtocol) SendPacket(destIP string, destPort, sourcePort, length int, payload []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.Enabled {
		return
	}

	if stats, exists := u.PacketStats[sourcePort]; exists {
		stats.PacketsSent++
		stats.BytesSent += length
	}

	fmt.Printf("[UDP] %s: Sent packet to %s:%d from port %d (%d bytes)\n",
		u.DeviceID, destIP, destPort, sourcePort, length)
}

func (u *UDPProtocol) GetServers() []*UDPServer {
	u.mu.RLock()
	defer u.mu.RUnlock()

	var servers []*UDPServer
	for _, server := range u.Servers {
		servers = append(servers, server)
	}

	return servers
}

func (u *UDPProtocol) GetStats() map[int]*UDPPacketStats {
	u.mu.RLock()
	defer u.mu.RUnlock()

	result := make(map[int]*UDPPacketStats)
	for port, stats := range u.PacketStats {
		result[port] = stats
	}

	return result
}

func (u *UDPProtocol) FormatServers() string {
	servers := u.GetServers()
	if len(servers) == 0 {
		return "No UDP servers running"
	}

	var result string
	result += "UDP Servers:\n"
	result += "------------\n"

	for _, server := range servers {
		result += fmt.Sprintf("  * Port %d: %d packets received, %d bytes\n",
			server.Port, server.PacketsReceived, server.BytesReceived)
	}

	return result
}

func (u *UDPProtocol) FormatStats() string {
	stats := u.GetStats()
	if len(stats) == 0 {
		return "No UDP statistics available"
	}

	var result string
	result += fmt.Sprintf("%-6s %-20s %-20s\n", "Port", "Received", "Sent")
	result += "--------------------------------------------------\n"

	for port, stat := range stats {
		result += fmt.Sprintf("%-6d %-10d packets (%d bytes) %-10d packets (%d bytes)\n",
			port, stat.PacketsReceived, stat.BytesReceived, stat.PacketsSent, stat.BytesSent)
	}

	return result
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (u *UDPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that UDPProtocol satisfies Handler.
var _ Handler = (*UDPProtocol)(nil)
