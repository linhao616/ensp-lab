# PROJECT SNAPSHOT - eNSP Network Simulator

**Last Updated:** 2026-07-22 16:46:00
**Status:** ✅ API handler 拆分重构已完成（`internal/api` 编译通过，根目录 `ensp-lab.exe` 为最新构建）+ 文件持久化 + 结构化日志 + CORS + VXLAN Spine-Leaf + 拓扑标注 + 20+ 协议建模 + SSE 事件流 + FRR(Linux) 集成 + 调试/分析工具链已就绪

> ✅ **构建状态**：`go build ./internal/api/...` 与 `go build -a -o ensp-lab.exe ./cmd/server` 均通过（EXIT=0），`router.go` 仅保留 `NewRouter()` 路由注册，前端 `frontend/dist` 已嵌入二进制；根目录 `ensp-lab.exe` 为最新构建。

---

## 1. Project Directory Tree

```
D:\Projects\Go\src\ensp-lab
├── .trae\
│   └── specs\
│       └── frr-router\
│           ├── checklist.md
│           ├── spec.md
│           └── tasks.md
├── archive\
│   ├── engine\
│   │   ├── SimpleSwitch.go
│   │   ├── SimpleSwitch_other.go
│   │   ├── SimpleSwitch_test.go
│   │   └── SimpleSwitch_test_other.go
│   ├── factory\
│   │   ├── factory.go
│   │   └── factory_test.go
│   ├── gateway\
│   │   └── gateway.go
│   └── simulator\
│       ├── device.go
│       ├── engine.go
│       ├── packet.go
│       └── scheduler.go
├── cmd\
│   └── server\
│       └── main.go
├── frontend\
│   ├── src\
│   │   ├── components\
│   │   │   ├── AnnotationLayer.tsx
│   │   │   ├── CliTerminal.tsx
│   │   │   ├── DescriptionBox.tsx
│   │   │   ├── PacketAnimator.tsx
│   │   │   └── TopologyCanvas.tsx
│   │   ├── hooks\
│   │   │   └── useSimEvents.ts
│   │   ├── api.ts
│   │   ├── App.tsx
│   │   ├── main.tsx
│   │   ├── styles.css
│   │   ├── types.ts
│   │   ├── data\
│   │   │   └── vxlanTemplate.ts
│   │   └── vite-env.d.ts
│   ├── .gitignore
│   ├── index.html
│   ├── package-lock.json
│   ├── package.json
│   ├── tsconfig.json
│   ├── tsconfig.node.json
│   ├── tsconfig.tsbuildinfo
│   └── vite.config.ts
├── internal\
│   ├── api\
│   │   ├── router.go            # 路由注册（NewRouter）
│   │   ├── topology_handlers.go
│   │   ├── device_handlers.go
│   │   ├── link_handlers.go
│   │   ├── cli_handlers.go
│   │   ├── annotation_handlers.go
│   │   ├── system_handlers.go
│   │   ├── vxlan_topo.go
│   │   ├── api_types.go
│   │   └── integration_test*.go
│   ├── cli\
│   │   ├── parser.go
│   │   ├── capabilities.go
│   │   ├── host.go
│   │   ├── state.go
│   │   └── tools.go
│   ├── logging\
│   │   └── logger.go
│   ├── protocol\
│   │   ├── arp.go, bgp.go, dhcp.go, dns.go, firewall.go
│   │   ├── ftp.go, handler.go, http.go, icmp.go, ipv6.go
│   │   ├── mpls.go, ospf.go, ppp.go, pppoe.go, protocol.go
│   │   ├── rip.go, smtp.go, stp.go, tcp.go, tls.go, udp.go
│   │   └── vxlan.go
│   ├── router\
│   │   ├── router.go
│   │   ├── router_other.go
│   │   ├── router_test.go
│   │   └── router_test_other.go
│   ├── sim\
│   │   ├── doc.go
│   │   ├── engine.go
│   │   ├── engine_nsx.go
│   │   ├── engine_nsx_test.go
│   │   ├── engine_stub.go
│   │   ├── engine_test.go
│   │   ├── gont_emulator.go
│   │   ├── gont_emulator_other.go
│   │   ├── gont_emulator_test.go
│   │   ├── platform.go
│   │   └── types.go
│   ├── storage\
│   │   ├── file_storage.go
│   │   └── memory.go
│   ├── testutil\
│   │   └── testutil.go
│   └── topology\
│       ├── model.go
│       ├── graph.go
│       ├── manager.go
│       └── manager_description_test.go
├── data\
│   └── vxlan-spine-leaf.json
├── docs\
│   └── ensp-lab_manual.md
├── embed.go
├── go.mod
├── go.sum
├── Makefile
├── README.md
├── PROJECT_SNAPSHOT.md
├── create_cli_parser.ps1
└── ensp-lab.exe~
```

