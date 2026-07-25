package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	mrand "math/rand"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/logging"
	"ensp-lab/internal/metrics"
	"ensp-lab/internal/protocol"
	"ensp-lab/internal/router"
	"ensp-lab/internal/sim"
	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var version = "dev"
var buildTime = "unknown"

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type Router struct {
	store      storage.Storage
	cliStates  map[string]*cli.CLIState
	cliMu      sync.RWMutex           // protects cliStates map (was previously unprotected -> data race)
	cliLocks   map[string]*sync.Mutex // per-device mutex: serializes concurrent CLI on the SAME device
	protoSim   *protocol.ProtocolSimulator
	engines    map[string]sim.Engine  // topology_id -> engine
	engMu      sync.Mutex             // protects engines map
	syncTimers map[string]*time.Timer // per-topology debounce timers (R1)
	syncMu     sync.Mutex             // protects syncTimers
}

// deviceCLIMutex 返回指定设备的 CLI 串行锁（按需创建）。
// 同一设备多条并发 CLI 请求会争用同一把锁 -> 串行执行，避免争抢同一
// CLIState 导致视图/配置错乱；不同设备使用不同锁，互不阻塞。
// 调用方需持有返回的锁（通常 defer Unlock），锁自身生命周期与进程一致。
func (r *Router) deviceCLIMutex(deviceID string) *sync.Mutex {
	r.cliMu.Lock()
	defer r.cliMu.Unlock()
	if r.cliLocks == nil {
		r.cliLocks = make(map[string]*sync.Mutex)
	}
	m, ok := r.cliLocks[deviceID]
	if !ok {
		m = &sync.Mutex{}
		r.cliLocks[deviceID] = m
	}
	return m
}

// lookupDeviceType 根据拓扑 ID 和设备 ID 查询设备类型，未找到时返回空字符串。
func (r *Router) lookupDeviceType(topoID, deviceID string) topology.DeviceType {
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		return ""
	}
	for id, d := range t.Devices {
		if id == deviceID {
			return d.Type
		}
	}
	return ""
}

// getOrCreateEngine 返回指定拓扑的 Engine，如果尚未创建则懒加载。
//
// 引擎由 sim.NewEngine 工厂创建，自动根据平台选择 gont 或 ns-x
// 后端。首次调用会启动引擎的事件循环。
func (r *Router) getOrCreateEngine(topoID string) (sim.Engine, error) {
	r.engMu.Lock()
	defer r.engMu.Unlock()
	if eng, ok := r.engines[topoID]; ok {
		return eng, nil
	}
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		return nil, fmt.Errorf("api: topology %s not found", topoID)
	}
	eng, err := sim.NewEngine(t)
	if err != nil {
		return nil, fmt.Errorf("api: create engine: %w", err)
	}
	eng.Start()
	r.engines[topoID] = eng
	return eng, nil
}

// stopEngine 停止并移除指定拓扑的 Engine。
func (r *Router) stopEngine(topoID string) {
	r.engMu.Lock()
	defer r.engMu.Unlock()
	if eng, ok := r.engines[topoID]; ok {
		eng.Stop()
		delete(r.engines, topoID)
	}
}

// syncEngine 将拓扑的最新状态同步到该拓扑对应的仿真引擎（若存在）。
//
// 这是「编辑→仿真」链路的关键闭环（原 B1）。编辑操作（尤其拖拽连发）会高频
// 触发，若每次都立即 eng.Rebuild 做全量 Clone+build，会造成引擎抖动与 CPU
// 尖峰（R1）。这里对同一个 topoID 做 ~100ms 去抖合并：连续编辑只重置计时器，
// 静默 100ms 后才真正 Rebuild，且不会丢失末次编辑。
func (r *Router) syncEngine(topoID string) {
	const debounce = 100 * time.Millisecond
	var timer *time.Timer
	timer = time.AfterFunc(debounce, func() {
		r.runSync(topoID)
		r.syncMu.Lock()
		// 仅当记录的计时器仍是本计时器时才删除，避免与后续重置
		// （已替换 map 中的计时器）冲突导致新计时器被误删。
		if r.syncTimers[topoID] == timer {
			delete(r.syncTimers, topoID)
		}
		r.syncMu.Unlock()
	})
	r.syncMu.Lock()
	if old, ok := r.syncTimers[topoID]; ok {
		old.Stop()
	}
	r.syncTimers[topoID] = timer
	r.syncMu.Unlock()
}

