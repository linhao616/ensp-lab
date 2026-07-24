//go:build ignore

package simulator

import (
	"fmt"
	"net"
	"time"
)

// EtherType represents the type of payload in an Ethernet frame
type EtherType uint16

const (
	EtherTypeIPv4 EtherType = 0x0800
	EtherTypeARP  EtherType = 0x0806
	EtherTypeVLAN EtherType = 0x8100
)

// Packet represents a network packet at the data link layer
type Packet struct {
	ID        string
	SrcMAC    net.HardwareAddr
	DstMAC    net.HardwareAddr
	SrcIP     net.IP
	DstIP     net.IP
	Protocol  uint8 // IP protocol number
	VLANID    int
	EtherType EtherType
	Payload   []byte
	TTL       int
	Timestamp time.Time
	Path      []string // Device IDs the packet has traversed
}

// EthernetFrame represents the L2 frame
type EthernetFrame struct {
	DstMAC    net.HardwareAddr
	SrcMAC    net.HardwareAddr
	VLANTag   *VLANTag
	EtherType EtherType
	Payload   []byte
}

// VLANTag represents 802.1Q VLAN tag
type VLANTag struct {
	Priority uint8
	CFI      uint8
	VLANID   uint16
}

// ARPPacket represents an ARP packet
type ARPPacket struct {
	HardwareType uint16
	ProtocolType uint16
	HWAddrLen    uint8
	ProtoAddrLen uint8
	Operation    uint16 // 1=request, 2=reply
	SenderHWAddr net.HardwareAddr
	SenderIPAddr net.IP
	TargetHWAddr net.HardwareAddr
	TargetIPAddr net.IP
}

// IPPacket represents an IPv4 packet
type IPPacket struct {
	Version        uint8
	IHL            uint8
	TOS            uint8
	TotalLength    uint16
	ID             uint16
	Flags          uint8
	FragmentOffset uint16
	TTL            uint8
	Protocol       uint8
	Checksum       uint16
	SrcIP          net.IP
	DstIP          net.IP
	Payload        []byte
}

// ICMPPacket represents an ICMP packet
type ICMPPacket struct {
	Type     uint8
	Code     uint8
	Checksum uint16
	ID       uint16
	Seq      uint16
	Payload  []byte
}

// TCPPacket represents a TCP segment
type TCPPacket struct {
	SrcPort  uint16
	DstPort  uint16
	SeqNum   uint32
	AckNum   uint32
	Flags    uint8
	Window   uint16
	Checksum uint16
	Urgent   uint16
	Payload  []byte
}

// UDPPacket represents a UDP datagram
type UDPPacket struct {
	SrcPort  uint16
	DstPort  uint16
	Length   uint16
	Checksum uint16
	Payload  []byte
}

// NewPacket creates a new packet
func NewPacket(srcMAC, dstMAC string, srcIP, dstIP net.IP, proto uint8, payload []byte) *Packet {
	sm, _ := net.ParseMAC(srcMAC)
	dm, _ := net.ParseMAC(dstMAC)
	return &Packet{
		ID:        fmt.Sprintf("pkt-%d", time.Now().UnixNano()),
		SrcMAC:    sm,
		DstMAC:    dm,
		SrcIP:     srcIP,
		DstIP:     dstIP,
		Protocol:  proto,
		VLANID:    0,
		EtherType: EtherTypeIPv4,
		Payload:   payload,
		TTL:       64,
		Timestamp: time.Now(),
		Path:      []string{},
	}
}

// NewARPPacket creates an ARP packet
func NewARPPacket(op uint16, senderMAC, senderIP, targetMAC, targetIP string) *ARPPacket {
	sm, _ := net.ParseMAC(senderMAC)
	tm, _ := net.ParseMAC(targetMAC)
	return &ARPPacket{
		HardwareType: 1,
		ProtocolType: 0x0800,
		HWAddrLen:    6,
		ProtoAddrLen: 4,
		Operation:    op,
		SenderHWAddr: sm,
		SenderIPAddr: net.ParseIP(senderIP).To4(),
		TargetHWAddr: tm,
		TargetIPAddr: net.ParseIP(targetIP).To4(),
	}
}

// NewICMPPacket creates an ICMP echo request/reply
func NewICMPPacket(pktType uint8, code uint8, id, seq uint16, payload []byte) *ICMPPacket {
	return &ICMPPacket{
		Type:     pktType,
		Code:     code,
		ID:       id,
		Seq:      seq,
		Payload:  payload,
		Checksum: 0, // Will be calculated
	}
}

// Protocol names
const (
	ProtocolICMP uint8 = 1
	ProtocolTCP  uint8 = 6
	ProtocolUDP  uint8 = 17
)

// ICMP types
const (
	ICMPTypeEchoReply   uint8 = 0
	ICMPTypeEchoRequest uint8 = 8
)

// String returns a human-readable description of the packet
func (p *Packet) String() string {
	protoName := "Unknown"
	switch p.Protocol {
	case ProtocolICMP:
		protoName = "ICMP"
	case ProtocolTCP:
		protoName = "TCP"
	case ProtocolUDP:
		protoName = "UDP"
	}
	
	vlanInfo := ""
	if p.VLANID > 0 {
		vlanInfo = fmt.Sprintf(" VLAN:%d", p.VLANID)
	}
	
	return fmt.Sprintf("[%s] %s -> %s (%s)%s TTL:%d",
		p.ID[:12], p.SrcIP, p.DstIP, protoName, vlanInfo, p.TTL)
}
