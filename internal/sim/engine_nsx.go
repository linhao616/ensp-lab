package sim

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/logging"
	"ensp-lab/internal/topology"
	nsx "github.com/bytedance/ns-x/v2"
	"github.com/bytedance/ns-x/v2/base"
	"github.com/bytedance/ns-x/v2/node"
	"github.com/bytedance/ns-x/v2/tick"
	"go.uber.org/zap"
)

// debugSim 控制是否输出“每个数据包”级别的仿真跟踪日志。
// 默认关闭，避免在高负载下刷爆 stdout/磁盘（此前曾产生上 GB 日志并拖死 HTTP 服务）。
// 设置环境变量 ENSP_DEBUG=1 可重新开启，用于深度排错。
var debugSim = os.Getenv("ENSP_DEBUG") == "1"

// dbgSimOut 是 dbgSim 的日志输出目标，默认 os.Stdout；测试时可替换为 bytes.Buffer 等可注入写入器，
// 也便于将来把调试日志重定向到统一日志而非写死 stdout。
var dbgSimOut io.Writer = os.Stdout

// dbgSimRateLimit 控制开启 ENSP_DEBUG 时“每个数据包”级日志的最大输出速率，
// 即使误开 DEBUG 也不会再刷爆 stdout/磁盘（此前曾产生 1GB 日志并拖死 HTTP）。
const (
	dbgSimWindowSize   = 1 * time.Second // 限流窗口
	dbgSimMaxPerWindow = 300             // 每个窗口最多输出的日志行数
)

var (
	dbgSimLimitMu     sync.Mutex
	dbgSimWindowStart int64 // 当前窗口起始时间（UnixNano）
	dbgSimWindowCount int   // 当前窗口已输出行数
	dbgSimSuppressed  int64 // 上一窗口起被抑制的行数（仅在新窗口开始时汇报一次）
)

func dbgSim(format string, a ...interface{}) {
	if !debugSim {
		return
	}
	dbgSimLimitMu.Lock()
	now := time.Now().UnixNano()
	if now-dbgSimWindowStart >= int64(dbgSimWindowSize) {
		// 新窗口开始：汇报上一窗口被抑制的日志量，然后重置计数。
		if dbgSimSuppressed > 0 {
			fmt.Fprintf(dbgSimOut, "DEBUG: [rate-limited] suppressed %d trace lines in previous 1s window\n", dbgSimSuppressed)
			dbgSimSuppressed = 0
		}
		dbgSimWindowStart = now
		dbgSimWindowCount = 0
	}
	if dbgSimWindowCount >= dbgSimMaxPerWindow {
		dbgSimSuppressed++
		dbgSimLimitMu.Unlock()
		return
	}
	dbgSimWindowCount++
	dbgSimLimitMu.Unlock()
	fmt.Fprintf(dbgSimOut, format, a...)
}

type BridgeNode struct {
	*node.BasicNode
	engine   *nsxEngine
	deviceID string
}

func NewBridgeNode(engine *nsxEngine, deviceID string) *BridgeNode {
	return &BridgeNode{
		BasicNode: &node.BasicNode{},
		engine:    engine,
		deviceID:  deviceID,
	}
}

