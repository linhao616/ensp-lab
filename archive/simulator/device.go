//go:build ignore

package simulator

import (
	"fmt"
	"net"
	"sync"

	"ensp-lab/internal/topology"
)

// SimulatedDevice represents a network device in the simulator
type SimulatedDevice struct {
	*topology.Device
	MAC      net.HardwareAddr
	ARPTable map[string]net.HardwareAddr // IP -> MAC
	MACTable map[string]int              // MAC -> VLAN (for switches)
	Routes   []*RouteEntry
	VLANs    map[int]*VLANConfig
	IsRouter bool
	IsSwitch bool
	mu       sync.RWMutex
}

// RouteEntry represents a routing table entry
type RouteEntry struct {
	Destination net.IP
	Mask        net.IPMask
	NextHop     net.IP
	Interface   string
	Protocol    string
	Metric      int
}

// VLANConfig represents VLAN configuration
type VLANConfig struct {
	ID     int
	Name   string
	Ports  []string
	Active bool
}

// InterfaceState represents the runtime state of an interface
type InterfaceState struct {
	Name        string
	MAC         net.HardwareAddr
	IP          net.IP
	Mask        net.IPMask
	Gateway     net.IP
	Status      string // up/down
	VLAN        int
	Description string
}

// NewSimulatedDevice creates a new simulated device
func NewSimulatedDevice(dev *topology.Device) *SimulatedDevice {
	sd := &SimulatedDevice{
		Device:   dev,
		MAC:      generateMAC(dev.ID),
		ARPTable: make(map[string]net.HardwareAddr),
		MACTable: make(map[string]int),
		Routes:   []*RouteEntry{},
		VLANs:    make(map[int]*VLANConfig),
	}

	// Determine device type capabilities
	switch dev.Type {
	case topology.DeviceRouter:
		sd.IsRouter = true
	case topology.DeviceL3Switch:
		sd.IsRouter = true
		sd.IsSwitch = true
	case topology.DeviceSwitch:
		sd.IsSwitch = true
	}

	// Initialize VLANs
	sd.VLANs[1] = &VLANConfig{ID: 1, Name: "VLAN1", Ports: []string{}, Active: true}

	return sd
}

// GetInterfaceState returns the current state of an interface
func (d *SimulatedDevice) GetInterfaceState(name string) *InterfaceState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	iface, ok := d.Interfaces[name]
	if !ok {
		return nil
	}

	ip := net.ParseIP(iface.IPAddress)
	mask := net.IPMask(net.ParseIP(iface.SubnetMask).To4())
	mac := d.MAC

	return &InterfaceState{
		Name:    iface.Name,
		MAC:     mac,
		IP:      ip,
		Mask:    mask,
		Gateway: net.ParseIP(iface.Gateway),
		Status:  iface.Status,
		VLAN:    iface.VLAN,
	}
}

// ProcessPacket processes an incoming packet and returns outgoing packets
func (d *SimulatedDevice) ProcessPacket(pkt *Packet, ingressPort string) []*Packet {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Record packet path
	pkt.Path = append(pkt.Path, d.ID)

	// Handle ARP packets at L2
	if pkt.EtherType == EtherTypeARP {
		return d.processARP(pkt)
	}

	// Decrement TTL for IP packets
	pkt.TTL--
	if pkt.TTL <= 0 {
		return []*Packet{} // Packet dropped due to TTL expiration
	}

	// Handle based on device type
	if d.IsSwitch {
		return d.processSwitchPacket(pkt, ingressPort)
	}
	if d.IsRouter {
		return d.processRouterPacket(pkt, ingressPort)
	}

	// End device (PC/Server)
	return d.processEndDevicePacket(pkt)
}

