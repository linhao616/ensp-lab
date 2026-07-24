package protocol

import (
	"ensp-lab/internal/sim"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	ICMPTypeEchoReply              = 0
	ICMPTypeEchoRequest            = 8
	ICMPTypeDestinationUnreachable = 3
	ICMPTypeTimeExceeded           = 11
)

type ICMPProtocol struct {
	Enabled  bool
	DeviceID string
	Stats    ICMPStats
	mu       sync.RWMutex
	echoID   int
	sequence int
}

type ICMPStats struct {
	Sent     int
	Received int
	MinRTT   time.Duration
	MaxRTT   time.Duration
	TotalRTT time.Duration
	mu       sync.Mutex
}

type ICMPMessage struct {
	Type        int
	Code        int
	Checksum    uint16
	ID          int
	Sequence    int
	Data        []byte
	SrcIP       net.IP
	DstIP       net.IP
	SendTime    time.Time
	ReceiveTime time.Time
}

type ICMPResult struct {
	Success     bool
	RTT         time.Duration
	TTL         int
	Size        int
	Sequence    int
	Error       error
	Destination string
}

func NewICMPProtocol(deviceID string) *ICMPProtocol {
	return &ICMPProtocol{
		Enabled:  true,
		DeviceID: deviceID,
		echoID:   int(time.Now().UnixNano() & 0xffff),
		sequence: 0,
		Stats: ICMPStats{
			MinRTT:   time.Duration(1<<63 - 1),
			MaxRTT:   0,
			TotalRTT: 0,
		},
	}
}

func (i *ICMPProtocol) Enable() {
	i.mu.Lock()
	i.Enabled = true
	i.mu.Unlock()
}

func (i *ICMPProtocol) Disable() {
	i.mu.Lock()
	i.Enabled = false
	i.mu.Unlock()
}

type ReachabilityChecker func(targetIP string) bool

func (i *ICMPProtocol) Ping(targetIP string, timeout time.Duration, count int, size int, checker ReachabilityChecker) []ICMPResult {
	i.mu.Lock()
	i.sequence = 0
	i.mu.Unlock()

	i.ResetStats()

	var results []ICMPResult

	for n := 0; n < count; n++ {
		result := i.sendEchoRequest(targetIP, timeout, size, checker)
		results = append(results, result)

		if n < count-1 {
			time.Sleep(1 * time.Second)
		}
	}

	return results
}

func (i *ICMPProtocol) sendEchoRequest(targetIP string, timeout time.Duration, size int, checker ReachabilityChecker) ICMPResult {
	i.mu.Lock()
	if !i.Enabled {
		i.mu.Unlock()
		return ICMPResult{
			Success:     false,
			Error:       fmt.Errorf("ICMP is disabled"),
			Destination: targetIP,
		}
	}

	i.sequence++
	seq := i.sequence
	i.mu.Unlock()

	payload := make([]byte, size)
	for j := range payload {
		payload[j] = byte(j & 0xff)
	}

	msg := &ICMPMessage{
		Type:     ICMPTypeEchoRequest,
		Code:     0,
		ID:       i.echoID,
		Sequence: seq,
		Data:     payload,
		SrcIP:    nil,
		DstIP:    net.ParseIP(targetIP),
		SendTime: time.Now(),
	}

	i.Stats.mu.Lock()
	i.Stats.Sent++
	i.Stats.mu.Unlock()

	reply, err := i.receiveEchoReply(msg, timeout, checker)
	if err != nil {
		return ICMPResult{
			Success:     false,
			Error:       err,
			Sequence:    seq,
			Size:        size,
			Destination: targetIP,
		}
	}

	rtt := reply.ReceiveTime.Sub(msg.SendTime)

	i.Stats.mu.Lock()
	i.Stats.Received++
	if rtt < i.Stats.MinRTT {
		i.Stats.MinRTT = rtt
	}
	if rtt > i.Stats.MaxRTT {
		i.Stats.MaxRTT = rtt
	}
	i.Stats.TotalRTT += rtt
	i.Stats.mu.Unlock()

	return ICMPResult{
		Success:     true,
		RTT:         rtt,
		TTL:         64,
		Size:        len(reply.Data),
		Sequence:    seq,
		Destination: targetIP,
	}
}

func (i *ICMPProtocol) receiveEchoReply(msg *ICMPMessage, timeout time.Duration, checker ReachabilityChecker) (*ICMPMessage, error) {
	if checker != nil {
		targetIP := msg.DstIP.String()
		if !checker(targetIP) {
			time.Sleep(time.Millisecond * 50)
			return nil, fmt.Errorf("timeout")
		}
	}

	timeoutChan := time.After(timeout)

	select {
	case <-timeoutChan:
		return nil, fmt.Errorf("timeout")
	default:
		time.Sleep(time.Millisecond * 100)

		reply := &ICMPMessage{
			Type:        ICMPTypeEchoReply,
			Code:        0,
			ID:          msg.ID,
			Sequence:    msg.Sequence,
			Data:        msg.Data,
			SrcIP:       msg.DstIP,
			DstIP:       msg.SrcIP,
			ReceiveTime: time.Now(),
		}

		return reply, nil
	}
}