---

## 2. go.mod

```go
module ensp-lab

go 1.26.3

require (
	github.com/bytedance/ns-x/v2 v2.4.5
	github.com/gin-gonic/gin v1.12.0
	github.com/stretchr/testify v1.11.1
	github.com/stv0g/gont/v2 v2.3.6
)

require (
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cilium/ebpf v0.10.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fxamacker/cbor/v2 v2.4.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/cors v1.7.7 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/go-delve/delve v1.20.2 // indirect
	github.com/go-ping/ping v1.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-dap v0.8.0 // indirect
	github.com/google/nftables v0.1.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/gopacket/gopacket v1.1.1-0.20230504215803-44b8a6a7a299 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mdlayher/netlink v1.7.2 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/sirupsen/logrus v1.9.2 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	github.com/vishvananda/netlink v1.2.1-beta.2.0.20221214185949-378a404a26f0 // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	golang.org/x/arch v0.23.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20230522175609-2e198f4a06a1 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	kernel.org/pub/linux/libs/security/libcap/cap v1.2.69 // indirect
	kernel.org/pub/linux/libs/security/libcap/psx v1.2.69 // indirect
)
```

---

## 3. cmd/server/main.go

```go
package main

import (
	"flag"
	"os"
	"runtime"

	_ "net/http/pprof"

	"ensp-lab"
	"ensp-lab/internal/api"
	"ensp-lab/internal/logging"
	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"go.uber.org/zap"
)

var version = "dev"
var buildTime = "unknown"

func main() {
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	logFormat := flag.String("log-format", "console", "Log format: console, json")
	port := flag.String("port", "", "Server port (overrides PORT env var)")
	dataDir := flag.String("data-dir", "", "Storage directory (overrides DATA_DIR env var)")
	demoVXLAN := flag.Bool("demo-vxlan", false, "Create and start a VXLAN Spine-Leaf demo topology on startup")
	flag.Parse()

	if *port == "" {
		if envPort := os.Getenv("PORT"); envPort != "" {
			*port = envPort
		} else {
			*port = "8080"
		}
	}

	if *dataDir == "" {
		if envDir := os.Getenv("DATA_DIR"); envDir != "" {
			*dataDir = envDir
		}
	}

	logging.SetLogLevel(*logLevel)
	logging.SetLogFormat(*logFormat)

	logger := logging.InitLogger()
	defer logging.Sync()

	store := storage.NewFileStorage(*dataDir)
	topos, _ := store.ListTopologies()
	if len(topos) == 0 {
		store.CreateTopology(topology.NewTopology("default", "Default Topology"))
		logger.Info("Created default topology", zap.String("id", "default"))
	} else {
		logger.Info("Loaded existing topologies", zap.Int("count", len(topos)))
		for _, t := range topos {
			logger.Info("Topology loaded", zap.String("id", t.ID), zap.String("name", t.Name))
		}
	}

	r := api.NewRouter(store, ensp.StaticFS)

	logger.Info("eNSP Web Lab Server starting",
		zap.String("port", ":"+*port),
		zap.String("platform", runtime.GOOS),
		zap.String("storage_dir", store.StorageDir()),
		zap.String("version", version),
		zap.String("build_time", buildTime),
	)
	logger.Info("Engine mode: auto-selected by sim.NewEngine",
		zap.String("linux_mode", "gont"),
		zap.String("other_mode", "ns-x"),
	)
	logger.Info("API endpoints available",
		zap.String("status", "/api/sim/status"),
		zap.String("ui", "/"),
	)

	if err := r.Run(":" + *port); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
}
```

---

## 4. Core Engine Architecture

### 4.1 Engine Interface (internal/sim/engine.go)