func (n *BridgeNode) Transfer(packet base.Packet, now time.Time) []base.Event {
	dbgSim("DEBUG: Transfer START - device=%s\n", n.deviceID)
	np, ok := packet.(*nsxPacket)
	if !ok || np == nil || np.sim == nil {
		dbgSim("DEBUG: Transfer END - invalid packet\n")
		return nil
	}
	dbgSim("DEBUG: Transfer packet valid - Protocol=%d, SrcIP=%s, DstIP=%s, Path=%v\n",
		np.sim.Protocol, np.sim.SrcIP, np.sim.DstIP, np.sim.Path)

	nodeID := n.deviceID + "-bridge"
	for _, visited := range np.sim.Path {
		if visited == nodeID {
			dbgSim("DEBUG: Transfer END - already visited %s\n", nodeID)
			return nil
		}
	}
	np.sim.Path = append(np.sim.Path, nodeID)

	dbgSim("DEBUG: Entering ICMP check - Protocol=%d, ICMP=%d\n", np.sim.Protocol, ProtocolICMP)
	if np.sim.Protocol == ProtocolICMP && len(np.sim.Payload) > 0 && np.sim.Payload[0] == ICMPTypeEchoRequest {
		dbgSim("DEBUG: ICMP check passed!\n")
		srcVLAN := n.engine.effectiveVLAN(np.sim)
		dstVLAN := n.engine.getDeviceVLAN(np.sim.DstIP)
		dbgSim("DEBUG: srcVLAN=%d, dstVLAN=%d\n", srcVLAN, dstVLAN)

		if srcVLAN > 0 && dstVLAN > 0 && srcVLAN != dstVLAN {
			// VTEP 作为三层网关：尝试 L3 路由（VBDIF/Vlanif 式 inter-VLAN 转发）。
			if n.engine.isVTEP(n.deviceID) {
				if ev := n.engine.routeL3(n.deviceID, np.sim, now); ev != nil {
					return ev
				}
				n.engine.emit(&PacketEvent{
					PacketID:    np.sim.ID,
					Type:        PacketEventDrop,
					DeviceID:    n.deviceID,
					Interface:   "",
					Timestamp:   now,
					Description: fmt.Sprintf("VLAN mismatch at VTEP %s: src VLAN=%d, dst VLAN=%d, no L3 route - dropping", n.deviceID, srcVLAN, dstVLAN),
					Path:        append([]string{}, np.sim.Path...),
				})
				return nil
			}
			// 纯 L2 桥（server/switch）：不丢弃，泛洪把帧交给网关（VTEP）做三层转发。
		}

		dbgSim("DEBUG: VXLAN check - device=%s, isVTEP=%v, srcVLAN=%d, dstIP=%s\n",
			n.deviceID, n.engine.isVTEP(n.deviceID), srcVLAN, np.sim.DstIP)
		if srcVLAN > 0 && n.engine.isVTEP(n.deviceID) {
			vni := n.engine.findVNIForVLAN(n.deviceID, srcVLAN)
			dbgSim("DEBUG: VXLAN - VNI for VLAN %d = %d\n", srcVLAN, vni)
			if vni > 0 {
				targetVTEP := n.engine.findVTEPForIP(np.sim.DstIP, vni)
				dbgSim("DEBUG: VXLAN - targetVTEP for IP %s = %s\n", np.sim.DstIP, targetVTEP)
				if targetVTEP != "" && targetVTEP != n.deviceID {
					if targetBridge, ok := n.engine.bridges[targetVTEP]; ok {
						n.engine.emit(&PacketEvent{
							PacketID:    np.sim.ID,
							Type:        PacketEventForward,
							DeviceID:    n.deviceID,
							Interface:   "",
							Timestamp:   now,
							Description: fmt.Sprintf("VXLAN tunnel forwarding from bridge to %s (VNI=%d)", targetVTEP, vni),
							Path:        append([]string{}, np.sim.Path...),
						})
						return targetBridge.Transfer(&nsxPacket{sim: clonePacket(np.sim), data: np.sim.Payload}, now.Add(time.Millisecond))
					} else {
						dbgSim("DEBUG: VXLAN - targetBridge not found for %s\n", targetVTEP)
					}
				}
			}
		}
	}

	var events []base.Event
	n.engine.emit(&PacketEvent{
		PacketID:    np.sim.ID,
		Type:        PacketEventForward,
		DeviceID:    n.deviceID,
		Interface:   "",
		Timestamp:   now,
		Description: fmt.Sprintf("Bridge forwarding to %d next nodes", len(n.GetNext())),
		Path:        append([]string{}, np.sim.Path...),
	})
	for _, next := range n.GetNext() {
		clonedNP := &nsxPacket{
			sim:  clonePacket(np.sim),
			data: np.sim.Payload,
		}
		events = append(events, next.Transfer(clonedNP, now)...)
	}
	return events
}

func (n *BridgeNode) SetNext(next ...base.Node) {
	n.BasicNode.SetNext(next...)
}

// nsxPacket wraps sim.Packet to satisfy base.Packet interface.
//
// ns-x's base.Packet only requires Size() int; we keep the original
// *Packet pointer alongside the wire-encoded bytes so that device
// handlers can recover the structured packet when reacting to events.
type nsxPacket struct {
	sim  *Packet
	data []byte
}

// Size returns the wire size of the packet.
func (p *nsxPacket) Size() int {
	if p == nil {
		return 0
	}
	return len(p.data)
}

// EngineMode reports which underlying simulation backend is active.
type EngineMode string

const (
	// EngineModeNSX indicates pure ns-x event-driven simulation.
	EngineModeNSX EngineMode = "ns-x"
	// EngineModeGont indicates gont-based namespace emulation (Linux only).
	EngineModeGont EngineMode = "gont"
)

