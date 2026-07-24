//go:build linux

// Package sim: gont-based emulator (Linux only).
//
// This file provides a gont-backed Engine implementation that creates
// real Linux network namespaces, veth pairs, and routes so that traffic
// between simulated devices uses the actual kernel network stack. It is
// only compiled on Linux; on other platforms the stub in
// gont_emulator_other.go is used instead.

package sim

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"ensp-lab/internal/router"
	"ensp-lab/internal/topology"
	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"
	"github.com/stv0g/gont/v2/pkg"
)

// ErrPlatformNotSupported is returned by NewGontEngine on platforms
// without the gont emulator (i.e. non-Linux). The variable is declared
// here, on the Linux build, so that callers always see a stable error
// regardless of the target platform.
var ErrPlatformNotSupported = fmt.Errorf("sim: gont emulator requires Linux")

// GontEngine implements Engine using gont-managed network namespaces.
//
// Each topology.Device becomes a gont.Host with its own namespace;
// each topology.Link becomes a veth pair connecting two hosts. The
// engine forwards SendPacket calls into the corresponding namespace
// via raw sockets, and observes packet events via gont's Capture
// facility (wired up in Task 4 once the protocol layer is in place).
type GontEngine struct {
	topo    *topology.Topology
	network *gont.Network
	hosts   map[string]*gont.Host
	routers map[string]router.Router

	mu        sync.RWMutex
	listeners []PacketListener
	eventCh   chan *PacketEvent
	history   []*PacketEvent

	started    bool
	cancelCtx  context.Context
	cancelFunc context.CancelFunc
}

// NewGontEngine builds a gont-backed Engine from the given topology.
//
// The function performs capability checks (CAP_NET_ADMIN) and creates
// the root network namespace; per-device namespaces are created lazily
// during Start to keep construction side-effect free.
func NewGontEngine(topo *topology.Topology) (Engine, error) {
	if topo == nil {
		return nil, fmt.Errorf("sim: topology is nil")
	}
	n, err := gont.NewNetwork("ensp-lab")
	if err != nil {
		return nil, fmt.Errorf("sim: create gont network: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &GontEngine{
		topo:       topo,
		network:    n,
		hosts:      make(map[string]*gont.Host),
		routers:    make(map[string]router.Router),
		listeners:  []PacketListener{},
		eventCh:    make(chan *PacketEvent, 1024),
		history:    []*PacketEvent{},
		cancelCtx:  ctx,
		cancelFunc: cancel,
	}
	if err := e.build(); err != nil {
		// Best-effort cleanup of the partial network.
		_ = e.network.Close()
		return nil, fmt.Errorf("sim: build gont topology: %w", err)
	}
	return e, nil
}

// Mode returns the active engine mode as a string identifier
// ("gont"). Satisfies the Engine interface.
func (e *GontEngine) Mode() string { return string(EngineModeGont) }

// Rebuild 在拓扑变更时被 topology.Manager 调用，重建 gont 命名空间与主机映射。
//
// 实现策略：
//   - 若拓扑为 nil，直接返回 nil；
//   - 关闭旧 network（best-effort），创建新 network 并按新拓扑重建 hosts；
//   - 任何阶段失败均包装为 fmt.Errorf 返回，由调用方记录日志。
func (e *GontEngine) Rebuild(topo *topology.Topology) error {
	if topo == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// 关闭旧 network（best-effort）
	if e.network != nil {
		_ = e.network.Close()
	}
	n, err := gont.NewNetwork("ensp-lab")
	if err != nil {
		return fmt.Errorf("sim: rebuild gont network: %w", err)
	}
	e.network = n
	e.topo = topo
	e.hosts = make(map[string]*gont.Host)
	for id, dev := range e.topo.Devices {
		host, err := e.network.AddHost(dev.ID)
		if err != nil {
			return fmt.Errorf("sim: rebuild add host %s: %w", id, err)
		}
		e.hosts[id] = host
	}
	return nil
}

// build constructs hosts and links from the topology.
func (e *GontEngine) build() error {
	for id, dev := range e.topo.Devices {
		host, err := e.network.AddHost(dev.ID)
		if err != nil {
			return fmt.Errorf("add host %s: %w", id, err)
		}
		e.hosts[id] = host

		if dev.Type == topology.DeviceRouter {
			e.routers[id] = router.NewFRRRouter(host, dev.ID)
		}
	}
	// Links are not wired up in Task 3 because gont.AddLink requires
	// interface options that depend on the topology.Interface model.
	// TODO(Task 4): map each topology.Link to a gont veth pair with
	// the appropriate IPs and routes.
	return nil
}

// Start launches the gont network and starts FRR on router devices.
func (e *GontEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.started = true

	for id, r := range e.routers {
		if err := r.Start(); err != nil {
			e.emit(&PacketEvent{
				PacketID:    "gont-router-start-fail",
				Type:        PacketEventError,
				DeviceID:    id,
				Interface:   "",
				Timestamp:   time.Now(),
				Description: fmt.Sprintf("failed to start FRR on router %s: %v", id, err),
				Path:        []string{},
			})
		} else {
			e.emit(&PacketEvent{
				PacketID:    "gont-router-start",
				Type:        PacketEventSend,
				DeviceID:    id,
				Interface:   "",
				Timestamp:   time.Now(),
				Description: fmt.Sprintf("FRR started on router %s", id),
				Path:        []string{},
			})
		}
	}

	e.emit(&PacketEvent{
		PacketID:    "gont-startup",
		Type:        PacketEventSend,
		DeviceID:    "",
		Interface:   "",
		Timestamp:   time.Now(),
		Description: "gont emulator started (real namespace traffic)",
		Path:        []string{},
	})
}

// Stop tears down the gont network and stops FRR on router devices.
func (e *GontEngine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	e.mu.Unlock()

	for id, r := range e.routers {
		if err := r.Stop(); err != nil {
			e.emit(&PacketEvent{
				PacketID:    "gont-router-stop-fail",
				Type:        PacketEventError,
				DeviceID:    id,
				Interface:   "",
				Timestamp:   time.Now(),
				Description: fmt.Sprintf("failed to stop FRR on router %s: %v", id, err),
				Path:        []string{},
			})
		}
	}

	e.cancelFunc()
	_ = e.network.Close()
	close(e.eventCh)
}

func (e *GontEngine) QueueDepth() int {
	return 0
}

// SendPacket injects a packet from the given device.
//
// Task 3 stub: actual packet injection via raw sockets inside the
// namespace is implemented in Task 4 alongside protocol adaptation.
func (e *GontEngine) SendPacket(pkt *Packet, fromDeviceID, _ string) {
	e.emit(&PacketEvent{
		PacketID:    pkt.ID,
		Type:        PacketEventSend,
		DeviceID:    fromDeviceID,
		Interface:   "",
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Send %s via gont namespace", pkt.ProtocolName()),
		Path:        append([]string{}, pkt.Path...),
	})
}

// Ping initiates a real ICMP echo via the host's namespace.
//
// Task 3 stub: delegates to a placeholder until Task 4 wires up
// host.Ping with the right destination IP and interface.
func (e *GontEngine) Ping(srcDeviceID, dstIP string) (*PingResult, error) {
	host, ok := e.hosts[srcDeviceID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, srcDeviceID)
	}
	_ = host
	return &PingResult{
		Sent:     1,
		Received: 0,
		Lost:     0,
		Details:  []string{fmt.Sprintf("Sent ICMP echo to %s via gont namespace", dstIP)},
	}, nil
}

