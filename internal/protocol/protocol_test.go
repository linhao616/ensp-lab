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
	chainTopo := newChainTopo()
	sim := NewProtocolSimulator(chainTopo)

	// A 经 B 可达 C
	if !sim.CheckReachability("A", "C", "", "", chainTopo) {
		t.Error("expected A->C reachable via B")
	}
	// A 与自身可达
	if !sim.CheckReachability("A", "A", "", "", chainTopo) {
		t.Error("expected A->A reachable")
	}
	// 未知设备 -> 不可达（边界处理）
	if sim.CheckReachability("A", "Z", "", "", chainTopo) {
		t.Error("expected unknown device Z unreachable")
	}
	// 空设备 -> 不可达（边界处理）
	if sim.CheckReachability("", "A", "", "", chainTopo) {
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
	if simIso.CheckReachability("X", "Y", "", "", isolated) {
		t.Error("expected X->Y unreachable in isolated topology")
	}
}

// TestCheckReachabilityMultiTopoIndependence 验证 T0 修复后，CheckReachability 使用
// 调用方显式传入的 topo，不受其他拓扑影响——即便两个拓扑含有同名 deviceID（多拓扑
// deviceID 碰撞场景），结果仍由各拓扑自身拓扑图独立决定（不会串扰）。
func TestCheckReachabilityMultiTopoIndependence(t *testing.T) {
	// topoA：A-B 相连（A 经 B 可达 B）。
	topoA := topology.NewTopology("A", "A")
	for _, id := range []string{"A", "B"} {
		d := &topology.Device{ID: id, Name: id, Type: topology.DeviceRouter}
		d.InitializeDefaults()
		topoA.AddDevice(d)
	}
	topoA.AddLink(&topology.Link{
		ID: "lA", SourceDevice: "A", SourcePort: "G0/0/0",
		TargetDevice: "B", TargetPort: "G0/0/0", LinkType: topology.LinkTypeBusiness,
	})

	// topoB：同样含设备 A、B，但二者不相连（A 不可达 B）。
	topoB := topology.NewTopology("B", "B")
	for _, id := range []string{"A", "B"} {
		d := &topology.Device{ID: id, Name: id, Type: topology.DeviceRouter}
		d.InitializeDefaults()
		topoB.AddDevice(d)
	}

	sim := NewProtocolSimulator(topoA)

	// 同一组 deviceID，在 topoA 下可达。
	if !sim.CheckReachability("A", "B", "", "", topoA) {
		t.Error("topoA: expected A->B reachable")
	}
	// 同一组 deviceID，在 topoB（无链路）下不可达——证明不被 topoA 串扰。
	if sim.CheckReachability("A", "B", "", "", topoB) {
		t.Error("topoB: expected A->B unreachable (independent of topoA)")
	}
	// nil topo 防御分支。
	if sim.CheckReachability("A", "B", "", "", nil) {
		t.Error("nil topo: expected unreachable")
	}
}
