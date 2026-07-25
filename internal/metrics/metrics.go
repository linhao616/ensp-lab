// Package metrics 提供进程级资源使用率的常驻采集，以及把"某一刻资源
// 飙高"归因到具体业务路径（拓扑重建 / Ping 流量 / GC 压力）的计数与诊断。
//
// 设计目标：
//   - 零外部依赖（仅标准库 + golang.org/x/sys/windows 用于 Windows 真实 CPU 率）。
//   - 常驻、极低开销：单后台 goroutine 每 1s 采样一次 runtime.MemStats 与
//     进程 CPU 时间；所有业务计数走 atomic，不阻塞热路径。
//   - 与已内置的 pprof（ENSP_PPROF=1）互补：pprof 用于事后深度剖析，本包用于
//     实时观测与尖峰归因。
package metrics

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// sampleInterval 是后台采样间隔。1s 足够捕捉尖峰，又不会给系统添负担。
const sampleInterval = 1 * time.Second

// Snapshot 是某一时刻的资源与活动计数快照，直接序列化为 JSON 供端点返回。
type Snapshot struct {
	Timestamp time.Time `json:"timestamp"`
	UptimeSec float64   `json:"uptime_sec"`

	// 进程资源使用率
	CPUPercent   float64 `json:"cpu_percent"`    // 采样窗口内进程占整机 CPU 的百分比（已按核数归一到 0-100）
	Goroutines   int     `json:"goroutines"`     // 当前 goroutine 数
	HeapAllocMB  float64 `json:"heap_alloc_mb"`  // 当前堆占用（Live Heap）
	HeapSysMB    float64 `json:"heap_sys_mb"`    // 向 OS 申请的堆总量
	HeapObjects  uint64  `json:"heap_objects"`   // 当前堆对象数
	NumGC        uint32  `json:"num_gc"`         // 累计 GC 次数
	GCCPUPercent float64 `json:"gc_cpu_percent"` // 采样窗口内 GC 占用 CPU 百分比
	PauseTotalMs float64 `json:"pause_total_ms"` // 累计 GC STW 停顿（ms）

	// 引擎活动计数：用于将资源尖峰归因到具体代码路径
	PacketsProcessed uint64  `json:"packets_processed"` // 累计处理的包（makeReact 触发）
	PingsActive      int64   `json:"pings_active"`      // 当前在途 Ping 数（瞬时）
	PingsTotal       uint64  `json:"pings_total"`       // 累计 Ping 次数
	PingsTimeout     uint64  `json:"pings_timeout"`     // 累计 Ping 超时数
	RebuildsTotal    uint64  `json:"rebuilds_total"`    // 累计引擎重建次数（syncEngine→Rebuild）
	TopoMutations    uint64  `json:"topo_mutations"`    // 累计拓扑变更次数（触发 syncEngine 的 handler 数）
	RebuildLastMs    float64 `json:"rebuild_last_ms"`   // 最近一次 Rebuild 耗时（ms）
	RebuildsLast10s  uint64  `json:"rebuilds_last_10s"` // 近 10s 内 Rebuild 次数
	RebuildBusyMs    float64 `json:"rebuild_busy_ms"`   // 累计 Rebuild 耗时（含 Clone+build，ms）
	PendingEventMax  int     `json:"pending_event_max"` // 观察到的引擎待处理事件队列最大深度

	// Diagnosis 是基于上述计数给出的"为什么现在飙高"的归因建议。
	Diagnosis []string `json:"diagnosis"`
}

// Collector 维护所有指标。热路径只用 atomic；采样与快照读取走 memMu。
type Collector struct {
	startTime time.Time

	// 单调计数器（atomic）
	packets      uint64
	pingsTotal   uint64
	pingsTimeout uint64
	rebuilds     uint64
	topoMutations uint64
	rebuildBusyNs uint64
	lastRebuildNs uint64

	// 瞬时 gauge（atomic）
	pingsActive int64
	pendingMax  int64

	// 滚动窗口：记录每次 Rebuild 的时间戳（unix ms），用于统计近 10s 次数
	windowMu      sync.Mutex
	rebuildWindow []int64

	// 采样缓存（由后台 goroutine 写入，Snapshot 读取）
	memMu        sync.Mutex
	cpuPercent   float64
	goroutines   int
	heapAlloc    uint64
	heapSys      uint64
	heapObjects  uint64
	numGC        uint32
	gcCPUPercent float64
	pauseTotalNs uint64

	started atomic.Bool
}

