package protocol

import (
	"fmt"
	"net"
	"sync"

	"ensp-lab/internal/sim"
)

// Firewall represents a firewall with ACL and NAT capabilities
type Firewall struct {
	Enabled      bool
	ACLs         map[string]*ACL
	NATRules     []*NATRule
	PATRules     []*PATRule
	Stateful     bool
	SessionTable map[string]*Session
	mu           sync.RWMutex
}

// ACL represents an Access Control List
type ACL struct {
	Name        string
	Rules       []*ACLRule
	Description string
}

// ACLRule represents a single ACL rule
type ACLRule struct {
	ID          int
	Action      string // permit or deny
	SourceIP    string
	SourceMask  string
	DestIP      string
	DestMask    string
	Protocol    string // tcp, udp, icmp, ip
	SrcPort     string // e.g., "80", "1000-2000"
	DestPort    string
	Logging     bool
	Description string
}

// NATRule represents a static NAT rule
type NATRule struct {
	ID          int
	InsideIP    string
	OutsideIP   string
	Protocol    string
	InsidePort  int
	OutsidePort int
}

// PATRule represents a Port Address Translation rule
type PATRule struct {
	ID          int
	InsideIP    string
	OutsideIP   string
	Protocol    string
	StartPort   int
	EndPort     int
	CurrentPort int
}

// Session represents a stateful firewall session
type Session struct {
	ID         string
	Protocol   string
	SourceIP   net.IP
	SourcePort int
	DestIP     net.IP
	DestPort   int
	StartTime  int64
	LastActive int64
	Timeout    int64
}

// NewFirewall creates a new firewall instance
func NewFirewall() *Firewall {
	return &Firewall{
		Enabled:      false,
		ACLs:         make(map[string]*ACL),
		NATRules:     []*NATRule{},
		PATRules:     []*PATRule{},
		Stateful:     true,
		SessionTable: make(map[string]*Session),
	}
}

// Enable enables the firewall
func (f *Firewall) Enable() {
	f.mu.Lock()
	f.Enabled = true
	f.mu.Unlock()
}

// Disable disables the firewall
func (f *Firewall) Disable() {
	f.mu.Lock()
	f.Enabled = false
	f.mu.Unlock()
}

// CreateACL creates a new ACL
func (f *Firewall) CreateACL(name string, description string) *ACL {
	f.mu.Lock()
	defer f.mu.Unlock()

	acl := &ACL{
		Name:        name,
		Rules:       []*ACLRule{},
		Description: description,
	}
	f.ACLs[name] = acl
	return acl
}

// AddACLRule adds a rule to an ACL
func (f *Firewall) AddACLRule(aclName string, action, srcIP, srcMask, destIP, destMask, proto string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	acl, ok := f.ACLs[aclName]
	if !ok {
		return
	}

	rule := &ACLRule{
		ID:         len(acl.Rules) + 1,
		Action:     action,
		SourceIP:   srcIP,
		SourceMask: srcMask,
		DestIP:     destIP,
		DestMask:   destMask,
		Protocol:   proto,
		Logging:    false,
	}
	acl.Rules = append(acl.Rules, rule)
}

// AddNATRule adds a static NAT rule
func (f *Firewall) AddNATRule(insideIP, outsideIP string, protocol string, insidePort, outsidePort int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule := &NATRule{
		ID:          len(f.NATRules) + 1,
		InsideIP:    insideIP,
		OutsideIP:   outsideIP,
		Protocol:    protocol,
		InsidePort:  insidePort,
		OutsidePort: outsidePort,
	}
	f.NATRules = append(f.NATRules, rule)
}

// AddPATRule adds a PAT rule
func (f *Firewall) AddPATRule(insideIP, outsideIP, protocol string, startPort, endPort int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule := &PATRule{
		ID:          len(f.PATRules) + 1,
		InsideIP:    insideIP,
		OutsideIP:   outsideIP,
		Protocol:    protocol,
		StartPort:   startPort,
		EndPort:     endPort,
		CurrentPort: startPort,
	}
	f.PATRules = append(f.PATRules, rule)
}

// ApplyACL applies an ACL to a packet
func (f *Firewall) ApplyACL(aclName string, srcIP, destIP net.IP, protocol string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	acl, ok := f.ACLs[aclName]
	if !ok {
		return true
	}

	for _, rule := range acl.Rules {
		if f.matchRule(rule, srcIP, destIP, protocol) {
			if rule.Logging {
				fmt.Printf("[Firewall] ACL %s Rule %d: %s %s->%s\n",
					aclName, rule.ID, rule.Action, srcIP, destIP)
			}
			return rule.Action == "permit"
		}
	}

	return true
}