// ErrEngineAlreadyStarted is returned when Start is called twice.
var ErrEngineAlreadyStarted = fmt.Errorf("sim: engine already started")

// Ensure nsxEngine implements Engine at compile time.
var _ Engine = (*nsxEngine)(nil)

// nsxEngine implements Engine using ns-x as the event scheduler.
//
// Devices from the topology.Topology are mapped 1:1 to ns-x EndpointNode
// instances. Each pair of connected devices is wired through a chain of
// EndpointNode -> (channel nodes if added later) -> EndpointNode, with
// a TransferCallback attached to emit PacketEvents for visualization.
//
// The engine runs ns-x's event loop in a background goroutine for the
// lifetime of the process. A periodic heartbeat event keeps the loop
// alive even when no user traffic is flowing.
type nsxEngine struct {
	mode      EngineMode
	topo      *topology.Topology
	clock     tick.Clock
	network   *nsx.Network
	endpoints map[string]*node.EndpointNode // deviceID -> endpoint
	bridges   map[string]*BridgeNode        // deviceID -> bridge

	mu         sync.RWMutex
	listeners  []PacketListener
	eventCh    chan *PacketEvent
	history    []*PacketEvent
	lastEmitAt time.Time
	closed     bool

	started       bool
	cancelCtx     context.Context
	cancelFunc    context.CancelFunc
	wg            sync.WaitGroup
	pendingEvents chan base.Event
	pingResults   map[string]chan *PingResult
	pingSemaphore chan struct{}
}

// NewNSxEngine builds an ns-x-backed Engine from the given topology.
//
// The topology must already have devices and links populated; later
// topology updates require a fresh engine (see UpdateTopology in
// future tasks).
const (
	maxConcurrentPings = 10
	emitRateLimit      = 100 * time.Millisecond
	maxHistorySize     = 500
)