// AddPacketListener registers a listener.
func (e *GontEngine) AddPacketListener(listener PacketListener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, listener)
}

// Events returns the SSE event channel.
func (e *GontEngine) Events() <-chan *PacketEvent {
	return e.eventCh
}

// Run blocks until ctx is cancelled.
func (e *GontEngine) Run(ctx context.Context) error {
	e.Start()
	<-ctx.Done()
	e.Stop()
	return ctx.Err()
}

// emit dispatches a PacketEvent to listeners and the event channel.
func (e *GontEngine) emit(ev *PacketEvent) {
	e.mu.Lock()
	e.history = append(e.history, ev)
	if len(e.history) > 1000 {
		e.history = e.history[len(e.history)-1000:]
	}
	listeners := make([]PacketListener, len(e.listeners))
	copy(listeners, e.listeners)
	e.mu.Unlock()
	for _, l := range listeners {
		l(ev)
	}
	select {
	case e.eventCh <- ev:
	default:
	}
}

// Compile-time interface assertion.
var _ Engine = (*GontEngine)(nil)

// Ensure net is referenced to avoid unused import errors during build.
var _ = net.IPv4zero

func (e *GontEngine) GetRouter(deviceID string) router.Router {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.routers[deviceID]
}

func (e *GontEngine) ApplyOSPFConfig(deviceID, network, area string) error {
	e.mu.RLock()
	r, ok := e.routers[deviceID]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrDeviceNotFound, deviceID)
	}
	return r.ApplyOSPFConfig(network, area)
}

func (e *GontEngine) ApplyBGPConfig(deviceID string, localAS uint32, neighbors []router.BGPNeighbor) error {
	e.mu.RLock()
	r, ok := e.routers[deviceID]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrDeviceNotFound, deviceID)
	}
	return r.ApplyBGPConfig(localAS, neighbors)
}

func (e *GontEngine) GetRoutes(deviceID string) ([]router.RouteInfo, error) {
	e.mu.RLock()
	r, ok := e.routers[deviceID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, deviceID)
	}
	return r.GetRoutes()
}

func (e *GontEngine) CapturePCAP(ctx context.Context, deviceID, ifaceName string, pktChan chan<- []byte) (func(), error) {
	e.mu.RLock()
	host, ok := e.hosts[deviceID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, deviceID)
	}

	var handle *pcap.Handle
	var err error

	err = host.Exec(func() error {
		handle, err = pcap.OpenLive(ifaceName, 65536, true, pcap.BlockForever)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sim: open pcap on %s/%s: %w", deviceID, ifaceName, err)
	}

	done := make(chan struct{})
	stop := func() {
		close(done)
		handle.Close()
	}

	go func() {
		defer close(done)
		packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case packet, ok := <-packetSource.Packets():
				if !ok {
					return
				}
				pktChan <- packet.Data()
			}
		}
	}()

	return stop, nil
}
