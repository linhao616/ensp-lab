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
	"strconv"
	"strings"
	"time"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/logging"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *Router) executeCLI(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")

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

	var req struct {
		Command string `json:"command"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cmd := cli.ParseCommand(req.Command)

	var output string
	t, err := r.store.GetTopology(id)

	if err == nil && t != nil && strings.ToLower(cmd.Command) == "ping" && len(cmd.Args) > 0 {
		targetIP := cmd.Args[0]
		count := 4
		size := 56
		if len(cmd.Args) >= 2 {
			if n, err := strconv.Atoi(cmd.Args[1]); err == nil && n > 0 {
				count = n
			}
		}

		// checker 先检查是否为本机接口 IP，再做拓扑可达性检查
		checker := func(ip string) bool {
			// 本机接口 IP 直接可达
			hostIfaces := cli.GetHostInterfaces(state)
			for _, iface := range hostIfaces {
				ifaceIP := iface["ip"]
				if idx := strings.Index(ifaceIP, "/"); idx > 0 {
					ifaceIP = ifaceIP[:idx]
				}
				if ifaceIP == ip {
					return true
				}
			}
			return cli.CheckReachability(state, ip, t)
		}

		results := r.protoSim.Ping(deviceId, targetIP, 2*time.Second, count, size, checker)

		if router, ok := r.protoSim.GetRouter(deviceId); ok && router.ICMP != nil {
			output = router.ICMP.FormatPingResults(results)
		} else {
			output = cli.ExecuteCommandWithContext(state, cmd, dt, t)
		}

		// 查找目标设备 ID，供前端动画使用
		targetDeviceID := ""
		for _, dev := range t.Devices {
			if cli.IsDeviceIPMatch(dev, targetIP) {
				targetDeviceID = dev.ID
				break
			}
		}

		// 额外触发 sim.Engine 的事件流，用于 SSE 推送。
		// 返回的 PingResult 不影响 CLI 输出（CLI 输出仍走 protoSim）。
		// 任何失败仅记录日志，不影响原有逻辑。
		if eng, engErr := r.getOrCreateEngine(id); engErr == nil {
			if _, pingErr := eng.Ping(deviceId, targetIP); pingErr != nil {
				logging.Warn("engine.Ping failed",
					zap.String("deviceId", deviceId),
					zap.String("targetIP", targetIP),
					zap.Error(pingErr),
				)
			}
		} else {
			logging.Warn("getOrCreateEngine failed",
				zap.String("id", id),
				zap.Error(engErr),
			)
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
		output = cli.ExecuteCommandWithContext(state, cmd, dt, t)
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
	if state, ok := r.cliStates[deviceId]; ok {
		state.DeviceType = dt
		return state
	}
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
	} else {
		state = cli.NewCLIStateWithType(dt)
	}
	state.DeviceID = deviceId
	state.DeviceType = dt
	r.cliStates[deviceId] = state
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
