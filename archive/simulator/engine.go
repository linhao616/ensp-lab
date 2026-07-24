//go:build ignore

// Package simulator 提供包级别的网络仿真引擎，管理设备状态和数据包转发。
//
// ⚠️ 当前状态：暂未启用（预留架构）
//   - 被 Gateway 架构引用，但 Gateway 暂未启用
//   - 启用方式：在 main.go 中启用 Gateway 架构后自动生效
//   - 当前主流程使用 internal/sim 包作为仿真引擎
package simulator

import (
	"fmt"
	"net"
	"sync"
	"time"

	"ensp-lab/internal/topology"
)

// SimulationEngine manages the packet-level simulation
type SimulationEngine struct {
	topology      *topology.Topology
	devices       map[string]*SimulatedDevice
	links         map[string]*LinkState
	packetQueue   chan *QueuedPacket
	active        bool
	mu            sync.RWMutex
	listeners     []PacketListener
	packetHistory []*PacketEvent
}

// LinkState represents the runtime state of a link
type LinkState struct {
	*topology.Link
	SrcDevice *SimulatedDevice
	DstDevice *SimulatedDevice
	Status    string // up/down
}

// QueuedPacket represents a packet waiting to be processed
type QueuedPacket struct {
	Packet      *Packet
	DeviceID    string
	IngressPort string
}

// PacketEvent represents a packet event for visualization
type PacketEvent struct {
	PacketID    string
	Type        string // send/receive/forward/drop
	DeviceID    string
	Interface   string
	Timestamp   time.Time
	Description string
	Path        []string
}

// PacketListener is called when a packet event occurs
type PacketListener func(event *PacketEvent)

// NewSimulationEngine creates a new simulation engine
func NewSimulationEngine(topo *topology.Topology) *SimulationEngine {
	return &SimulationEngine{
		topology:      topo,
		devices:       make(map[string]*SimulatedDevice),
		links:         make(map[string]*LinkState),
		packetQueue:   make(chan *QueuedPacket, 1000),
		active:        false,
		listeners:     []PacketListener{},
		packetHistory: []*PacketEvent{},
	}
}

// Initialize sets up the simulation with the current topology
func (e *SimulationEngine) Initialize() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Create simulated devices
	for _, dev := range e.topology.Devices {
		sd := NewSimulatedDevice(dev)
		e.devices[dev.ID] = sd
	}

	// Create link states
	for _, link := range e.topology.Links {
		srcDev := e.devices[link.SourceDevice]
		dstDev := e.devices[link.TargetDevice]
		
		if srcDev == nil || dstDev == nil {
			continue
		}

		ls := &LinkState{
			Link:      link,
			SrcDevice: srcDev,
			DstDevice: dstDev,
			Status:    "up",
		}
		e.links[link.ID] = ls

		// Set interface status to up
		if iface, ok := srcDev.Interfaces[link.SourcePort]; ok {
			iface.Status = "up"
		}
		if iface, ok := dstDev.Interfaces[link.TargetPort]; ok {
			iface.Status = "up"
		}
	}

	// Initialize routing tables based on interface IPs
	e.initializeRoutingTables()

	return nil
}

// Start begins the simulation loop
func (e *SimulationEngine) Start() {
	e.mu.Lock()
	if e.active {
		e.mu.Unlock()
		return
	}
	e.active = true
	e.mu.Unlock()

	go e.simulationLoop()
}

// Stop halts the simulation
func (e *SimulationEngine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = false
}

// simulationLoop processes packets from the queue
func (e *SimulationEngine) simulationLoop() {
	for {
		e.mu.RLock()
		if !e.active {
			e.mu.RUnlock()
			return
		}
		e.mu.RUnlock()

		select {
		case qp := <-e.packetQueue:
			e.processQueuedPacket(qp)
		case <-time.After(100 * time.Millisecond):
			// Timeout to check if still active
		}
	}
}

// processQueuedPacket processes a single packet
func (e *SimulationEngine) processQueuedPacket(qp *QueuedPacket) {
	dev := e.devices[qp.DeviceID]
	if dev == nil {
		return
	}

	// Emit receive event
	e.emitEvent(&PacketEvent{
		PacketID:    qp.Packet.ID,
		Type:        "receive",
		DeviceID:    qp.DeviceID,
		Interface:   qp.IngressPort,
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Received %s on %s", qp.Packet.ProtocolName(), qp.IngressPort),
		Path:        qp.Packet.Path,
	})

	// Process packet through device
	outPackets := dev.ProcessPacket(qp.Packet, qp.IngressPort)

	if len(outPackets) == 0 {
		// Packet dropped
		e.emitEvent(&PacketEvent{
			PacketID:    qp.Packet.ID,
			Type:        "drop",
			DeviceID:    qp.DeviceID,
			Interface:   qp.IngressPort,
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Dropped %s", qp.Packet.ProtocolName()),
			Path:        qp.Packet.Path,
		})
		return
	}

	// Forward packets to next hop(s)
	for _, pkt := range outPackets {
		e.forwardPacket(pkt, qp.DeviceID)
	}
}

