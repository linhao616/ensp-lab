package protocol

import (
	"net"
	"testing"

	"ensp-lab/internal/sim"
)

func TestHandlerRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := NewHandlerRegistry()
	if h := r.Lookup("icmp"); h != nil {
		t.Fatal("expected nil before Register")
	}
	r.Register("icmp", &ICMPProtocol{})
	if h := r.Lookup("icmp"); h == nil {
		t.Fatal("expected handler after Register")
	}
}

func TestHandlerRegistry_NilSafe(t *testing.T) {
	t.Parallel()
	var r *HandlerRegistry
	r.Register("icmp", &ICMPProtocol{}) // must not panic
	if h := r.Lookup("icmp"); h != nil {
		t.Fatal("expected nil lookup on nil registry")
	}
	if all := r.All(); all != nil {
		t.Fatal("expected nil All() on nil registry")
	}
}

func TestICMPProtocol_HandlePacket_EchoReply(t *testing.T) {
	t.Parallel()
	p := NewICMPProtocol("dev1")
	// Construct an ICMP Echo Request whose destination matches a
	// local interface — for the protocol-layer unit test we do not
	// have a topology, so any IP works because the handler replies
	// unconditionally on Echo Request.
	request := &sim.Packet{
		ID:       "pkt-1",
		Protocol: sim.ProtocolICMP,
		SrcIP:    net.IPv4(10, 0, 0, 1),
		DstIP:    net.IPv4(10, 0, 0, 2),
		Payload:  []byte{sim.ICMPTypeEchoRequest, 0, 0, 0, 0, 1, 0, 1, 'p'},
		TTL:      64,
	}
	out := p.HandlePacket(request)
	if len(out) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(out))
	}
	reply := out[0]
	if reply.Payload[0] != sim.ICMPTypeEchoReply {
		t.Fatalf("expected echo reply type, got %d", reply.Payload[0])
	}
	if !reply.SrcIP.Equal(request.DstIP) || !reply.DstIP.Equal(request.SrcIP) {
		t.Fatalf("addresses not swapped: reply=%v->%v", reply.SrcIP, reply.DstIP)
	}
}

func TestICMPProtocol_HandlePacket_DisabledDrops(t *testing.T) {
	t.Parallel()
	p := NewICMPProtocol("dev1")
	p.Disable()
	request := &sim.Packet{
		Protocol: sim.ProtocolICMP,
		Payload:  []byte{sim.ICMPTypeEchoRequest},
	}
	if out := p.HandlePacket(request); out != nil {
		t.Fatalf("expected nil when disabled, got %v", out)
	}
}

func TestICMPProtocol_HandlePacket_NonICMPDrops(t *testing.T) {
	t.Parallel()
	p := NewICMPProtocol("dev1")
	request := &sim.Packet{
		Protocol: sim.ProtocolTCP,
		Payload:  []byte{0},
	}
	if out := p.HandlePacket(request); out != nil {
		t.Fatalf("expected nil for TCP, got %v", out)
	}
}

func TestARPProtocol_HandlePacket_ReplyFromStatic(t *testing.T) {
	t.Parallel()
	a := NewARPProtocol("dev1")
	if err := a.AddStaticARP("10.0.0.2", "00:11:22:33:44:55", "eth0"); err != nil {
		t.Fatalf("AddStaticARP: %v", err)
	}
	senderMAC, _ := net.ParseMAC("00:aa:bb:cc:dd:ee")
	arp := &arpPayload{
		HardwareType: 1,
		ProtocolType: 0x0800,
		HWAddrLen:    6,
		ProtoAddrLen: 4,
		Operation:    ARPOperationRequest,
		SenderHWAddr: senderMAC,
		SenderIPAddr: net.IPv4(10, 0, 0, 1),
		TargetHWAddr: net.HardwareAddr{0, 0, 0, 0, 0, 0},
		TargetIPAddr: net.IPv4(10, 0, 0, 2),
	}
	request := &sim.Packet{
		EtherType: sim.EtherTypeARP,
		SrcMAC:    senderMAC,
		DstMAC:    net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		SrcIP:     net.IPv4(10, 0, 0, 1),
		DstIP:     net.IPv4(10, 0, 0, 2),
		Payload:   serializeARPPayload(arp),
	}
	out := a.HandlePacket(request)
	if len(out) != 1 {
		t.Fatalf("expected 1 ARP reply, got %d", len(out))
	}
	reply := out[0]
	if reply.SrcMAC.String() != "00:11:22:33:44:55" {
		t.Fatalf("expected reply MAC 00:11:22:33:44:55, got %s", reply.SrcMAC)
	}
}