func NewNSxEngine(topo *topology.Topology) (Engine, error) {
	if topo == nil {
		return nil, fmt.Errorf("sim: topology is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &nsxEngine{
		mode:          EngineModeNSX,
		topo:          topo,
		clock:         tick.NewRealClock(),
		endpoints:     make(map[string]*node.EndpointNode),
		bridges:       make(map[string]*BridgeNode),
		listeners:     []PacketListener{},
		eventCh:       make(chan *PacketEvent, 128),
		history:       []*PacketEvent{},
		cancelCtx:     ctx,
		cancelFunc:    cancel,
		pendingEvents: make(chan base.Event, 32),
		pingResults:   make(map[string]chan *PingResult),
		pingSemaphore: make(chan struct{}, maxConcurrentPings),
	}
	if err := e.build(); err != nil {
		return nil, fmt.Errorf("sim: build ns-x network: %w", err)
	}
	return e, nil
}

// Mode returns the active engine mode as a string identifier
// ("ns-x" or "gont"). Satisfies the Engine interface.
func (e *nsxEngine) Mode() string { return string(e.mode) }

// Rebuild 在拓扑变更时被 topology.Manager 调用，用于重建内部节点图。
//
// 加锁后更新 e.topo、清空 endpoints 并重新执行 build()。若引擎尚未启动
// 或新拓扑为 nil，则直接返回 nil 以保持幂等。
func (e *nsxEngine) Rebuild(topo *topology.Topology) error {
	if topo == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.topo = topo
	e.endpoints = make(map[string]*node.EndpointNode)
	if err := e.build(); err != nil {
		return fmt.Errorf("sim: rebuild ns-x network: %w", err)
	}
	return nil
}

func (e *nsxEngine) build() error {
	builder := nsx.NewBuilder()

	for id, dev := range e.topo.Devices {
		ep := node.NewEndpointNode(
			node.WithTransferCallback(e.makeTransferCallback(dev.ID)),
		)
		ep.Receive(e.makeReact(dev.ID))
		e.endpoints[id] = ep
		builder.NodeWithName(dev.ID, ep)
		logging.Info("Created endpoint", zap.String("device", id), zap.String("devType", string(dev.Type)))
	}

	linksByDevice := make(map[string][]*topology.Link)
	vxlanLinks := make(map[string][]*topology.Link)
	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 {
			vxlanLinks[link.SourceDevice] = append(vxlanLinks[link.SourceDevice], link)
			vxlanLinks[link.TargetDevice] = append(vxlanLinks[link.TargetDevice], link)
		} else {
			linksByDevice[link.SourceDevice] = append(linksByDevice[link.SourceDevice], link)
			linksByDevice[link.TargetDevice] = append(linksByDevice[link.TargetDevice], link)
		}
	}

	bridges := make(map[string]*BridgeNode)
	for id := range e.topo.Devices {
		totalLinks := len(linksByDevice[id]) + len(vxlanLinks[id])
		if totalLinks > 1 {
			bridge := NewBridgeNode(e, id)
			bridges[id] = bridge
			builder.NodeWithName(id+"-bridge", bridge)
		}
	}
	e.bridges = bridges

	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}

		srcEp := e.endpoints[link.SourceDevice]
		dstEp := e.endpoints[link.TargetDevice]
		if srcEp == nil || dstEp == nil {
			continue
		}

		var srcNode base.Node = srcEp
		if bridge, ok := bridges[link.SourceDevice]; ok {
			srcNode = bridge
			srcEp.SetNext(bridge)
			bridge.SetNext(append(bridge.GetNext(), srcEp)...)
		}

		var dstNode base.Node = dstEp
		if bridge, ok := bridges[link.TargetDevice]; ok {
			dstNode = bridge
			dstEp.SetNext(bridge)
			bridge.SetNext(append(bridge.GetNext(), dstEp)...)
		}

		srcNode.SetNext(append(srcNode.GetNext(), dstNode)...)
		dstNode.SetNext(append(dstNode.GetNext(), srcNode)...)
	}

	for _, link := range e.topo.Links {
		if link.VXLANVNI <= 0 {
			continue
		}

		srcEp := e.endpoints[link.SourceDevice]
		dstEp := e.endpoints[link.TargetDevice]
		if srcEp == nil || dstEp == nil {
			continue
		}

		var srcNode base.Node = srcEp
		if bridge, ok := bridges[link.SourceDevice]; ok {
			srcNode = bridge
		}

		var dstNode base.Node = dstEp
		if bridge, ok := bridges[link.TargetDevice]; ok {
			dstNode = bridge
		}

		srcNode.SetNext(append(srcNode.GetNext(), dstNode)...)
		dstNode.SetNext(append(dstNode.GetNext(), srcNode)...)
	}

	dbgSim("DEBUG: Build complete - endpoints=%d, bridges=%d\n", len(e.endpoints), len(bridges))
	for devID, ep := range e.endpoints {
		dbgSim("DEBUG: Endpoint %s next=%d\n", devID, len(ep.GetNext()))
	}
	for devID, bridge := range bridges {
		dbgSim("DEBUG: Bridge %s next=%d\n", devID, len(bridge.GetNext()))
	}

	nodes := make([]base.Node, 0, len(e.endpoints)+len(bridges))
	for _, ep := range e.endpoints {
		nodes = append(nodes, ep)
	}
	for _, bridge := range bridges {
		nodes = append(nodes, bridge)
	}
	e.network = nsx.NewNetwork(nodes)
	return nil
}

// makeTransferCallback returns a callback that emits forward events.
func (e *nsxEngine) makeTransferCallback(deviceID string) base.TransferCallback {
	return func(packet base.Packet, source, target base.Node, now time.Time) {
		np, ok := packet.(*nsxPacket)
		if !ok || np == nil || np.sim == nil {
			return
		}
		var targetID string
		for id, ep := range e.endpoints {
			if ep == target {
				targetID = id
				break
			}
		}
		e.emit(&PacketEvent{
			PacketID:    np.sim.ID,
			Type:        PacketEventForward,
			DeviceID:    deviceID,
			Interface:   "",
			Timestamp:   now,
			Description: fmt.Sprintf("Forward %s to %s", np.sim.ProtocolName(), targetID),
			Path:        append([]string{}, np.sim.Path...),
		})
	}
}

func (e *nsxEngine) findVXLANPeers(deviceID string, vni int) []string {
	var peers []string
	for _, link := range e.topo.Links {
		if link.VXLANVNI == vni && (link.SourceDevice == deviceID || link.TargetDevice == deviceID) {
			if link.SourceDevice == deviceID {
				peers = append(peers, link.TargetDevice)
			} else {
				peers = append(peers, link.SourceDevice)
			}
		}
	}
	return peers
}

func (e *nsxEngine) findDeviceByIP(ip net.IP) (string, bool) {
	for id, dev := range e.topo.Devices {
		for _, iface := range dev.Interfaces {
			if iface.IPAddress == ip.String() {
				return id, true
			}
		}
	}
	return "", false
}