// processSwitchPacket handles L2 switching
func (d *SimulatedDevice) processSwitchPacket(pkt *Packet, ingressPort string) []*Packet {
	// Learn source MAC
	if pkt.SrcMAC != nil {
		vlanID := pkt.VLANID
		if vlanID == 0 {
			vlanID = 1 // Default VLAN
		}
		d.MACTable[pkt.SrcMAC.String()] = vlanID
	}

	// If destination is broadcast or multicast, flood to all ports in VLAN
	if pkt.DstMAC == nil || isBroadcastMAC(pkt.DstMAC) {
		return d.floodPacket(pkt, ingressPort)
	}

	// Check MAC table for destination
	vlanID := pkt.VLANID
	if vlanID == 0 {
		vlanID = 1
	}

	// Find egress port based on MAC and VLAN
	egressPorts := d.findEgressPorts(pkt.DstMAC, vlanID)
	if len(egressPorts) == 0 {
		// Unknown unicast, flood to all ports in VLAN
		return d.floodPacket(pkt, ingressPort)
	}

	// Forward to known port(s)
	var out []*Packet
	for _, port := range egressPorts {
		if port != ingressPort {
			newPkt := clonePacket(pkt)
			newPkt.Path = append(newPkt.Path, fmt.Sprintf("%s:%s", d.ID, port))
			out = append(out, newPkt)
		}
	}
	return out
}

// processRouterPacket handles L3 routing
func (d *SimulatedDevice) processRouterPacket(pkt *Packet, ingressPort string) []*Packet {
	// Check if packet is for one of our interfaces
	for _, iface := range d.Interfaces {
		if iface.IPAddress == pkt.DstIP.String() {
			// Packet is for us
			return d.processLocalPacket(pkt)
		}
	}

	// Route the packet
	route := d.findRoute(pkt.DstIP)
	if route == nil {
		// No route, send ICMP unreachable
		return d.sendICMPUnreachable(pkt, ingressPort)
	}

	// Determine next hop
	nextHop := route.NextHop
	if nextHop == nil || nextHop.Equal(net.IPv4zero) {
		nextHop = pkt.DstIP
	}

	// Resolve next hop MAC (ARP)
	nextHopMAC := d.resolveARP(nextHop)
	if nextHopMAC == nil {
		// Need to ARP, queue packet and send ARP request
		return d.sendARPRequest(nextHop, route.Interface)
	}

	// Forward packet
	newPkt := clonePacket(pkt)
	newPkt.SrcMAC = d.MAC
	newPkt.DstMAC = nextHopMAC
	newPkt.Path = append(newPkt.Path, fmt.Sprintf("%s:%s", d.ID, route.Interface))

	return []*Packet{newPkt}
}

// processEndDevicePacket handles packets for end devices (PC/Server)
func (d *SimulatedDevice) processEndDevicePacket(pkt *Packet) []*Packet {
	// Check if packet is for us
	if !d.isLocalAddress(pkt.DstIP) {
		return []*Packet{} // Not for us, drop
	}

	// Process based on protocol
	switch pkt.Protocol {
	case ProtocolICMP:
		return d.processICMP(pkt)
	default:
		return []*Packet{}
	}
}

// processLocalPacket processes packets destined for this device
func (d *SimulatedDevice) processLocalPacket(pkt *Packet) []*Packet {
	switch pkt.Protocol {
	case ProtocolICMP:
		return d.processICMP(pkt)
	default:
		return []*Packet{}
	}
}

// processICMP handles ICMP packets
func (d *SimulatedDevice) processICMP(pkt *Packet) []*Packet {
	// For now, just echo reply for echo requests
	if len(pkt.Payload) > 0 && pkt.Payload[0] == ICMPTypeEchoRequest {
		reply := clonePacket(pkt)
		reply.SrcIP, reply.DstIP = reply.DstIP, reply.SrcIP
		reply.SrcMAC, reply.DstMAC = reply.DstMAC, reply.SrcMAC
		reply.Payload[0] = ICMPTypeEchoReply
		reply.TTL = 64
		return []*Packet{reply}
	}
	return []*Packet{}
}

