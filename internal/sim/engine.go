package sim

import (
	"context"
	"fmt"

	"ensp-lab/internal/topology"
)

// Engine is the abstraction used by the API and topology layers to
// drive packet simulation. The concrete implementation backed by ns-x
// lives in engine_nsx.go (see Task 2); the optional gont-backed
// emulator lives in gont_emulator.go (see Task 3).
//
// The interface intentionally mirrors the public surface of the legacy
// simulator.SimulationEngine so that callers can be migrated with
// minimal changes.
//
// Engine also satisfies topology.NodeRebuilder so that the topology
// Manager can notify the engine of topology changes (device/link
// add/update/remove) without taking a direct dependency on the sim
// package.
type Engine interface {
	// Start launches the simulation loop. Must be idempotent.
	Start()
	// Stop halts the simulation and releases underlying resources.
	Stop()
	// SendPacket injects a packet into the simulation from the given
	// device and ingress interface.
	SendPacket(pkt *Packet, fromDeviceID, fromInterface string)
	// Ping initiates an ICMP echo from srcDeviceID to dstIP and
	// returns the aggregated result.
	Ping(srcDeviceID, dstIP string) (*PingResult, error)
	// AddPacketListener registers a listener invoked on every
	// PacketEvent produced by the engine.
	AddPacketListener(listener PacketListener)
	// Events returns a channel from which the API layer can drain
	// PacketEvents for SSE streaming. The channel is closed when
	// the engine stops.
	Events() <-chan *PacketEvent
	// Run blocks until ctx is cancelled, draining pending events.
	// It is safe to call in a goroutine started by the API layer.
	Run(ctx context.Context) error
	// Mode returns a short identifier of the active backend
	// (e.g. "ns-x", "gont", "stub"). The API layer exposes this
	// via the /api/sim/status endpoint.
	Mode() string
	// Rebuild 通知引擎拓扑已变更，要求其重建内部节点图。
	// 实现需保证并发安全；失败时返回错误，调用方（topology.Manager）
	// 仅记录日志、不阻断主流程。
	Rebuild(topo *topology.Topology) error
	// CapturePCAP starts capturing packets on the specified device interface
	// and sends them to the provided channel. Returns a stop function.
	CapturePCAP(ctx context.Context, deviceID, ifaceName string, pktChan chan<- []byte) (func(), error)
	// QueueDepth returns the current depth of pending events in the engine's
	// event queue. Useful for monitoring and debugging performance.
	QueueDepth() int
}

// 编译期断言：Engine 接口满足 topology.NodeRebuilder，确保拓扑
// Manager 可以通过 SetRebuilder 注册任意 Engine 实现而无需额外适配。
var _ topology.NodeRebuilder = (Engine)(nil)

// ErrDeviceNotFound is returned by Engine methods when the supplied
// device id does not exist in the loaded topology.
var ErrDeviceNotFound = fmt.Errorf("device not found")

// ErrNoActiveInterface is returned by Ping when the source device has
// no interface in the up state with an IP address assigned.
var ErrNoActiveInterface = fmt.Errorf("no active interface on source device")

// ErrInvalidDestination is returned by Ping when dstIP cannot be parsed.
var ErrInvalidDestination = fmt.Errorf("invalid destination IP")
