package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	_ "net/http/pprof"

	"ensp-lab"
	"ensp-lab/internal/api"
	"ensp-lab/internal/buildinfo"
	"ensp-lab/internal/logging"
	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// store 前置声明，供下方顶层 defer recover 引用（其声明早于赋值点）。
	var store storage.Storage

	// 顶层 panic 兜底：任何未被 goroutine 局部 recover 捕获的 panic 都会在此被捕获，
	// 先尽力把内存态拓扑落盘，再以非零退出码结束，避免「崩溃即丢状态」。
	// 注意：此处只依赖包级 logging.* 与 store 变量（store 在下方赋值，未赋值则为 nil）；
	// 不使用 main 内声明的 logger 变量，避免其在 panic 早于赋值时被解引用。
	defer func() {
		if r := recover(); r != nil {
			logging.Error("FATAL panic, attempting to save state before exit",
				zap.Any("panic", r),
				zap.Stack("stack"),
			)
			if store != nil {
				if err := store.Flush(); err != nil {
					logging.Error("state save on panic failed", zap.Error(err))
				}
			}
			logging.Sync()
			os.Exit(2)
		}
	}()

	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	logFormat := flag.String("log-format", "console", "Log format: console, json")
	port := flag.String("port", envOrDefault("PORT", ""), "Server port (overrides PORT env var)")
	bind := flag.String("bind", envOrDefault("BIND_ADDR", "127.0.0.1"),
		"Bind address (default 127.0.0.1 = localhost only). Set to 0.0.0.0 to expose on all interfaces (not recommended without a trusted network / reverse proxy).")
	dataDir := flag.String("data-dir", envOrDefault("DATA_DIR", ""), "Storage directory (overrides DATA_DIR env var)")
	demoVXLAN := flag.Bool("demo-vxlan", false, "Create and start a VXLAN Spine-Leaf demo topology on startup")
	autosaveInterval := flag.Duration("autosave-interval", 5*time.Second,
		"后台自动保存间隔（落盘内存态拓扑以兜底崩溃/掉电）；0 关闭")
	flag.Parse()

	if *port == "" {
		*port = "8080"
	}

	// 安全（F6 纵深）：生产二进制强制 release 模式，禁用 GIN_MODE=debug / ENS_DEBUG
	// 回退，避免调试模式下 gin.Recovery() 向客户端泄露完整堆栈（含内部路径/变量）。
	gin.SetMode(gin.ReleaseMode)

	logging.SetLogLevel(*logLevel)
	logging.SetLogFormat(*logFormat)

	logger := logging.InitLogger()
	defer logging.Sync()

	// 陈旧产物自检：只跑一次，结论缓存在 buildinfo.Stale，供启动日志与 /version 复用。
	// 它不会阻塞或中断启动——最坏情况只是维持 Stale=false。
	buildinfo.Init()

	store = storage.NewFileStorage(*dataDir)
	topos, err := store.ListTopologies()
	if err != nil {
		logger.Fatal("Failed to list topologies", zap.Error(err), zap.String("data_dir", store.StorageDir()))
	}

	switch {
	case *demoVXLAN:
		vxlanTopo := api.CreateVXLANTopology()
		if err := store.CreateTopology(vxlanTopo); err != nil {
			logger.Fatal("Failed to create VXLAN demo topology", zap.Error(err))
		}
		logger.Info("Created VXLAN Spine-Leaf demo topology", zap.String("id", vxlanTopo.ID), zap.String("name", vxlanTopo.Name))
	case len(topos) == 0:
		if err := store.CreateTopology(topology.NewTopology("default", "Default Topology")); err != nil {
			logger.Fatal("Failed to create default topology", zap.Error(err))
		}
		logger.Info("Created default topology", zap.String("id", "default"))
	default:
		logger.Info("Loaded existing topologies", zap.Int("count", len(topos)))
		for _, t := range topos {
			logger.Info("Topology loaded", zap.String("id", t.ID), zap.String("name", t.Name))
		}
	}

	r := api.NewRouter(store, ensp.StaticFS, api.ServerConfig{BindAddr: *bind, Port: *port})

	// 启动后台定时自动保存：兜底崩溃/掉电，周期把内存态拓扑落盘。
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	if *autosaveInterval > 0 {
		store.StartAutoSave(appCtx, *autosaveInterval)
	}

	logger.Info("eNSP Web Lab Server starting",
		zap.String("bind", *bind),
		zap.String("port", ":"+*port),
		zap.String("platform", runtime.GOOS),
		zap.String("storage_dir", store.StorageDir()),
		zap.String("version", buildinfo.Version),
		zap.String("build_time", buildinfo.BuildTime),
		zap.String("commit", buildinfo.Commit),
	)

	// 陈旧产物只告警、不阻断：拦下「跑的是旧 exe，源码早修好了」这类幽灵 bug，
	// 但绝不能因为自检把服务拦在门外（部署环境没有 git 是常态）。
	if buildinfo.Stale {
		logger.Warn("构建产物可能已陈旧：源码已变更，请执行 make build（Windows: ./build.ps1）重新构建",
			zap.String("reason", buildinfo.StaleReason),
			zap.String("build_time", buildinfo.BuildTime),
			zap.String("commit", buildinfo.Commit),
		)
	}
	logger.Info("Engine mode: auto-selected by sim.NewEngine",
		zap.String("linux_mode", "gont"),
		zap.String("other_mode", "ns-x"),
	)
	logger.Info("API endpoints available",
		zap.String("status", "/api/sim/status"),
		zap.String("ui", "/"),
	)

	// 用 http.Server 包装 gin 引擎，以支持优雅关停（Shutdown）。
	// 默认绑定 127.0.0.1（仅本地），避免误将服务暴露到网络（V-01 修复）。
	srv := &http.Server{
		Addr:    *bind + ":" + *port,
		Handler: r,
	}

	// 在独立 goroutine 中启动 HTTP 服务，并为其加 panic 兜底（gin.Recovery 已兜住
	// 处理器级 panic，这里再兜住 ListenAndServe 自身异常），避免服务崩溃无声。
	serverErr := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.Error("HTTP server panic, saving state before exit",
					zap.Any("panic", rec), zap.Stack("stack"))
				if err := store.Flush(); err != nil {
					logging.Error("state save on server panic failed", zap.Error(err))
				}
				serverErr <- fmt.Errorf("http server panic: %v", rec)
			}
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// 信号驱动的优雅退出：收到 SIGINT/SIGTERM 时先落盘再停服务。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		logger.Info("Received shutdown signal, saving state", zap.String("signal", s.String()))
	case err := <-serverErr:
		logger.Error("HTTP server stopped unexpectedly", zap.Error(err))
	}

	// 退出前统一落盘（即使是非信号崩溃，也尽量保存最新内存态）。
	if err := store.Flush(); err != nil {
		logger.Error("state save on shutdown failed", zap.Error(err))
	}
	appCancel() // 停止自动保存循环

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", zap.Error(err))
	}
	logger.Info("eNSP Web Lab Server stopped")
}
