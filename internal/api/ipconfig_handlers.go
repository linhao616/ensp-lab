// ipconfig_handlers.go 提供设备 IP 地址配置的 REST 端点（方案三）。
//
//   - GET  /api/topologies/:id/devices/:deviceId/ip-config  读取当前 IP 配置
//   - POST /api/topologies/:id/devices/:deviceId/ip-config  设置 IP 配置
//
// 两种模式：
//   - host 模式：适用于 PC/Client/Server 等终端主机，配置 ip/subnet_mask/gateway/dns；
//     底层复用 CLIState.HostIP/HostSubnet/DefaultGateway/HostDNS，并通过
//     SerializeToDeviceConfigData + updateDeviceInterfaces 持久化（与方案一的 ipconfig /set、netsh 完全一致）。
//   - interface 模式：适用于交换机/路由/VTEP 等网络设备，对指定物理接口配置 ip/subnet_mask；
//     底层同时写 CLIState.Interfaces[ifName] 与 DeviceConfig["interface:<ifName>:ip"]，
//     与方案一的 `interface X` + `ip address` 完全一致。
//
// Web UI 配置面板（方案二）与 REST API（方案三）共用本文件，保证三套入口（CLI/Web/REST）一致。
package api

import (
	"net/http"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

// SetIPConfigRequest 是 REST 配置设备 IP 的请求体。
// 主机模式（PC/Client/Server）：提供 ip / subnet_mask / gateway / dns。
// 接口模式（交换机/路由/VTEP）：提供 interface + ip / subnet_mask。
// mode 缺省时按设备类型自动判定（终端主机 -> host，其余且带 interface -> interface）。
type SetIPConfigRequest struct {
	IP         string `json:"ip"`
	SubnetMask string `json:"subnet_mask"` // 点分十进制 / 前缀长度(0-32) / CIDR
	Gateway    string `json:"gateway"`
	DNS        string `json:"dns"`
	Interface  string `json:"interface"` // 如 10GE5/0/1
	Mode       string `json:"mode"`      // "host" | "interface"
}

// IPConfigResponse 返回设备当前 IP 配置。
type IPConfigResponse struct {
	DeviceID   string `json:"device_id"`
	Type       string `json:"type"`
	Mode       string `json:"mode"` // host | interface
	Interface  string `json:"interface,omitempty"`
	IP         string `json:"ip"`
	SubnetMask string `json:"subnet_mask"`
	Gateway    string `json:"gateway,omitempty"`
	DNS        string `json:"dns,omitempty"`
	CIDR       string `json:"cidr,omitempty"`
}

func isTerminalHost(dt topology.DeviceType) bool {
	return dt == topology.DevicePC || dt == topology.DeviceClient || dt == topology.DeviceServer
}

// getIPConfig GET /api/topologies/:id/devices/:deviceId/ip-config
func (r *Router) getIPConfig(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "topology not found"})
		return
	}
	device, exists := t.GetDevice(deviceId)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
		return
	}
	dt := device.Type
	state := r.getOrInitCLIState(id, deviceId, dt)
	ifName := c.Query("interface")

	if isTerminalHost(dt) {
		ip := state.HostIP
		mask := state.HostSubnet
		cidr := ""
		if ip != "" {
			cidr = cli.IPToCIDR(ip, mask)
		}
		c.JSON(http.StatusOK, IPConfigResponse{
			DeviceID:   deviceId,
			Type:       string(dt),
			Mode:       "host",
			Interface:  "Ethernet0",
			IP:         ip,
			SubnetMask: mask,
			Gateway:    state.DefaultGateway,
			DNS:        state.HostDNS,
			CIDR:       cidr,
		})
		return
	}

	// 网络设备接口模式：未指定 interface 时，优先返回第一个有 IP 的接口，否则第一个接口。
	if ifName == "" {
		for name, ifc := range state.Interfaces {
			if ifc.IP != "" {
				ifName = name
				break
			}
		}
		if ifName == "" {
			for name := range state.Interfaces {
				ifName = name
				break
			}
		}
	}
	ifc := state.Interfaces[ifName]
	ip, mask := "", ""
	if ifc != nil {
		ip, mask = ifc.IP, ifc.Mask
	}
	cidr := ""
	if ip != "" {
		cidr = cli.IPToCIDR(ip, mask)
	}
	c.JSON(http.StatusOK, IPConfigResponse{
		DeviceID:   deviceId,
		Type:       string(dt),
		Mode:       "interface",
		Interface:  ifName,
		IP:         ip,
		SubnetMask: mask,
		CIDR:       cidr,
	})
}

