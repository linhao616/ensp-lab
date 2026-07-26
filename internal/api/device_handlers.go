// device_handlers.go 提供拓扑设备的增删改查与电源控制端点。
//
//   - POST   /api/topologies/:id/devices                       新增设备
//   - PUT    /api/topologies/:id/devices/:deviceId             更新设备
//   - DELETE /api/topologies/:id/devices/:deviceId             删除设备（同步清理 CLIState 与 protoSim）
//   - POST   /api/topologies/:id/devices/:deviceId/power       开机/关机
//   - GET    /api/devices/types                                查询支持的设备类型清单
//
// 设备类型清单在前端"新增设备"对话框中作为下拉项使用。
package api

import (
	"fmt"
	"net/http"

	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

func (r *Router) addDevice(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	var device topology.Device
	if err := c.ShouldBindJSON(&device); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 安全校验：ID 缺失则自动生成；类型必须已知；名称不含控制字符。
	if device.ID == "" {
		device.ID = generateID()
	}
	if err := validateIdent(device.ID, maxIdentLen); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !IsValidDeviceType(device.Type) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid device type %q", device.Type)})
		return
	}
	if err := validateName(device.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	device.InitializeDefaults()
	t = t.Clone()
	t.AddDevice(&device)
	r.store.UpdateTopology(t)
	r.syncEngine(id)
	c.JSON(http.StatusCreated, device)
}

func (r *Router) updateDevice(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	existing, ok := t.GetDevice(deviceId)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}
	t = t.Clone()
	// 安全/并发：绑定到设备副本，避免直接改写 store 中存活的 *Device
	// （被并发读请求 / 仿真引擎共享），造成数据竞争或 TOCTOU。
	updated := *existing
	if existing.Interfaces != nil {
		updated.Interfaces = make(map[string]*topology.Interface, len(existing.Interfaces))
		for k, v := range existing.Interfaces {
			cp := *v
			updated.Interfaces[k] = &cp
		}
	}
	if existing.ConfigData != nil {
		cd := *existing.ConfigData
		if existing.ConfigData.Interfaces != nil {
			cd.Interfaces = make(map[string]string, len(existing.ConfigData.Interfaces))
			for k, v := range existing.ConfigData.Interfaces {
				cd.Interfaces[k] = v
			}
		}
		if existing.ConfigData.History != nil {
			cd.History = append([]*topology.HistoryEntry(nil), existing.ConfigData.History...)
		}
		updated.ConfigData = &cd
	}
	if err := c.ShouldBindJSON(&updated); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated.ID = deviceId // 以路径参数为准，避免请求体篡改设备 ID
	if !IsValidDeviceType(updated.Type) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid device type %q", updated.Type)})
		return
	}
	t.Devices[deviceId] = &updated
	r.store.UpdateTopology(t)
	// 设备配置可能变更接口/路由，失效缓存使下次 CLI 从新拓扑重建。
	r.dropCLIState(deviceId)
	// 把最新拓扑同步给已存在的仿真引擎（B1）。
	r.syncEngine(id)
	c.JSON(http.StatusOK, &updated)
}

func (r *Router) deleteDevice(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	t = t.Clone()
	t.RemoveDevice(deviceId)
	r.store.UpdateTopology(t)

	r.dropCLIState(deviceId)
	// 设备已移除，把最新拓扑同步给已存在的仿真引擎（B1）。
	r.syncEngine(id)

	if r.protoSim != nil {
		r.protoSim.RemoveRouter(deviceId)
	}

	c.JSON(http.StatusNoContent, nil)
}

func (r *Router) powerDevice(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	device, ok := t.GetDevice(deviceId)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Action == "on" {
		device.Status = topology.StatusRunning
	} else if req.Action == "off" {
		device.Status = topology.StatusPowerOff
	}
	t = t.Clone()
	r.store.UpdateTopology(t)
	r.syncEngine(id)
	c.JSON(http.StatusOK, device)
}

func (r *Router) getDeviceTypes(c *gin.Context) {
	types := []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}{
		{string(topology.DeviceRouter), "Router"},
		{string(topology.DeviceSwitch), "Switch"},
		{string(topology.DeviceL3Switch), "L3 Switch"},
		{string(topology.DeviceFirewall), "Firewall"},
		{string(topology.DeviceAC), "AC"},
		{string(topology.DeviceAP), "AP"},
		{string(topology.DevicePC), "PC"},
		{string(topology.DeviceClient), "Client"},
		{string(topology.DeviceServer), "Server"},
		{string(topology.DeviceCloud), "Cloud"},
		{string(topology.DeviceHub), "Hub"},
		{string(topology.DeviceVTEP), "VTEP"},
	}
	c.JSON(http.StatusOK, types)
}