```go
type Engine interface {
	Mode() string
	Start()
	Stop()
	SendPacket(pkt *Packet, fromDeviceID, ifaceName string)
	Ping(srcDeviceID, dstIP string) (*PingResult, error)
	Rebuild(topo *topology.Topology) error
	AddPacketListener(listener PacketListener)
	Events() <-chan *PacketEvent
	CapturePCAP(ctx context.Context, deviceID, ifaceName string, pktChan chan<- []byte) (func(), error)
	Run(ctx context.Context) error
	QueueDepth() int
}
```

### 4.2 Factory Function (internal/sim/platform.go)

```go
func canRunGont() bool {
    if runtime.GOOS != "linux" {
        return false
    }
    if os.Geteuid() != 0 {
        log.Printf("sim: gont requires root privileges (CAP_NET_ADMIN), current uid=%d", os.Geteuid())
        return false
    }
    _, err := exec.LookPath("ovs-vsctl")
    if err != nil {
        log.Printf("sim: gont requires Open vSwitch, ovs-vsctl not found: %v", err)
        return false
    }
    return true
}

func NewEngine(topo *topology.Topology) (Engine, error) {
    if runtime.GOOS == "linux" && canRunGont() {
        eng, err := NewGontEngine(topo)
        if err == nil {
            log.Printf("sim: engine mode=gont (real namespace traffic on %s)", runtime.GOOS)
            return eng, nil
        }
        log.Printf("sim: gont initialization failed (%v), falling back to ns-x", err)
    } else {
        if runtime.GOOS == "linux" {
            log.Printf("sim: running in simulation-only mode (gont unavailable due to permissions or missing OVS)")
        } else {
            log.Printf("sim: running in simulation-only mode (gont unavailable on %s)", runtime.GOOS)
        }
    }
    eng, err := NewNSxEngine(topo)
    if err != nil {
        return nil, err
    }
    log.Printf("sim: engine mode=ns-x (event-driven simulation)")
    return eng, nil
}
```

### 4.3 NSx Engine (internal/sim/engine_nsx.go) - Cross-platform

Key implementation details:
- Uses `github.com/bytedance/ns-x/v2` for event-driven simulation
- Maps topology.Device to ns-x EndpointNode
- Implements ICMP echo handler for Ping support
- Runs event loop in background goroutine with 1ms periodic poller
- Uses `pendingEvents` channel for asynchronous packet injection
- `Ping()` waits for echo reply with 5-second timeout
- `Stop()` properly cleans up all channels and pending operations
- `QueueDepth()` returns current pending event count
- **VXLAN Tunnel Support**: BridgeNode for multi-connection devices, path tracking to prevent infinite loops, VLAN isolation check in Transfer() method for VTEP/Switch/Server devices

### 4.5 VXLAN Spine-Leaf Topology (internal/api/vxlan_topo.go)

Key implementation details:
- Creates Spine-Leaf topology with 2 Spine switches, 3 Leaf switches (VTEP), 3 servers, and 4 VMs
- VXLAN tunnels configured with VNI 5000 between Leaf switches
- Head-end replication lists configured for each VTEP
- BD 10 mapped to VNI 5000 for Layer 2 extension across VTEPs
- Supports inter-VTEP communication for VMs in the same VNI

### 4.4 Gont Engine (internal/sim/gont_emulator.go) - Linux only

Key implementation details:
- Creates real Linux network namespaces via gont
- Supports FRRouting (FRR) for router devices
- Uses raw sockets for packet injection
- Requires CAP_NET_ADMIN permission
- `QueueDepth()` returns 0 (real network)

---

