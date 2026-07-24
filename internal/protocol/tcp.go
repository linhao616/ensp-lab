package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type TCPConnection struct {
	SourceIP      string
	SourcePort    int
	DestIP        string
	DestPort      int
	State         string
	LocalIP       string
	LocalPort     int
	RemoteIP      string
	RemotePort    int
	EstablishedAt time.Time
	LastActive    time.Time
}

type TCPProtocol struct {
	Enabled     bool
	DeviceID    string
	Connections map[string]*TCPConnection
	Listeners   map[int]bool
	mu          sync.RWMutex
}

func NewTCPProtocol(deviceID string) *TCPProtocol {
	return &TCPProtocol{
		Enabled:     true,
		DeviceID:    deviceID,
		Connections: make(map[string]*TCPConnection),
		Listeners:   make(map[int]bool),
	}
}

func (t *TCPProtocol) Enable() {
	t.mu.Lock()
	t.Enabled = true
	t.mu.Unlock()
}

func (t *TCPProtocol) Disable() {
	t.mu.Lock()
	t.Enabled = false
	t.mu.Unlock()
}

func (t *TCPProtocol) AddListener(port int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.Listeners[port] = true
}

func (t *TCPProtocol) RemoveListener(port int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.Listeners, port)
}

func (t *TCPProtocol) IsListening(port int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	_, exists := t.Listeners[port]
	return exists
}

func (t *TCPProtocol) Connect(localIP, remoteIP string, localPort, remotePort int) (*TCPConnection, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.Enabled {
		return nil, fmt.Errorf("TCP is disabled")
	}

	key := fmt.Sprintf("%s:%d->%s:%d", localIP, localPort, remoteIP, remotePort)
	if _, exists := t.Connections[key]; exists {
		return nil, fmt.Errorf("connection already exists")
	}

	conn := &TCPConnection{
		SourceIP:      localIP,
		SourcePort:    localPort,
		DestIP:        remoteIP,
		DestPort:      remotePort,
		LocalIP:       localIP,
		LocalPort:     localPort,
		RemoteIP:      remoteIP,
		RemotePort:    remotePort,
		State:         "ESTABLISHED",
		EstablishedAt: time.Now(),
		LastActive:    time.Now(),
	}

	t.Connections[key] = conn

	fmt.Printf("[TCP] %s: Connection established %s:%d -> %s:%d\n",
		t.DeviceID, localIP, localPort, remoteIP, remotePort)

	return conn, nil
}

func (t *TCPProtocol) Disconnect(localIP, remoteIP string, localPort, remotePort int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := fmt.Sprintf("%s:%d->%s:%d", localIP, localPort, remoteIP, remotePort)
	if conn, exists := t.Connections[key]; exists {
		conn.State = "CLOSED"
		delete(t.Connections, key)
		fmt.Printf("[TCP] %s: Connection closed %s:%d -> %s:%d\n",
			t.DeviceID, localIP, localPort, remoteIP, remotePort)
	}
}

func (t *TCPProtocol) GetConnections() []*TCPConnection {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var conns []*TCPConnection
	for _, conn := range t.Connections {
		conns = append(conns, conn)
	}

	return conns
}

func (t *TCPProtocol) GetListeners() []int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var ports []int
	for port := range t.Listeners {
		ports = append(ports, port)
	}

	return ports
}

func (t *TCPProtocol) FormatConnections() string {
	conns := t.GetConnections()
	if len(conns) == 0 {
		return "No TCP connections"
	}

	var result string
	result += fmt.Sprintf("%-18s %-6s %-18s %-6s %-15s\n",
		"Local Address", "Port", "Foreign Address", "Port", "State")
	result += "-------------------------------------------------------------\n"

	for _, conn := range conns {
		result += fmt.Sprintf("%-18s %-6d %-18s %-6d %-15s\n",
			conn.LocalIP,
			conn.LocalPort,
			conn.RemoteIP,
			conn.RemotePort,
			conn.State,
		)
	}

	return result
}

func (t *TCPProtocol) FormatListeners() string {
	ports := t.GetListeners()
	if len(ports) == 0 {
		return "No TCP listeners"
	}

	var result string
	result += "TCP Listeners:\n"
	result += "--------------\n"

	for _, port := range ports {
		result += fmt.Sprintf("  * Port %d\n", port)
	}

	return result
}

func (t *TCPProtocol) Telnet(remoteIP string, remotePort int) string {
	t.mu.RLock()
	enabled := t.Enabled
	t.mu.RUnlock()

	if !enabled {
		return "TCP is disabled"
	}

	if !t.IsListening(remotePort) {
		return fmt.Sprintf("Connection refused: %s:%d is not listening", remoteIP, remotePort)
	}

	localIP := "0.0.0.0"
	localPort := 1024 + len(t.GetConnections())%64511

	conn, err := t.Connect(localIP, remoteIP, localPort, remotePort)
	if err != nil {
		return err.Error()
	}

	return fmt.Sprintf("Connected to %s:%d from %s:%d\nConnection state: %s",
		remoteIP, remotePort, conn.LocalIP, conn.LocalPort, conn.State)
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (t *TCPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that TCPProtocol satisfies Handler.
var _ Handler = (*TCPProtocol)(nil)