func (e *nsxEngine) getDeviceVLAN(ip net.IP) int {
	for _, dev := range e.topo.Devices {
		for _, iface := range dev.Interfaces {
			if iface.IPAddress == ip.String() {
				return iface.VLAN
			}
		}
	}
	return 0
}

// effectiveVLAN 返回数据包当前的 L2 VLAN 上下文。
// 优先使用路由阶段写入的 VLANID（已标记出接口 VLAN），
// 否则按源 IP 所属设备推断其入向 VLAN。
func (e *nsxEngine) effectiveVLAN(pkt *Packet) int {
	if pkt != nil && pkt.VLANID > 0 {
		return pkt.VLANID
	}
	if pkt != nil {
		return e.getDeviceVLAN(pkt.SrcIP)
	}
	return 0
}

// isL3Iface 判断接口是否为三层网关接口（Vlanif/Vbdif）。
func isL3Iface(name string) bool {
	return strings.HasPrefix(name, "Vlanif") || strings.HasPrefix(name, "Vbdif")
}

// ipInSubnet 判断 ip 是否落在 ifaceIP/mask 直连子网内（mask 支持点分格式，
// 如 255.255.255.0，避免依赖 CIDR 斜杠写法）。
func ipInSubnet(ip, ifaceIP, mask string) bool {
	pip := net.ParseIP(ip)
	iip := net.ParseIP(ifaceIP)
	pmask := net.ParseIP(mask)
	if pip == nil || iip == nil || pmask == nil {
		return false
	}
	m4 := pmask.To4()
	if m4 == nil {
		return false
	}
	netMask := net.IPMask(m4)
	network := &net.IPNet{IP: iip.Mask(netMask), Mask: netMask}
	return network.Contains(pip)
}

// findAccessNeighbor 返回 deviceID 在指定 VLAN 上的直连接入邻居
// （仅非 VXLAN 链路，即 access / underlay 侧）。
func (e *nsxEngine) findAccessNeighbor(deviceID string, vlan int) string {
	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 || link.VLAN != vlan {
			continue
		}
		if link.SourceDevice == deviceID {
			return link.TargetDevice
		}
		if link.TargetDevice == deviceID {
			return link.SourceDevice
		}
	}
	return ""
}

// routeL3 在三层网关设备上对跨 VLAN 单播做路由：若本设备某 L3 接口子网包含
// 目的 IP，则将其从对应 egress VLAN 转发出去，模拟 VBDIF/Vlanif 网关的
// inter-VLAN 路由。返回转发产生的事件；若无需或无法路由则返回 nil。
func (e *nsxEngine) routeL3(deviceID string, pkt *Packet, now time.Time) []base.Event {
	dev := e.topo.Devices[deviceID]
	if dev == nil || pkt == nil {
		return nil
	}
	for _, iface := range dev.Interfaces {
		if !isL3Iface(iface.Name) || iface.IPAddress == "" || iface.SubnetMask == "" {
			continue
		}
		if !ipInSubnet(pkt.DstIP.String(), iface.IPAddress, iface.SubnetMask) {
			continue
		}
		egressVLAN := iface.VLAN
		if egressVLAN <= 0 {
			return nil
		}
		nextDev := e.findAccessNeighbor(deviceID, egressVLAN)
		if nextDev == "" {
			return nil
		}
		cloned := clonePacket(pkt)
		cloned.VLANID = egressVLAN
		e.emit(&PacketEvent{
			PacketID:    pkt.ID,
			Type:        PacketEventForward,
			DeviceID:    deviceID,
			Interface:   iface.Name,
			Timestamp:   now,
			Description: fmt.Sprintf("L3 route at %s via %s (VLAN %d) -> %s", deviceID, iface.Name, egressVLAN, nextDev),
			Path:        append([]string{}, pkt.Path...),
		})
		if target, ok := e.bridges[nextDev]; ok {
			return target.Transfer(&nsxPacket{sim: cloned, data: cloned.Payload}, now.Add(time.Millisecond))
		}
		if ep, ok := e.endpoints[nextDev]; ok {
			return ep.Transfer(&nsxPacket{sim: cloned, data: cloned.Payload}, now.Add(time.Millisecond))
		}
		return nil
	}
	return nil
}

func (e *nsxEngine) findVTEPsInVNI(vni int) []string {
	var vteps []string
	for _, link := range e.topo.Links {
		if link.VXLANVNI == vni {
			if !contains(vteps, link.SourceDevice) {
				vteps = append(vteps, link.SourceDevice)
			}
			if !contains(vteps, link.TargetDevice) {
				vteps = append(vteps, link.TargetDevice)
			}
		}
	}
	return vteps
}

