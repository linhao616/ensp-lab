// cli_handlers.go 提供设备 CLI 终端命令执行端点。
//
//   - POST /api/topologies/:id/devices/:deviceId/cli  执行单条 CLI 命令
//
// executeCLI 是仿真器最复杂的 handler 之一，承担三个职责：
//  1. 懒加载并维护每个设备的 CLIState（用户视图、当前子模式、配置）
//  2. 识别 ping 指令并走 protoSim（带 BFS 可达性校验），输出 Linux 风格结果
//  3. 把 CLI 状态（IP/网关/DNS）回写到拓扑的 device.ConfigData 与 interfaces
//
// updateDeviceInterfaces 是 executeCLI 内部用的小辅助函数，把 CLI 层的
// HostIP/HostSubnet/DefaultGateway 同步到 topology.Device.Interfaces["Ethernet0"]。
package api

import (
	"net/http"
	"strings"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/logging"
	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *Router) executeCLI(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")

	// 同一设备串行化：并发 CLI 请求按设备排队，避免争抢同一 CLIState
	// （视图切换、配置写入、历史记录互斥），与华为 VRP 单机单会话语义一致。
	devMu := r.deviceCLIMutex(deviceId)
	devMu.Lock()
	defer devMu.Unlock()

	dt := r.lookupDeviceType(id, deviceId)
	if dt == "" {
		t, _ := r.store.GetTopology(id)
		if t != nil {
			if device, exists := t.GetDevice(deviceId); exists {
				dt = device.Type
			}
		}
	}

	state := r.getOrInitCLIState(id, deviceId, dt)
	// 拓扑级 CLIState 注册表快照：供 P1-C ACL 跨设备评估（途径 L3/防火墙设备自身 ACL）。
	registry := r.cliStateRegistry()

	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := cli.ParseCommand(req.Command)

	// 记录命令历史（空命令已在 RecordHistory 内忽略）。历史随 CLIState 序列化
	// 进拓扑 DeviceConfigData，executeCLI 末尾的 UpdateTopology 一并落盘。
	state.RecordHistory(req.Command)

	var output string
	t, err := r.store.GetTopology(id)

	// P0-B：ping / tracert / traceroute 走真实仿真引擎（sim.Engine），
	// 取代此前 parser.go 的硬编码成功/固定 2 跳结果。引擎不可用时
	//（拓扑未加载、设备缺失等）回退到 parser 原逻辑，保证健壮性。
	lowerCmd := strings.ToLower(cmd.Command)
	isPing := lowerCmd == "ping"
	isTraceroute := lowerCmd == "tracert" || lowerCmd == "traceroute"
	if err == nil && t != nil && (isPing || isTraceroute) && len(cmd.Args) > 0 {
		targetIP := cmd.Args[0]

		eng, engErr := r.getOrCreateEngine(id)
		if engErr == nil && eng != nil {
			if isPing {
				output = r.renderEnginePing(eng, deviceId, targetIP, t)
			} else {
				output = r.renderEngineTraceroute(eng, deviceId, targetIP, t)
			}
		} else {
			// 引擎不可用：回退到 parser.go 兜底（恒成功/硬编码 2 跳），
			// 避免在极端路径下命令无输出。
			logging.Warn("getOrCreateEngine failed, fallback to parser",
				zap.String("id", id), zap.String("command", lowerCmd), zap.Error(engErr))
			output = cli.ExecuteCommandWithContext(registry, state, cmd, dt, t)
		}

		// 查找目标设备 ID，供前端动画使用
		targetDeviceID := ""
		for _, dev := range t.Devices {
			if cli.IsDeviceIPMatch(dev, targetIP) {
				targetDeviceID = dev.ID
				break
			}
		}

		// 保存配置到拓扑
		if device, exists := t.GetDevice(deviceId); exists {
			device.ConfigData = state.SerializeToDeviceConfigData()
			updateDeviceInterfaces(device, state)
			r.store.UpdateTopology(t)
		}

		c.JSON(http.StatusOK, gin.H{
			"output":         output,
			"view":           state.CurrentView,
			"sub":            state.CurrentSub,
			"targetDeviceID": targetDeviceID,
		})
		return
	} else if err == nil && t != nil {
		output = cli.ExecuteCommandWithContext(registry, state, cmd, dt, t)
	} else {
		output = cli.ExecuteCommandOn(state, cmd, dt)
	}

	// 保存配置到拓扑
	if err == nil && t != nil {
		if device, exists := t.GetDevice(deviceId); exists {
			device.ConfigData = state.SerializeToDeviceConfigData()
			updateDeviceInterfaces(device, state)
			r.store.UpdateTopology(t)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"output": output,
		"view":   state.CurrentView,
		"sub":    state.CurrentSub,
	})
}