## 5. API Endpoints Summary

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | /api/topologies | listTopologies | 获取所有拓扑列表 |
| GET | /api/topologies/:id | getTopology | 获取指定拓扑详情（含 annotations） |
| POST | /api/topologies | createTopology | 创建拓扑（完整） |
| PUT | /api/topologies/:id | updateTopology | 更新拓扑 |
| DELETE | /api/topologies/:id | deleteTopology | 删除拓扑 |
| POST | /api/topology | createTopologySimple | 创建简单拓扑（含链路） |
| GET | /api/topology/:id/ping | pingTopology | Ping 测试（首次调用自动启动引擎） |
| GET | /api/topology/:id/pcap | streamPCAP | 实时抓包数据流（SSE） |
| GET | /api/topology/:id/vxlan-status | vxlanStatus | VXLAN 隧道状态 |
| POST | /api/topologies/:id/simulate-packet | simulatePacket | 包路径模拟（BFS） |
| POST | /api/topologies/:id/devices | addDevice | 添加设备 |
| PUT | /api/topologies/:id/devices/:deviceId | updateDevice | 更新设备 |
| DELETE | /api/topologies/:id/devices/:deviceId | deleteDevice | 删除设备 |
| POST | /api/topologies/:id/devices/:deviceId/power | powerDevice | 电源控制 |
| POST | /api/topologies/:id/devices/:deviceId/cli | executeCLI | 执行 VRP CLI 命令 |
| GET | /api/devices/types | getDeviceTypes | 获取支持的设备类型列表 |
| POST | /api/topologies/:id/annotations | addAnnotation | 新增标注 |
| PUT | /api/topologies/:id/annotations/:annotationId | updateAnnotation | 更新标注 |
| DELETE | /api/topologies/:id/annotations/:annotationId | deleteAnnotation | 删除标注 |
| POST | /api/topology/:id/router/:device/ospf | applyOSPFConfig | 应用 OSPF 配置（FRR/Linux） |
| POST | /api/topology/:id/router/:device/bgp | applyBGPConfig | 应用 BGP 配置（FRR/Linux） |
| GET | /api/topology/:id/router/:device/routes | getRoutes | 获取路由表（FRR/Linux） |
| GET | /api/sim/events | streamSimEvents | SSE 事件流 |
| GET | /api/sim/status | getSimStatus | 获取引擎状态 |
| GET | /api/sim/queue-depth | getQueueDepth | 获取事件队列深度 |
| GET | /health | health | 健康检查 |
| GET | /version | version | 版本与构建信息 |
| GET | / | webIndexHTML | 前端入口（embed） |

---

## 6. Recent Terminal Logs

### 6.1 go build ./...

```
# 所有包均可正常构建（已验证 go build -a 通过）；根目录 ensp-lab.exe 为最新构建（含嵌入前端）
```

### 6.2 go test ./...（当前包列表，示意）

> 注：以下为当前实际包结构。`internal/engine`、`internal/factory`、`internal/simulator` 已归档至 `archive/`；`pkg/*` 已移除。`internal/api` 已拆分完成并**可正常编译**，集成测试（`//go:build integration`）可通过 `go test -tags integration` 运行。

```
?       ensp-lab                        [no test files]
?       ensp-lab/cmd/server             [no test files]
?       ensp-lab/internal/api           [no test files]
?       ensp-lab/internal/cli           [no test files]
?       ensp-lab/internal/logging       [no test files]
ok      ensp-lab/internal/protocol      (cached)
?       ensp-lab/internal/router        [no test files]
ok      ensp-lab/internal/sim           (cached)
?       ensp-lab/internal/storage       [no test files]
?       ensp-lab/internal/testutil      [no test files]
ok      ensp-lab/internal/topology      (cached)
```

### 6.3 End-to-End Test (Windows - ns-x mode)

```
POST /api/topology → {"id":"d45f51dcfca51bd8","name":"TestTopo-E2E","device_count":2,"link_count":1}
GET /api/topology/d45f51dcfca51bd8/ping?src=h1&dst=h2 → {"src":"h1","dst":"h2","dst_ip":"192.168.1.2","sent":1,"received":1,"lost":0,"details":["ICMP echo reply received"]}
DELETE /api/topologies/d45f51dcfca51bd8 → 204 No Content (engine properly stopped and cleaned up)
GET / → 200 OK (React frontend loaded from embedded static files)
GET /health → {"status":"ok","platform":"windows","engine_count":0,"timestamp":"2026-07-19T21:30:00Z"}
```

**Key Verification:**
- ✅ Ping returns `received: 1` - ICMP echo reply working correctly
- ✅ Engine mode auto-detection working (ns-x on Windows)
- ✅ Topology deletion triggers engine cleanup
- ✅ Frontend static files served from embedded FS (no external directory required)
- ✅ Health check endpoint working
- ✅ File persistence: topologies survive server restart

### 6.4 Engine Mode Detection (Linux)

```
// With sudo + OVS installed:
sim: engine mode=gont (real namespace traffic on linux)

// Without sudo or OVS:
sim: gont requires root privileges (CAP_NET_ADMIN), current uid=1000
sim: running in simulation-only mode (gont unavailable due to permissions or missing OVS)
sim: engine mode=ns-x (event-driven simulation)
```

