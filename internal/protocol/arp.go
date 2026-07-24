package protocol

import (
	"ensp-lab/internal/sim"
	"fmt"
	"net"
	"sync"
	"time"
)

type ARPEntry struct {
	IP        string
	MAC       string
	Interface string
	Type      string
	ExpireAt  time.Time
}

type ARPProtocol struct {
	Enabled     bool
	DeviceID    string
	Table       map[string]*ARPEntry
	StaticTable map[string]*ARPEntry
	mu          sync.RWMutex
}

func NewARPProtocol(deviceID string) *ARPProtocol {
	return &ARPProtocol{
		Enabled:     true,
		DeviceID:    deviceID,
		Table:       make(map[string]*ARPEntry),
		StaticTable: make(map[string]*ARPEntry),
	}
}

func (a *ARPProtocol) Enable() {
	a.mu.Lock()
	a.Enabled = true
	a.mu.Unlock()
}

func (a *ARPProtocol) Disable() {
	a.mu.Lock()
	a.Enabled = false
	a.mu.Unlock()
}

func (a *ARPProtocol) AddStaticARP(ip, mac, iface string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}

	a.StaticTable[ip] = &ARPEntry{
		IP:        ip,
		MAC:       mac,
		Interface: iface,
		Type:      "Static",
		ExpireAt:  time.Time{},
	}

	if _, exists := a.Table[ip]; exists {
		delete(a.Table, ip)
	}

	return nil
}

func (a *ARPProtocol) AddDynamicARP(ip, mac, iface string, expireAfter time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.StaticTable[ip]; exists {
		return
	}

	a.Table[ip] = &ARPEntry{
		IP:        ip,
		MAC:       mac,
		Interface: iface,
		Type:      "Dynamic",
		ExpireAt:  time.Now().Add(expireAfter),
	}
}

func (a *ARPProtocol) DeleteARP(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.Table, ip)
	delete(a.StaticTable, ip)
}

func (a *ARPProtocol) GetARPTable() []*ARPEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var entries []*ARPEntry
	for _, entry := range a.StaticTable {
		entries = append(entries, entry)
	}
	for _, entry := range a.Table {
		if entry.ExpireAt.IsZero() || time.Now().Before(entry.ExpireAt) {
			entries = append(entries, entry)
		}
	}

	return entries
}

func (a *ARPProtocol) ResolveMAC(ip string) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if entry, exists := a.StaticTable[ip]; exists {
		return entry.MAC, true
	}

	if entry, exists := a.Table[ip]; exists {
		if entry.ExpireAt.IsZero() || time.Now().Before(entry.ExpireAt) {
			return entry.MAC, true
		}
		delete(a.Table, ip)
	}

	return "", false
}

func (a *ARPProtocol) ClearDynamicARP() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.Table = make(map[string]*ARPEntry)
}

func (a *ARPProtocol) FormatARPTable() string {
	entries := a.GetARPTable()
	if len(entries) == 0 {
		return "ARP table is empty"
	}

	var result string
	result += fmt.Sprintf("%-18s %-18s %-15s %-10s\n", "IP Address", "MAC Address", "Interface", "Type")
	result += "-----------------------------------------------------------\n"

	for _, entry := range entries {
		result += fmt.Sprintf("%-18s %-18s %-15s %-10s\n",
			entry.IP,
			entry.MAC,
			entry.Interface,
			entry.Type,
		)
	}

	return result
}

func (a *ARPProtocol) SendARPRequest(targetIP, sourceIP, sourceMAC, iface string) {
	fmt.Printf("[ARP] %s: Sending ARP Request for %s on %s\n",
		a.DeviceID, targetIP, iface)
}

func (a *ARPProtocol) ReceiveARPResponse(targetIP, sourceIP, sourceMAC, iface string) {
	a.AddDynamicARP(sourceIP, sourceMAC, iface, 30*time.Minute)
	fmt.Printf("[ARP] %s: Received ARP Response from %s -> %s on %s\n",
		a.DeviceID, sourceIP, sourceMAC, iface)
}

