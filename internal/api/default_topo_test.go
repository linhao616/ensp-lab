package api

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestCreateDefaultTopology 锁死开箱引导示例拓扑的结构契约：
// 3 设备（1 交换机 + 2 PC）、2 条接入链路、含 VLAN 引导标注、PC 预置同网段 IP。
func TestCreateDefaultTopology(t *testing.T) {
	topo := CreateDefaultTopology()

	if topo.ID != "default" {
		t.Fatalf("expected id 'default', got %q", topo.ID)
	}
	if got := len(topo.Devices); got != 3 {
		t.Fatalf("expected 3 devices, got %d", got)
	}
	sw, ok := topo.Devices["sw1"]
	if !ok {
		t.Fatal("missing switch sw1")
	}
	if sw.Type != topology.DeviceSwitch {
		t.Fatalf("sw1 type = %q, want switch", sw.Type)
	}
	for _, pcID := range []string{"pc1", "pc2"} {
		pc, ok := topo.Devices[pcID]
		if !ok {
			t.Fatalf("missing %s", pcID)
		}
		if pc.Type != topology.DevicePC {
			t.Fatalf("%s type = %q, want pc", pcID, pc.Type)
		}
		iface, ok := pc.Interfaces["Ethernet0"]
		if !ok || iface.IPAddress == "" {
			t.Fatalf("%s Ethernet0 missing IP", pcID)
		}
		// 两 PC 必须同网段 192.168.1.0/24（未划 VLAN 前可直接互通的练习前提）。
		if !strings.HasPrefix(iface.IPAddress, "192.168.1.") {
			t.Fatalf("%s IP %q not in 192.168.1.0/24", pcID, iface.IPAddress)
		}
	}

	if got := len(topo.Links); got != 2 {
		t.Fatalf("expected 2 links, got %d", got)
	}
	for _, l := range topo.Links {
		if l.SourceDevice == "" || l.TargetDevice == "" || l.SourcePort == "" || l.TargetPort == "" {
			t.Fatalf("link %q has empty endpoint", l.ID)
		}
	}

	if len(topo.Annotations) == 0 {
		t.Fatal("expected VLAN guide annotation")
	}
	if !strings.Contains(topo.Annotations[0].Text, "VLAN 入门引导") {
		t.Fatal("VLAN guide annotation text missing")
	}
}
