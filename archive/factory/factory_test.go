//go:build ignore

package factory

import (
	"testing"

	"ensp-lab/internal/protocol"
	"ensp-lab/internal/topology"

	"github.com/stretchr/testify/assert"
)

func TestVTEPCreate(t *testing.T) {
	t.Run("basic creation", func(t *testing.T) {
		f := NewFactory()
		dev, err := f.Create(topology.DeviceVTEP, DeviceSpec{
			ID:   "test-vtep-1",
			Name: "Test-VTEP",
		})

		assert.NoError(t, err)
		assert.NotNil(t, dev)
		assert.Equal(t, topology.DeviceVTEP, dev.Type)
		assert.Equal(t, "VTEP", dev.Model)
		assert.Equal(t, "VRP5", dev.VRPVersion)
	})
}

func TestVTEPInterfaces(t *testing.T) {
	t.Run("interface count and names", func(t *testing.T) {
		f := NewFactory()
		dev, err := f.Create(topology.DeviceVTEP, DeviceSpec{
			ID: "test-vtep-ifaces",
		})
		assert.NoError(t, err)
		assert.NotNil(t, dev)

		// Verify interface count is 4
		assert.Len(t, dev.Interfaces, 4)

		// Verify interface names
		expectedInterfaces := []string{
			"GigabitEthernet0/0/1",
			"GigabitEthernet0/0/2",
			"GigabitEthernet0/0/3",
			"GigabitEthernet0/0/4",
		}
		for _, ifName := range expectedInterfaces {
			_, exists := dev.Interfaces[ifName]
			assert.True(t, exists, "Expected interface %s to exist", ifName)
		}

		// Verify all interfaces are initially "down"
		for name, iface := range dev.Interfaces {
			assert.Equal(t, "down", iface.Status, "Interface %s should be down initially", name)
		}
	})
}

func TestVTEPIDUniqueness(t *testing.T) {
	t.Run("duplicate ID returns error", func(t *testing.T) {
		f := NewFactory()

		// Create first device with specific ID
		dev1, err := f.Create(topology.DeviceVTEP, DeviceSpec{
			ID: "duplicate-vtep",
		})
		assert.NoError(t, err)
		assert.NotNil(t, dev1)

		// Try to create second device with same ID
		dev2, err := f.Create(topology.DeviceVTEP, DeviceSpec{
			ID: "duplicate-vtep",
		})
		assert.Error(t, err)
		assert.Nil(t, dev2)
		assert.Contains(t, err.Error(), "already used")
	})

	t.Run("auto-generated IDs are unique", func(t *testing.T) {
		f := NewFactory()

		// Create multiple devices without specifying IDs
		devices := make([]*topology.Device, 5)
		for i := 0; i < 5; i++ {
			dev, err := f.Create(topology.DeviceVTEP, DeviceSpec{})
			assert.NoError(t, err)
			assert.NotNil(t, dev)
			devices[i] = dev
		}

		// Verify all IDs are unique
		seenIDs := make(map[string]bool)
		for _, dev := range devices {
			assert.False(t, seenIDs[dev.ID], "Device ID %s should be unique", dev.ID)
			seenIDs[dev.ID] = true
		}
	})
}

func TestVXLANConfigDefaults(t *testing.T) {
	t.Run("vxlan config enabled defaults to false", func(t *testing.T) {
		// Create a topology with a VTEP device
		f := NewFactory()
		dev, err := f.Create(topology.DeviceVTEP, DeviceSpec{
			ID: "test-vtep-vxlan",
		})
		assert.NoError(t, err)
		assert.NotNil(t, dev)

		topo := topology.NewTopology("test-topo", "Test Topology")
		topo.AddDevice(dev)

		// Initialize protocol simulator and router state
		sim := protocol.NewProtocolSimulator(topo)
		sim.InitRouter(dev.ID)

		// Get router state and verify VXLAN config
		router, ok := sim.GetRouter(dev.ID)
		assert.True(t, ok)
		assert.NotNil(t, router.VXLAN)

		// Verify VXLAN.Enabled defaults to false
		assert.False(t, router.VXLAN.Enabled, "VXLAN.Enabled should default to false")
	})
}
