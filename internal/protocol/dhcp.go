package protocol

import (
	"fmt"
	"net"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type DHCPPool struct {
	Name        string
	Network     string
	Mask        string
	StartIP     string
	EndIP       string
	Gateway     string
	DNS         []string
	LeaseTime   int
	ExcludedIPs []string
	AssignedIPs map[string]*DHCPLease
}

type DHCPLease struct {
	IP         string
	MAC        string
	ClientID   string
	Hostname   string
	LeaseTime  int
	Remaining  int
	AssignedAt time.Time
	ExpiresAt  time.Time
	Status     string
}

type DHCPProtocol struct {
	Enabled      bool
	DeviceID     string
	Pools        map[string]*DHCPPool
	RelayEnabled bool
	RelayAgentIP string
	ServerIP     string
	Requests     int
	Offers       int
	ACKs         int
	NACKs        int
	Declines     int
	mu           sync.RWMutex
}

func NewDHCPProtocol(deviceID string) *DHCPProtocol {
	return &DHCPProtocol{
		Enabled:      false,
		DeviceID:     deviceID,
		Pools:        make(map[string]*DHCPPool),
		RelayEnabled: false,
		Requests:     0,
		Offers:       0,
		ACKs:         0,
		NACKs:        0,
		Declines:     0,
	}
}

func (d *DHCPProtocol) Enable() {
	d.mu.Lock()
	d.Enabled = true
	d.mu.Unlock()
}

func (d *DHCPProtocol) Disable() {
	d.mu.Lock()
	d.Enabled = false
	d.mu.Unlock()
}

func (d *DHCPProtocol) AddPool(name, network, mask, startIP, endIP, gateway string, dns []string, leaseTime int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Pools[name] = &DHCPPool{
		Name:        name,
		Network:     network,
		Mask:        mask,
		StartIP:     startIP,
		EndIP:       endIP,
		Gateway:     gateway,
		DNS:         dns,
		LeaseTime:   leaseTime,
		ExcludedIPs: []string{},
		AssignedIPs: make(map[string]*DHCPLease),
	}

	fmt.Printf("[DHCP] %s: Pool created: %s (%s/%s)\n", d.DeviceID, name, network, mask)
}

func (d *DHCPProtocol) RemovePool(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.Pools, name)
	fmt.Printf("[DHCP] %s: Pool removed: %s\n", d.DeviceID, name)
}

func (d *DHCPProtocol) ExcludeIP(poolName, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, exists := d.Pools[poolName]
	if !exists {
		return
	}

	for _, excluded := range pool.ExcludedIPs {
		if excluded == ip {
			return
		}
	}

	pool.ExcludedIPs = append(pool.ExcludedIPs, ip)
	fmt.Printf("[DHCP] %s: IP %s excluded from pool %s\n", d.DeviceID, ip, poolName)
}

func (d *DHCPProtocol) AssignIP(poolName, mac, clientID, hostname string) *DHCPLease {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.Enabled {
		return nil
	}

	pool, exists := d.Pools[poolName]
	if !exists {
		return nil
	}

	d.Requests++

	for _, lease := range pool.AssignedIPs {
		if lease.MAC == mac && lease.Status == "active" {
			lease.Remaining = pool.LeaseTime
			lease.ExpiresAt = time.Now().Add(time.Duration(pool.LeaseTime) * time.Second)
			d.ACKs++
			return lease
		}
	}

	start := ipToInt(pool.StartIP)
	end := ipToInt(pool.EndIP)

	for ipInt := start; ipInt <= end; ipInt++ {
		ip := intToIP(ipInt)

		isExcluded := false
		for _, excluded := range pool.ExcludedIPs {
			if excluded == ip {
				isExcluded = true
				break
			}
		}
		if isExcluded {
			continue
		}

		if _, exists := pool.AssignedIPs[ip]; !exists {
			now := time.Now()
			expiresAt := now.Add(time.Duration(pool.LeaseTime) * time.Second)

			lease := &DHCPLease{
				IP:         ip,
				MAC:        mac,
				ClientID:   clientID,
				Hostname:   hostname,
				LeaseTime:  pool.LeaseTime,
				Remaining:  pool.LeaseTime,
				AssignedAt: now,
				ExpiresAt:  expiresAt,
				Status:     "active",
			}

			pool.AssignedIPs[ip] = lease
			d.Offers++
			d.ACKs++

			fmt.Printf("[DHCP] %s: IP %s assigned to %s from pool %s\n", d.DeviceID, ip, mac, poolName)
			return lease
		}
	}

	d.NACKs++
	return nil
}

