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
	"sort"

	"ensp-lab/internal/storage"
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
	// 安全校验：拓扑 ID 形态、设备类型、链路端点存在性。
	if err := validateTopoID(t.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTopologyPayload(&t); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := r.store.CreateTopology(&t); err != nil {
		// 非法 IP 配置 / 非法拓扑 ID 属客户端错误，返回 400；其余归为 500。
		if errors.Is(err, topology.ErrInvalidIPConfig) || errors.Is(err, storage.ErrInvalidTopoID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	// 若此前已为该拓扑创建引擎（懒加载），把最新拓扑同步给它，闭合「编辑→仿真」。
	r.syncEngine(t.ID)
	c.JSON(http.StatusCreated, &t)
}

func (r *Router) updateTopology(c *gin.Context) {
	id := c.Param("id")
	t, err := r.store.GetTopology(id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology not found"})
		return
	}
	// 安全/并发：绝不直接把请求体绑定进 store 中存活的 *Topology（会被并发读请求
	// 共享，造成数据竞争 / TOCTOU）。先深拷贝，绑定到副本，校验后再整体替换。
	updated := t.Clone()
	updated.ID = id
	if err := c.ShouldBindJSON(updated); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated.ID = id // 以路径参数为准，避免请求体篡改拓扑 ID
	// 安全校验：拓扑 ID 形态、设备类型、链路端点存在性。
	if err := validateTopoID(updated.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTopologyPayload(updated); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := r.store.UpdateTopology(updated); err != nil {
		// 非法 IP 配置 / 非法拓扑 ID 属客户端错误，返回 400；其余归为 500。
		if errors.Is(err, topology.ErrInvalidIPConfig) || errors.Is(err, storage.ErrInvalidTopoID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	// 拓扑整体更新可能改变设备接口/配置，失效所有相关设备的 CLI 缓存，
	// 下次 CLI 访问会从更新后的拓扑重新构建，避免显示过期接口/ARP。
	for deviceId := range updated.Devices {
		r.dropCLIState(deviceId)
	}
	// 把最新拓扑同步给已存在的仿真引擎，使 Ping/路径计算反映本次更新（B1）。
	r.syncEngine(id)
	c.JSON(http.StatusOK, updated)
}

func (r *Router) deleteTopology(c *gin.Context) {
	id := c.Param("id")

	t, err := r.store.GetTopology(id)
	if err == nil && t != nil {
		for deviceId := range t.Devices {
			r.dropCLIState(deviceId)
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

	// 先收集所有设备，便于后续依据链路做邻接 IP 分配。
	devices := make(map[string]*topology.Device, len(req.Nodes))
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
		devices[nodeID] = device
	}

	// 安全校验：节点 ID / 类型必须合法，链路两端必须引用已存在的节点，
	// 拒绝悬空链路导致引擎在 BFS / 转发时取到 nil 设备。
	for _, node := range req.Nodes {
		if err := validateIdent(node["id"], maxIdentLen); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid node id: %v", err)})
			return
		}
		if !IsValidDeviceType(topology.DeviceType(node["type"])) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid device type %q", node["type"])})
			return
		}
	}
	for _, link := range req.Links {
		if _, ok := devices[link.SourceDevice]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("link references unknown source device %q", link.SourceDevice)})
			return
		}
		if _, ok := devices[link.TargetDevice]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("link references unknown target device %q", link.TargetDevice)})
			return
		}
	}

	// 1) 为每台路由器的每个接口分配独立 /24 子网，网关取 .1。
	//    记录 (设备, 端口) -> 网关IP，供终端设备的邻接分配复用。
	subnetSeq := 2 // 从 192.168.2.0/24 起，避免与常用 192.168.1.0 冲突
	routerPortGW := make(map[string]string)
	for _, device := range devices {
		if device.Type != topology.DeviceRouter {
			continue
		}
		for ifName := range device.Interfaces {
			subnet := subnetSeq
			subnetSeq++
			gw := fmt.Sprintf("192.168.%d.1", subnet)
			device.Interfaces[ifName].IPAddress = gw
			device.Interfaces[ifName].SubnetMask = "255.255.255.0"
			device.Interfaces[ifName].Status = "up"
			routerPortGW[device.ID+"/"+ifName] = gw
		}
	}

	// 2) 终端（PC/Server）：依据链路找到上游路由器端口，把终端放入同一子网并设网关。
	//    找不到路由器邻居时，退化为独立 /24，保证至少有可达地址。
	// subnetHost 记录每个子网已分配的主机数，保证「多台终端接在同一路由器端口」
	// 这种同子网场景下主机位 .2/.3/.4… 递增，不会把同一 IP 分给两台终端。
	subnetHost := make(map[int]int)
	for _, device := range devices {
		if device.Type != topology.DevicePC && device.Type != topology.DeviceServer {
			continue
		}
		gw := ""
		targetIface := firstInterfaceName(device)
		for _, link := range req.Links {
			var neighborDev, neighborPort, localPort string
			if link.SourceDevice == device.ID {
				neighborDev, neighborPort, localPort = link.TargetDevice, link.TargetPort, link.SourcePort
			} else if link.TargetDevice == device.ID {
				neighborDev, neighborPort, localPort = link.SourceDevice, link.SourcePort, link.TargetPort
			} else {
				continue
			}
			if g := routerPortGW[neighborDev+"/"+neighborPort]; g != "" {
				gw = g
				if localPort != "" {
					targetIface = localPort
				}
				break
			}
		}

		var subnet int
		if gw != "" {
			// 网关形如 192.168.<subnet>.1，从中取出子网第三段。
			fmt.Sscanf(gw, "192.168.%d.1", &subnet)
		} else {
			// 无路由器邻居：从 subnetSeq 继续分配独立 /24，避免与路由器已用子网重叠。
			subnet = subnetSeq
			subnetSeq++
		}
		// 同一子网内递增主机位（网关占 .1，主机从 .2 起），防止重复 IP。
		subnetHost[subnet]++
		host := subnetHost[subnet] + 1
		if targetIface == "" {
			// 无接口可配（极端情况），跳过避免 panic。
			continue
		}
		iface, ok := device.Interfaces[targetIface]
		if !ok {
			// 链路端口名与默认接口不一致时，退回第一个接口。
			targetIface = firstInterfaceName(device)
			iface, ok = device.Interfaces[targetIface]
			if !ok {
				continue
			}
		}
		iface.IPAddress = fmt.Sprintf("192.168.%d.%d", subnet, host)
		iface.SubnetMask = "255.255.255.0"
		iface.Status = "up"
		iface.Gateway = gw
		device.Interfaces[targetIface] = iface
	}

	for _, device := range devices {
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

	// 若设备坐标全部堆叠在原点（导入/简化建图未给坐标），自动展开布局，
	// 避免画布上设备重叠成一团。已有合理坐标的拓扑（如手工编排的 VXLAN）
	// 不受影响。
	if t.NeedsAutoLayout() {
		t.AutoLayout()
	}

	if err := r.store.CreateTopology(t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 若此前已为该拓扑创建引擎（懒加载），把最新拓扑同步给它，闭合「编辑→仿真」。
	r.syncEngine(id)

	c.JSON(http.StatusCreated, CreateTopologyResponse{
		ID:          id,
		Name:        req.Name,
		DeviceCount: t.DeviceCount(),
		LinkCount:   t.LinkCount(),
		CreatedAt:   t.CreatedAt,
	})
}

// firstInterfaceName 返回设备接口集合中确定性的第一个接口名（按名称排序）。
// Go 的 map 遍历顺序随机，简化建图时为终端设备挑选分配 IP 的接口需确定性结果。
func firstInterfaceName(device *topology.Device) string {
	if len(device.Interfaces) == 0 {
		return ""
	}
	names := make([]string, 0, len(device.Interfaces))
	for name := range device.Interfaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

