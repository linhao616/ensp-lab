package sim

import (
	"testing"

	"ensp-lab/internal/testutil"
	"ensp-lab/internal/topology"
)

// Verify at compile time that nsxEngine implements Engine.
var _ Engine = (*nsxEngine)(nil)

func TestNewNSxEngine_NilTopology(t *testing.T) {
	t.Parallel()
	_, err := NewNSxEngine(nil)
	if err == nil {
		t.Fatal("expected error for nil topology, got nil")
	}
}

func TestNewNSxEngine_EmptyTopology(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("empty", "Empty")
	eng, err := NewNSxEngine(topo)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	if eng == nil {
		t.Fatal("engine is nil")
	}
	testutil.EnsureEngineCleanup(t, eng)
	if mode := eng.(*nsxEngine).Mode(); mode != string(EngineModeNSX) {
		t.Fatalf("Mode = %q, want %q", mode, EngineModeNSX)
	}
}

func TestNSxEngine_AddPacketListener(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("t", "T")
	eng, err := NewNSxEngine(topo)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	testutil.EnsureEngineCleanup(t, eng)
	eng.AddPacketListener(func(*PacketEvent) {})
	eng.AddPacketListener(func(*PacketEvent) {})
}

func TestNSxEngine_EventsChannelNonNil(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("t", "T")
	eng, _ := NewNSxEngine(topo)
	testutil.EnsureEngineCleanup(t, eng)
	if eng.Events() == nil {
		t.Fatal("Events() returned nil channel")
	}
}

func TestNSxEngine_PingUnknownDevice(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("t", "T")
	eng, _ := NewNSxEngine(topo)
	testutil.EnsureEngineCleanup(t, eng)
	_, err := eng.Ping("missing", "10.0.0.1")
	if err == nil {
		t.Fatal("expected error for unknown device, got nil")
	}
}

func TestNSxEngine_PingDirectConnected(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("ping-test", "Ping Test")

	h1 := &topology.Device{ID: "h1", Name: "Host1", Type: topology.DevicePC}
	h1.InitializeDefaults()
	h1.Interfaces["Ethernet0"].IPAddress = "10.0.0.1"
	h1.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	h1.Interfaces["Ethernet0"].Status = "up"
	topo.AddDevice(h1)

	h2 := &topology.Device{ID: "h2", Name: "Host2", Type: topology.DevicePC}
	h2.InitializeDefaults()
	h2.Interfaces["Ethernet0"].IPAddress = "10.0.0.2"
	h2.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	h2.Interfaces["Ethernet0"].Status = "up"
	topo.AddDevice(h2)

	topo.AddLink(&topology.Link{
		ID:           "link-h1-h2",
		SourceDevice: "h1",
		SourcePort:   "Ethernet0",
		TargetDevice: "h2",
		TargetPort:   "Ethernet0",
		LinkType:     topology.LinkTypeBusiness,
		CableType:    topology.PortCopper,
	})

	eng, err := NewNSxEngine(topo)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	testutil.EnsureEngineCleanup(t, eng)
	eng.Start()
	defer eng.Stop()

	result, err := eng.Ping("h1", "10.0.0.2")
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if result.Received != 1 {
		t.Errorf("Expected 1 received, got %d: %v", result.Received, result.Details)
	}
}

func TestNSxEngine_PingMultiHop(t *testing.T) {
	t.Parallel()
	topo := topology.NewTopology("multihop-test", "Multi-hop Test")

	h1 := &topology.Device{ID: "h1", Name: "Host1", Type: topology.DevicePC}
	h1.InitializeDefaults()
	h1.Interfaces["Ethernet0"].IPAddress = "10.0.0.1"
	h1.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	h1.Interfaces["Ethernet0"].Status = "up"
	topo.AddDevice(h1)

	switch1 := &topology.Device{ID: "switch1", Name: "Switch1", Type: topology.DeviceSwitch}
	switch1.InitializeDefaults()
	topo.AddDevice(switch1)

	h2 := &topology.Device{ID: "h2", Name: "Host2", Type: topology.DevicePC}
	h2.InitializeDefaults()
	h2.Interfaces["Ethernet0"].IPAddress = "10.0.0.2"
	h2.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	h2.Interfaces["Ethernet0"].Status = "up"
	topo.AddDevice(h2)

	topo.AddLink(&topology.Link{
		ID:           "link-h1-sw",
		SourceDevice: "h1",
		SourcePort:   "Ethernet0",
		TargetDevice: "switch1",
		TargetPort:   "GigabitEthernet0/0/1",
		LinkType:     topology.LinkTypeBusiness,
		CableType:    topology.PortCopper,
	})

	topo.AddLink(&topology.Link{
		ID:           "link-sw-h2",
		SourceDevice: "switch1",
		SourcePort:   "GigabitEthernet0/0/2",
		TargetDevice: "h2",
		TargetPort:   "Ethernet0",
		LinkType:     topology.LinkTypeBusiness,
		CableType:    topology.PortCopper,
	})

	eng, err := NewNSxEngine(topo)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	testutil.EnsureEngineCleanup(t, eng)
	eng.Start()
	defer eng.Stop()

	result, err := eng.Ping("h1", "10.0.0.2")
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if result.Received != 1 {
		t.Errorf("Expected 1 received, got %d: %v", result.Received, result.Details)
	}
}