// Default 是进程级单例采集器，NewRouter 启动时通过 Start() 拉起后台采样。
var Default = NewCollector()

// NewCollector 创建一个采集器（不自动启动采样，需调用 Start）。
func NewCollector() *Collector {
	return &Collector{startTime: time.Now()}
}

// Start 启动后台采样 goroutine。幂等：重复调用安全。
func (c *Collector) Start() {
	if c.started.Load() {
		return
	}
	c.started.Store(true)
	go c.sampleLoop()
}

// ---- 业务埋点 API（热路径，全部 atomic，零分配）----

// IncrPacket 记录一个被引擎处理的包（在 makeReact 入口调用）。
func (c *Collector) IncrPacket() { atomic.AddUint64(&c.packets, 1) }

// AddPingsActive 改变"在途 Ping"瞬时计数（Ping 开始 +1，结束 -1）。
func (c *Collector) AddPingsActive(delta int64) { atomic.AddInt64(&c.pingsActive, delta) }

// IncrPing 记录一次 Ping 完成；timeout=true 时额外累计超时数。
func (c *Collector) IncrPing(timeout bool) {
	atomic.AddUint64(&c.pingsTotal, 1)
	if timeout {
		atomic.AddUint64(&c.pingsTimeout, 1)
	}
}

// RecordRebuild 记录一次引擎重建及其耗时（含 Clone+build）。
func (c *Collector) RecordRebuild(dur time.Duration) {
	ns := uint64(dur.Nanoseconds())
	atomic.AddUint64(&c.rebuilds, 1)
	atomic.AddUint64(&c.rebuildBusyNs, ns)
	atomic.StoreUint64(&c.lastRebuildNs, ns)

	c.windowMu.Lock()
	c.rebuildWindow = append(c.rebuildWindow, time.Now().UnixMilli())
	c.windowMu.Unlock()
}

// IncrTopoMutation 记录一次触发 syncEngine 的拓扑变更。
func (c *Collector) IncrTopoMutation() { atomic.AddUint64(&c.topoMutations, 1) }

// NotePending 记录引擎待处理事件队列的当前深度（取历史最大值）。
func (c *Collector) NotePending(depth int) {
	for {
		old := atomic.LoadInt64(&c.pendingMax)
		if int64(depth) <= old {
			return
		}
		if atomic.CompareAndSwapInt64(&c.pendingMax, old, int64(depth)) {
			return
		}
	}
}

// ---- 包级便捷函数：委托给 Default 单例，供热路径直接调用 ----

// Start 启动后台采样（幂等）。
func Start() { Default.Start() }

// IncrPacket 记录一个被引擎处理的包。
func IncrPacket() { Default.IncrPacket() }

// AddPingsActive 改变在途 Ping 瞬时计数。
func AddPingsActive(delta int64) { Default.AddPingsActive(delta) }

// IncrPing 记录一次 Ping 完成；timeout=true 额外累计超时数。
func IncrPing(timeout bool) { Default.IncrPing(timeout) }

// RecordRebuild 记录一次引擎重建及其耗时。
func RecordRebuild(dur time.Duration) { Default.RecordRebuild(dur) }

// IncrTopoMutation 记录一次触发 syncEngine 的拓扑变更。
func IncrTopoMutation() { Default.IncrTopoMutation() }

// NotePending 记录引擎待处理事件队列的当前深度。
func NotePending(depth int) { Default.NotePending(depth) }

// ---- 后台采样 ----

func (c *Collector) sampleLoop() {
	prevCPU, prevWall := processCPU()
	var prevPause uint64
	// 先读一次 MemStats 拿到初始 PauseTotalNs 基准
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)
	prevPause = m0.PauseTotalNs

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for range ticker.C {
		curCPU, curWall := processCPU()

		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		dWall := curWall - prevWall
		var cpuPct, gcPct float64
		if dWall > 0 {
			// 归一到「整机 CPU 占用百分比」：进程 CPU 时间 / 墙钟时间 / 核数，
			// 使读数落在直观的 0-100%（单核跑满 = 100/核数，而非 100）。
			cpuPct = float64(curCPU-prevCPU) / float64(dWall) / float64(runtime.NumCPU()) * 100
			gcPct = float64(m.PauseTotalNs-prevPause) / float64(dWall) * 100
		}
		if cpuPct < 0 {
			cpuPct = 0
		}
		// 归一到整机后上限为 100，钳制异常值（时钟回拨等）。
		if cpuPct > 100 {
			cpuPct = 100
		}
		if gcPct < 0 {
			gcPct = 0
		}

		c.memMu.Lock()
		c.cpuPercent = cpuPct
		c.goroutines = runtime.NumGoroutine()
		c.heapAlloc = m.HeapAlloc
		c.heapSys = m.HeapSys
		c.heapObjects = m.HeapObjects
		c.numGC = m.NumGC
		c.gcCPUPercent = gcPct
		c.pauseTotalNs = m.PauseTotalNs
		c.memMu.Unlock()

		prevCPU, prevWall = curCPU, curWall
		prevPause = m.PauseTotalNs
	}
}