// runSync 执行真正的引擎重建，仅在去抖计时器到期后才被调用。
func (r *Router) runSync(topoID string) {
	r.engMu.Lock()
	eng, ok := r.engines[topoID]
	r.engMu.Unlock()
	if !ok {
		return
	}
	// 记录一次触发重建的拓扑变更，用于把资源尖峰归因到编辑突发。
	metrics.IncrTopoMutation()
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		return
	}
	if err := eng.Rebuild(t); err != nil {
		logging.Warn("engine rebuild failed after topology change",
			zap.String("topology_id", topoID), zap.Error(err))
	}
}

func NewRouter(store storage.Storage, staticFS fs.FS) *gin.Engine {
	// 默认使用最小中间件栈：仅 Recovery（防止 panic 拖垮服务）。
	// gin.Default() 额外挂的 gin.Logger() 会为每个请求写一行 stdout 访问日志，
	// 属于持续的 I/O 与内存分配开销；在轻量/嵌入式场景下默认关闭，
	// 需要时通过 ENS_ACCESS_LOG=1 重新开启。
	r := gin.New()
	r.Use(gin.Recovery())
	if os.Getenv("ENS_ACCESS_LOG") != "" {
		r.Use(gin.Logger())
	}

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:8080"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	router := &Router{
		store:      store,
		cliStates:  make(map[string]*cli.CLIState),
		protoSim:   protocol.NewProtocolSimulator(nil),
		engines:    make(map[string]sim.Engine),
		syncTimers: make(map[string]*time.Timer),
	}

	// 启动常驻资源采集（CPU%/goroutine/heap/GC + 引擎活动计数），
	// 供 /api/system/metrics 与 health 端点实时观测与尖峰归因。
	metrics.Start()

	r.GET("/api/topologies", router.listTopologies)
	r.GET("/api/topologies/:id", router.getTopology)
	r.POST("/api/topologies", router.createTopology)
	r.PUT("/api/topologies/:id", router.updateTopology)
	r.DELETE("/api/topologies/:id", router.deleteTopology)

	r.POST("/api/topologies/:id/devices", router.addDevice)
	r.PUT("/api/topologies/:id/devices/:deviceId", router.updateDevice)
	r.DELETE("/api/topologies/:id/devices/:deviceId", router.deleteDevice)
	r.POST("/api/topologies/:id/devices/:deviceId/power", router.powerDevice)

	r.POST("/api/topologies/:id/links", router.addLink)
	r.PUT("/api/topologies/:id/links/:linkId", router.updateLink)
	r.DELETE("/api/topologies/:id/links/:linkId", router.deleteLink)

	r.POST("/api/topologies/:id/devices/:deviceId/cli", router.executeCLI)
	r.GET("/api/topologies/:id/devices/:deviceId/ip-config", router.getIPConfig)
	r.POST("/api/topologies/:id/devices/:deviceId/ip-config", router.setIPConfig)

	r.POST("/api/topologies/:id/annotations", router.addAnnotation)
	r.PUT("/api/topologies/:id/annotations/:annotationId", router.updateAnnotation)
	r.DELETE("/api/topologies/:id/annotations/:annotationId", router.deleteAnnotation)

	r.GET("/api/devices/types", router.getDeviceTypes)

	// 包模拟：执行 ping/traceroute 后，查询源→目标路径，返回链路列表+方向
	r.POST("/api/topologies/:id/simulate-packet", router.simulatePacket)

	// SSE 与状态端点：暴露 sim.Engine 的事件流和后端模式
	r.GET("/api/sim/events", router.streamSimEvents)
	r.GET("/api/sim/status", router.getSimStatus)
	r.GET("/api/sim/queue-depth", router.getQueueDepth)

	r.GET("/health", router.health)
	r.GET("/version", router.version)
	r.GET("/api/system/metrics", router.metrics)

	// 低资源稳定性测试：当 ENSP_PPROF 环境变量非空时，把标准 net/http/pprof
	// 端点挂到 gin，便于 `go tool pprof http://localhost:<port>/debug/pprof/...`
	// 采集 heap/goroutine 等 profile（默认关闭，不影响生产环境）。
	if os.Getenv("ENSP_PPROF") != "" {
		r.Any("/debug/pprof/*pprof", gin.WrapF(http.DefaultServeMux.ServeHTTP))
	}

	r.POST("/api/topology", router.createTopologySimple)
	r.GET("/api/topology/:id/pcap", router.streamPCAP)

	r.POST("/api/topology/:id/router/:device/ospf", router.applyOSPFConfig)
	r.POST("/api/topology/:id/router/:device/bgp", router.applyBGPConfig)
	r.GET("/api/topology/:id/router/:device/routes", router.getRoutes)

	r.GET("/api/topology/:id/ping", router.pingTopology)
	r.GET("/api/topology/:id/vxlan-status", router.vxlanStatus)

	if staticFS != nil {
		distFS, err := fs.Sub(staticFS, "frontend/dist")
		if err != nil {
			logging.Error("Failed to get dist FS", zap.Error(err))
			return r
		}
		assetsFS, err := fs.Sub(distFS, "assets")
		if err != nil {
			logging.Error("Failed to get assets FS", zap.Error(err))
			return r
		}
		r.StaticFS("/assets", http.FS(assetsFS))
		r.GET("/", func(c *gin.Context) {
			data, err := fs.ReadFile(distFS, "index.html")
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, "%s", data)
		})
		r.NoRoute(func(c *gin.Context) {
			data, err := fs.ReadFile(distFS, "index.html")
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, "%s", data)
		})
	}

	return r
}