// matchRule checks if a packet matches a rule
func (f *Firewall) matchRule(rule *ACLRule, srcIP, destIP net.IP, protocol string) bool {
	if rule.Protocol != "" && rule.Protocol != protocol && rule.Protocol != "ip" {
		return false
	}

	if rule.SourceIP != "" {
		srcNet := parseCIDR(rule.SourceIP, rule.SourceMask)
		if !srcNet.Contains(srcIP) {
			return false
		}
	}

	if rule.DestIP != "" {
		destNet := parseCIDR(rule.DestIP, rule.DestMask)
		if !destNet.Contains(destIP) {
			return false
		}
	}

	return true
}

// parseCIDR parses IP and mask into IPNet
func parseCIDR(ipStr, maskStr string) net.IPNet {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return net.IPNet{}
	}

	// 尝试解析掩码，如果失败则使用默认掩码
	var mask net.IPMask
	if maskStr != "" {
		maskIP := net.ParseIP(maskStr)
		if maskIP != nil {
			// 将 IP 格式的掩码转换为 IPMask
			mask = net.IPMask(maskIP.To4())
		} else {
			// 使用 CIDR 格式的掩码（例如 "/24"）
			mask = net.CIDRMask(32, 32)
		}
	} else {
		mask = net.CIDRMask(32, 32)
	}

	return net.IPNet{IP: ip, Mask: mask}
}

// ApplyNAT applies NAT translation
func (f *Firewall) ApplyNAT(srcIP, destIP net.IP, srcPort, destPort int, inside bool) (net.IP, int) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if inside {
		// Inside to outside: check NAT rules
		for _, rule := range f.NATRules {
			if rule.InsideIP == srcIP.String() {
				newIP := net.ParseIP(rule.OutsideIP)
				if newIP != nil {
					return newIP, rule.OutsidePort
				}
			}
		}

		// Check PAT rules
		for _, rule := range f.PATRules {
			if rule.InsideIP == srcIP.String() {
				newIP := net.ParseIP(rule.OutsideIP)
				if newIP != nil {
					port := rule.CurrentPort
					rule.CurrentPort++
					if rule.CurrentPort > rule.EndPort {
						rule.CurrentPort = rule.StartPort
					}
					return newIP, port
				}
			}
		}
	} else {
		// Outside to inside: reverse NAT
		for _, rule := range f.NATRules {
			if rule.OutsideIP == destIP.String() {
				newIP := net.ParseIP(rule.InsideIP)
				if newIP != nil {
					return newIP, rule.InsidePort
				}
			}
		}
	}

	return srcIP, srcPort
}

// AddSession adds a stateful session
func (f *Firewall) AddSession(protocol string, srcIP net.IP, srcPort int, destIP net.IP, destPort int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.Stateful {
		return
	}

	sessionID := fmt.Sprintf("%s:%s:%d:%s:%d", protocol, srcIP, srcPort, destIP, destPort)
	f.SessionTable[sessionID] = &Session{
		ID:         sessionID,
		Protocol:   protocol,
		SourceIP:   srcIP,
		SourcePort: srcPort,
		DestIP:     destIP,
		DestPort:   destPort,
		StartTime:  0,
		LastActive: 0,
		Timeout:    3600,
	}
}

// CheckSession checks if a return packet belongs to an existing session
func (f *Firewall) CheckSession(protocol string, srcIP net.IP, srcPort int, destIP net.IP, destPort int) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if !f.Stateful {
		return true
	}

	sessionID := fmt.Sprintf("%s:%s:%d:%s:%d", protocol, destIP, destPort, srcIP, srcPort)
	_, ok := f.SessionTable[sessionID]
	return ok
}

// GetACLs returns all ACLs
func (f *Firewall) GetACLs() map[string]*ACL {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ACLs
}

// GetNATRules returns NAT rules
func (f *Firewall) GetNATRules() []*NATRule {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.NATRules
}

// GetPATRules returns PAT rules
func (f *Firewall) GetPATRules() []*PATRule {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.PATRules
}

// GetSessions returns active sessions
func (f *Firewall) GetSessions() map[string]*Session {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.SessionTable
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the firewall currently does not participate in
// packet-level simulation. When ACL/NAT evaluation moves into the
// simulation pipeline, replace this with real handling logic that
// consults f.ACLs / f.NATRules / f.PATRules and returns either the
// (possibly rewritten) packet to forward or nil to drop.
// Deprecated: superseded by cli ACL evaluator
// （internal/cli/acl_eval.go 的 EvaluateDeviceACL/EvaluatePathACL）。
// 本期 CLIState 评估器为 ACL 判定的唯一消费方；请勿在新代码中调用本方法。
func (f *Firewall) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that Firewall satisfies Handler.
var _ Handler = (*Firewall)(nil)
