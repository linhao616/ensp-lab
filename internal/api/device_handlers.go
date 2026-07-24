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
	device.InitializeDefaults()
	t.AddDevice(&device)
	r.store.UpdateTopology(t)
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
	device, ok := t.GetDevice(deviceId)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not found"})
		return
	}
	if err := c.ShouldBindJSON(device); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.store.UpdateTopology(t)
	c.JSON(http.StatusOK, device)
}

func (r *Router) deleteDevice(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	t.RemoveDevice(deviceId)
	r.store.UpdateTopology(t)

	delete(r.cliStates, deviceId)

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
	r.store.UpdateTopology(t)
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