// simulatePacket 请求体：
//
//	{ "src": "device-id", "dst": "device-id", "protocol": "ICMP"|"TCP"|"UDP", "ttl": 64 }
//
// 返回体：
//
//	{
//	  "path": [
//	    { "linkId": "...", "from": "A", "to": "B", "fromPort": "G0/0/0", "toPort": "G0/0/1" },
//	    ...
//	  ],
//	  "totalHops": 3
//	}
//
// 路径计算使用 BFS（无权图），返回从 src 到 dst 的最短链路序列。
func (r *Router) simulatePacket(c *gin.Context) {
	topoID := c.Param("id")
	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
		TTL int    `json:"ttl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Src == "" || req.Dst == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src and dst are required"})
		return
	}

	// BFS 找最短路径
	path := bfsPath(t, req.Src, req.Dst)
	if path == nil {
		c.JSON(http.StatusOK, gin.H{
			"path":      []any{},
			"totalHops": 0,
			"reachable": false,
			"message":   "No path found between src and dst",
		})
		return
	}

	// 构造链路序列（含方向）
	segments := make([]gin.H, 0, len(path)-1)
	for i := 0; i < len(path)-1; i++ {
		a, b := path[i], path[i+1]
		var link *topology.Link
		for _, l := range t.GetLinks() {
			if (l.SourceDevice == a && l.TargetDevice == b) ||
				(l.SourceDevice == b && l.TargetDevice == a) {
				link = l
				break
			}
		}
		if link == nil {
			continue
		}
		// 方向始终为 path 的走向 a→b
		from, to := a, b
		var fromPort, toPort string
		if link.SourceDevice == a && link.TargetDevice == b {
			fromPort, toPort = link.SourcePort, link.TargetPort
		} else {
			fromPort, toPort = link.TargetPort, link.SourcePort
		}
		segments = append(segments, gin.H{
			"linkId":   link.ID,
			"from":     from,
			"to":       to,
			"fromPort": fromPort,
			"toPort":   toPort,
			"linkType": link.LinkType,
			"status":   link.Status,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"path":      segments,
		"totalHops": len(segments),
		"reachable": true,
		"ttl":       req.TTL,
	})
}

// bfsPath 返回 topo 中从 src 到 dst 的最短设备 ID 序列（无权 BFS）。
// 未连通时返回 nil。
func bfsPath(t *topology.Topology, src, dst string) []string {
	if src == dst {
		return []string{src}
	}

	// 建邻接表
	adj := make(map[string][]string)
	for _, d := range t.GetDeviceIDs() {
		adj[d] = nil
	}
	for _, l := range t.GetLinks() {
		if l.SourceDevice != "" && l.TargetDevice != "" {
			adj[l.SourceDevice] = append(adj[l.SourceDevice], l.TargetDevice)
			adj[l.TargetDevice] = append(adj[l.TargetDevice], l.SourceDevice)
		}
	}

	visited := map[string]bool{src: true}
	queue := []string{src}
	parent := map[string]string{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if visited[nb] {
				continue
			}
			visited[nb] = true
			parent[nb] = cur
			if nb == dst {
				// 回溯
				var path []string
				for id := dst; id != ""; id = parent[id] {
					path = append([]string{id}, path...)
				}
				return path
			}
			queue = append(queue, nb)
		}
	}
	return nil // 不连通
}

// streamSimEvents 通过 SSE 持续推送 PacketEvent。
//
// 客户端通过 EventSource 连接此端点，每次 PacketEvent 会以
// "event: packet\ndata: <json>\n\n" 格式推送。连接保持直到
// 客户端断开或服务端关闭。
//
// 查询参数：
//   - topology: 拓扑 ID（必填）
func (r *Router) streamSimEvents(c *gin.Context) {
	topoID := c.Query("topology")
	if topoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topology query parameter required"})
		return
	}
	// 安全：topoID 会被原样写入 SSE 事件体，拒绝控制字符避免破坏 SSE 帧结构。
	if strings.ContainsAny(topoID, "\x00\n\r") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid topology parameter"})
		return
	}
	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	ctx := c.Request.Context()
	events := eng.Events()
	flusher, _ := c.Writer.(http.Flusher)

	// 发送初始连接确认事件
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"topology\":\"%s\"}\n\n", topoID)
	if flusher != nil {
		flusher.Flush()
	}

	ticker := time.NewTicker(30 * time.Second) // 心跳，防止代理超时
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 发送心跳注释（SSE 注释格式，客户端会忽略）
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(c.Writer, "event: packet\ndata: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// getSimStatus 返回当前引擎模式（ns-x 或 gont）。
//
// 查询参数：
//   - topology: 拓扑 ID（可选，若提供则返回该拓扑的引擎状态）
func (r *Router) getSimStatus(c *gin.Context) {
	topoID := c.Query("topology")
	status := gin.H{
		"platform": runtime.GOOS,
	}
	if topoID == "" {
		// 全局状态：返回平台探测结果
		status["mode"] = "auto"
		c.JSON(http.StatusOK, status)
		return
	}
	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		status["mode"] = "unavailable"
		status["error"] = err.Error()
		c.JSON(http.StatusOK, status)
		return
	}
	status["mode"] = eng.Mode()
	c.JSON(http.StatusOK, status)
}

func (r *Router) getQueueDepth(c *gin.Context) {
	topoID := c.Query("topology")
	if topoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topology query parameter is required"})
		return
	}
	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"topology":    topoID,
		"queue_depth": eng.QueueDepth(),
	})
}

func (r *Router) streamPCAP(c *gin.Context) {
	id := c.Param("id")
	deviceID := c.Query("device")
	ifaceName := c.Query("interface")

	if deviceID == "" || ifaceName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device and interface query parameters required"})
		return
	}

	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	_, ok := t.GetDevice(deviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}

	eng, err := r.getOrCreateEngine(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	pktChan := make(chan []byte, 1024)
	ctx := c.Request.Context()

	stop, err := eng.CapturePCAP(ctx, deviceID, ifaceName, pktChan)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer stop()

	c.Writer.Header().Set("Content-Type", "application/octet-stream")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	flusher, _ := c.Writer.(http.Flusher)

	for {
		select {
		case <-ctx.Done():
			return
		case pkt := <-pktChan:
			c.Writer.Write(pkt)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (r *Router) applyOSPFConfig(c *gin.Context) {
	topoID := c.Param("id")
	deviceID := c.Param("device")

	var req ApplyOSPFConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	_, ok := t.GetDevice(deviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}

	// 安全：OSPF network/area 将被直接写入 FRR ospfd.conf，必须先校验，
	// 否则恶意 network 可注入额外配置行（配置注入 / 非预期邻居）。
	if err := validateCIDR(req.Network); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateOSPFArea(req.Area); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type ospfConfigurer interface {
		ApplyOSPFConfig(deviceID, network, area string) error
	}

	if conf, ok := eng.(ospfConfigurer); ok {
		if err := conf.ApplyOSPFConfig(deviceID, req.Network, req.Area); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, ApplyOSPFConfigResponse{
			DeviceID: deviceID,
			Status:   "success",
			Message:  fmt.Sprintf("OSPF config applied: network %s area %s", req.Network, req.Area),
		})
	} else {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "OSPF config not supported by current engine"})
	}
}

func (r *Router) applyBGPConfig(c *gin.Context) {
	topoID := c.Param("id")
	deviceID := c.Param("device")

	var req ApplyBGPConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	_, ok := t.GetDevice(deviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}

	// 安全：BGP 邻居 IP / AS 号将被写入 FRR bgpd.conf，必须先校验，
	// 否则恶意 neighbor IP 或非法 AS 可注入非预期 BGP 会话。
	if err := validateASN(req.LocalAS); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for _, n := range req.Neighbors {
		if err := validateIP(n.IP); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("neighbor %q: %v", n.IP, err)})
			return
		}
		if err := validateASN(n.RemoteAS); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type bgpConfigurer interface {
		ApplyBGPConfig(deviceID string, localAS uint32, neighbors []router.BGPNeighbor) error
	}

	if conf, ok := eng.(bgpConfigurer); ok {
		neighbors := make([]router.BGPNeighbor, len(req.Neighbors))
		for i, n := range req.Neighbors {
			neighbors[i] = router.BGPNeighbor{
				IP:       n.IP,
				RemoteAS: n.RemoteAS,
			}
		}
		if err := conf.ApplyBGPConfig(deviceID, req.LocalAS, neighbors); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, ApplyBGPConfigResponse{
			DeviceID: deviceID,
			Status:   "success",
			Message:  fmt.Sprintf("BGP config applied: local AS %d, %d neighbors", req.LocalAS, len(req.Neighbors)),
		})
	} else {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "BGP config not supported by current engine"})
	}
}

func (r *Router) getRoutes(c *gin.Context) {
	topoID := c.Param("id")
	deviceID := c.Param("device")

	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	_, ok := t.GetDevice(deviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}

	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type routeGetter interface {
		GetRoutes(deviceID string) ([]router.RouteInfo, error)
	}

	if getter, ok := eng.(routeGetter); ok {
		routes, err := getter.GetRoutes(deviceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		apiRoutes := make([]RouteInfo, len(routes))
		for i, r := range routes {
			apiRoutes[i] = RouteInfo{
				Destination: r.Destination,
				NextHop:     r.NextHop,
				Metric:      r.Metric,
				Protocol:    r.Protocol,
			}
		}

		c.JSON(http.StatusOK, GetRoutesResponse{
			DeviceID: deviceID,
			Routes:   apiRoutes,
		})
	} else {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Route query not supported by current engine"})
	}
}

func (r *Router) pingTopology(c *gin.Context) {
	topoID := c.Param("id")
	src := c.Query("src")
	dst := c.Query("dst")
	countStr := c.DefaultQuery("count", "4")
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		count = 4
	}
	if count > 100 {
		count = 100
	}

	if src == "" || dst == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src and dst query parameters are required"})
		return
	}

	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	_, ok := t.GetDevice(src)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Source device not found"})
		return
	}

	var dstIP string
	dstDev, ok := t.GetDevice(dst)
	if ok {
		for _, iface := range dstDev.Interfaces {
			if iface.Status == "up" && iface.IPAddress != "" {
				dstIP = iface.IPAddress
				break
			}
		}
		if dstIP == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Destination device has no active interface"})
			return
		}
	} else {
		dstIP = dst
	}

	eng, err := r.getOrCreateEngine(topoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var sent, received, lost int
	var details []string
	var rtts []float64
	for i := 0; i < count; i++ {
		start := time.Now()
		result, perr := eng.Ping(src, dstIP)
		elapsed := time.Since(start).Seconds() * 1000
		if perr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": perr.Error()})
			return
		}
		seq := i + 1
		if result == nil {
			result = &sim.PingResult{Sent: 1, Received: 0, Lost: 1}
		}
		sent += result.Sent
		received += result.Received
		lost += result.Lost
		if result.Received > 0 {
			// 模拟 RTT：若引擎已返回 RTT 则使用，否则按实际耗时稍加抖动
			rtt := elapsed
			if rtt <= 0 {
				rtt = 0.5 + mrand.Float64()*0.8
			}
			rtts = append(rtts, rtt)
			details = append(details, fmt.Sprintf("64 bytes from %s: icmp_seq=%d ttl=64 %.2fms", dstIP, seq, rtt))
		} else {
			details = append(details, fmt.Sprintf("Request timeout for icmp_seq=%d", seq))
		}
	}

	var rttMs *float64
	if len(rtts) > 0 {
		var sum float64
		var min, max float64
		for i, v := range rtts {
			sum += v
			if i == 0 || v < min {
				min = v
			}
			if i == 0 || v > max {
				max = v
			}
		}
		avg := sum / float64(len(rtts))
		details = append(details, fmt.Sprintf("round-trip min/avg/max = %.2f/%.2f/%.2f ms", min, avg, max))
		rttMs = &avg
	}
	lossRate := 0
	if sent > 0 {
		lossRate = lost * 100 / sent
	}
	details = append(details, fmt.Sprintf("%d packets transmitted, %d received, %d%% loss", sent, received, lossRate))

	resp := gin.H{
		"src":      src,
		"dst":      dst,
		"dst_ip":   dstIP,
		"sent":     sent,
		"received": received,
		"lost":     lost,
		"details":  details,
	}
	if rttMs != nil {
		resp["rtt_ms"] = *rttMs
	}
	c.JSON(http.StatusOK, resp)
}

func (r *Router) vxlanStatus(c *gin.Context) {
	topoID := c.Param("id")

	t, err := r.store.GetTopology(topoID)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}

	type VXLANTunnel struct {
		VNI      int      `json:"vni"`
		Source   string   `json:"source"`
		Target   string   `json:"target"`
		PeerList []string `json:"peer_list"`
		Status   string   `json:"status"`
		LinkID   string   `json:"link_id"`
	}

	tunnels := []VXLANTunnel{}
	for _, link := range t.GetLinks() {
		if link.VXLANVNI > 0 {
			status := "DOWN"
			if link.Status == "up" {
				status = "UP"
			}
			tunnels = append(tunnels, VXLANTunnel{
				VNI:      link.VXLANVNI,
				Source:   link.SourceDevice,
				Target:   link.TargetDevice,
				PeerList: link.VXLANPeerList,
				Status:   status,
				LinkID:   link.ID,
			})
		}
	}

	type VTEPDevice struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		IP        string   `json:"ip"`
		BDs       []int    `json:"bds"`
		TunnelIDs []string `json:"tunnel_ids"`
	}

	vteps := []VTEPDevice{}
	for _, dev := range t.Devices {
		if dev.Type == topology.DeviceVTEP {
			var ip string
			// 优先使用 LoopBack0 作为 VTEP IP（华为 VXLAN 以环回口作源 VTEP），
			// 否则回退到第一个有 IP 的接口。
			if lb, ok := dev.Interfaces["LoopBack0"]; ok && lb.IPAddress != "" {
				ip = lb.IPAddress
			} else {
				for _, iface := range dev.Interfaces {
					if iface.IPAddress != "" {
						ip = iface.IPAddress
						break
					}
				}
			}
			var bds []int
			var tunnelIDs []string
			for _, link := range t.GetLinks() {
				if (link.SourceDevice == dev.ID || link.TargetDevice == dev.ID) && link.VXLANVNI > 0 {
					tunnelIDs = append(tunnelIDs, link.ID)
					found := false
					for _, bd := range bds {
						if bd == link.VXLANVNI {
							found = true
							break
						}
					}
					if !found {
						bds = append(bds, link.VXLANVNI)
					}
				}
			}
			vteps = append(vteps, VTEPDevice{
				ID:        dev.ID,
				Name:      dev.Name,
				IP:        ip,
				BDs:       bds,
				TunnelIDs: tunnelIDs,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"topology_id":   topoID,
		"tunnels":       tunnels,
		"vteps":         vteps,
		"total_tunnels": len(tunnels),
		"total_vteps":   len(vteps),
	})
}