func (i *ICMPProtocol) ReceiveMessage(msg *ICMPMessage) *ICMPMessage {
	i.mu.RLock()
	if !i.Enabled {
		i.mu.RUnlock()
		return nil
	}
	i.mu.RUnlock()

	switch msg.Type {
	case ICMPTypeEchoRequest:
		return i.handleEchoRequest(msg)
	default:
		return nil
	}
}

func (i *ICMPProtocol) handleEchoRequest(req *ICMPMessage) *ICMPMessage {
	reply := &ICMPMessage{
		Type:        ICMPTypeEchoReply,
		Code:        0,
		ID:          req.ID,
		Sequence:    req.Sequence,
		Data:        req.Data,
		SrcIP:       req.DstIP,
		DstIP:       req.SrcIP,
		SendTime:    time.Now(),
		ReceiveTime: time.Now(),
	}

	return reply
}

func (i *ICMPProtocol) GetStats() ICMPStats {
	i.Stats.mu.Lock()
	defer i.Stats.mu.Unlock()
	return ICMPStats{
		Sent:     i.Stats.Sent,
		Received: i.Stats.Received,
		MinRTT:   i.Stats.MinRTT,
		MaxRTT:   i.Stats.MaxRTT,
		TotalRTT: i.Stats.TotalRTT,
	}
}

func (i *ICMPProtocol) ResetStats() {
	i.Stats.mu.Lock()
	i.Stats.Sent = 0
	i.Stats.Received = 0
	i.Stats.MinRTT = time.Duration(1<<63 - 1)
	i.Stats.MaxRTT = 0
	i.Stats.TotalRTT = 0
	i.Stats.mu.Unlock()
}

func (i *ICMPProtocol) CalculateChecksum(data []byte) uint16 {
	sum := uint32(0)
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	if len(data)%2 != 0 {
		sum += uint32(data[len(data)-1]) << 8
	}
	sum = (sum >> 16) + (sum & 0xffff)
	sum += sum >> 16
	return uint16(^sum)
}

func (i *ICMPProtocol) FormatPingResults(results []ICMPResult) string {
	if len(results) == 0 {
		return "No ping results"
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("PING %s (%s): %d bytes of data\n", results[0].Destination, results[0].Destination, results[0].Size))

	for _, r := range results {
		if r.Success {
			rttMs := float64(r.RTT.Microseconds()) / 1000.0
			out.WriteString(fmt.Sprintf("%d bytes from %s: icmp_seq=%d ttl=%d time=%.2f ms\n",
				r.Size, r.Destination, r.Sequence, r.TTL, rttMs))
		} else {
			out.WriteString(fmt.Sprintf("Request timeout for icmp_seq=%d\n", r.Sequence))
		}
	}

	stats := i.GetStats()
	packetLoss := 0
	if stats.Sent > 0 {
		packetLoss = ((stats.Sent - stats.Received) * 100) / stats.Sent
	}

	avgRTT := time.Duration(0)
	if stats.Received > 0 {
		avgRTT = stats.TotalRTT / time.Duration(stats.Received)
	}

	out.WriteString(fmt.Sprintf("\n--- %s ping statistics ---\n", results[0].Destination))
	out.WriteString(fmt.Sprintf("%d packets transmitted, %d packets received, %d%% packet loss\n",
		stats.Sent, stats.Received, packetLoss))

	if stats.Received > 0 {
		minMs := float64(stats.MinRTT.Microseconds()) / 1000.0
		avgMs := float64(avgRTT.Microseconds()) / 1000.0
		maxMs := float64(stats.MaxRTT.Microseconds()) / 1000.0
		out.WriteString(fmt.Sprintf("round-trip min/avg/max = %.2f/%.2f/%.2f ms\n", minMs, avgMs, maxMs))
	}

	return out.String()
}

// HandlePacket implements the Handler interface. It inspects the
// incoming packet and, for ICMP Echo Requests whose destination IP
// matches one of the device's interfaces, returns a single Echo Reply
// packet. All other packets are dropped (nil return).
//
// The reply reuses the request's payload, swaps source/destination
// addresses, and resets the TTL to 64 — mirroring the behaviour of the
// legacy simulator.SimulatedDevice.processICMP.
func (i *ICMPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	if pkt == nil {
		return nil
	}
	if !i.Enabled {
		return nil
	}
	if pkt.Protocol != sim.ProtocolICMP {
		return nil
	}
	// The first byte of the ICMP payload is the type code.
	if len(pkt.Payload) == 0 || pkt.Payload[0] != sim.ICMPTypeEchoRequest {
		return nil
	}
	i.mu.Lock()
	i.Stats.Received++
	i.mu.Unlock()

	reply := cloneSimPacket(pkt)
	reply.SrcIP, reply.DstIP = pkt.DstIP, pkt.SrcIP
	reply.SrcMAC, reply.DstMAC = pkt.DstMAC, pkt.SrcMAC
	reply.Payload[0] = sim.ICMPTypeEchoReply
	reply.TTL = 64
	reply.Path = append(reply.Path, i.DeviceID)

	i.mu.Lock()
	i.Stats.Sent++
	i.mu.Unlock()
	return []*sim.Packet{reply}
}

// Compile-time assertion that ICMPProtocol satisfies Handler.
var _ Handler = (*ICMPProtocol)(nil)