func (d *DHCPProtocol) ReleaseIP(poolName, ip string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, exists := d.Pools[poolName]
	if !exists {
		return
	}

	if lease, exists := pool.AssignedIPs[ip]; exists {
		lease.Status = "released"
		fmt.Printf("[DHCP] %s: IP %s released by %s\n", d.DeviceID, ip, lease.MAC)
	}
}

func (d *DHCPProtocol) RenewLease(poolName, mac string) *DHCPLease {
	d.mu.Lock()
	defer d.mu.Unlock()

	pool, exists := d.Pools[poolName]
	if !exists {
		return nil
	}

	for _, lease := range pool.AssignedIPs {
		if lease.MAC == mac && lease.Status == "active" {
			lease.Remaining = pool.LeaseTime
			lease.ExpiresAt = time.Now().Add(time.Duration(pool.LeaseTime) * time.Second)
			d.ACKs++
			return lease
		}
	}

	return nil
}

func (d *DHCPProtocol) GetPools() []*DHCPPool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var pools []*DHCPPool
	for _, pool := range d.Pools {
		pools = append(pools, pool)
	}

	return pools
}

func (d *DHCPProtocol) GetLeases(poolName string) []*DHCPLease {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var leases []*DHCPLease
	if pool, exists := d.Pools[poolName]; exists {
		for _, lease := range pool.AssignedIPs {
			leases = append(leases, lease)
		}
	}

	return leases
}

func (d *DHCPProtocol) FormatPools() string {
	pools := d.GetPools()
	if len(pools) == 0 {
		return "No DHCP pools configured"
	}

	var result string
	result += "DHCP Pools:\n"
	result += "-----------\n"

	for _, pool := range pools {
		result += fmt.Sprintf("Pool: %s\n", pool.Name)
		result += fmt.Sprintf("  Network: %s/%s\n", pool.Network, pool.Mask)
		result += fmt.Sprintf("  Range: %s - %s\n", pool.StartIP, pool.EndIP)
		result += fmt.Sprintf("  Gateway: %s\n", pool.Gateway)
		if len(pool.DNS) > 0 {
			result += fmt.Sprintf("  DNS: %v\n", pool.DNS)
		}
		result += fmt.Sprintf("  Lease Time: %d seconds\n", pool.LeaseTime)
		result += fmt.Sprintf("  Assigned IPs: %d\n", len(pool.AssignedIPs))
		if len(pool.ExcludedIPs) > 0 {
			result += fmt.Sprintf("  Excluded IPs: %v\n", pool.ExcludedIPs)
		}
	}

	return result
}

func (d *DHCPProtocol) FormatLeases(poolName string) string {
	leases := d.GetLeases(poolName)
	if len(leases) == 0 {
		return fmt.Sprintf("No leases in pool %s", poolName)
	}

	var result string
	result += fmt.Sprintf("DHCP Leases for pool %s:\n", poolName)
	result += fmt.Sprintf("--------------------------\n")
	result += fmt.Sprintf("%-18s %-18s %-12s %-10s %-20s\n",
		"IP Address", "MAC Address", "Status", "Remaining", "Expires")
	result += "---------------------------------------------------------------\n"

	for _, lease := range leases {
		result += fmt.Sprintf("%-18s %-18s %-12s %-10d %-20s\n",
			lease.IP,
			lease.MAC,
			lease.Status,
			lease.Remaining,
			lease.ExpiresAt.Format("2006-01-02 15:04:05"),
		)
	}

	return result
}

func (d *DHCPProtocol) FormatStats() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result string
	result += fmt.Sprintf("DHCP Statistics:\n")
	result += fmt.Sprintf("----------------\n")
	result += fmt.Sprintf("  Requests: %d\n", d.Requests)
	result += fmt.Sprintf("  Offers: %d\n", d.Offers)
	result += fmt.Sprintf("  ACKs: %d\n", d.ACKs)
	result += fmt.Sprintf("  NACKs: %d\n", d.NACKs)
	result += fmt.Sprintf("  Declines: %d\n", d.Declines)
	result += fmt.Sprintf("  Relay Agent: %s\n", boolToString(d.RelayEnabled))
	if d.RelayAgentIP != "" {
		result += fmt.Sprintf("  Relay Agent IP: %s\n", d.RelayAgentIP)
	}

	return result
}

func ipToInt(ipStr string) uint32 {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0
	}
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func intToIP(ipInt uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		(ipInt>>24)&0xff,
		(ipInt>>16)&0xff,
		(ipInt>>8)&0xff,
		ipInt&0xff,
	)
}

func boolToString(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (d *DHCPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that DHCPProtocol satisfies Handler.
var _ Handler = (*DHCPProtocol)(nil)