// forwardPacket forwards a packet to the next device
func (e *SimulationEngine) forwardPacket(pkt *Packet, fromDeviceID string) {
	// Find the link to forward on
	lastHop := ""
	if len(pkt.Path) > 0 {
		lastHop = pkt.Path[len(pkt.Path)-1]
	}

	// Parse last hop to get egress interface
	egressIface := ""
	if lastHop != "" && len(lastHop) > len(fromDeviceID) {
		egressIface = lastHop[len(fromDeviceID)+1:]
	}

	// Find connected device
	for _, link := range e.links {
		if link.Status != "up" {
			continue
		}

		var nextDevID string
		var ingressPort string

		if link.SourceDevice == fromDeviceID && link.SourcePort == egressIface {
			nextDevID = link.TargetDevice
			ingressPort = link.TargetPort
		} else if link.TargetDevice == fromDeviceID && link.TargetPort == egressIface {
			nextDevID = link.SourceDevice
			ingressPort = link.SourcePort
		}

		if nextDevID != "" {
			// Emit forward event
			e.emitEvent(&PacketEvent{
				PacketID:    pkt.ID,
				Type:        "forward",
				DeviceID:    fromDeviceID,
				Interface:   egressIface,
				Timestamp:   time.Now(),
				Description: fmt.Sprintf("Forward %s to %s", pkt.ProtocolName(), nextDevID),
				Path:        pkt.Path,
			})

			// Queue packet for next device
			e.packetQueue <- &QueuedPacket{
				Packet:      pkt,
				DeviceID:    nextDevID,
				IngressPort: ingressPort,
			}
			return
		}
	}

	// No link found, packet dropped
	e.emitEvent(&PacketEvent{
		PacketID:    pkt.ID,
		Type:        "drop",
		DeviceID:    fromDeviceID,
		Interface:   egressIface,
		Timestamp:   time.Now(),
		Description: "No route to destination",
		Path:        pkt.Path,
	})
}

// SendPacket injects a packet into the simulation from a device
func (e *SimulationEngine) SendPacket(pkt *Packet, fromDeviceID, fromInterface string) {
	e.packetQueue <- &QueuedPacket{
		Packet:      pkt,
		DeviceID:    fromDeviceID,
		IngressPort: fromInterface,
	}
}

// Ping initiates a ping from one device to another
func (e *SimulationEngine) Ping(srcDeviceID, dstIP string) (*PingResult, error) {
	srcDev := e.devices[srcDeviceID]
	if srcDev == nil {
		return nil, fmt.Errorf("source device not found: %s", srcDeviceID)
	}

	// Find source interface
	var srcIface *topology.Interface
	var srcIP net.IP
	for _, iface := range srcDev.Interfaces {
		if iface.Status == "up" && iface.IPAddress != "" {
			srcIface = iface
			srcIP = net.ParseIP(iface.IPAddress)
			break
		}
	}

	if srcIface == nil {
		return nil, fmt.Errorf("no active interface on source device")
	}

	dst := net.ParseIP(dstIP)
	if dst == nil {
		return nil, fmt.Errorf("invalid destination IP: %s", dstIP)
	}

	// Create ICMP echo request
	icmp := NewICMPPacket(ICMPTypeEchoRequest, 0, 1, 1, []byte("ping"))
	payload := serializeICMPPacket(icmp)
	
	pkt := NewPacket(srcDev.MAC.String(), "ff:ff:ff:ff:ff:ff", srcIP, dst, ProtocolICMP, payload)
	
	result := &PingResult{
		Sent:     1,
		Received: 0,
		Lost:     0,
		Details:  []string{},
	}

	// Send packet and wait for response
	e.SendPacket(pkt, srcDeviceID, srcIface.Name)

	// For simulation, we'll check if the packet can reach the destination
	// In a real implementation, we'd wait for the response asynchronously
	path := e.traceRoute(pkt)
	if len(path) > 0 {
		result.Received = 1
		result.Details = append(result.Details, fmt.Sprintf("Reply from %s: bytes=32 time=1ms TTL=%d", dstIP, pkt.TTL))
	} else {
		result.Lost = 1
		result.Details = append(result.Details, fmt.Sprintf("Request timed out for %s", dstIP))
	}

	return result, nil
}

// PingResult represents the result of a ping operation
type PingResult struct {
	Sent     int
	Received int
	Lost     int
	Details  []string
}

// traceRoute traces the route a packet would take
func (e *SimulationEngine) traceRoute(pkt *Packet) []string {
	// Simplified trace - in reality this would follow the actual simulation
	var path []string
	currentDev := e.findDeviceByIP(pkt.SrcIP)
	
	for i := 0; i < 30; i++ { // Max 30 hops
		if currentDev == nil {
			break
		}
		
		path = append(path, currentDev.ID)
		
		if currentDev.isLocalAddress(pkt.DstIP) {
			break // Reached destination
		}
		
		route := currentDev.findRoute(pkt.DstIP)
		if route == nil {
			break // No route
		}
		
		// Find next device
		nextDev := e.findDeviceOnInterface(currentDev.ID, route.Interface)
		if nextDev == nil {
			break
		}
		
		currentDev = nextDev
	}
	
	return path
}

