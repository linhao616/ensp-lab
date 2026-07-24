package sim

import (
	"context"
	"testing"
	"time"
)

// Verify at compile time that stubEngine implements Engine.
var _ Engine = (*stubEngine)(nil)
var _ Engine = NewStubEngine()

func TestStubEngine_EventsChannelNonNil(t *testing.T) {
	t.Parallel()
	e := NewStubEngine()
	if e.Events() == nil {
		t.Fatal("Events() returned nil channel")
	}
}

func TestStubEngine_AddPacketListenerDoesNotPanic(t *testing.T) {
	t.Parallel()
	e := NewStubEngine()
	e.AddPacketListener(func(*PacketEvent) {})
	e.AddPacketListener(func(*PacketEvent) {})
}

func TestStubEngine_RunCancelsOnContextDone(t *testing.T) {
	t.Parallel()
	e := NewStubEngine()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := e.Run(ctx); err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestPacketProtocolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pkt  *Packet
		want string
	}{
		{"icmp", &Packet{Protocol: ProtocolICMP}, "ICMP"},
		{"tcp", &Packet{Protocol: ProtocolTCP}, "TCP"},
		{"udp", &Packet{Protocol: ProtocolUDP}, "UDP"},
		{"arp", &Packet{EtherType: EtherTypeARP}, "ARP"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.pkt.ProtocolName(); got != tc.want {
				t.Fatalf("ProtocolName() = %q, want %q", got, tc.want)
			}
		})
	}
}