// inferInterfaceDescription 为接口生成一个贴近华为 VRP 的 Description。
// 优先使用拓扑模型中已配置的 Description；否则按接口命名推断。
// 仅 executeCLI 调用，因此放在本文件而不放 device_handlers.go。
func inferInterfaceDescription(name string, iface *topology.Interface) string {
	if iface != nil && iface.Description != "" {
		return iface.Description
	}
	switch {
	case strings.HasPrefix(name, "LoopBack"):
		return "LoopBack Interface"
	case strings.HasPrefix(name, "Vlanif"), strings.HasPrefix(name, "Vlan"):
		return name
	case strings.HasPrefix(name, "MEth"):
		return "Management Interface"
	case strings.HasPrefix(name, "10GE"), strings.HasPrefix(name, "GE"), strings.HasPrefix(name, "GigabitEthernet"):
		return "10GE Interface"
	default:
		return name
	}
}

// getOrInitCLIState 懒加载并维护每个设备的 CLIState（与 executeCLI 的逻辑完全一致）。
// 供 executeCLI 与 IP 配置 REST 端点复用，确保 CLI、Web UI、REST API 三套入口
// 共享同一份配置状态与持久化路径。
func (r *Router) getOrInitCLIState(id, deviceId string, dt topology.DeviceType) *cli.CLIState {
	// 快速读路径：命中缓存直接返回，多设备并发 CLI 互不阻塞。
	r.cliMu.RLock()
	state, ok := r.cliStates[deviceId]
	r.cliMu.RUnlock()
	if ok {
		return state
	}

	// 未命中：从拓扑构建（可能触发 store 读取，放在锁外避免持锁 I/O）。
	state = r.buildCLIState(id, deviceId, dt)

	// 写回前二次检查，避免两个并发 miss 各自写入（后者覆盖前者，结果等价）。
	r.cliMu.Lock()
	if s, exists := r.cliStates[deviceId]; exists {
		r.cliMu.Unlock()
		return s
	}
	r.cliStates[deviceId] = state
	r.cliMu.Unlock()
	return state
}

// dropCLIState 移除某设备的缓存 CLI 会话，使其下次访问时从（可能已变更的）
// 拓扑重新构建。供拓扑/设备更新、删除时调用，保证 CLI 显示与拓扑模型一致。
// 调用方无需持有 cliMu。
func (r *Router) dropCLIState(deviceId string) {
	r.cliMu.Lock()
	delete(r.cliStates, deviceId)
	r.cliMu.Unlock()
}

// cliStateRegistry 返回拓扑内各设备 CLIState 的快照（deviceID→*CLIState），
// 供 ACL 跨设备评估使用（P1-C Round 1 修复：使评估器能读取途径 L3/防火墙设备
// 「自身」的 traffic-filter ACL，而非仅源设备 state）。在 cliMu.RLock 下复制，
// 返回的快照为只读，可安全跨 goroutine 传递给评估器（避免并发修改底层 map 触发
// fatal；个别 *CLIState 字段的并发读写沿用既有共享状态模型，不在本次范围）。
func (r *Router) cliStateRegistry() map[string]*cli.CLIState {
	r.cliMu.RLock()
	defer r.cliMu.RUnlock()
	reg := make(map[string]*cli.CLIState, len(r.cliStates))
	for k, v := range r.cliStates {
		reg[k] = v
	}
	return reg
}

// buildCLIState 从拓扑模型构造一台设备的 CLIState（不含 cliStates map 的读写）。
func (r *Router) buildCLIState(id, deviceId string, dt topology.DeviceType) *cli.CLIState {
	var state *cli.CLIState
	if t, err := r.store.GetTopology(id); err == nil && t != nil {
		if device, exists := t.GetDevice(deviceId); exists && device.ConfigData != nil {
			state = cli.NewCLIStateFromDeviceConfig(dt, device.ConfigData, device.Name)
		} else {
			state = cli.NewCLIStateWithType(dt)
			if device, exists := t.GetDevice(deviceId); exists {
				state.DeviceName = device.Name
			}
		}
		// 终端类设备（PC/Client/Server）的 CLIState.HostIP 必须来自拓扑模型真实 IP。
		if dt == topology.DevicePC || dt == topology.DeviceClient || dt == topology.DeviceServer {
			if device, exists := t.GetDevice(deviceId); exists && state.HostIP == "" {
				for _, iface := range device.Interfaces {
					if iface.IPAddress != "" {
						state.HostIP = iface.IPAddress
						if iface.SubnetMask != "" {
							state.HostSubnet = iface.SubnetMask
						}
						break
					}
				}
			}
		}
		// 所有设备的 CLIState.Interfaces 必须以拓扑模型真实接口为准，
		// 覆盖 newCLIStateWithType 中写死的模板（如 GE0/0/1=192.168.1.1）。
		if device, exists := t.GetDevice(deviceId); exists && len(device.Interfaces) > 0 {
			realIfaces := make(map[string]*cli.InterfaceConfig, len(device.Interfaces))
			for _, iface := range device.Interfaces {
				status := strings.ToUpper(strings.TrimSpace(iface.Status))
				if status == "" {
					status = "Up"
				}
				realIfaces[iface.Name] = &cli.InterfaceConfig{
					Name:        iface.Name,
					Status:      status,
					Protocol:    status,
					IP:          iface.IPAddress,
					Mask:        iface.SubnetMask,
					Description: inferInterfaceDescription(iface.Name, iface),
				}
			}
			state.Interfaces = realIfaces
			state.ARPTable = []*cli.ARPEntry{}
		}
		// 注入拓扑引用，使 dis vxlan tunnel 等命令能读取 VXLAN 隧道链路。
		state.Topology = t
	} else {
		state = cli.NewCLIStateWithType(dt)
	}
	state.DeviceID = deviceId
	state.DeviceType = dt
	return state
}

