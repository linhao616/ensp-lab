package api

import (
	"time"

	"ensp-lab/internal/topology"
)

// CreateDefaultTopology 构建开箱引导示例拓扑：1 台交换机 + 2 台 PC 的 VLAN 入门场景。
//
// 取代原先的空壳 default 拓扑——新用户首次启动即看到可操作的示例，而非空白画布。
//
// 设计约定：
//   - PC1/PC2 预置同网段 IP（192.168.1.0/24），默认同处 VLAN 1，可直接练习 ping 互通。
//   - 不预置交换机 VLAN 配置，保持为「练习模板」：用户按引导标注自行敲命令体会 VLAN 隔离，
//     避免预置状态与 lite 引擎实际行为（诚实占位）产生不一致的解读。
//   - 拓扑下方附「VLAN 入门引导」标注，给出完整配置命令与三段式验证（协议→哪里到哪通→PC 验证）。
func CreateDefaultTopology() *topology.Topology {
	now := time.Now()
	topo := topology.NewTopology("default", "VLAN 入门引导示例（1 交换机 + 2 PC）")
	topo.Description = "开箱引导示例：一台 S5700 交换机下挂两台 PC。" +
		"默认两 PC 同处 VLAN 1、同网段 192.168.1.0/24，可直接互通；" +
		"按下方引导标注练习划分 VLAN 10/20 后，跨 VLAN 二层隔离，直观体会 VLAN 的广播域分割作用。"
	topo.Devices = make(map[string]*topology.Device)

	// ──────────────────────────── 交换机 SW1 ────────────────────────────
	sw := &topology.Device{
		ID:         "sw1",
		Name:       "SW1",
		Type:       topology.DeviceSwitch,
		Model:      "S5700-28P-LI",
		Status:     topology.StatusRunning,
		PositionX:  500,
		PositionY:  300,
		Interfaces: make(map[string]*topology.Interface),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	sw.InitializeDefaults()
	// 标注两个 PC 接入端口为已连线（up），便于直接练习。
	for _, p := range []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2"} {
		if iface, ok := sw.Interfaces[p]; ok {
			iface.Status = "up"
		}
	}
	topo.Devices[sw.ID] = sw

	// ──────────────────────────── PC1 / PC2 ────────────────────────────
	pcSpecs := []struct {
		id, name, ip, mask, gw, port string
		x                          float64
	}{
		{"pc1", "PC1", "192.168.1.10", "255.255.255.0", "192.168.1.1", "Ethernet0", 200},
		{"pc2", "PC2", "192.168.1.20", "255.255.255.0", "192.168.1.1", "Ethernet0", 800},
	}
	for _, spec := range pcSpecs {
		topo.Devices[spec.id] = &topology.Device{
			ID: spec.id, Name: spec.name,
			Type:      topology.DevicePC,
			Model:     "PC",
			Status:    topology.StatusRunning,
			PositionX: spec.x,
			PositionY: 300,
			Interfaces: map[string]*topology.Interface{
				spec.port: {
					Name: spec.port, IPAddress: spec.ip, SubnetMask: spec.mask,
					Gateway: spec.gw, Status: "up", Bandwidth: 1000,
				},
			},
			CreatedAt: now, UpdatedAt: now,
		}
	}

	// ──────────────────────────── 链路：PC ↔ SW 接入 ────────────────────────────
	topo.Links = []*topology.Link{
		{
			ID: "link-pc1-sw1", SourceDevice: "pc1", SourcePort: "Ethernet0",
			TargetDevice: "sw1", TargetPort: "GigabitEthernet0/0/1",
			LinkType: topology.LinkTypeBusiness, CableType: topology.PortCopper,
			Bandwidth: 1000, Status: "up", CreatedAt: now,
			Subnet: "192.168.1.0/24",
		},
		{
			ID: "link-pc2-sw1", SourceDevice: "pc2", SourcePort: "Ethernet0",
			TargetDevice: "sw1", TargetPort: "GigabitEthernet0/0/2",
			LinkType: topology.LinkTypeBusiness, CableType: topology.PortCopper,
			Bandwidth: 1000, Status: "up", CreatedAt: now,
			Subnet: "192.168.1.0/24",
		},
	}

	// ──────────────────────────── 引导标注 ────────────────────────────
	guide := "【VLAN 入门引导】\n" +
		"────────────────────────\n" +
		"本拓扑：SW1 下挂 PC1、PC2，默认同处 VLAN 1、同网段 192.168.1.0/24。\n" +
		"\n" +
		"① 先验证互通（未划 VLAN 前）\n" +
		"  双击 PC1 打开终端，执行：\n" +
		"  ping 192.168.1.20   （应通：PC1↔PC2 同广播域）\n" +
		"\n" +
		"② 在 SW1 上划分 VLAN，隔离两台 PC\n" +
		"  system-view\n" +
		"  vlan batch 10 20\n" +
		"  interface GigabitEthernet0/0/1\n" +
		"   port link-type access\n" +
		"   port default vlan 10\n" +
		"  interface GigabitEthernet0/0/2\n" +
		"   port link-type access\n" +
		"   port default vlan 20\n" +
		"  return\n" +
		"\n" +
		"③ 再验证隔离（划 VLAN 后，预期二层不通）\n" +
		"  PC1 终端：ping 192.168.1.20  （不同 VLAN 二层隔离）\n" +
		"\n" +
		"原理：VLAN 将一台交换机按端口逻辑分割成多个广播域；\n" +
		"同 VLAN 二层互通，跨 VLAN 需三层设备（Vlanif/SVI）转发。"
	topo.Annotations = []*topology.TextAnnotation{
		{
			ID:        "default-vlan-guide",
			Text:      guide,
			PositionX: 500,
			PositionY: 560,
			CreatedAt: now,
		},
	}

	return topo
}