---

## 7. Network Environment Check

```
OS: Windows_NT
Version: Microsoft Windows NT 10.0.26200.0
```

OVS: Not installed (Windows environment - using ns-x simulation)

---

## 8. Architecture Summary

```
┌─────────────────────────────────────────────────────────────────┐
│                        Frontend (React)                        │
│              http://localhost:8080 → embed.FS /frontend/dist        │
└──────────────────────────────┬──────────────────────────────────┘
                               │ HTTP/REST (CORS enabled)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                    API Layer (Gin Router)                       │
│              internal/api/router.go                             │
│  Routes: /api/topologies, /api/topology/:id/ping, /api/devices/types, /api/sim/*, /version │
└──────────────────────────────┬──────────────────────────────────┘
                               │ sim.Engine interface
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Engine Abstraction Layer                     │
│              internal/sim/engine.go (Interface)                 │
│              internal/sim/platform.go (Factory)                 │
└──────────────────────────────┬──────────────────────────────────┘
                               │ runtime.GOOS
              ┌────────────────┴────────────────┐
              ▼                                 ▼
┌─────────────────────┐         ┌─────────────────────────────┐
│    GontEngine       │         │          NSxEngine          │
│  (Linux only)       │         │   (Cross-platform)          │
│  • Real netns       │         │   • Pure Go simulation     │
│  • OVS switches     │         │   • Event-driven           │
│  • FRR routing      │         │   • ICMP echo support      │
│  • Raw sockets      │         │   • No OS dependencies     │
└─────────────────────┘         └─────────────────────────────┘
```

---

## 9. Production-Ready Features

| Feature | Implementation | Description |
|---------|----------------|-------------|
| **Static file embedding** | embed.go + router.go | frontend/dist compiled into binary via `//go:embed frontend/dist` |
| **CORS support** | router.go + gin-contrib/cors | Allow localhost:5173 and localhost:8080 |
| **Structured logging** | internal/logging/logger.go | zap-based, supports console/json format, 4 log levels |
| **Configuration** | cmd/server/main.go | -port, -data-dir, -log-level, -log-format + env vars |
| **File persistence** | internal/storage/file_storage.go | Topologies saved as JSON, loaded on startup |
| **Concurrent safety** | internal/storage/file_storage.go, model.go | sync.RWMutex on all map operations |
| **Health check** | router.go | GET /health returns status, platform, engine_count |
| **Queue depth monitoring** | engine.go, engine_nsx.go | Engine.QueueDepth() + API endpoint |
| **浮动窗口 CLI/配置** | FloatWindow.tsx + DeviceDetail.tsx + App.tsx | 设备 CLI/配置改为可拖动浮动小窗，多窗口 + 任务栏 + localStorage 持久化 |
| **Ping 测试面板** | PingPanel.tsx + api.ts + router.go | 任意源/目标、连续 Ping、结果历史、拓扑高亮联动 |

---

## 10. Key Improvements Since Last Snapshot