// HandlePacket implements the Handler interface for ARP. It accepts
// ARP Request packets whose target IP matches a local interface IP
// and returns an ARP Reply. ARP Replies are also recorded in the
// dynamic ARP table so that subsequent lookups succeed.
//
// The implementation uses the legacy parseARPPacket / serializeARPPacket
// helpers from internal/simulator/device.go via a local re-implementation
// to avoid an import cycle. The wire format follows RFC 826.
func (a *ARPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	if pkt == nil || !a.Enabled {
		return nil
	}
	if pkt.EtherType != sim.EtherTypeARP {
		return nil
	}
	arp := parseARPPayload(pkt.Payload)
	if arp == nil {
		return nil
	}
	// Learn the sender's MAC/IP pair regardless of operation.
	a.mu.Lock()
	if arp.SenderIPAddr != nil && arp.SenderHWAddr != nil {
		a.Table[arp.SenderIPAddr.String()] = &ARPEntry{
			IP:        arp.SenderIPAddr.String(),
			MAC:       arp.SenderHWAddr.String(),
			Interface: "",
			Type:      "Dynamic",
			ExpireAt:  time.Now().Add(20 * time.Minute),
		}
	}
	a.mu.Unlock()

	if arp.Operation != ARPOperationRequest {
		return nil
	}
	// The ARP table does not yet know which interface owns the target
	// IP; that binding is supplied by the topology layer in Task 6.
	// For Task 4 we reply only if the target IP appears in the static
	// table, which keeps the handler self-contained.
	a.mu.RLock()
	entry, ok := a.StaticTable[arp.TargetIPAddr.String()]
	a.mu.RUnlock()
	if !ok {
		return nil
	}
	replyMAC, err := net.ParseMAC(entry.MAC)
	if err != nil {
		return nil
	}
	replyARP := &arpPayload{
		HardwareType: 1,
		ProtocolType: 0x0800,
		HWAddrLen:    6,
		ProtoAddrLen: 4,
		Operation:    ARPOperationReply,
		SenderHWAddr: replyMAC,
		SenderIPAddr: arp.TargetIPAddr,
		TargetHWAddr: arp.SenderHWAddr,
		TargetIPAddr: arp.SenderIPAddr,
	}
	reply := cloneSimPacket(pkt)
	reply.SrcMAC = replyMAC
	reply.DstMAC = arp.SenderHWAddr
	reply.SrcIP = arp.TargetIPAddr
	reply.DstIP = arp.SenderIPAddr
	reply.Payload = serializeARPPayload(replyARP)
	reply.Path = append(reply.Path, a.DeviceID)
	return []*sim.Packet{reply}
}

// Compile-time assertion that ARPProtocol satisfies Handler.
var _ Handler = (*ARPProtocol)(nil)

// ARPOperation constants per RFC 826.
const (
	ARPOperationRequest uint16 = 1
	ARPOperationReply   uint16 = 2
)

// arpPayload is a local mirror of simulator.ARPPacket so that the
// protocol package can decode ARP requests without an import cycle.
type arpPayload struct {
	HardwareType uint16
	ProtocolType uint16
	HWAddrLen    uint8
	ProtoAddrLen uint8
	Operation    uint16
	SenderHWAddr net.HardwareAddr
	SenderIPAddr net.IP
	TargetHWAddr net.HardwareAddr
	TargetIPAddr net.IP
}

// parseARPPayload decodes the RFC 826 wire format.
func parseARPPayload(data []byte) *arpPayload {
	if len(data) < 28 {
		return nil
	}
	return &arpPayload{
		HardwareType: uint16(data[0])<<8 | uint16(data[1]),
		ProtocolType: uint16(data[2])<<8 | uint16(data[3]),
		HWAddrLen:    data[4],
		ProtoAddrLen: data[5],
		Operation:    uint16(data[6])<<8 | uint16(data[7]),
		SenderHWAddr: net.HardwareAddr(data[8:14]),
		SenderIPAddr: net.IP(data[14:18]).To4(),
		TargetHWAddr: net.HardwareAddr(data[18:24]),
		TargetIPAddr: net.IP(data[24:28]).To4(),
	}
}

// serializeARPPayload encodes the RFC 826 wire format.
func serializeARPPayload(arp *arpPayload) []byte {
	data := make([]byte, 28)
	data[0] = byte(arp.HardwareType >> 8)
	data[1] = byte(arp.HardwareType)
	data[2] = byte(arp.ProtocolType >> 8)
	data[3] = byte(arp.ProtocolType)
	data[4] = arp.HWAddrLen
	data[5] = arp.ProtoAddrLen
	data[6] = byte(arp.Operation >> 8)
	data[7] = byte(arp.Operation)
	copy(data[8:14], arp.SenderHWAddr)
	copy(data[14:18], arp.SenderIPAddr.To4())
	copy(data[18:24], arp.TargetHWAddr)
	copy(data[24:28], arp.TargetIPAddr.To4())
	return data
}

// cloneSimPacket returns a deep copy of pkt.
//
// The implementation mirrors sim.clonePacket but lives in the protocol
// package to avoid a circular import (sim -> protocol -> sim). The two
// helpers must stay in sync.
func cloneSimPacket(pkt *sim.Packet) *sim.Packet {
	if pkt == nil {
		return nil
	}
	out := *pkt
	if pkt.Payload != nil {
		out.Payload = make([]byte, len(pkt.Payload))
		copy(out.Payload, pkt.Payload)
	}
	if pkt.Path != nil {
		out.Path = make([]string, len(pkt.Path))
		copy(out.Path, pkt.Path)
	}
	if pkt.SrcMAC != nil {
		out.SrcMAC = make(net.HardwareAddr, len(pkt.SrcMAC))
		copy(out.SrcMAC, pkt.SrcMAC)
	}
	if pkt.DstMAC != nil {
		out.DstMAC = make(net.HardwareAddr, len(pkt.DstMAC))
		copy(out.DstMAC, pkt.DstMAC)
	}
	return &out
}