// setIPConfig POST /api/topologies/:id/devices/:deviceId/ip-config
func (r *Router) setIPConfig(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "topology not found"})
		return
	}
	device, exists := t.GetDevice(deviceId)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
		return
	}
	dt := device.Type

	var req SetIPConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	state := r.getOrInitCLIState(id, deviceId, dt)
	terminal := isTerminalHost(dt)

	mode := req.Mode
	if mode == "" {
		if terminal {
			mode = "host"
		} else if req.Interface != "" {
			mode = "interface"
		} else {
			mode = "host"
		}
	}

	var ifName string
	switch mode {
	case "host":
		if !terminal {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host mode IP config is only supported on PC/Client/Server devices"})
			return
		}
		// ip 为空表示清除主机 IP；否则设置
		if req.IP == "" {
			state.HostIP = ""
			state.HostSubnet = ""
		} else {
			state.HostIP = req.IP
			state.HostSubnet = cli.NormalizeMask(req.SubnetMask)
		}
		if req.Gateway != "" {
			state.DefaultGateway = req.Gateway
		}
		if req.DNS != "" {
			state.HostDNS = req.DNS
		}
		ifName = "Ethernet0"
	case "interface":
		if req.Interface == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "interface is required for interface mode"})
			return
		}
		keys := make([]string, 0, len(state.Interfaces))
		for k := range state.Interfaces {
			keys = append(keys, k)
		}
		resolved, perr := cli.ParseInterfaceName(req.Interface, keys)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": perr.Error()})
			return
		}
		ifName = resolved
		normalized := cli.NormalizeMask(req.SubnetMask)
		// 同时更新 Interfaces map 与 DeviceConfig（与方案一 `ip address` 一致）
		ifc := state.Interfaces[ifName]
		if ifc == nil {
			ifc = &cli.InterfaceConfig{Name: ifName}
			state.Interfaces[ifName] = ifc
		}
		ifc.IP = req.IP
		ifc.Mask = normalized
		if req.IP == "" {
			delete(state.DeviceConfig, "interface:"+ifName+":ip")
		} else {
			state.DeviceConfig["interface:"+ifName+":ip"] = req.IP + " " + normalized
		}
		// 同步到拓扑模型接口（用于 display / 仿真引擎）
		syncInterfaceIP(device, ifName, req.IP, normalized)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mode, expect 'host' or 'interface'"})
		return
	}

	// 持久化（与 executeCLI 完全一致）
	device.ConfigData = state.SerializeToDeviceConfigData()
	updateDeviceInterfaces(device, state)
	if err := r.store.UpdateTopology(t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 读取回当前配置返回
	ip := state.HostIP
	mask := state.HostSubnet
	if mode == "interface" {
		if ifc := state.Interfaces[ifName]; ifc != nil {
			ip, mask = ifc.IP, ifc.Mask
		}
	}
	cidr := ""
	if ip != "" {
		cidr = cli.IPToCIDR(ip, mask)
	}
	c.JSON(http.StatusOK, IPConfigResponse{
		DeviceID:   deviceId,
		Type:       string(dt),
		Mode:       mode,
		Interface:  ifName,
		IP:         ip,
		SubnetMask: mask,
		Gateway:    state.DefaultGateway,
		DNS:        state.HostDNS,
		CIDR:       cidr,
	})
}

// syncInterfaceIP 把接口 IP/Mask 同步到 topology.Device.Interfaces[ifName]。
func syncInterfaceIP(device *topology.Device, ifName, ip, mask string) {
	if device.Interfaces == nil {
		device.Interfaces = make(map[string]*topology.Interface)
	}
	iface, ok := device.Interfaces[ifName]
	if !ok {
		iface = &topology.Interface{Name: ifName}
		device.Interfaces[ifName] = iface
	}
	iface.IPAddress = ip
	iface.SubnetMask = mask
}