// processARP handles ARP packets
func (d *SimulatedDevice) processARP(pkt *Packet) []*Packet {
	// Parse ARP packet
	arp := parseARPPacket(pkt.Payload)
	if arp == nil {
		return []*Packet{}
	}

	if arp.Operation == 1 { // ARP Request
		// Check if target IP is ours
		for _, iface := range d.Interfaces {
			if iface.IPAddress == arp.TargetIPAddr.String() {
				// Send ARP reply
				replyARP := NewARPPacket(2, d.MAC.String(), iface.IPAddress,
					arp.SenderHWAddr.String(), arp.SenderIPAddr.String())
				replyPkt := NewPacket(d.MAC.String(), arp.SenderHWAddr.String(),
					net.ParseIP(iface.IPAddress), arp.SenderIPAddr, 0, serializeARPPacket(replyARP))
				replyPkt.EtherType = EtherTypeARP
				return []*Packet{replyPkt}
			}
		}
	} else if arp.Operation == 2 { // ARP Reply
		// Learn the mapping
		d.ARPTable[arp.SenderIPAddr.String()] = arp.SenderHWAddr
	}

	return []*Packet{}
}

// findRoute finds the best route for a destination IP
func (d *SimulatedDevice) findRoute(dst net.IP) *RouteEntry {
	var bestRoute *RouteEntry
	bestPrefixLen := -1

	for _, route := range d.Routes {
		if route.Destination == nil {
			// Default route
			if bestRoute == nil {
				bestRoute = route
			}
			continue
		}

		if route.Mask == nil {
			continue
		}

		// Check if destination is in this network
		if dst.Mask(route.Mask).Equal(route.Destination.Mask(route.Mask)) {
			prefixLen, _ := route.Mask.Size()
			if prefixLen > bestPrefixLen {
				bestPrefixLen = prefixLen
				bestRoute = route
			}
		}
	}

	return bestRoute
}

// resolveARP resolves IP to MAC using ARP table
func (d *SimulatedDevice) resolveARP(ip net.IP) net.HardwareAddr {
	if mac, ok := d.ARPTable[ip.String()]; ok {
		return mac
	}
	return nil
}

// sendARPRequest sends an ARP request
func (d *SimulatedDevice) sendARPRequest(targetIP net.IP, ifaceName string) []*Packet {
	iface, ok := d.Interfaces[ifaceName]
	if !ok {
		return []*Packet{}
	}

	arp := NewARPPacket(1, d.MAC.String(), iface.IPAddress,
		"00:00:00:00:00:00", targetIP.String())

	broadcastMAC, _ := net.ParseMAC("ff:ff:ff:ff:ff:ff")
	pkt := NewPacket(d.MAC.String(), broadcastMAC.String(),
		net.ParseIP(iface.IPAddress), targetIP, 0, serializeARPPacket(arp))
	pkt.EtherType = EtherTypeARP

	return []*Packet{pkt}
}

// sendICMPUnreachable sends ICMP destination unreachable
func (d *SimulatedDevice) sendICMPUnreachable(pkt *Packet, ifaceName string) []*Packet {
	// Simplified: just drop the packet for now
	return []*Packet{}
}

// isLocalAddress checks if an IP is local to this device
func (d *SimulatedDevice) isLocalAddress(ip net.IP) bool {
	for _, iface := range d.Interfaces {
		if iface.IPAddress == ip.String() {
			return true
		}
	}
	return false
}

// findEgressPorts finds the egress port(s) for a MAC address in a VLAN
func (d *SimulatedDevice) findEgressPorts(dstMAC net.HardwareAddr, vlanID int) []string {
	var ports []string

	// Check MAC table
	macStr := dstMAC.String()
	if learnedVLAN, ok := d.MACTable[macStr]; ok && learnedVLAN == vlanID {
		// Find which port this MAC is on
		for _, iface := range d.Interfaces {
			if iface.Status == "up" {
				ports = append(ports, iface.Name)
			}
		}
	}

	return ports
}

