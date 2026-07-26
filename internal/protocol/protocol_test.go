package protocol

import (
	"testing"

	"ensp-lab/internal/topology"
)

// newMeshTopo 构造 A-B-C 链式拓扑（A 与 C 不直接相连，经 B 中转可达）。
func newChainTopo() *topology.Topology {
	t := topology.NewTopology("chain", "chain")
	for _, id := range []string{"A", "B", "C"} {
		d := &topology.Device{ID: id, Name: id, Type: topology.DeviceRouter}
		d.InitializeDefaults()
		t.AddDevice(d)
	}
	t.AddLink(&topology.Link{ID: "l1", SourceDevice: "A", SourcePort: "GigabitEthernet0/0/0", TargetDevice: "B", TargetPort: "GigabitEthernet0/0/0", LinkType: topology.LinkTypeBusiness})
	t.AddLink(&topology.Link{ID: "l2", SourceDevice: "B", SourcePort: "GigabitEthernet0/0/1", TargetDevice: "C", TargetPort: "GigabitEthernet0/0/0", LinkType: topology.LinkTypeBusiness})
	return t
}

// TestCheckReachability 验证 F3：CheckReachability 为真实 BFS，而非恒真桩。
func TestCheckReachability(t *testing.T) {
	sim := NewProtocolSimulator(newChainTopo())

	// A 经 B 可达 C
	if !sim.CheckReachability("A", "C", "", "") {
		t.Error("expected A->C reachable via B")
	}
	// A 与自身可达
	if !sim.CheckReachability("A", "A", "", "") {
		t.Error("expected A->A reachable")
	}
	// 未知设备 -> 不可达（边界处理）
	if sim.CheckReachability("A", "Z", "", "") {
		t.Error("expected unknown device Z unreachable")
	}
	// 空设备 -> 不可达（边界处理）
	if sim.CheckReachability("", "A", "", "") {
		t.Error("expected empty source unreachable")
	}

	// 独立拓扑：两台不相连设备不可达
	isolated := topology.NewTopology("iso", "iso")
	for _, id := range []string{"X", "Y"} {
		d := &topology.Device{ID: id, Name: id, Type: topology.DeviceRouter}
		d.InitializeDefaults()
		isolated.AddDevice(d)
	}
	simIso := NewProtocolSimulator(isolated)
	if simIso.CheckReachability("X", "Y", "", "") {
		t.Error("expected X->Y unreachable in isolated topology")
	}
}
