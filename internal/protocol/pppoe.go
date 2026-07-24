package protocol

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type PPPoEProtocol struct {
	Enabled  bool
	DeviceID string
	Sessions map[string]*PPPoESession
	mu       sync.RWMutex
}

type PPPoESession struct {
	ID          int
	Interface   string
	ACName      string
	RemoteMAC   string
	IPAddress   string
	SubnetMask  string
	Gateway     string
	DNS         string
	Status      string
	ConnectedAt time.Time
}

func NewPPPoEProtocol(deviceID string) *PPPoEProtocol {
	return &PPPoEProtocol{
		DeviceID: deviceID,
		Enabled:  false,
		Sessions: make(map[string]*PPPoESession),
	}
}

func (p *PPPoEProtocol) Enable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Enabled = true
}

func (p *PPPoEProtocol) Disable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Enabled = false
}

func (p *PPPoEProtocol) StartSession(bundleID int, ifaceName, acName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := fmt.Sprintf("%s-%d", ifaceName, bundleID)
	p.Sessions[key] = &PPPoESession{
		ID:          bundleID,
		Interface:   ifaceName,
		ACName:      acName,
		Status:      "connected",
		ConnectedAt: time.Now(),
	}
}

func (p *PPPoEProtocol) StopSession(bundleID int, ifaceName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := fmt.Sprintf("%s-%d", ifaceName, bundleID)
	delete(p.Sessions, key)
}

func (p *PPPoEProtocol) SetSessionIP(bundleID int, ifaceName, ip, subnet, gateway, dns string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := fmt.Sprintf("%s-%d", ifaceName, bundleID)
	if session, ok := p.Sessions[key]; ok {
		session.IPAddress = ip
		session.SubnetMask = subnet
		session.Gateway = gateway
		session.DNS = dns
	}
}

func (p *PPPoEProtocol) GetStatus() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString("PPPoE Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[p.Enabled]))
	if len(p.Sessions) > 0 {
		sb.WriteString("  Sessions:\n")
		for key, session := range p.Sessions {
			sb.WriteString(fmt.Sprintf("    %s: ID=%d, Interface=%s, AC=%s, Status=%s\n",
				key, session.ID, session.Interface, session.ACName, session.Status))
			if session.IPAddress != "" {
				sb.WriteString(fmt.Sprintf("         IP=%s/%s, Gateway=%s, DNS=%s\n",
					session.IPAddress, session.SubnetMask, session.Gateway, session.DNS))
			}
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
func (p *PPPoEProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that PPPoEProtocol satisfies Handler.
var _ Handler = (*PPPoEProtocol)(nil)