// floodPacket floods a packet to all ports in a VLAN except ingress
func (d *SimulatedDevice) floodPacket(pkt *Packet, ingressPort string) []*Packet {
	var out []*Packet

	vlanID := pkt.VLANID
	if vlanID == 0 {
		vlanID = 1
	}

	for _, iface := range d.Interfaces {
		if iface.Name != ingressPort && iface.Status == "up" {
			newPkt := clonePacket(pkt)
			newPkt.Path = append(newPkt.Path, fmt.Sprintf("%s:%s", d.ID, iface.Name))
			out = append(out, newPkt)
		}
	}

	return out
}

// Helper functions

func generateMAC(deviceID string) net.HardwareAddr {
	// Generate a deterministic MAC based on device ID
	hash := 0
	for i, c := range deviceID {
		hash += int(c) * (i + 1)
	}
	return net.HardwareAddr{0x00, 0xe0, 0xfc,
		byte(hash >> 8), byte(hash >> 4), byte(hash)}
}

func isBroadcastMAC(mac net.HardwareAddr) bool {
	for _, b := range mac {
		if b != 0xff {
			return false
		}
	}
	return true
}

func clonePacket(pkt *Packet) *Packet {
	newPkt := *pkt
	newPkt.Payload = make([]byte, len(pkt.Payload))
	copy(newPkt.Payload, pkt.Payload)
	newPkt.Path = make([]string, len(pkt.Path))
	copy(newPkt.Path, pkt.Path)
	return &newPkt
}

func parseARPPacket(data []byte) *ARPPacket {
	if len(data) < 28 {
		return nil
	}
	return &ARPPacket{
		HardwareType: uint16(data[0])<<8 | uint16(data[1]),
		ProtocolType: uint16(data[2])<<8 | uint16(data[3]),
		HWAddrLen:    data[4],
		ProtoAddrLen: data[5],
		Operation:    uint16(data[6])<<8 | uint16(data[7]),
		SenderHWAddr: net.HardwareAddr(data[8:14]),
		SenderIPAddr: net.IP(data[14:18]),
		TargetHWAddr: net.HardwareAddr(data[18:24]),
		TargetIPAddr: net.IP(data[24:28]),
	}
}

func serializeARPPacket(arp *ARPPacket) []byte {
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

// AddRoute adds a route to the routing table
func (d *SimulatedDevice) AddRoute(dest, mask, nextHop, iface string, metric int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Routes = append(d.Routes, &RouteEntry{
		Destination: net.ParseIP(dest),
		Mask:        net.IPMask(net.ParseIP(mask).To4()),
		NextHop:     net.ParseIP(nextHop),
		Interface:   iface,
		Protocol:    "static",
		Metric:      metric,
	})
}

// AddVLAN adds a VLAN to the device
func (d *SimulatedDevice) AddVLAN(id int, name string, ports []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.VLANs[id] = &VLANConfig{
		ID:     id,
		Name:   name,
		Ports:  ports,
		Active: true,
	}
}

// GetARPTable returns the ARP table as strings
func (d *SimulatedDevice) GetARPTable() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var entries []string
	for ip, mac := range d.ARPTable {
		entries = append(entries, fmt.Sprintf("%s -> %s", ip, mac))
	}
	return entries
}

// GetMACTable returns the MAC table as strings
func (d *SimulatedDevice) GetMACTable() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var entries []string
	for mac, vlan := range d.MACTable {
		entries = append(entries, fmt.Sprintf("%s VLAN:%d", mac, vlan))
	}
	return entries
}

// String returns device info
func (d *SimulatedDevice) String() string {
	return fmt.Sprintf("%s[%s] MAC:%s Router:%v Switch:%v",
		d.Name, d.Type, d.MAC, d.IsRouter, d.IsSwitch)
}
