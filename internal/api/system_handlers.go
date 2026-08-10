// system_handlers.go 提供基础系统信息端点。
//
//   - GET /health  返回服务健康状态、平台与当前 engine 数量
//   - GET /version 返回构建版本、构建时间、提交、陈旧标记与状态
//
// 这两个端点是排查服务是否正常启动的最快入口。
// /version 的构建元信息全部来自 internal/buildinfo（ldflags 唯一注入点）；
// 若响应中 stale=true，说明运行的二进制可能落后于源码，应重新构建。
package api

import (
	"net/http"
	"runtime"
	"time"

	"ensp-lab/internal/buildinfo"
	"ensp-lab/internal/metrics"
	"ensp-lab/internal/sim"

	"github.com/gin-gonic/gin"
)

func (r *Router) health(c *gin.Context) {
	r.engMu.Lock()
	engineCount := len(r.engines)
	r.engMu.Unlock()

	// 注入实时资源读数，便于一眼判断服务当下是否处于资源高压。
	s := metrics.Default.Snapshot()
	status := gin.H{
		"status":        "ok",
		"platform":      runtime.GOOS,
		"engine_count":  engineCount,
		"goroutines":    s.Goroutines,
		"cpu_percent":   round1(s.CPUPercent),
		"heap_alloc_mb": round1(s.HeapAllocMB),
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, status)
}

func (r *Router) version(c *gin.Context) {
	r.engMu.Lock()
	engineCount := len(r.engines)
	r.engMu.Unlock()

	s := metrics.Default.Snapshot()
	info := gin.H{
		// 构建元信息统一取自 buildinfo 包（ldflags 唯一注入点）。
		// stale=true 表示当前二进制可能落后于源码，详见 stale_reason。
		"version":       buildinfo.Version,
		"build_time":    buildinfo.BuildTime,
		"commit":        buildinfo.Commit,
		"dirty":         buildinfo.IsDirtyBuild(),
		"stale":         buildinfo.Stale,
		"stale_reason":  buildinfo.StaleReason,
		"status":        "ok",
		"platform":      runtime.GOOS,
		"engine_count":  engineCount,
		"goroutines":    s.Goroutines,
		"cpu_percent":   round1(s.CPUPercent),
		"heap_alloc_mb": round1(s.HeapAllocMB),
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, info)
}

// metrics 返回进程资源使用率与引擎活动计数的完整快照（含尖峰归因诊断）。
//
//	GET /api/system/metrics
//
// 这是"监听资源使用率 / 为什么某一刻飙高"的核心端点：轮询它即可实时观测
// CPU%、goroutine、heap、GC，以及 rebuilds_last_10s / pings_active 等业务计数；
// 响应中的 diagnosis 字段会直接给出最可能的尖峰成因（R1–R5）。
func (r *Router) metrics(c *gin.Context) {
	c.JSON(http.StatusOK, metrics.Default.Snapshot())
}

// systemStatus 返回后端全局状态，含引擎能力级别（full/lite）。
//
//	GET /api/system/status
//
// engine_mode 由 build tag 决定：启用 gont 的 Linux 构建为 "full"（真实协议栈），
// 其余（含 Windows 的 ns-x 仿真子集）为 "lite"。前端据此展示"真实引擎/仿真子集"
// 标签，并据实在 lite 模式下提示"部分结果基于拓扑模拟"。
func (r *Router) systemStatus(c *gin.Context) {
	r.engMu.Lock()
	engineCount := len(r.engines)
	r.engMu.Unlock()

	s := metrics.Default.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"engine_mode":   sim.EngineModeName(),
		"platform":      runtime.GOOS,
		"engine_count":  engineCount,
		"goroutines":    s.Goroutines,
		"cpu_percent":   round1(s.CPUPercent),
		"heap_alloc_mb": round1(s.HeapAllocMB),
		"timestamp":     time.Now().Format(time.RFC3339),
		// stale 与 /version 同源，供前端在状态栏直接打「产物已陈旧」角标，
		// 无需再单独请求 /version。
		"version":      buildinfo.Version,
		"stale":        buildinfo.Stale,
		"stale_reason": buildinfo.StaleReason,
	})
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
