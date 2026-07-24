package protocol

import (
	"net"
	"testing"
)

func TestVXLANProtocol(t *testing.T) {
	vxlan := NewVXLANProtocol()

	t.Run("SetVTEPIP", func(t *testing.T) {
		ip := net.ParseIP("10.0.0.1")
		vxlan.SetVTEPIP(ip)
		if !vxlan.VTEPIP.Equal(ip) {
			t.Errorf("Expected VTEP IP %s, got %v", ip, vxlan.VTEPIP)
		}
	})

	t.Run("CreateVSI", func(t *testing.T) {
		err := vxlan.CreateVSI("vpna", 5010)
		if err != nil {
			t.Errorf("Failed to create VSI: %v", err)
		}

		// 尝试创建重复的 VSI
		err = vxlan.CreateVSI("vpna", 5020)
		if err == nil {
			t.Error("Expected error for duplicate VSI")
		}
	})

	t.Run("BindInterface", func(t *testing.T) {
		err := vxlan.BindInterface("vpna", "GigabitEthernet0/0/1", "access")
		if err != nil {
			t.Errorf("Failed to bind interface: %v", err)
		}

		// 检查接口是否已绑定
		vsi, ok := vxlan.GetVSI("vpna")
		if !ok {
			t.Fatal("VSI not found")
		}
		if _, exists := vsi.Ports["GigabitEthernet0/0/1"]; !exists {
			t.Error("Interface not bound to VSI")
		}
	})

	t.Run("EnableEVPN", func(t *testing.T) {
		err := vxlan.EnableEVPN("vpna", "evpn-vxlan")
		if err != nil {
			t.Errorf("Failed to enable EVPN: %v", err)
		}

		vsi, ok := vxlan.GetVSI("vpna")
		if !ok {
			t.Fatal("VSI not found")
		}
		if vsi.EvpnEncap != "evpn-vxlan" {
			t.Errorf("Expected EVPN encap 'evpn-vxlan', got '%s'", vsi.EvpnEncap)
		}
	})

	t.Run("EnableDistributedGateway", func(t *testing.T) {
		err := vxlan.EnableDistributedGateway("vpna")
		if err != nil {
			t.Errorf("Failed to enable distributed gateway: %v", err)
		}

		vsi, ok := vxlan.GetVSI("vpna")
		if !ok {
			t.Fatal("VSI not found")
		}
		if !vsi.Distributed {
			t.Error("Distributed gateway not enabled")
		}
	})

	t.Run("CreateTunnel", func(t *testing.T) {
		remoteIP := net.ParseIP("10.0.0.2")
		vxlan.CreateTunnel(remoteIP, 5010)

		key := "10.0.0.2-5010"
		tunnel, ok := vxlan.Tunnels[key]
		if !ok {
			t.Fatal("Tunnel not created")
		}
		if tunnel.Status != "UP" {
			t.Errorf("Expected tunnel status UP, got %s", tunnel.Status)
		}
		if int(tunnel.VNI) != 5010 {
			t.Errorf("Expected VNI 5010, got %d", tunnel.VNI)
		}
	})

	t.Run("GetVSIs", func(t *testing.T) {
		vsis := vxlan.GetVSIs()
		if len(vsis) != 1 {
			t.Errorf("Expected 1 VSI, got %d", len(vsis))
		}
	})

	t.Run("GetTunnels", func(t *testing.T) {
		tunnels := vxlan.GetTunnels()
		if len(tunnels) != 1 {
			t.Errorf("Expected 1 tunnel, got %d", len(tunnels))
		}
	})
}

func TestMultipleVSIs(t *testing.T) {
	vxlan := NewVXLANProtocol()

	// 创建多个 VSI（对应 vpna、vpnb、vpnc）
	vsis := []struct {
		name string
		vni  int
	}{
		{"vpna", 5010},
		{"vpnb", 5020},
		{"vpnc", 5030},
	}

	for _, vsi := range vsis {
		err := vxlan.CreateVSI(vsi.name, vsi.vni)
		if err != nil {
			t.Fatalf("Failed to create VSI %s: %v", vsi.name, err)
		}
	}

	// 为每个 VSI 配置 EVPN 和分布式网关
	for _, v := range vsis {
		vxlan.EnableEVPN(v.name, "evpn-vxlan")
		vxlan.EnableDistributedGateway(v.name)
	}

	// 创建隧道
	vtepIPs := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	vxlan.SetVTEPIP(net.ParseIP(vtepIPs[0]))
	for j := 1; j < len(vtepIPs); j++ {
		vxlan.CreateTunnel(net.ParseIP(vtepIPs[j]), 5010)
	}

	// 验证所有 VSI 和隧道
	allVSIs := vxlan.GetVSIs()
	if len(allVSIs) != 3 {
		t.Errorf("Expected 3 VSIs, got %d", len(allVSIs))
	}

	allTunnels := vxlan.GetTunnels()
	expectedTunnels := 2 // 从 10.0.0.1 到 10.0.0.2 和 10.0.0.3
	if len(allTunnels) != expectedTunnels {
		t.Errorf("Expected %d tunnels, got %d", expectedTunnels, len(allTunnels))
	}
}
