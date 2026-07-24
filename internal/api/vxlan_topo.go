package api

import (
	"fmt"
	"strings"
	"time"

	"ensp-lab/internal/topology"
)

// CreateVXLANTopology 构建 VXLAN Spine-Leaf 拓扑（eNSP 风格，标注紧贴设备）：
//
// 设计原则：
//   - 设备名称 / 机型 / 关键 IP 由前端画布直接渲染在设备图标旁（紧贴，不遮挡）
//   - 设备不再生成独立 HTML 标注框（避免大框漂浮、遮挡拓扑）
//   - VLAN↔VNI 映射与角色说明集中为拓扑下方的一个独立图例标注
//   - 四层垂直布局：Spine → Leaf/VTEP → Server → VM
//   - 对称端口映射：spine-i 连 leaf-k 时 spine 口=10GE{i}/0/{k}，leaf 口=10GE{k}/0/{i}
//   - 物理/接入链路端点标签（接口编号）由前端 drawLinks 紧贴连线渲染
//   - VXLAN/虚拟链路无物理端口，前端从设备边缘引出（edgePointToward）
//
// 拓扑规模常量（调整此两项即可缩放）：
//   - spineCount: Spine 核心交换机数量
//   - leafCount: Leaf VTEP 节点数量
func CreateVXLANTopology() *topology.Topology {
	now := time.Now()
	const (
		spineCount = 4 // spine-1 ~ spine-4
		leafCount  = 3 // leaf-1 ~ leaf-3 (VTEP)
		vni        = 5000
	)

	topo := topology.NewTopology("vxlan-spine-leaf", "VXLAN Spine-Leaf 组网")
	topo.Devices = make(map[string]*topology.Device)

	// ────────────────────────────────────────────────
	// 全局配置表（VLAN ↔ VNI 映射，用于标注层展示）
	// ────────────────────────────────────────────────
	vlanVniMap := map[int]int{
		10: vni, // VLAN 10 → VNI 5000（业务 VLAN A）
		20: vni, // VLAN 20 → VNI 5000（业务 VLAN B）
	}
	// LoopBack 地址池（每个 VTEP 一个唯一 /32 地址）
	leafLB := map[string]string{
		"leaf-1": "1.1.1.1",
		"leaf-2": "2.2.2.2",
		"leaf-3": "2.2.2.3",
	}

	// ══════════════════════════════════════════════
	// 第 1 层 — Spine：Underlay 核心交换机（循环生成）
	// ══════════════════════════════════════════════
	spineXBase := float64(200)
	spineY     := float64(60)
	spineXGap  := float64(280)
	for i := 1; i <= spineCount; i++ {
		id := fmt.Sprintf("spine-%d", i)
		lbIP := fmt.Sprintf("100.%d.%d.%d", i/100+1, i%100+1, i) // 唯一 Loopback
		topo.Devices[id] = &topology.Device{
			ID: id, Name: id,
			Type:      topology.DeviceSwitch,
			Model:     "CE12800", // 机型；Loopback 见节点外标注
			Status:    topology.StatusRunning,
			PositionX: spineXBase + float64(i-1)*spineXGap,
			PositionY: spineY,
			Interfaces: func() map[string]*topology.Interface {
				ifs := make(map[string]*topology.Interface)
				// 管理环回口（Underlay Router-ID / Loopback，标注层读取用）
			ifs["LoopBack0"] = &topology.Interface{
				Name: "LoopBack0", IPAddress: lbIP, SubnetMask: "255.255.255.255", Status: "up",
				Description: "LoopBack Interface",
			}
			for k := 1; k <= leafCount; k++ {
				portName := fmt.Sprintf("10GE%d/0/%d", i, k)
				ifs[portName] = &topology.Interface{
					Name: portName, Status: "up", Bandwidth: 10000,
					IPAddress:  fmt.Sprintf("10.%d.%d.2", k, i),
					SubnetMask: "255.255.255.0",
					Description: fmt.Sprintf("Connect to Leaf-%d", k),
				}
			}
				return ifs
			}(),
			CreatedAt: now, UpdatedAt: now,
		}
	}

	// ══════════════════════════════════════════════
	// 第 2 层 — Leaf / VTEP：VXLAN 隧道端点（循环生成）
	// ══════════════════════════════════════════════
	leafXBase := float64(120)
	leafY     := float64(220)
	leafXGap  := float64(340)
	for k := 1; k <= leafCount; k++ {
		id := fmt.Sprintf("leaf-%d", k)
		lb := leafLB[id]
		dev := &topology.Device{
			ID: id, Name: id,
			Type:      topology.DeviceVTEP,
			Model:     "CE6800", // 机型；VTEP/Loopback 见节点外标注
			Status:    topology.StatusRunning,
			PositionX: leafXBase + float64(k-1)*leafXGap,
			PositionY: leafY,
			Interfaces: map[string]*topology.Interface{
				// 管理环回口（VTEP Source IP）
				"LoopBack0": {Name: "LoopBack0", IPAddress: lb, SubnetMask: "255.255.255.255", Status: "up",
					Description: "LoopBack Interface"},
				// L3 网关接口（VBDIF / Vlanif 等价，跨 VLAN 路由用）
				"Vlanif10": {Name: "Vlanif10",
					IPAddress: fmt.Sprintf("10.0.10.%d", k), SubnetMask: "255.255.255.0",
					Status: "up", VLAN: 10, Description: "Vlanif10"},
				"Vlanif20": {Name: "Vlanif20",
					IPAddress: fmt.Sprintf("10.0.20.%d", k), SubnetMask: "255.255.255.0",
					Status: "up", VLAN: 20, Description: "Vlanif20"},
				// 接入侧端口（连接 server，trunk/access 混合）
				"10GE5/0/1": {Name: "10GE5/0/1", Status: "up", Bandwidth: 10000, VLAN: 10, Description: "Connect to server"}, // access VLAN 10
				"10GE5/0/2": {Name: "10GE5/0/2", Status: "up", Bandwidth: 10000, VLAN: 20, Description: "Connect to server"}, // access VLAN 20
			},
			CreatedAt: now, UpdatedAt: now,
		}
		// 上联全部 Spine（对称端口映射）
		for i := 1; i <= spineCount; i++ {
			port := fmt.Sprintf("10GE%d/0/%d", k, i)
			dev.Interfaces[port] = &topology.Interface{
				Name:        port,
				IPAddress:   fmt.Sprintf("10.%d.%d.1", k, i),
				SubnetMask:  "255.255.255.0",
				Status:      "up",
				Bandwidth:   10000,
				Description: fmt.Sprintf("Connect to Spine-%d", i),
			}
		}
		topo.Devices[id] = dev
	}

	// ══════════════════════════════════════════════
	// 第 3 层 — Server：接入交换机/服务器
	// ══════════════════════════════════════════════
	serverConfigs := []struct {
		id          string
		x           float64
		ips         []string // 多网卡场景
		vlans       []int
		connLeaf    string   // 上联 leaf
		connPort    string   // leaf 侧端口
		connSvrPort string   // server 侧端口
	}{
		// Server 仅 1 个物理网卡 Ethernet0（上联 Leaf），VM 通过内部 vSwitch 转发，不占用物理接口
		{"server-1", 160, []string{"10.0.10.100"}, []int{10}, "leaf-1", "10GE5/0/1", "Ethernet0"},
		{"server-2", 480, []string{"10.0.10.200"}, []int{10}, "leaf-2", "10GE5/0/1", "Ethernet0"},
		{"server-3", 800, []string{"10.0.10.30"}, []int{10}, "leaf-3", "10GE5/0/1", "Ethernet0"},
	}
	// 用 map 去重 server 设备定义
	serverMap := make(map[string]*topology.Device)
	for _, sc := range serverConfigs {
		if existing, ok := serverMap[sc.id]; ok {
			// 追加接口到已有 server
			existing.Interfaces[sc.connSvrPort] = &topology.Interface{
				Name: sc.connSvrPort, IPAddress: sc.ips[0],
				SubnetMask: "255.255.255.0", Status: "up", Bandwidth: 10000, VLAN: sc.vlans[0],
			}
			continue
		}
		ifs := map[string]*topology.Interface{}
		for j, ip := range sc.ips {
			portName := fmt.Sprintf("Ethernet%d", j)
			ifs[portName] = &topology.Interface{
				Name: portName, IPAddress: ip, SubnetMask: "255.255.255.0",
				Status: "up", Bandwidth: 10000, VLAN: sc.vlans[j],
			}
		}
		serverMap[sc.id] = &topology.Device{
			ID: sc.id, Name: sc.id,
			Type:      topology.DeviceServer,
			Model:     "RH2288H", // 机型；IP 见节点外标注
			Status:    topology.StatusRunning,
			PositionX: sc.x,
			PositionY: 400,
			Interfaces: ifs,
			CreatedAt: now, UpdatedAt: now,
		}
	}
	for id, dev := range serverMap {
		topo.Devices[id] = dev
	}

	// ══════════════════════════════════════════════
	// 第 4 层 — VM / PC：终端主机
	// ══════════════════════════════════════════════
	vmConfigs := []struct {
		id    string
		x     float64
		ip    string
		mask  string
		gw    string
		vlan  int
		svr   string // 所属 server
	}{
		{"vm-1", 110, "10.0.10.10", "255.255.255.0", "10.0.10.1", 10, "server-1"},
		{"vm-2", 240, "10.0.20.20", "255.255.255.0", "10.0.20.1", 20, "server-1"},
		{"vm-3", 440, "10.0.10.30", "255.255.255.0", "10.0.10.2", 10, "server-2"},
		{"vm-4", 840, "10.0.10.40", "255.255.255.0", "10.0.10.3", 10, "server-3"},
	}
	for _, vc := range vmConfigs {
		topo.Devices[vc.id] = &topology.Device{
			ID: vc.id, Name: vc.id,
			Type:      topology.DevicePC,
			Model:     "VM", // 机型；IP/GW 见节点外标注
			Status:    topology.StatusRunning,
			PositionX: vc.x,
			PositionY: 560,
			Interfaces: map[string]*topology.Interface{
				"Ethernet0": {
					Name: "Ethernet0", IPAddress: vc.ip, SubnetMask: vc.mask,
					Gateway: vc.gw, Status: "up", VLAN: vc.vlan, Bandwidth: 1000,
				},
			},
			CreatedAt: now, UpdatedAt: now,
		}
	}

	// ══════════════════════════════════════════════
	// 链路生成
	// ══════════════════════════════════════════════
	links := make([]*topology.Link, 0, spineCount*leafCount+16)

	// --- Underlay：Spine ↔ Leaf Clos 全互联（黑色实线） ---
	for i := 1; i <= spineCount; i++ {
		spineID := fmt.Sprintf("spine-%d", i)
		for k := 1; k <= leafCount; k++ {
			leafID := fmt.Sprintf("leaf-%d", k)
			links = append(links, &topology.Link{
				ID:           fmt.Sprintf("underlay-sp%d-l%d", i, k),
				SourceDevice: spineID,
				SourcePort:   fmt.Sprintf("10GE%d/0/%d", i, k),
				TargetDevice: leafID,
				TargetPort:   fmt.Sprintf("10GE%d/0/%d", k, i),
				LinkType:     topology.LinkTypeBusiness,
				CableType:     topology.PortFiber,
				Bandwidth:     10000,
				Status:        "up",
				CreatedAt:     now,
			})
		}
	}

	// --- Access：Leaf → Server（灰色虚线 + VLAN 标签）---
	accessLinks := []struct {
		id, srcDev, srcPort, tgtDev, tgtPort string
		vlan                                   int
	}{
		{"acc-l1-s1-v10", "leaf-1", "10GE5/0/1", "server-1", "Ethernet0", 10},
		{"acc-l1-s1-v20", "leaf-1", "10GE5/0/2", "server-1", "Ethernet0", 20},
		{"acc-l2-s2-v10", "leaf-2", "10GE5/0/1", "server-2", "Ethernet0", 10},
		{"acc-l3-s3-v10", "leaf-3", "10GE5/0/1", "server-3", "Ethernet0", 10},
	}
	for _, al := range accessLinks {
		links = append(links, &topology.Link{
			ID: al.id, SourceDevice: al.srcDev, SourcePort: al.srcPort,
			TargetDevice: al.tgtDev, TargetPort: al.tgtPort,
			LinkType: topology.LinkTypeBusiness, CableType: topology.PortCopper,
			Bandwidth: 10000, Status: "up", VLAN: al.vlan, CreatedAt: now,
		})
	}

	// --- Virtual：Server → VM（灰色虚线，有实际端口）---
	virtualLinks := []struct {
		id, srcDev, srcPort, tgtDev, tgtPort string
		vlan                                 int
	}{
		{"virt-s1-vm1", "server-1", "Ethernet0", "vm-1", "Ethernet0", 10},
		{"virt-s1-vm2", "server-1", "Ethernet0", "vm-2", "Ethernet0", 20},
		{"virt-s2-vm3", "server-2", "Ethernet0", "vm-3", "Ethernet0", 10},
		{"virt-s3-vm4", "server-3", "Ethernet0", "vm-4", "Ethernet0", 10},
	}
	for _, vl := range virtualLinks {
		links = append(links, &topology.Link{
			ID: vl.id, SourceDevice: vl.srcDev, SourcePort: vl.srcPort,
			TargetDevice: vl.tgtDev, TargetPort: vl.tgtPort,
			LinkType: topology.LinkTypeVirtual, CableType: topology.PortCopper,
			Bandwidth: 1000, VLAN: vl.vlan, Status: "up", CreatedAt: now,
		})
	}

	// --- Overlay VXLAN：Leaf ↔ Leaf 全互联（红色虚线 + VNI 标签，无端口名）---
	for a := 1; a <= leafCount; a++ {
		for b := a + 1; b <= leafCount; b++ {
			aID := fmt.Sprintf("leaf-%d", a)
			bID := fmt.Sprintf("leaf-%d", b)
			links = append(links, &topology.Link{
				ID:            fmt.Sprintf("vxlan-%s-%s", aID, bID),
				SourceDevice:   aID, SourcePort: "-",
				TargetDevice:   bID, TargetPort: "-",
				LinkType:      topology.LinkTypeBusiness,
				CableType:      topology.PortFiber,
				Bandwidth:      10000,
				Status:         "up",
				CreatedAt:      now,
				VXLANVNI:       vni,
				VXLANPeerList:  []string{leafLB[bID]},
			})
		}
	}
	topo.Links = links

	// ══════════════════════════════════════════════
	// TextAnnotation 标注层：仅保留一个独立于拓扑的图例框（底部），
	// 设备名称/机型/IP 改由前端画布紧贴设备渲染，不再生成设备级大框标注。
	// ══════════════════════════════════════════════
	annoIdx := 0
	addAnno := func(x, y float64, text string) {
		annoIdx++
		topo.Annotations = append(topo.Annotations, &topology.TextAnnotation{
			ID:        fmt.Sprintf("anno-%d", annoIdx),
			Text:      text,
			PositionX: x,
			PositionY: y,
			CreatedAt: now,
		})
	}

	topo.Annotations = []*topology.TextAnnotation{}

	// -- VXLAN 网络规划说明（拓扑右侧空白区，独立文本框） --
	planning := "╔══════════════════════════════════════╗\n" +
		"║    VXLAN Spine-Leaf 网络规划说明        ║\n" +
		"╠══════════════════════════════════════╣\n" +
		"║ 【网络架构】                           ║\n" +
		"║  · Underlay: OSPF + IBGP 基础互通      ║\n" +
		"║  · Overlay: VXLAN (BGP EVPN 信令)      ║\n" +
		"║  · 控制平面: BGP EVPN Type-2/3/5 路由   ║\n" +
		"║  · 数据平面: VXLAN 封装 (VNI 5000)     ║\n" +
		"╠══════════════════════════════════════╣\n" +
		"║ 【设备角色】                            ║\n" +
		"║  · Spine: Underlay 核心交换机 (L3)     ║\n" +
		"║  · Leaf: VTEP 端点 + L2/L3 网关       ║\n" +
		"║  · Server: 接入服务器 (vSwitch)         ║\n" +
		"║  · VM/PC: 业务终端                     ║\n" +
		"╠══════════════════════════════════════╣\n" +
		"║ 【地址规划】                            ║\n" +
		"║  · VTEP Loopback0: /32 环回口           ║\n" +
		"║    - leaf-1: 1.1.1.1                   ║\n" +
		"║    - leaf-2: 2.2.2.2                   ║\n" +
		"║    - leaf-3: 2.2.2.3                   ║\n" +
		"║  · Underlay互联: 10.x.y.z /24          ║\n" +
		"║  · VLAN 10 → VNI 5000 (业务网段A)      ║\n" +
		"║    - 网段: 10.10.x.0/24                ║\n" +
		"║  · VLAN 20 → VNI 5000 (业务网段B)      ║\n" +
		"║    - 网段: 10.20.x.0/24                ║\n" +
		"╠══════════════════════════════════════╣\n" +
		"║ 【链路规划】                            ║\n" +
		"║  · Spine↔Leaf: 10GE Clos 全互联       ║\n" +
		"║  · Leaf→Server: 10GE access/trunk      ║\n" +
		"║  · Server↔VM: vNIC 虚拟线             ║\n" +
		"║  · Leaf↔Leaf: VXLAN 隧道(Overlay)     ║\n" +
		"╠══════════════════════════════════════╣\n" +
		"║ 【VXLAN 参数】                          ║\n" +
		"║  · NVE接口: VNI 5000                  ║\n" +
		"║  · BD(Bridge Domain): VLAN 10,20       ║\n" +
		"║  · VBDIF: 三层网关接口                ║\n" +
		"║  · EVPN: RD/RT 自动生成               ║\n" +
		"╚══════════════════════════════════════╝"
	addAnno(1020, 50, planning)

	// -- 简明图例（规划框下方）--
	var vlanLines []string
	for vlan, v := range vlanVniMap {
		vlanLines = append(vlanLines, fmt.Sprintf("  VLAN %d → VNI %d", vlan, v))
	}
	legend := "图例 Legend\n" +
		"────────────────────────\n" +
		"【设备角色 / Device】\n" +
		"  Spine   : 核心交换机 L3\n" +
		"  Leaf    : VTEP 隧道端点\n" +
		"  Server  : 接入服务器\n" +
		"  VM/PC   : 业务终端\n" +
		"────────────────────────\n" +
		"【链路类型 / Link】\n" +
		"  ━━ 实线 : Underlay 物理\n" +
		"  ┄┄ 虚线 : Access 接入\n" +
		"  ┉┉ 红虚 : VXLAN 隧道\n" +
		"────────────────────────\n" +
		"【关键映射 / Map】\n" +
		strings.Join(vlanLines, "\n") + "\n" +
		"  VBDIF10/20 : 三层网关"
	addAnno(1020, 520, legend)

	// ══════════════════════════════════════════════
	// VXLAN ConfigData 预置（display vxlan 即显示已配通）
	// ══════════════════════════════════════════════
	for lid := 1; lid <= leafCount; lid++ {
		leafID := fmt.Sprintf("leaf-%d", lid)
		var peers []string
		for oid := 1; oid <= leafCount; oid++ {
			if oid != lid {
				peers = append(peers, leafLB[fmt.Sprintf("leaf-%d", oid)])
			}
		}
		if dev, ok := topo.Devices[leafID]; ok {
			dev.ConfigData = &topology.DeviceConfigData{
				DeviceName: dev.Name,
				Interfaces: map[string]string{
					"vxlan:vni":    fmt.Sprintf("%d", vni),
					"vxlan:source": leafLB[leafID],
					"vxlan:peer":   strings.Join(peers, ","),
				},
			}
		}
	}

	return topo
}
