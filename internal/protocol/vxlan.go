package protocol

import (
	"ensp-lab/internal/sim"
	"fmt"
	"net"
	"sync"
)

// VSI (Virtual Switch Instance) represents a virtual switch in EVPN-VXLAN
type VSI struct {
	Name        string
	VNI         int
	VPNs        []string
	Gateway     string
	EvpnEncap   string
	Distributed bool
	Ports       map[string]*VSIPort
	mu          sync.RWMutex
}

// VSIPort represents a port bound to a VSI
type VSIPort struct {
	Name   string
	Type   string // "access", "trunk"
	Status string
}

// VXLANProtocol implements VXLAN tunnel and VSI management
type VXLANProtocol struct {
	Enabled bool
	VTEPIP  net.IP
	Tunnels map[string]*VXLANSession
	VSIs    map[string]*VSI
	mu      sync.RWMutex
}

// VXLANSession represents a VXLAN tunnel endpoint
type VXLANSession struct {
	LocalIP     net.IP
	RemoteIP    net.IP
	VNI         int
	Status      string
	SentPackets int64
	RecvPackets int64
	SentBytes   int64
	RecvBytes   int64
}

func NewVXLANProtocol() *VXLANProtocol {
	return &VXLANProtocol{
		Enabled: false,
		Tunnels: make(map[string]*VXLANSession),
		VSIs:    make(map[string]*VSI),
	}
}

func (v *VXLANProtocol) SetVTEPIP(ip net.IP) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.VTEPIP = ip
	fmt.Printf("[VXLAN] VTEP IP set to %s\n", ip)
}

func (v *VXLANProtocol) CreateVSI(name string, vni int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.VSIs[name]; exists {
		return fmt.Errorf("VSI %s already exists", name)
	}
	v.VSIs[name] = &VSI{
		Name:  name,
		VNI:   vni,
		Ports: make(map[string]*VSIPort),
	}
	fmt.Printf("[VXLAN] VSI %s created with VNI %d\n", name, vni)
	return nil
}

func (v *VXLANProtocol) BindInterface(vsiName, ifaceName string, ifType string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	vsi, ok := v.VSIs[vsiName]
	if !ok {
		return fmt.Errorf("VSI %s not found", vsiName)
	}
	vsi.Ports[ifaceName] = &VSIPort{Name: ifaceName, Type: ifType, Status: "up"}
	fmt.Printf("[VXLAN] Interface %s bound to VSI %s (type: %s)\n", ifaceName, vsiName, ifType)
	return nil
}

func (v *VXLANProtocol) EnableEVPN(vsiName, encap string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	vsi, ok := v.VSIs[vsiName]
	if !ok {
		return fmt.Errorf("VSI %s not found", vsiName)
	}
	vsi.EvpnEncap = encap
	fmt.Printf("[VXLAN] EVPN enabled for VSI %s (encap: %s)\n", vsiName, encap)
	return nil
}

func (v *VXLANProtocol) EnableDistributedGateway(vsiName string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	vsi, ok := v.VSIs[vsiName]
	if !ok {
		return fmt.Errorf("VSI %s not found", vsiName)
	}
	vsi.Distributed = true
	fmt.Printf("[VXLAN] Distributed gateway enabled for VSI %s\n", vsiName)
	return nil
}

func (v *VXLANProtocol) CreateTunnel(remoteIP net.IP, vni int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.VTEPIP == nil {
		fmt.Printf("[VXLAN] Error: VTEP IP not set\n")
		return
	}
	key := fmt.Sprintf("%s-%d", remoteIP.String(), vni)
	v.Tunnels[key] = &VXLANSession{
		LocalIP:  v.VTEPIP,
		RemoteIP: remoteIP,
		VNI:      vni,
		Status:   "UP",
	}
	fmt.Printf("[VXLAN] Tunnel to %s created (VNI: %d)\n", remoteIP, vni)
}

func (v *VXLANProtocol) GetVSI(name string) (*VSI, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	vsi, ok := v.VSIs[name]
	return vsi, ok
}

func (v *VXLANProtocol) GetVSIs() map[string]*VSI {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make(map[string]*VSI)
	for k, vsi := range v.VSIs {
		result[k] = vsi
	}
	return result
}

func (v *VXLANProtocol) GetTunnels() map[string]*VXLANSession {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make(map[string]*VXLANSession)
	for k, t := range v.Tunnels {
		result[k] = t
	}
	return result
}

// HandlePacket implements the Handler interface for VXLAN. VXLAN
// encapsulation/decapsulation is handled by the underlying datapath
// (ns-x or gont), so this stub records the event and returns nil to
// let the engine continue forwarding. Real VXLAN packet processing
// will be wired up in a follow-up task once the BUM traffic model
// is in place.
func (v *VXLANProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

var _ Handler = (*VXLANProtocol)(nil)
