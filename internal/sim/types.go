package sim

import (
	"fmt"
	"net"
	"time"
)

// EtherType represents the type of payload carried by an Ethernet frame.
type EtherType uint16

// Common EtherType values used by the simulator.
const (
	EtherTypeIPv4 EtherType = 0x0800
	EtherTypeARP  EtherType = 0x0806
	EtherTypeVLAN EtherType = 0x8100
)

// IP protocol numbers (RFC 790).
const (
	ProtocolICMP uint8 = 1
	ProtocolTCP  uint8 = 6
	ProtocolUDP  uint8 = 17
)

// ICMP type codes (RFC 792).
const (
	ICMPTypeEchoReply   uint8 = 0
	ICMPTypeEchoRequest uint8 = 8
)

// Packet represents a network packet at the data link layer.
//
// This struct mirrors simulator.Packet so that the existing API and
// protocol layers can be migrated incrementally without breaking the
// JSON contract exposed to the frontend.
type Packet struct {
	ID        string
	SrcMAC    net.HardwareAddr
	DstMAC    net.HardwareAddr
	SrcIP     net.IP
	DstIP     net.IP
	Protocol  uint8
	VLANID    int
	EtherType EtherType
	Payload   []byte
	TTL       int
	Timestamp time.Time
	Path      []string
}

// PacketEventType classifies a PacketEvent for visualization.
type PacketEventType string

// PacketEventSend, PacketEventReceive, PacketEventForward and PacketEventDrop
// describe the four lifecycle stages of a packet observed by the engine.
const (
	PacketEventSend    PacketEventType = "send"
	PacketEventReceive PacketEventType = "receive"
	PacketEventForward PacketEventType = "forward"
	PacketEventDrop    PacketEventType = "drop"
)

// PacketEvent describes a single packet lifecycle event.
//
// It is consumed by the API layer (SSE endpoint) and by the frontend
// packet animator. Keep field names aligned with the existing JSON
// contract (snake_case) to avoid breaking the UI.
type PacketEvent struct {
	PacketID    string
	Type        PacketEventType
	DeviceID    string
	Interface   string
	Timestamp   time.Time
	Description string
	Path        []string
}

// PacketListener is invoked synchronously whenever a PacketEvent is
// produced by the engine. Implementations must not block.
type PacketListener func(event *PacketEvent)

// PingResult summarises a ping operation.
//
// RTTMs holds per-reply round-trip time in milliseconds (len == Received).
// A nil/empty slice indicates no RTT samples (e.g. total loss); callers
// must not index it without checking Received first.
type PingResult struct {
	Sent     int
	Received int
	Lost     int
	RTTMs    []float64
	Details  []string
}

// TracerouteHop is a single hop in a traceroute result.
//
// DeviceID is the device that answered (or "timeout" when the hop
// exceeded the max TTL without a reply). IP is the hop's responding
// address; for the final destination DeviceID equals the target device.
// DelayMs is the simulated one-way latency to reach this hop from the
// previous one (0 when unknown).
type TracerouteHop struct {
	Hop      int
	DeviceID string
	IP       string
	DelayMs  float64
}

// TracerouteResult summarises a traceroute operation.
//
// Hops contains one entry per TTL probed. Reached indicates the final
// destination answered within maxTTL; if false, the path is
// inconclusive (no reply before TTL expired).
type TracerouteResult struct {
	TargetIP string
	MaxTTL   int
	Hops     []TracerouteHop
	Reached  bool
}

// ProtocolName returns a human-readable name for the packet's protocol.
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

// GenerateMAC returns a deterministic MAC address derived from the
// given device id. The same algorithm is used by the legacy
// internal/simulator package (generateMAC) so that devices keep a
// stable L2 address across both backends.
//
// The OUI 00:e0:fc is reserved for Huawei devices, which matches the
// VRP-flavoured device models exposed by the topology layer.
func GenerateMAC(deviceID string) net.HardwareAddr {
	hash := 0
	for i, c := range deviceID {
		hash += int(c) * (i + 1)
	}
	return net.HardwareAddr{0x00, 0xe0, 0xfc,
		byte(hash >> 8), byte(hash >> 4), byte(hash)}
}
