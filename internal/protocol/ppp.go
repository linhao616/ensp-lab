package protocol

import (
	"fmt"
	"strings"
	"sync"

	"ensp-lab/internal/sim"
)

type PPPProtocol struct {
	Enabled    bool
	DeviceID   string
	Interfaces map[string]*PPPInterface
	AuthType   string
	Username   string
	Password   string
	mu         sync.RWMutex
}

type PPPInterface struct {
	Name      string
	Enabled   bool
	AuthType  string
	Username  string
	Password  string
	IPAddress string
	PeerIP    string
	Status    string
}

func NewPPPProtocol(deviceID string) *PPPProtocol {
	return &PPPProtocol{
		DeviceID:   deviceID,
		Enabled:    false,
		Interfaces: make(map[string]*PPPInterface),
		AuthType:   "",
	}
}

func (p *PPPProtocol) Enable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Enabled = true
}

func (p *PPPProtocol) Disable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Enabled = false
}

func (p *PPPProtocol) ConfigureInterface(ifaceName string, authType, username, password string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.Interfaces[ifaceName]; !ok {
		p.Interfaces[ifaceName] = &PPPInterface{
			Name:     ifaceName,
			Enabled:  true,
			AuthType: authType,
			Username: username,
			Password: password,
			Status:   "connected",
		}
	}
}

func (p *PPPProtocol) SetIPAddress(ifaceName, ip, peerIP string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if iface, ok := p.Interfaces[ifaceName]; ok {
		iface.IPAddress = ip
		iface.PeerIP = peerIP
	}
}

func (p *PPPProtocol) SetAuthType(authType, username, password string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.AuthType = authType
	p.Username = username
	p.Password = password
}

func (p *PPPProtocol) GetStatus() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString("PPP Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[p.Enabled]))
	sb.WriteString(fmt.Sprintf("  Auth Type: %s\n", p.AuthType))
	if len(p.Interfaces) > 0 {
		sb.WriteString("  Interfaces:\n")
		for name, iface := range p.Interfaces {
			sb.WriteString(fmt.Sprintf("    %s: Status=%s, IP=%s, PeerIP=%s, Auth=%s\n",
				name, iface.Status, iface.IPAddress, iface.PeerIP, iface.AuthType))
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
func (p *PPPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that PPPProtocol satisfies Handler.
var _ Handler = (*PPPProtocol)(nil)
