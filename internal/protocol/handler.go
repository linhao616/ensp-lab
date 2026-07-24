package protocol

import (
	"ensp-lab/internal/sim"
)

// Handler is the abstraction used by the ns-x engine to dispatch packets
// to protocol implementations. Each protocol that wants to participate
// in packet-level simulation implements this interface so that the
// engine (internal/sim) can invoke HandlePacket without knowing the
// concrete protocol type.
//
// The signature mirrors ns-x's node.React callback: it receives a
// packet and returns the follow-up packets (echo replies, ARP replies,
// etc.) that the engine should forward. Returning nil or an empty
// slice drops the packet.
type Handler interface {
	// HandlePacket processes the given packet and returns zero or more
	// reply packets to be forwarded by the engine.
	HandlePacket(pkt *sim.Packet) []*sim.Packet
}

// HandlerRegistry maps protocol identifiers to Handlers.
//
// The registry is populated by the topology loader (see Task 6) and
// queried by the ns-x engine when dispatching packets. Lookups are
// goroutine-safe.
type HandlerRegistry struct {
	handlers map[string]Handler
}

// NewHandlerRegistry returns an empty registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

// Register associates the given protocol identifier with a Handler.
// Registering the same id twice replaces the previous handler.
func (r *HandlerRegistry) Register(id string, h Handler) {
	if r == nil || h == nil {
		return
	}
	r.handlers[id] = h
}

// Lookup returns the Handler for the given id, or nil if no handler
// is registered.
func (r *HandlerRegistry) Lookup(id string) Handler {
	if r == nil {
		return nil
	}
	return r.handlers[id]
}

// All returns a snapshot of every registered Handler keyed by id.
// The returned map is a copy and may be modified freely.
func (r *HandlerRegistry) All() map[string]Handler {
	if r == nil {
		return nil
	}
	out := make(map[string]Handler, len(r.handlers))
	for k, v := range r.handlers {
		out[k] = v
	}
	return out
}
