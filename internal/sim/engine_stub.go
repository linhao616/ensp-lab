package sim

import (
	"context"
	"fmt"
	"sync"

	"ensp-lab/internal/topology"
)

// stubEngine is a placeholder Engine implementation used until the
// ns-x-backed implementation lands in Task 2. It records listener
// registrations and channels but does not actually simulate packets.
type stubEngine struct {
	mu        sync.RWMutex
	listeners []PacketListener
	eventCh   chan *PacketEvent
	stopped   chan struct{}
}

// NewStubEngine returns an Engine that satisfies the interface contract
// without performing real simulation. Useful for bootstrapping the API
// layer before the ns-x implementation is wired in.
func NewStubEngine() Engine {
	return &stubEngine{
		eventCh: make(chan *PacketEvent, 1024),
		stopped: make(chan struct{}),
	}
}

func (s *stubEngine) Start()                             {}
func (s *stubEngine) Stop()                              {}
func (s *stubEngine) SendPacket(*Packet, string, string) {}
func (s *stubEngine) Ping(string, string) (*PingResult, error) {
	return nil, fmt.Errorf("stub engine: ping not implemented")
}
func (s *stubEngine) AddPacketListener(listener PacketListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)
}
func (s *stubEngine) Events() <-chan *PacketEvent { return s.eventCh }
func (s *stubEngine) Run(ctx context.Context) error {
	<-ctx.Done()
	close(s.stopped)
	return ctx.Err()
}

// Mode returns "stub" to indicate that the stubEngine does not perform
// real simulation. Satisfies the Engine interface.
func (s *stubEngine) Mode() string { return "stub" }

// Rebuild 是 stubEngine 的空实现：stub 不维护真实节点图，因此直接返回 nil。
// 满足 Engine 接口的 Rebuild 方法与 topology.NodeRebuilder 契约。
func (s *stubEngine) Rebuild(*topology.Topology) error { return nil }

func (s *stubEngine) CapturePCAP(ctx context.Context, deviceID, ifaceName string, pktChan chan<- []byte) (func(), error) {
	return nil, fmt.Errorf("stub engine: pcap capture not implemented")
}

func (s *stubEngine) QueueDepth() int {
	return 0
}