// findDeviceByIP finds a device by its IP address
func (e *SimulationEngine) findDeviceByIP(ip net.IP) *SimulatedDevice {
	for _, dev := range e.devices {
		if dev.isLocalAddress(ip) {
			return dev
		}
	}
	return nil
}

// findDeviceOnInterface finds the device connected to an interface
func (e *SimulationEngine) findDeviceOnInterface(deviceID, ifaceName string) *SimulatedDevice {
	for _, link := range e.links {
		if link.Status != "up" {
			continue
		}
		
		if link.SourceDevice == deviceID && link.SourcePort == ifaceName {
			return e.devices[link.TargetDevice]
		}
		if link.TargetDevice == deviceID && link.TargetPort == ifaceName {
			return e.devices[link.SourceDevice]
		}
	}
	return nil
}

// AddPacketListener adds a listener for packet events
func (e *SimulationEngine) AddPacketListener(listener PacketListener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, listener)
}

// emitEvent emits a packet event to all listeners
func (e *SimulationEngine) emitEvent(event *PacketEvent) {
	e.mu.Lock()
	e.packetHistory = append(e.packetHistory, event)
	if len(e.packetHistory) > 1000 {
		e.packetHistory = e.packetHistory[len(e.packetHistory)-1000:]
	}
	listeners := make([]PacketListener, len(e.listeners))
	copy(listeners, e.listeners)
	e.mu.Unlock()

	for _, listener := range listeners {
		listener(event)
	}
}

// GetPacketHistory returns recent packet events
func (e *SimulationEngine) GetPacketHistory(limit int) []*PacketEvent {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if limit <= 0 || limit > len(e.packetHistory) {
		limit = len(e.packetHistory)
	}

	start := len(e.packetHistory) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*PacketEvent, limit)
	copy(result, e.packetHistory[start:])
	return result
}

// GetDeviceState returns the state of a device
func (e *SimulationEngine) GetDeviceState(deviceID string) (*SimulatedDevice, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	dev, ok := e.devices[deviceID]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", deviceID)
	}

	return dev, nil
}

// GetLinkState returns the state of a link
func (e *SimulationEngine) GetLinkState(linkID string) (*LinkState, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	link, ok := e.links[linkID]
	if !ok {
		return nil, fmt.Errorf("link not found: %s", linkID)
	}

	return link, nil
}

// UpdateTopology updates the simulation with a new topology
func (e *SimulationEngine) UpdateTopology(topo *topology.Topology) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.topology = topo
	e.devices = make(map[string]*SimulatedDevice)
	e.links = make(map[string]*LinkState)

	// Re-initialize
	for _, dev := range topo.Devices {
		sd := NewSimulatedDevice(dev)
		e.devices[dev.ID] = sd
	}

	for _, link := range topo.Links {
		srcDev := e.devices[link.SourceDevice]
		dstDev := e.devices[link.TargetDevice]
		
		if srcDev == nil || dstDev == nil {
			continue
		}

		ls := &LinkState{
			Link:      link,
			SrcDevice: srcDev,
			DstDevice: dstDev,
			Status:    "up",
		}
		e.links[link.ID] = ls
	}

	e.initializeRoutingTables()
}

// initializeRoutingTables sets up initial routes based on interface IPs
func (e *SimulationEngine) initializeRoutingTables() {
	for _, dev := range e.devices {
		if !dev.IsRouter {
			continue
		}

		// Add connected routes
		for _, iface := range dev.Interfaces {
			if iface.IPAddress == "" || iface.SubnetMask == "" {
				continue
			}

			ip := net.ParseIP(iface.IPAddress)
			mask := net.IPMask(net.ParseIP(iface.SubnetMask).To4())
			
			// Add connected route
			dev.Routes = append(dev.Routes, &RouteEntry{
				Destination: ip.Mask(mask),
				Mask:        mask,
				NextHop:     net.IPv4zero,
				Interface:   iface.Name,
				Protocol:    "connected",
				Metric:      0,
			})
		}
	}
}

// ProtocolName returns the name of the protocol
func (p *Packet) ProtocolName() string {
	switch p.Protocol {
	case ProtocolICMP:
		return "ICMP"
	case ProtocolTCP:
		return "TCP"
	case ProtocolUDP:
		return "UDP"
	case 0:
		if p.EtherType == EtherTypeARP {
			return "ARP"
		}
		return "Unknown"
	default:
		return fmt.Sprintf("Protocol(%d)", p.Protocol)
	}
}

func serializeICMPPacket(icmp *ICMPPacket) []byte {
	data := make([]byte, 8+len(icmp.Payload))
	data[0] = icmp.Type
	data[1] = icmp.Code
	data[2] = 0 // Checksum high
	data[3] = 0 // Checksum low
	data[4] = byte(icmp.ID >> 8)
	data[5] = byte(icmp.ID)
	data[6] = byte(icmp.Seq >> 8)
	data[7] = byte(icmp.Seq)
	copy(data[8:], icmp.Payload)
	return data
}