func (e *nsxEngine) isVTEP(deviceID string) bool {
	dev := e.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	return dev.Type == topology.DeviceVTEP
}

func (e *nsxEngine) isSwitch(deviceID string) bool {
	dev := e.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	return dev.Type == topology.DeviceSwitch || dev.Type == topology.DeviceL3Switch
}

func (e *nsxEngine) isServer(deviceID string) bool {
	dev := e.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	return dev.Type == topology.DeviceServer
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (e *nsxEngine) makeReact(deviceID string) node.React {
	return func(packet base.Packet, now time.Time) []base.Event {
		np, ok := packet.(*nsxPacket)
		if !ok || np == nil || np.sim == nil {
			return nil
		}
		pkt := np.sim

		e.emit(&PacketEvent{
			PacketID:    pkt.ID,
			Type:        PacketEventReceive,
			DeviceID:    deviceID,
			Interface:   "",
			Timestamp:   now,
			Description: fmt.Sprintf("Received %s from %s to %s, path=%v", pkt.ProtocolName(), pkt.SrcIP, pkt.DstIP, pkt.Path),
			Path:        append([]string{}, pkt.Path...),
		})

		if pkt.TTL <= 1 {
			return nil
		}
		pkt.TTL--

		if pkt.Protocol == ProtocolICMP && len(pkt.Payload) > 0 {
			if pkt.Payload[0] == ICMPTypeEchoRequest {
				if e.deviceOwnsIP(deviceID, pkt.DstIP) {
					reply := clonePacket(pkt)
					reply.SrcIP, reply.DstIP = pkt.DstIP, pkt.SrcIP
					reply.SrcMAC, reply.DstMAC = pkt.DstMAC, pkt.SrcMAC
					reply.Payload[0] = ICMPTypeEchoReply
					reply.TTL = 64
					reply.Path = []string{}

					ep := e.endpoints[deviceID]
					if ep == nil {
						return nil
					}
					nextNodes := ep.GetNext()
					if len(nextNodes) > 0 {
						return nextNodes[0].Transfer(&nsxPacket{sim: reply, data: reply.Payload}, now.Add(time.Millisecond))
					}
					return nil
				}
			} else if pkt.Payload[0] == ICMPTypeEchoReply {
				e.mu.Lock()
				if resultCh, ok := e.pingResults[pkt.ID]; ok {
					select {
					case resultCh <- &PingResult{
						Sent:     1,
						Received: 1,
						Lost:     0,
						Details:  []string{"ICMP echo reply received"},
					}:
					default:
					}
					e.mu.Unlock()
					return nil
				}
				e.mu.Unlock()
			}
		}

		ep := e.endpoints[deviceID]
		if ep == nil {
			return nil
		}
		nextNodes := ep.GetNext()
		dbgSim("DEBUG: makeReact device=%s, nextNodes count=%d\n", deviceID, len(nextNodes))
		if len(nextNodes) > 0 {
			var events []base.Event
			for _, next := range nextNodes {
				events = append(events, next.Transfer(&nsxPacket{sim: clonePacket(pkt), data: pkt.Payload}, now.Add(time.Millisecond))...)
			}
			return events
		}
		return nil
	}
}

func (e *nsxEngine) findVNIForVLAN(deviceID string, vlan int) int {
	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 && (link.SourceDevice == deviceID || link.TargetDevice == deviceID) {
			return link.VXLANVNI
		}
	}
	return 0
}

func (e *nsxEngine) findVTEPForIP(ip net.IP, vni int) string {
	dstDeviceID, _ := e.findDeviceByIP(ip)
	if dstDeviceID == "" {
		return ""
	}

	if e.isVTEP(dstDeviceID) {
		return dstDeviceID
	}

	vteps := e.findVTEPsInVNI(vni)
	for _, vtep := range vteps {
		if e.isDirectlyConnectedTo(dstDeviceID, vtep) {
			return vtep
		}
	}

	for _, vtep := range vteps {
		return vtep
	}
	return ""
}

func (e *nsxEngine) isDirectlyConnectedTo(deviceID, targetID string) bool {
	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}
		if (link.SourceDevice == deviceID && link.TargetDevice == targetID) ||
			(link.TargetDevice == deviceID && link.SourceDevice == targetID) {
			return true
		}
	}

	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}
		if link.SourceDevice == deviceID || link.TargetDevice == deviceID {
			serverID := link.SourceDevice
			if serverID == deviceID {
				serverID = link.TargetDevice
			}
			for _, link2 := range e.topo.Links {
				if link2.VXLANVNI > 0 {
					continue
				}
				if (link2.SourceDevice == serverID && link2.TargetDevice == targetID) ||
					(link2.TargetDevice == serverID && link2.SourceDevice == targetID) {
					return true
				}
			}
		}
	}

	return false
}

