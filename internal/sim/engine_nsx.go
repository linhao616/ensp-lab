package sim

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ensp-lab/internal/logging"
	"ensp-lab/internal/metrics"
	"ensp-lab/internal/router"
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

// enginePollInterval 控制 ns-x 事件循环的轮询周期。
// 此前使用 1ms 会使事件循环几乎不休眠，导致引擎常驻 100% CPU（诊断 R4）；
// 5ms 对实验室级拓扑（Ping RTT 通常数十~数百 ms）无感知影响，
// 但能显著压低基线 CPU。可用环境变量 ENS_ENGINE_POLL_MS（毫秒，>0）覆盖。
var enginePollInterval = func() time.Duration {
	if v := os.Getenv("ENS_ENGINE_POLL_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 5 * time.Millisecond
}()

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
					g := n.engine.snap()
					if targetBridge, ok := g.bridges[targetVTEP]; ok {
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
// graphSnapshot 是引擎内部图状态的一份不可变快照。
//
// 设计要点：引擎绝不持有 API 层的 *topology.Topology 共享指针，而是保存一份
// 私有深拷贝。拓扑变更通过 Rebuild 用 atomic.Value 整体替换快照，包处理路径
// 用 snap() 读取，无需与 API 的 t.mu 协同步，从根本上消除「引擎直读共享拓扑」
// 引发的并发读写竞争（原 B2）。快照一旦存入即不再修改，并发读取安全。
type graphSnapshot struct {
	topo      *topology.Topology // 引擎私有深拷贝（不可变）
	endpoints map[string]*node.EndpointNode
	bridges   map[string]*BridgeNode
	network   *nsx.Network
}

type nsxEngine struct {
	mode  EngineMode
	clock tick.Clock
	graph atomic.Value // *graphSnapshot

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

	// routingConfig 记录各设备的路由协议配置意图（ns-x 不运行 FRR，
	// 仅持久化，便于状态查询与将来扩展；动态协议学到的路由需 gont 模式）。
	routingConfig map[string]*nsxRoutingConfig
}

// snap 以原子方式返回当前图状态快照。快照不可变，并发读取安全。
func (e *nsxEngine) snap() *graphSnapshot {
	return e.graph.Load().(*graphSnapshot)
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
		clock:         tick.NewRealClock(),
		listeners:     []PacketListener{},
		eventCh:       make(chan *PacketEvent, 128),
		history:       []*PacketEvent{},
		cancelCtx:     ctx,
		cancelFunc:    cancel,
		pendingEvents: make(chan base.Event, 32),
		pingResults:   make(map[string]chan *PingResult),
		pingSemaphore: make(chan struct{}, maxConcurrentPings),
		routingConfig: make(map[string]*nsxRoutingConfig),
	}
	snap, err := e.build(topo.Clone())
	if err != nil {
		return nil, fmt.Errorf("sim: build ns-x network: %w", err)
	}
	e.graph.Store(snap)
	return e, nil
}

// Mode returns the active engine mode as a string identifier
// ("ns-x" or "gont"). Satisfies the Engine interface.
func (e *nsxEngine) Mode() string { return string(e.mode) }

// Rebuild 在拓扑变更时被调用，用新拓扑的私有深拷贝整体替换引擎内部图状态。
//
// 通过 atomic.Value.Store 原子替换快照，包处理路径用 snap() 读取，因此无需
// 持 e.mu，调用方可在任意时刻安全调用（并发安全）。深拷贝保证 API 层后续对
// 共享 *Topology 的就地修改不会影响已加载的引擎视图（原 B2）。
func (e *nsxEngine) Rebuild(topo *topology.Topology) error {
	if topo == nil {
		return nil
	}
	snap, err := e.build(topo.Clone())
	if err != nil {
		return fmt.Errorf("sim: rebuild ns-x network: %w", err)
	}
	e.graph.Store(snap)
	return nil
}

// build 依据给定拓扑构建 ns-x 节点图，返回一份不可变快照。
//
// 构建过程完全基于入参 topo 的私有视图，不读取也不写入引擎的共享字段，
// 因此可在任意时刻并发调用。返回的快照存入 atomic.Value 后即视为只读。
func (e *nsxEngine) build(topo *topology.Topology) (*graphSnapshot, error) {
	// 统计每次节点图（重）构建的耗时与次数——初始懒加载建图与后续 Rebuild 都走这里，
	// 因此这是把「某一刻 CPU/GC 飙高」归因到拓扑编辑突发（R1）的统一口径。
	start := time.Now()
	defer func() { metrics.RecordRebuild(time.Since(start)) }()

	builder := nsx.NewBuilder()

	endpoints := make(map[string]*node.EndpointNode)
	for id, dev := range topo.Devices {
		ep := node.NewEndpointNode(
			node.WithTransferCallback(e.makeTransferCallback(dev.ID)),
		)
		ep.Receive(e.makeReact(dev.ID))
		endpoints[id] = ep
		builder.NodeWithName(dev.ID, ep)
		logging.Info("Created endpoint", zap.String("device", id), zap.String("devType", string(dev.Type)))
	}

	linksByDevice := make(map[string][]*topology.Link)
	vxlanLinks := make(map[string][]*topology.Link)
	for _, link := range topo.Links {
		if link.VXLANVNI > 0 {
			vxlanLinks[link.SourceDevice] = append(vxlanLinks[link.SourceDevice], link)
			vxlanLinks[link.TargetDevice] = append(vxlanLinks[link.TargetDevice], link)
		} else {
			linksByDevice[link.SourceDevice] = append(linksByDevice[link.SourceDevice], link)
			linksByDevice[link.TargetDevice] = append(linksByDevice[link.TargetDevice], link)
		}
	}

	bridges := make(map[string]*BridgeNode)
	for id := range topo.Devices {
		totalLinks := len(linksByDevice[id]) + len(vxlanLinks[id])
		if totalLinks > 1 {
			bridge := NewBridgeNode(e, id)
			bridges[id] = bridge
			builder.NodeWithName(id+"-bridge", bridge)
		}
	}

	for _, link := range topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}

		srcEp := endpoints[link.SourceDevice]
		dstEp := endpoints[link.TargetDevice]
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

	for _, link := range topo.Links {
		if link.VXLANVNI <= 0 {
			continue
		}

		srcEp := endpoints[link.SourceDevice]
		dstEp := endpoints[link.TargetDevice]
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

	dbgSim("DEBUG: Build complete - endpoints=%d, bridges=%d\n", len(endpoints), len(bridges))
	for devID, ep := range endpoints {
		dbgSim("DEBUG: Endpoint %s next=%d\n", devID, len(ep.GetNext()))
	}
	for devID, bridge := range bridges {
		dbgSim("DEBUG: Bridge %s next=%d\n", devID, len(bridge.GetNext()))
	}

	nodes := make([]base.Node, 0, len(endpoints)+len(bridges))
	for _, ep := range endpoints {
		nodes = append(nodes, ep)
	}
	for _, bridge := range bridges {
		nodes = append(nodes, bridge)
	}
	network := nsx.NewNetwork(nodes)
	return &graphSnapshot{
		topo:      topo,
		endpoints: endpoints,
		bridges:   bridges,
		network:   network,
	}, nil
}

// makeTransferCallback returns a callback that emits forward events.
func (e *nsxEngine) makeTransferCallback(deviceID string) base.TransferCallback {
	return func(packet base.Packet, source, target base.Node, now time.Time) {
		np, ok := packet.(*nsxPacket)
		if !ok || np == nil || np.sim == nil {
			return
		}
		var targetID string
		for id, ep := range e.snap().endpoints {
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
	g := e.snap()
	var peers []string
	for _, link := range g.topo.Links {
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
	g := e.snap()
	for id, dev := range g.topo.Devices {
		for _, iface := range dev.Interfaces {
			if iface.IPAddress == ip.String() {
				return id, true
			}
		}
	}
	return "", false
}

func (e *nsxEngine) getDeviceVLAN(ip net.IP) int {
	g := e.snap()
	for _, dev := range g.topo.Devices {
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
	g := e.snap()
	for _, link := range g.topo.Links {
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
	g := e.snap()
	dev := g.topo.Devices[deviceID]
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
		if target, ok := g.bridges[nextDev]; ok {
			return target.Transfer(&nsxPacket{sim: cloned, data: cloned.Payload}, now.Add(time.Millisecond))
		}
		if ep, ok := g.endpoints[nextDev]; ok {
			return ep.Transfer(&nsxPacket{sim: cloned, data: cloned.Payload}, now.Add(time.Millisecond))
		}
		return nil
	}
	return nil
}

func (e *nsxEngine) findVTEPsInVNI(vni int) []string {
	g := e.snap()
	var vteps []string
	for _, link := range g.topo.Links {
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
	g := e.snap()
	dev := g.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	return dev.Type == topology.DeviceVTEP
}

func (e *nsxEngine) isSwitch(deviceID string) bool {
	g := e.snap()
	dev := g.topo.Devices[deviceID]
	if dev == nil {
		return false
	}
	return dev.Type == topology.DeviceSwitch || dev.Type == topology.DeviceL3Switch
}

func (e *nsxEngine) isServer(deviceID string) bool {
	g := e.snap()
	dev := g.topo.Devices[deviceID]
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
		// 每个被引擎接收并处理的包都计数，用于把 CPU 尖峰关联到流量突发。
		metrics.IncrPacket()

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

					ep := e.snap().endpoints[deviceID]
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

		ep := e.snap().endpoints[deviceID]
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
	g := e.snap()
	for _, link := range g.topo.Links {
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
	g := e.snap()
	for _, link := range g.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}
		if (link.SourceDevice == deviceID && link.TargetDevice == targetID) ||
			(link.TargetDevice == deviceID && link.SourceDevice == targetID) {
			return true
		}
	}

	for _, link := range g.topo.Links {
		if link.VXLANVNI > 0 {
			continue
		}
		if link.SourceDevice == deviceID || link.TargetDevice == deviceID {
			serverID := link.SourceDevice
			if serverID == deviceID {
				serverID = link.TargetDevice
			}
			for _, link2 := range g.topo.Links {
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
	g := e.snap()
	dev := g.topo.Devices[deviceID]
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
	g := e.snap()
	if start == target {
		return true
	}
	if visited[start] {
		return false
	}
	visited[start] = true

	for _, link := range g.topo.Links {
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
	g := e.snap()
	dev, ok := g.topo.Devices[deviceID]
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
// network.Run 是非阻塞的（创建后台 goroutine 后立即返回），
// 因此 wg.Done() 在 Start goroutine 中会被快速调用，不会阻塞 24h。
func (e *nsxEngine) Start() {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.closed = false
	e.eventCh = make(chan *PacketEvent, 128)
	e.pendingEvents = make(chan base.Event, 32)
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelCtx = ctx
	e.cancelFunc = cancel
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
		}, enginePollInterval, time.Now())
		e.snap().network.Run([]base.Event{poller}, e.clock, 24*time.Hour)
	}()
}

// Stop 停止事件循环并释放资源。幂等：重复调用安全（已停止时直接返回）。
//
// 修正说明（原 B3）：
//   - 先置 e.closed=true（持锁），使并发的 SendPacket/emit 跳过后续操作。
//   - 不再 close 事件/待处理通道：ns-x 的 network.Run 是非阻塞的，其后台
//     事件循环 goroutine 会继续运行最多 24h；close channel 不会终止该循环，
//     反而会使并发的 emit() → send on closed channel panic。set closed=true
//     后 goroutine 空转无害，重启时 Start() 会创建新通道、旧通道被遗弃。
func (e *nsxEngine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	e.closed = true
	e.mu.Unlock()

	e.wg.Wait() // Start goroutine 已返回（network.Run 非阻塞）

	// 清理 ping 结果通道（避免泄漏），但不下 close(e.eventCh/pendingEvents)：
	// ns-x 网络事件循环仍在运行，close channel 会导致 send-on-closed panic。
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
	if e.closed {
		e.mu.RUnlock()
		logging.Error("SendPacket: engine closed, drop", zap.String("device", fromDeviceID))
		return
	}
	ep, ok := e.snap().endpoints[fromDeviceID]
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

	// 发送前再次检查 closed（持 RLock），与 Stop 的关闭互斥，避免 send on closed channel。
	e.mu.RLock()
	if !e.closed {
		metrics.NotePending(len(e.pendingEvents))
		select {
		case e.pendingEvents <- event:
			dbgSim("DEBUG: SendPacket event queued\n")
		default:
			dbgSim("DEBUG: SendPacket event queue full\n")
		}
	}
	e.mu.RUnlock()
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
	// 在途 Ping 计数 +1，用于关联 CPU 尖峰与并发 Ping 突发。
	metrics.AddPingsActive(1)
	defer metrics.AddPingsActive(-1)

	dev, ok := e.snap().topo.Devices[srcDeviceID]
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
		metrics.IncrPing(false)
		return result, nil
	case <-time.After(3 * time.Second):
		metrics.IncrPing(true)
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

// nsxRoutingConfig 与 nsx 引擎内持久化的路由协议配置意图（非 FRR 运行时）。
type nsxRoutingConfig struct {
	OSPF *nsxOSPFConfig
	BGP  *nsxBGPConfig
}

type nsxOSPFConfig struct {
	Network string
	Area    string
}

type nsxBGPConfig struct {
	LocalAS   uint32
	Neighbors []router.BGPNeighbor
}

// networkCIDR 由接口 IP 与掩码计算所属网络 CIDR（如 192.168.2.1/255.255.255.0 -> 192.168.2.0/24）。
// 入参非法时返回错误，调用方据此跳过该接口而非中断整表计算（边界/异常处理）。
func networkCIDR(ipStr, maskStr string) (string, error) {
	if ipStr == "" || maskStr == "" {
		return "", fmt.Errorf("empty address or mask")
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", fmt.Errorf("invalid IP %q", ipStr)
	}
	m := net.ParseIP(maskStr)
	if m == nil {
		return "", fmt.Errorf("invalid mask %q", maskStr)
	}
	v4 := m.To4()
	if v4 == nil {
		return "", fmt.Errorf("mask %q is not IPv4", maskStr)
	}
	mask := net.IPMask(v4)
	network := ip.Mask(mask)
	return (&net.IPNet{IP: network, Mask: mask}).String(), nil
}

// GetRoutes 返回指定设备的路由表。ns-x 模式不运行 FRR，故基于拓扑计算
// 「直连路由」与「同二层广播域内的静态路由」，覆盖 lab01~lab04 等
// 单段/VLAN/STP/静态路由场景；动态协议（OSPF/BGP）学到的路由需切到
// gont 模式（Linux + FRR）才能获得。该实现消除了此前 ns-x 返回 501 的缺口。
//
// 算法：交换机/集线器透传所有端口（同一广播域），L3 设备为泛洪终点；
// 直连路由来自本机接口，静态路由来自同广播域内其他设备的接口子网，
// 下一跳取其接口 IP。不含环（visited 去重），复杂度 O(V+E)。
func (e *nsxEngine) GetRoutes(deviceID string) ([]router.RouteInfo, error) {
	topo := e.snap().topo
	if topo == nil {
		return nil, fmt.Errorf("engine has no topology loaded")
	}
	if _, ok := topo.GetDevice(deviceID); !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, deviceID)
	}

	// 构建无向邻接表
	adj := make(map[string][]string)
	for _, l := range topo.GetLinks() {
		adj[l.SourceDevice] = append(adj[l.SourceDevice], l.TargetDevice)
		adj[l.TargetDevice] = append(adj[l.TargetDevice], l.SourceDevice)
	}

	// L2 泛洪：交换机/集线器透传（同一广播域），L3 设备为终点。
	// 注意：是否继续向下泛洪取决于「邻居」是否为交换机/集线器，而非当前节点。
	l2 := map[string]bool{deviceID: true}
	queue := []string{deviceID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if l2[nb] {
				continue
			}
			l2[nb] = true
			dev, _ := topo.GetDevice(nb)
			if dev != nil && (dev.Type == topology.DeviceSwitch || dev.Type == topology.DeviceHub) {
				queue = append(queue, nb)
			}
		}
	}

	routes := make([]router.RouteInfo, 0)
	self, _ := topo.GetDevice(deviceID)
	for _, iface := range self.Interfaces {
		cidr, err := networkCIDR(iface.IPAddress, iface.SubnetMask)
		if err != nil {
			continue
		}
		routes = append(routes, router.RouteInfo{
			Destination: cidr,
			NextHop:     "0.0.0.0",
			Metric:      0,
			Protocol:    "connected",
		})
	}
	for other := range l2 {
		if other == deviceID {
			continue
		}
		od, _ := topo.GetDevice(other)
		if od == nil {
			continue
		}
		for _, iface := range od.Interfaces {
			cidr, err := networkCIDR(iface.IPAddress, iface.SubnetMask)
			if err != nil {
				continue
			}
			routes = append(routes, router.RouteInfo{
				Destination: cidr,
				NextHop:     iface.IPAddress,
				Metric:      1,
				Protocol:    "static",
			})
		}
	}
	return routes, nil
}

// ApplyOSPFConfig 在 ns-x 模式下不调用 FRR，仅持久化配置意图。
// 参数校验已在 API 层完成（validateCIDR/validateOSPFArea）。返回 nil 使
// 接口在 Windows 默认环境下返回 200 而非 501。并发安全（持 e.mu）。
func (e *nsxEngine) ApplyOSPFConfig(deviceID, network, area string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg := e.routingConfig[deviceID]
	if cfg == nil {
		cfg = &nsxRoutingConfig{}
		e.routingConfig[deviceID] = cfg
	}
	cfg.OSPF = &nsxOSPFConfig{Network: network, Area: area}
	return nil
}

// ApplyBGPConfig 在 ns-x 模式下不调用 FRR，仅持久化配置意图。
// 参数校验已在 API 层完成（validateASN/validateIP）。返回 nil 使接口在
// Windows 默认环境下返回 200 而非 501。并发安全（持 e.mu）。
func (e *nsxEngine) ApplyBGPConfig(deviceID string, localAS uint32, neighbors []router.BGPNeighbor) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg := e.routingConfig[deviceID]
	if cfg == nil {
		cfg = &nsxRoutingConfig{}
		e.routingConfig[deviceID] = cfg
	}
	cfg.BGP = &nsxBGPConfig{LocalAS: localAS, Neighbors: neighbors}
	return nil
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