// updateDeviceInterfaces 把 CLI 层的 HostIP/HostSubnet/DefaultGateway/DNS
// 同步到 topology.Device.Interfaces["Ethernet0"]，确保下次启动时配置不丢。
// 仅 PC/Client/Server 这类终端主机才拥有 Ethernet0 接口；交换机/路由/VTEP 等
// 网络设备不应出现 Ethernet0（那是 PC/Server 的接口）。若历史上被误写入，
// 此处直接清理，避免 display ip interface brief 把 Ethernet0 当成交换机接口列出。
// 仅 executeCLI 调用，因此放在本文件而不放 device_handlers.go。
func updateDeviceInterfaces(device *topology.Device, state *cli.CLIState) {
	isTerminal := device.Type == topology.DevicePC ||
		device.Type == topology.DeviceClient ||
		device.Type == topology.DeviceServer
	if !isTerminal {
		// 交换机/路由/VTEP 等设备不保留 Ethernet0（含历史遗留），直接清理。
		if _, ok := device.Interfaces["Ethernet0"]; ok {
			delete(device.Interfaces, "Ethernet0")
		}
		return
	}
	if device.Interfaces == nil {
		device.Interfaces = make(map[string]*topology.Interface)
	}
	iface, exists := device.Interfaces["Ethernet0"]
	if !exists {
		iface = &topology.Interface{Name: "Ethernet0"}
		device.Interfaces["Ethernet0"] = iface
	}
	if state.HostIP != "" {
		iface.IPAddress = state.HostIP
		iface.Status = "up"
	}
	if state.HostSubnet != "" {
		iface.SubnetMask = state.HostSubnet
	}
	if state.DefaultGateway != "" {
		iface.Gateway = state.DefaultGateway
	}
	if state.HostDNS != "" {
		iface.DNS = state.HostDNS
	}
}

// renderEnginePing 调用真实引擎执行 ping 并渲染结果。
//
// 引擎返回真实的逐包 RTT 与丢包统计（nsxEngine 在 P0-B 中改为多次探测采集
// wall-clock RTT），不可达时如实报告 100% loss。任何引擎层错误都降级为
// parser 兜底输出，避免命令无结果。
func (r *Router) renderEnginePing(eng sim.Engine, deviceId, targetIP string, t *topology.Topology) string {
	registry := r.cliStateRegistry()
	res, perr := eng.Ping(deviceId, targetIP)
	if perr != nil {
		logging.Warn("engine.Ping failed, fallback to parser",
			zap.String("deviceId", deviceId),
			zap.String("targetIP", targetIP),
			zap.Error(perr))
		// 借用 parser 的 CLIState 兜底渲染：先构造一个临时状态。
		state := cli.NewCLIStateWithType(r.lookupDeviceType(t.ID, deviceId))
		return cli.ExecuteCommandWithContext(registry, state, cli.ParseCommand("ping "+targetIP), state.DeviceType, t)
	}
	// P1-C T03：经 CLIState ACL 评估器叠加真实过滤判定。
	dt := r.lookupDeviceType(t.ID, deviceId)
	state := r.getOrInitCLIState(t.ID, deviceId, dt)
	return cli.RenderPingWithACL(registry, state, res, targetIP, t)
}

// renderEngineTraceroute 调用真实引擎执行 tracert 并渲染逐跳结果。
//
// 引擎在真实拓扑图上做 BFS 路径发现（含 VXLAN overlay 链路），替代此前
// parser.go:1139 硬编码的 2 跳。不可达时渲染 "* * *" 而非伪造路径。
func (r *Router) renderEngineTraceroute(eng sim.Engine, deviceId, targetIP string, t *topology.Topology) string {
	const maxTTL = 30
	registry := r.cliStateRegistry()
	res, terr := eng.Traceroute(deviceId, targetIP, maxTTL)
	if terr != nil {
		logging.Warn("engine.Traceroute failed, fallback to parser",
			zap.String("deviceId", deviceId),
			zap.String("targetIP", targetIP),
			zap.Error(terr))
		state := cli.NewCLIStateWithType(r.lookupDeviceType(t.ID, deviceId))
		return cli.ExecuteCommandWithContext(registry, state, cli.ParseCommand("tracert "+targetIP), state.DeviceType, t)
	}
	// P1-C T02：经 CLIState ACL 评估器叠加真实过滤判定。
	dt := r.lookupDeviceType(t.ID, deviceId)
	state := r.getOrInitCLIState(t.ID, deviceId, dt)
	return cli.RenderTracerouteWithACL(registry, state, res, targetIP, maxTTL)
}