func (e *nsxEngine) isInSameSubnet(ip net.IP, deviceID string) bool {
	dev := e.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	for _, iface := range dev.Interfaces {
		if iface.IPAddress != "" && iface.SubnetMask != "" {
			ifaceIP := net.ParseIP(iface.IPAddress)
			if ifaceIP == nil {
				continue
			}
			_, subnet, err := net.ParseCIDR(iface.IPAddress + "/" + iface.SubnetMask)
			if err != nil {
				continue
			}
			if subnet.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (e *nsxEngine) isConnectedTo(deviceID, targetID string) bool {
	visited := make(map[string]bool)
	return e.isConnectedBFS(deviceID, targetID, visited)
}

func (e *nsxEngine) isConnectedBFS(start, target string, visited map[string]bool) bool {
	if start == target {
		return true
	}
	if visited[start] {
		return false
	}
	visited[start] = true

	for _, link := range e.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}
		var nextNode string
		if link.SourceDevice == start {
			nextNode = link.TargetDevice
		} else if link.TargetDevice == start {
			nextNode = link.SourceDevice
		} else {
			continue
		}
		if nextNode == target {
			return true
		}
		if e.isConnectedBFS(nextNode, target, visited) {
			return true
		}
	}
	return false
}

// deviceOwnsIP returns true if any interface on the device has the IP.
func (e *nsxEngine) deviceOwnsIP(deviceID string, ip net.IP) bool {
	dev, ok := e.topo.Devices[deviceID]
	if !ok {
		return false
	}
	for _, iface := range dev.Interfaces {
		if iface.IPAddress == ip.String() {
			return true
		}
	}
	return false
}

// Start launches the ns-x event loop in the background.
func (e *nsxEngine) Start() {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		poller := base.NewPeriodicEvent(func(time.Time) []base.Event {
			var events []base.Event
			for {
				select {
				case ev := <-e.pendingEvents:
					events = append(events, ev)
				default:
					return events
				}
			}
		}, 1*time.Millisecond, time.Now())

		e.network.Run([]base.Event{poller}, e.clock, 24*time.Hour)
	}()
}

// Stop signals the ns-x loop to exit and waits for it to drain.
func (e *nsxEngine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	e.closed = true
	e.mu.Unlock()
	e.cancelFunc()
	e.wg.Wait()
	close(e.eventCh)
	close(e.pendingEvents)
	e.mu.Lock()
	for pktID, ch := range e.pingResults {
		close(ch)
		delete(e.pingResults, pktID)
	}
	e.mu.Unlock()
}

func (e *nsxEngine) QueueDepth() int {
	return len(e.pendingEvents)
}

// SendPacket injects a packet from a device's interface.
func (e *nsxEngine) SendPacket(pkt *Packet, fromDeviceID, _ string) {
	e.mu.RLock()
	ep, ok := e.endpoints[fromDeviceID]
	e.mu.RUnlock()

	if !ok {
		logging.Error("SendPacket: endpoint not found", zap.String("device", fromDeviceID))
		dbgSim("DEBUG: SendPacket endpoint not found for device: %s\n", fromDeviceID)
		return
	}

	dbgSim("DEBUG: SendPacket from %s to %s, endpoint=%p\n", fromDeviceID, pkt.DstIP, ep)
	np := &nsxPacket{sim: pkt, data: pkt.Payload}
	event := ep.Send(np, time.Now())
	dbgSim("DEBUG: SendPacket event=%p, type=%T\n", event, event)
	logging.Info("SendPacket: sent", zap.String("device", fromDeviceID), zap.String("dstIP", pkt.DstIP.String()), zap.String("eventType", fmt.Sprintf("%T", event)))

	e.emit(&PacketEvent{
		PacketID:    pkt.ID,
		Type:        PacketEventSend,
		DeviceID:    fromDeviceID,
		Interface:   "",
		Timestamp:   time.Now(),
		Description: fmt.Sprintf("Send %s to %s", pkt.ProtocolName(), pkt.DstIP),
		Path:        append([]string{}, pkt.Path...),
	})

	select {
	case e.pendingEvents <- event:
		dbgSim("DEBUG: SendPacket event queued\n")
	default:
		dbgSim("DEBUG: SendPacket event queue full\n")
	}
}