// ---- 快照与诊断 ----

// Snapshot 返回当前时刻的完整快照（含诊断建议）。并发安全。
func (c *Collector) Snapshot() Snapshot {
	c.memMu.Lock()
	s := Snapshot{
		Timestamp:    time.Now(),
		UptimeSec:    time.Since(c.startTime).Seconds(),
		CPUPercent:   c.cpuPercent,
		Goroutines:   c.goroutines,
		HeapAllocMB:  float64(c.heapAlloc) / (1 << 20),
		HeapSysMB:    float64(c.heapSys) / (1 << 20),
		HeapObjects:  c.heapObjects,
		NumGC:        c.numGC,
		GCCPUPercent: c.gcCPUPercent,
		PauseTotalMs: float64(c.pauseTotalNs) / 1e6,
	}
	c.memMu.Unlock()

	s.PacketsProcessed = atomic.LoadUint64(&c.packets)
	s.PingsActive = atomic.LoadInt64(&c.pingsActive)
	s.PingsTotal = atomic.LoadUint64(&c.pingsTotal)
	s.PingsTimeout = atomic.LoadUint64(&c.pingsTimeout)
	s.RebuildsTotal = atomic.LoadUint64(&c.rebuilds)
	s.TopoMutations = atomic.LoadUint64(&c.topoMutations)
	s.RebuildLastMs = float64(atomic.LoadUint64(&c.lastRebuildNs)) / 1e6
	s.RebuildBusyMs = float64(atomic.LoadUint64(&c.rebuildBusyNs)) / 1e6
	s.PendingEventMax = int(atomic.LoadInt64(&c.pendingMax))

	// 统计近 10s 的 Rebuild 次数（修剪过期时间戳）
	cutoff := time.Now().UnixMilli() - 10_000
	c.windowMu.Lock()
	kept := c.rebuildWindow[:0]
	for _, ts := range c.rebuildWindow {
		if ts >= cutoff {
			kept = append(kept, ts)
		}
	}
	c.rebuildWindow = kept
	s.RebuildsLast10s = uint64(len(kept))
	c.windowMu.Unlock()

	s.Diagnosis = c.diagnose(s)
	return s
}

// diagnose 基于快照给出"为什么现在资源高"的归因。仅输出最相关的几条，
// 全部未命中时返回空切片（表示无明显异常路径）。
func (c *Collector) diagnose(s Snapshot) []string {
	var d []string
	if s.RebuildsLast10s >= 3 {
		d = append(d, fmt.Sprintf(
			"拓扑编辑突发：近 10s 发生 %d 次引擎重建(Rebuild)，每次含全量 Clone+build，"+
				"且同步执行在 API 处理协程上 —— 这是 CPU/GC 尖峰的主因（R1）。建议对 syncEngine 做去抖合并",
			s.RebuildsLast10s))
	}
	if s.PingsActive >= 5 {
		d = append(d, fmt.Sprintf(
			"Ping/流量突发：当前 %d 个并发 Ping 在途，单包触发可达性计算（isDirectlyConnectedTo 为 O(V·E²)）"+
				"—— CPU 尖峰来源（R2）", s.PingsActive))
	}
	if s.GCCPUPercent >= 15 {
		d = append(d, fmt.Sprintf(
			"GC 压力高(%.1f%% CPU)：短生命周期对象大量分配，多为 Rebuild 产生的旧拓扑快照被回收（R5）",
			s.GCCPUPercent))
	}
	if s.CPUPercent >= 8 && s.RebuildsLast10s < 3 && s.PingsActive < 5 {
		d = append(d, fmt.Sprintf(
			"CPU 偏高(%.1f%%)但业务计数低：ns-x 1ms 周期轮询常驻（事件循环永不休眠）构成恒定基线 + 可能的前端轮询/SSE 拉取（R4）。"+
				"若需压低基线，可将轮询周期从 1ms 调到 5-10ms",
			s.CPUPercent))
	}
	return d
}