| Improvement | File | Description |
|-------------|------|-------------|
| Static file embedding | embed.go | `//go:embed frontend/dist` embeds frontend into binary |
| CORS middleware | router.go | gin-contrib/cors allows cross-origin requests |
| Structured logging | internal/logging/logger.go | zap-based logging with level/format config |
| Command line parameters | main.go | -port, -data-dir, -log-level, -log-format |
| Environment variable support | main.go | PORT, DATA_DIR override defaults |
| FileStorage implementation | internal/storage/file_storage.go | JSON file persistence for topologies |
| Topology lock safety | model.go | sync.RWMutex added to Topology struct |
| Thread-safe access methods | model.go | GetDevice, GetLinks, AddDevice with lock protection |
| Health check endpoint | router.go | GET /health returns status, platform, engine_count |
| Queue depth monitoring | engine.go, engine_nsx.go, router.go | Engine.QueueDepth() + GET /api/sim/queue-depth |
| Frontend Loading states | App.tsx | Loading indicators for create topology / Ping |
| Frontend Error alerts | App.tsx | Toast notifications for API errors (4xx/5xx) |
| Frontend Ping results | App.tsx | RTT display and Ping result panel |
| README curl examples | README.md | Complete workflow: create → ping（引擎懒启动）→ delete |
| Link creation support | router.go:913-961 | Added `links` field to createTopologySimple |
| Auto IP assignment | router.go:948-966 | PC devices get 192.168.1.x/24, interfaces set to "up" |
| Ping API endpoint | router.go:1240-1299 | GET /api/topology/:id/ping?src=h1&dst=h2 |
| Device ID → IP conversion | router.go:1262-1276 | Ping accepts device ID, resolves to IP |
| ns-x pending-events channel | engine_nsx.go:61-78 | Added pendingEvents channel + periodic poller |
| ns-x Ping reply handling | engine_nsx.go:330-393 | Ping waits for ICMP echo reply with 5s timeout |
| Gont capability check | platform.go:12-26 | canRunGont() checks root and OVS presence |
| Auto fallback to ns-x | platform.go:28-49 | gont unavailable? Automatically use ns-x |
| Engine Stop cleanup | engine_nsx.go:299-318 | Stop() closes channels and cleans up |
| SimpleSwitch legacy marker | SimpleSwitch.go:3-14 | Documented as superseded by sim package |
| **Integration tests** | internal/api/integration_test.go | 7 integration tests covering E2E flow, concurrent ops, file storage |
| **Test resource cleanup** | internal/testutil/testutil.go | `t.Cleanup()` ensures engine.Stop() + runtime.GC() |
| **Test timeout/parallel** | Makefile, VS Code config | `-timeout 30s`, `-parallel 1` to prevent resource exhaustion |
| **System resource check** | internal/testutil/testutil.go | Linux netns count check + auto-cleanup stale netns |
| **Resource monitoring** | internal/testutil/testutil.go | Every 5s: goroutine count + memory usage |
| **Build tag separation** | internal/api/integration_test.go | `//go:build integration` for conditional test execution |
| **pprof integration** | cmd/server/main.go | `_ "net/http/pprof"` exposes /debug/pprof/ endpoints |
| **Static file serving fix** | internal/api/router.go | Added `fs.Sub(distFS, "assets")` for proper /assets path resolution, fixing JS/CSS loading |
| **Delve debugger** | External tool | go install github.com/go-delve/delve/cmd/dlv@latest |
| **staticcheck** | External tool | go install honnef.co/go/tools/cmd/staticcheck@latest |
| **go-callvis** | External tool | go install github.com/ofabry/go-callvis@latest |
| **Hoverfly** | External tool | go install github.com/SpectoLabs/hoverfly/core/cmd/hoverfly@latest |
| **拓扑标注层** | internal/api/annotation_handlers.go + frontend AnnotationLayer.tsx | 文本标注 CRUD + 画布叠加层 |
| **VXLAN 规划模板** | frontend/src/data/vxlanTemplate.ts + data/vxlan-spine-leaf.json | 演示拓扑种子与规划说明模板 |
| **协议模块扩展** | internal/protocol/* | 20+ 协议状态模型（OSPF/BGP/VXLAN/DHCP/DNS/HTTP/FTP/SMTP/MPLS/PPP 等） |
| **包路径模拟** | internal/api/router.go (simulatePacket) | BFS 路径计算端点 |
| **版本端点** | internal/api/system_handlers.go | GET /version 返回版本与构建信息 |
| **API handler 拆分** | internal/api/*_handlers.go | 按资源拆分 handler，router.go 仅保留 NewRouter() 路由注册，构建通过 |
| **左侧面板（Tab 整合）** | frontend/src/components/LeftPanel.tsx + LinkTypes.tsx + ConnectionList.tsx | 移除右侧面板，左侧 2 Tab（设备库 / 连线种类），可拖拽宽度 200–460px；连线种类 Tab = 连线种类选择器（含 auto，带线型预览）+ 连线清单 |
| **连线种类选择器 + 拖拽创建** | frontend/src/components/LinkTypes.tsx + TopologyCanvas.tsx + App.tsx | 「连线种类」Tab 选类型（自动/物理链路/VXLAN/接入/虚拟接入），进入连线模式从源设备拖到目标设备创建；auto 按约束矩阵派生类型，非法组合前端拒绝 + 后端 400；nextAvailablePort 自动分配最小未占用接口 |
| **连线约束 + 类型** | types.ts isLinkAllowed + model.go AllowedLinkType | 按设备角色限制非法直连（PC-Spine/Server-Spine/Server-Server），派生 underlay/vxlan/access/virtual 类型 |
| **浮动窗口（CLI/配置）** | FloatWindow.tsx + DeviceDetail.tsx + App.tsx | 设备 CLI/配置改为可拖动浮动小窗，双击/右键/列表触发，多窗口 + 任务栏 + localStorage 持久化 |
| **Ping 测试面板增强** | PingPanel.tsx + frontend/src/api.ts + router.go | 任意源/目标设备、count 参数、连续 Ping(-t)、结果历史、拓扑高亮联动 |
| **删除启动按钮 / 引擎懒启动** | App.tsx + topology_handlers.go + router.go | 移除「启动拓扑」按钮与 POST /start 端点，引擎首次 Ping/CLI 自动 eng.Start() |

---

## 11. Running the Server

### 11.1 Quick Start

```bash
# Windows (ns-x mode) - single binary, no external dependencies
go run cmd/server/main.go

# Linux (gont mode - requires root + OVS + FRR)
sudo apt install openvswitch-switch frr
sudo go run cmd/server/main.go

# Access frontend
http://localhost:8080

# Build standalone binary (includes embedded frontend)
go build -o ensp-lab cmd/server/main.go

# Run standalone binary
./ensp-lab
```

### 11.2 Configuration Options

```bash
# Command line parameters
go run cmd/server/main.go \
  -port 8080 \
  -data-dir ./data \
  -log-level debug \
  -log-format json

# Environment variables
export PORT=9090
export DATA_DIR=/var/lib/ensp-lab
export LOG_LEVEL=debug
go run cmd/server/main.go
```

### 11.3 API Workflow Examples

```bash
# Health check
curl http://localhost:8080/health

# Create topology
curl -X POST http://localhost:8080/api/topology \
  -H "Content-Type: application/json" \
  -d '{"name":"Test","nodes":[{"id":"h1","type":"pc"},{"id":"h2","type":"pc"}],"links":[{"source_device":"h1","source_port":"eth0","target_device":"h2","target_port":"eth0"}]}'

# 引擎懒启动：首次 Ping / CLI 自动 eng.Start()，无需显式 start
# Ping
curl "http://localhost:8080/api/topology/{id}/ping?src=h1&dst=h2"

# Check queue depth
curl "http://localhost:8080/api/sim/queue-depth?topology={id}"

# Delete topology
curl -X DELETE http://localhost:8080/api/topologies/{id}
```

### 11.4 Log Output Examples

**Console format (default):**
```
2026-07-19 21:30:00.000 INFO    Logger initialized      {"level": "info", "format": "console"}
2026-07-19 21:30:00.001 INFO    Created default topology        {"id": "default"}
2026-07-19 21:30:00.002 INFO    eNSP Web Lab Server starting    {"port": ":8080", "platform": "windows", "storage_dir": "./data"}
```

**JSON format:**
```json
{"level":"info","ts":1700000000.000,"msg":"eNSP Web Lab Server starting","port":":8080","platform":"windows","storage_dir":"./data"}
```

---

## 12. Package Status Summary

### 12.1 Active Packages

| Package | Status | Description |
|---------|--------|-------------|
| internal/cli/ | Enabled | Network device CLI simulation, command parsing and execution |
| internal/protocol/ | Enabled | Protocol handlers (ARP, ICMP, BGP, OSPF, etc.) - ICMP currently used by ns-x |
| internal/sim/ | Enabled | Core engine (GontEngine + NSxEngine) |
| internal/api/ | Enabled | REST API layer |
| internal/router/ | Enabled | FRR router integration |
| internal/storage/ | Enabled | Memory + File storage |
| internal/logging/ | Enabled | zap-based structured logging |

### 12.2 Archived Packages (moved to archive/)

| Package | Archive Path | Description |
|---------|-------------|-------------|
| internal/engine/ | archive/engine/ | Legacy SimpleSwitch code, superseded by internal/sim |
| internal/simulator/ | archive/simulator/ | Legacy event-driven simulation engine (not used in current flow) |
| internal/factory/ | archive/factory/ | Device factory placeholder (reserved for future Gateway architecture) |
| internal/api/gateway.go | archive/gateway/ | Reserved Gateway architecture (not enabled in current flow) |

All archived files have `//go:build ignore` directive and are excluded from build.

---

## 13. Testing Optimizations

### 13.1 Test Structure

| Test Type | Build Tag | Description |
|-----------|-----------|-------------|
| Unit Tests | None | Tests without engine startup, run by default |
| Integration Tests | `integration` | Tests requiring real engine, triggered via `-tags=integration` |

### 13.2 Test Configuration

```makefile
TEST_TIMEOUT ?= 30s
TEST_PARALLEL ?= 1

test:           # Run unit tests
test-unit:      # Run all unit tests
test-integration: # Run integration tests (-tags=integration)
test-all:       # Run all tests
race:           # Race detection
```

### 13.3 Testing Features

| Feature | Implementation | Description |
|---------|----------------|-------------|
| **Resource cleanup** | internal/testutil/testutil.go | `t.Cleanup()` ensures engine.Stop() and runtime.GC() |
| **Test timeout** | Makefile + VS Code config | Fixed `-timeout 30s` prevents infinite hangs |
| **Parallel limit** | Makefile + VS Code config | Default `-parallel 1` to prevent resource contention |
| **System resource check** | internal/testutil/testutil.go | Linux: check netns count before tests, auto-cleanup stale netns |
| **Resource monitoring** | internal/testutil/testutil.go | Every 5s: goroutine count + memory usage |
| **Build tag separation** | `//go:build integration` | Integration tests only run with `-tags=integration` |

### 13.4 Integration Test Coverage

| Test Function | Coverage |
|---------------|----------|
| `TestAPIEndToEnd` | Create → Ping（引擎懒启动）→ Delete |
| `TestAPIConcurrentTopologyOperations` | Concurrent create/delete with sync.WaitGroup |
| `TestFileStorageReadWrite` | File storage persistence and recovery |
| `TestFileStorageConcurrentAccess` | Concurrent file storage operations |
| `TestAPITopologyNotFound` | Error handling for invalid topology IDs |
| `TestAPIInvalidRequests` | Error handling for malformed requests |
| `TestAPISimStatus` | Simulation status API endpoint |

---

## 14. Debug and Profiling Tools

### 14.1 pprof Integration

```go
// cmd/server/main.go
import (
    _ "net/http/pprof"  // Exposes /debug/pprof/ endpoints
)
```

**pprof Endpoints:**

| Endpoint | Description |
|----------|-------------|
| `/debug/pprof/` | Main pprof dashboard |
| `/debug/pprof/heap` | Heap memory analysis |
| `/debug/pprof/profile?seconds=30` | CPU profiling |
| `/debug/pprof/goroutine` | Goroutine analysis |
| `/debug/pprof/block` | Blocking analysis |
| `/debug/pprof/mutex` | Mutex contention |

### 14.2 Installed Tools

| Tool | Version | Installation | Purpose |
|------|---------|--------------|---------|
| **Delve** | 1.27.0 | `go install github.com/go-delve/delve/cmd/dlv@latest` | Go debugger |
| **staticcheck** | 2026.1 | `go install honnef.co/go/tools/cmd/staticcheck@latest` | Static code analysis |
| **go-callvis** | 0.7.0 | `go install github.com/ofabry/go-callvis@latest` | Call graph visualization |
| **Hoverfly** | 1.12.10 | `go install github.com/SpectoLabs/hoverfly/core/cmd/hoverfly@latest` | API traffic simulation |
| **pprof** | Built-in | `_ "net/http/pprof"` | Performance profiling |

### 14.3 Tool Usage Examples

```bash
# Delve debugging
dlv debug ./cmd/server -- -port=8080

# Static code analysis
staticcheck ./...

# Call graph visualization (requires graphviz)
go-callvis -http=:7878 ./cmd/server

# CPU profiling
go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30

# Memory profiling
go tool pprof http://localhost:8080/debug/pprof/heap

# Hoverfly traffic simulation
hoverfly -listen-on-host 0.0.0.0 -proxy-port 8500
```

### 14.4 VS Code Configuration

**launch.json configurations:**
- `Launch Server` - Debug server with args
- `Launch Server (with pprof)` - Debug with GODEBUG=http2debug=2
- `Run Tests` - Run unit tests
- `Run Integration Tests` - Run integration tests with `-tags=integration`

**settings.json:**
- GOPATH: `d:\Projects\Go`
- Test flags: `-timeout 30s`, `-parallel 1`
- Format on save with gofmt