// Ping initiates an ICMP echo from srcDeviceID to dstIP.
func (e *nsxEngine) Ping(srcDeviceID, dstIP string) (*PingResult, error) {
	select {
	case e.pingSemaphore <- struct{}{}:
	default:
		return &PingResult{
			Sent:     1,
			Received: 0,
			Lost:     1,
			Details:  []string{"Too many concurrent pings"},
		}, nil
	}
	defer func() { <-e.pingSemaphore }()

	dev, ok := e.topo.Devices[srcDeviceID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, srcDeviceID)
	}
	var srcIP net.IP
	var srcIface string
	for _, iface := range dev.Interfaces {
		if iface.Status == "up" && iface.IPAddress != "" {
			srcIP = net.ParseIP(iface.IPAddress)
			srcIface = iface.Name
			break
		}
	}
	if srcIP == nil {
		return nil, ErrNoActiveInterface
	}
	dst := net.ParseIP(dstIP)
	if dst == nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDestination, dstIP)
	}
	payload := make([]byte, 8)
	payload[0] = ICMPTypeEchoRequest
	pktID := fmt.Sprintf("pkt-%d", time.Now().UnixNano())
	pkt := &Packet{
		ID:        pktID,
		SrcMAC:    GenerateMAC(dev.ID),
		DstMAC:    []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		SrcIP:     srcIP,
		DstIP:     dst,
		Protocol:  ProtocolICMP,
		EtherType: EtherTypeIPv4,
		Payload:   payload,
		TTL:       64,
		Timestamp: time.Now(),
		Path:      []string{srcDeviceID},
	}

	resultCh := make(chan *PingResult, 1)
	e.mu.Lock()
	e.pingResults[pktID] = resultCh
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.pingResults, pktID)
		e.mu.Unlock()
	}()

	e.SendPacket(pkt, srcDeviceID, srcIface)

	select {
	case result := <-resultCh:
		return result, nil
	case <-time.After(3 * time.Second):
		return &PingResult{
			Sent:     1,
			Received: 0,
			Lost:     1,
			Details:  []string{"Ping timeout"},
		}, nil
	}
}

// AddPacketListener registers a listener invoked on every PacketEvent.
func (e *nsxEngine) AddPacketListener(listener PacketListener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, listener)
}

// Events returns the channel from which the API layer drains events.
func (e *nsxEngine) Events() <-chan *PacketEvent {
	return e.eventCh
}

// Run blocks until ctx is cancelled, draining pending events.
func (e *nsxEngine) Run(ctx context.Context) error {
	e.Start()
	<-ctx.Done()
	e.Stop()
	return ctx.Err()
}

// emit dispatches a PacketEvent to all listeners and the event channel.
func (e *nsxEngine) emit(ev *PacketEvent) {
	e.mu.Lock()
	e.history = append(e.history, ev)
	if len(e.history) > maxHistorySize {
		e.history = e.history[len(e.history)-maxHistorySize:]
	}
	listeners := make([]PacketListener, len(e.listeners))
	copy(listeners, e.listeners)
	e.mu.Unlock()

	for _, l := range listeners {
		l(ev)
	}
	e.mu.RLock()
	if !e.closed {
		select {
		case e.eventCh <- ev:
		default:
		}
	}
	e.mu.RUnlock()
}

func (e *nsxEngine) CapturePCAP(ctx context.Context, deviceID, ifaceName string, pktChan chan<- []byte) (func(), error) {
	return nil, fmt.Errorf("sim: pcap capture not supported in ns-x mode, use gont mode on Linux")
}

// clonePacket returns a deep copy of pkt.
func clonePacket(pkt *Packet) *Packet {
	if pkt == nil {
		return nil
	}
	out := *pkt
	if pkt.Payload != nil {
		out.Payload = make([]byte, len(pkt.Payload))
		copy(out.Payload, pkt.Payload)
	}
	if pkt.Path != nil {
		out.Path = make([]string, len(pkt.Path))
		copy(out.Path, pkt.Path)
	}
	if pkt.SrcMAC != nil {
		out.SrcMAC = make(net.HardwareAddr, len(pkt.SrcMAC))
		copy(out.SrcMAC, pkt.SrcMAC)
	}
	if pkt.DstMAC != nil {
		out.DstMAC = make(net.HardwareAddr, len(pkt.DstMAC))
		copy(out.DstMAC, pkt.DstMAC)
	}
	return &out
}
