// topology_handlers.go 提供拓扑本身的增删改查与生命周期端点。
//
//   - GET    /api/topologies                   列出全部拓扑
//   - GET    /api/topologies/:id               查询单个拓扑
//   - POST   /api/topologies                   创建完整拓扑（带 devices/links/annotations）
//   - PUT    /api/topologies/:id               更新拓扑
//   - DELETE /api/topologies/:id               删除拓扑（同时停 engine、清 CLIState）
//   - POST   /api/topology                     简化版创建（按 nodes+links 自动分配 IP）
//
// 注：仿真引擎在首次 Ping / CLI 时通过 getOrCreateEngine 懒加载并自动 eng.Start()，
// 无需独立的「启动拓扑」端点。
//
// 简化版创建（createTopologySimple）面向前端拖拽建图，会为 PC/Server 自动
// 生成 192.168.x.x 段地址，为 Router 端口生成 192.168.x.1 网关。
package api

import (
	"errors"
	"fmt"
	"net/http"

	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

func (r *Router) listTopologies(c *gin.Context) {
	topologies, _ := r.store.ListTopologies()
	c.JSON(http.StatusOK, topologies)
}

func (r *Router) getTopology(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (r *Router) createTopology(c *gin.Context) {
	var t topology.Topology
	if err := c.ShouldBindJSON(&t); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if t.ID == "" {
		t.ID = generateID()
	}
	if t.Devices == nil {
		t.Devices = make(map[string]*topology.Device)
	}
	if t.Links == nil {
		t.Links = []*topology.Link{}
	}
	if t.Annotations == nil {
		t.Annotations = []*topology.TextAnnotation{}
	}
	if err := r.store.CreateTopology(&t); err != nil {
		// 非法 IP 配置属客户端错误，返回 400；其余归为 500。
		if errors.Is(err, topology.ErrInvalidIPConfig) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (r *Router) updateTopology(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	if err := c.ShouldBindJSON(t); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.store.UpdateTopology(t)
	c.JSON(http.StatusOK, t)
}

func (r *Router) deleteTopology(c *gin.Context) {
	id := c.Param("id")

	t, err := r.store.GetTopology(id)
	if err == nil && t != nil {
		for deviceId := range t.Devices {
			delete(r.cliStates, deviceId)
			if r.protoSim != nil {
				r.protoSim.RemoveRouter(deviceId)
			}
		}
	}

	// 停止并释放该拓扑对应的 sim.Engine
	r.stopEngine(id)

	r.store.DeleteTopology(id)
	c.JSON(http.StatusNoContent, nil)
}

func (r *Router) createTopologySimple(c *gin.Context) {
	var req struct {
		Name  string              `json:"name"`
		Nodes []map[string]string `json:"nodes"`
		Links []struct {
			SourceDevice string `json:"source_device"`
			SourcePort   string `json:"source_port"`
			TargetDevice string `json:"target_device"`
			TargetPort   string `json:"target_port"`
		} `json:"links"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := generateID()
	t := topology.NewTopology(id, req.Name)

	ipCounter := 1
	for _, node := range req.Nodes {
		nodeID := node["id"]
		nodeType := topology.DeviceType(node["type"])
		nodeName := node["name"]
		if nodeName == "" {
			nodeName = nodeID
		}

		device := &topology.Device{
			ID:     nodeID,
			Name:   nodeName,
			Type:   nodeType,
			Status: topology.StatusPowerOff,
		}
		device.InitializeDefaults()

		if nodeType == topology.DevicePC || nodeType == topology.DeviceServer {
			for ifName := range device.Interfaces {
				ip := fmt.Sprintf("192.168.%d.%d/24", 1, ipCounter)
				ipCounter++
				device.Interfaces[ifName].IPAddress = ip[:len(ip)-3]
				device.Interfaces[ifName].SubnetMask = "255.255.255.0"
				device.Interfaces[ifName].Status = "up"
				break
			}
		} else if nodeType == topology.DeviceRouter {
			routerSubnet := 1
			for ifName := range device.Interfaces {
				ip := fmt.Sprintf("192.168.%d.1/24", routerSubnet)
				routerSubnet++
				device.Interfaces[ifName].IPAddress = ip[:len(ip)-3]
				device.Interfaces[ifName].SubnetMask = "255.255.255.0"
				device.Interfaces[ifName].Status = "up"
			}
		}

		t.AddDevice(device)
	}

	for i, link := range req.Links {
		l := &topology.Link{
			ID:           fmt.Sprintf("link-%d", i),
			SourceDevice: link.SourceDevice,
			SourcePort:   link.SourcePort,
			TargetDevice: link.TargetDevice,
			TargetPort:   link.TargetPort,
			LinkType:     topology.LinkTypeBusiness,
			CableType:    topology.PortCopper,
		}
		t.AddLink(l)
	}

	if err := r.store.CreateTopology(t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, CreateTopologyResponse{
		ID:          id,
		Name:        req.Name,
		DeviceCount: t.DeviceCount(),
		LinkCount:   t.LinkCount(),
		CreatedAt:   t.CreatedAt,
	})
}

