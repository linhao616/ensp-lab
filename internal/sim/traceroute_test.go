package sim

import (
	"testing"

	"ensp-lab/internal/topology"
)

// newThreeHopTopo 构造 r1—sw1—r2—sw2—pc1 的多跳拓扑，用于验证 tracert 的
// 真实逐跳路径发现（应发现 4 跳：sw1, r2, sw2, pc1），而非硬编码 2 跳。
func newThreeHopTopo() *topology.Topology {
	t := topology.NewTopology("th", "three-hop")
	mk := func(id, name string, dt topology.DeviceType, ip string) *topology.Device {
		d := &topology.Device{ID: id, Name: name, Type: dt, Status: topology.StatusRunning}
		d.InitializeDefaults()
		if ip != "" {
			iface := d.Interfaces["Ethernet0"]
			if iface == nil {
				iface = &topology.Interface{}
				d.Interfaces["Ethernet0"] = iface
			}
			iface.IPAddress = ip
			iface.SubnetMask = "255.255.255.0"
			iface.Status = "up"
		}
		return d
	}
	r1 := mk("r1", "R1", topology.DeviceRouter, "10.0.1.1")
	sw1 := mk("sw1", "SW1", topology.DeviceSwitch, "")
	r2 := mk("r2", "R2", topology.DeviceRouter, "10.0.2.1")
	sw2 := mk("sw2", "SW2", topology.DeviceSwitch, "")
	pc1 := mk("pc1", "PC1", topology.DevicePC, "10.0.3.2")

	t.AddDevice(r1)
	t.AddDevice(sw1)
	t.AddDevice(r2)
	t.AddDevice(sw2)
	t.AddDevice(pc1)
	t.AddLink(&topology.Link{ID: "l1", SourceDevice: "r1", SourcePort: "GigabitEthernet0/0/0", TargetDevice: "sw1", TargetPort: "GigabitEthernet0/0/1", LinkType: topology.LinkTypeBusiness, Delay: 1})
	t.AddLink(&topology.Link{ID: "l2", SourceDevice: "sw1", SourcePort: "GigabitEthernet0/0/2", TargetDevice: "r2", TargetPort: "GigabitEthernet0/0/0", LinkType: topology.LinkTypeBusiness, Delay: 2})
	t.AddLink(&topology.Link{ID: "l3", SourceDevice: "r2", SourcePort: "GigabitEthernet0/0/1", TargetDevice: "sw2", TargetPort: "GigabitEthernet0/0/1", LinkType: topology.LinkTypeBusiness, Delay: 3})
	t.AddLink(&topology.Link{ID: "l4", SourceDevice: "sw2", SourcePort: "GigabitEthernet0/0/2", TargetDevice: "pc1", TargetPort: "Ethernet0", LinkType: topology.LinkTypeBusiness, Delay: 1})
	return t
}

// TestNSxTraceroutePath 验证 P0-B：tracert 在真实拓扑上做 BFS 路径发现，
// 返回完整多跳路径（而非硬编码 2 跳），并最终 Reached 目标。
func TestNSxTraceroutePath(t *testing.T) {
	eng, err := NewNSxEngine(newThreeHopTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()

	res, err := eng.Traceroute("r1", "10.0.3.2", 30)
	if err != nil {
		t.Fatalf("Traceroute: %v", err)
	}
	if !res.Reached {
		t.Fatalf("expected Reached=true, got hops=%v", res.Hops)
	}
	wantHops := []string{"sw1", "r2", "sw2", "pc1"}
	if len(res.Hops) != len(wantHops) {
		t.Fatalf("expected %d hops %v, got %d hops %v", len(wantHops), wantHops, len(res.Hops), res.Hops)
	}
	for i, want := range wantHops {
		if res.Hops[i].DeviceID != want {
			t.Errorf("hop %d: want %s, got %s", i+1, want, res.Hops[i].DeviceID)
		}
	}
	// 末跳必须是目标设备 pc1。
	if last := res.Hops[len(res.Hops)-1]; last.DeviceID != "pc1" {
		t.Errorf("last hop should be pc1, got %s", last.DeviceID)
	}
}

// TestNSxTracerouteUnreachable 验证 P0-B：目标 IP 不在拓扑中时如实返回
// 不可达（Hops 为空、Reached=false），不再伪造固定 2 跳。
func TestNSxTracerouteUnreachable(t *testing.T) {
	eng, err := NewNSxEngine(newThreeHopTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()

	res, err := eng.Traceroute("r1", "203.0.113.99", 30)
	if err != nil {
		t.Fatalf("Traceroute: %v", err)
	}
	if res.Reached {
		t.Fatalf("expected Reached=false for unknown target, got %+v", res)
	}
	if len(res.Hops) != 0 {
		t.Fatalf("expected no hops for unreachable target, got %v", res.Hops)
	}
}

// TestNSxTracerouteSelf 验证 P0-B：ping/tracert 自身接口时直接返回 1 跳到自身。
func TestNSxTracerouteSelf(t *testing.T) {
	eng, err := NewNSxEngine(newThreeHopTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()

	res, err := eng.Traceroute("r1", "10.0.1.1", 30)
	if err != nil {
		t.Fatalf("Traceroute: %v", err)
	}
	if !res.Reached || len(res.Hops) != 1 || res.Hops[0].DeviceID != "r1" {
		t.Fatalf("expected 1 hop to self r1, got %+v", res)
	}
}
