package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type DNSRecord struct {
	Name     string
	Type     string
	Value    string
	TTL      int
	Priority int
	Weight   int
}

type DNSZone struct {
	Name    string
	Records []*DNSRecord
}

type DNSQuery struct {
	Name       string
	Type       string
	SourceIP   string
	ReceivedAt time.Time
}

type DNSResponse struct {
	Name     string
	Type     string
	Answers  []*DNSRecord
	Received bool
	SentAt   time.Time
}

type DNSProtocol struct {
	Enabled     bool
	DeviceID    string
	Zones       map[string]*DNSZone
	Forwarders  []string
	QueryLog    []*DNSQuery
	ResponseLog []*DNSResponse
	Queries     int
	Responses   int
	Errors      int
	mu          sync.RWMutex
}

func NewDNSProtocol(deviceID string) *DNSProtocol {
	return &DNSProtocol{
		Enabled:     false,
		DeviceID:    deviceID,
		Zones:       make(map[string]*DNSZone),
		Forwarders:  []string{},
		QueryLog:    []*DNSQuery{},
		ResponseLog: []*DNSResponse{},
		Queries:     0,
		Responses:   0,
		Errors:      0,
	}
}

func (d *DNSProtocol) Enable() {
	d.mu.Lock()
	d.Enabled = true
	d.mu.Unlock()
}

func (d *DNSProtocol) Disable() {
	d.mu.Lock()
	d.Enabled = false
	d.mu.Unlock()
}

func (d *DNSProtocol) AddZone(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.Zones[name]; !exists {
		d.Zones[name] = &DNSZone{
			Name:    name,
			Records: []*DNSRecord{},
		}
		fmt.Printf("[DNS] %s: Zone created: %s\n", d.DeviceID, name)
	}
}

func (d *DNSProtocol) RemoveZone(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.Zones, name)
	fmt.Printf("[DNS] %s: Zone removed: %s\n", d.DeviceID, name)
}

func (d *DNSProtocol) AddRecord(zoneName, name, recordType, value string, ttl int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	zone, exists := d.Zones[zoneName]
	if !exists {
		zone = &DNSZone{
			Name:    zoneName,
			Records: []*DNSRecord{},
		}
		d.Zones[zoneName] = zone
	}

	zone.Records = append(zone.Records, &DNSRecord{
		Name:     name,
		Type:     recordType,
		Value:    value,
		TTL:      ttl,
		Priority: 0,
		Weight:   0,
	})

	fmt.Printf("[DNS] %s: Record added: %s %s %s\n", d.DeviceID, name, recordType, value)
}

func (d *DNSProtocol) RemoveRecord(zoneName, name, recordType string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	zone, exists := d.Zones[zoneName]
	if !exists {
		return
	}

	for i, record := range zone.Records {
		if record.Name == name && record.Type == recordType {
			zone.Records = append(zone.Records[:i], zone.Records[i+1:]...)
			fmt.Printf("[DNS] %s: Record removed: %s %s\n", d.DeviceID, name, recordType)
			return
		}
	}
}

func (d *DNSProtocol) SetForwarders(forwarders []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Forwarders = forwarders
}

func (d *DNSProtocol) Query(name, queryType, sourceIP string) *DNSResponse {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.Enabled {
		return &DNSResponse{
			Name:     name,
			Type:     queryType,
			Answers:  []*DNSRecord{},
			Received: false,
			SentAt:   time.Now(),
		}
	}

	query := &DNSQuery{
		Name:       name,
		Type:       queryType,
		SourceIP:   sourceIP,
		ReceivedAt: time.Now(),
	}
	d.QueryLog = append(d.QueryLog, query)
	d.Queries++

	var answers []*DNSRecord
	for _, zone := range d.Zones {
		for _, record := range zone.Records {
			if record.Name == name && record.Type == queryType {
				answers = append(answers, record)
			}
		}
	}

	if len(answers) == 0 && queryType == "A" {
		for _, zone := range d.Zones {
			for _, record := range zone.Records {
				if record.Name == name && record.Type == "CNAME" {
					cnameAnswers := d.resolveCNAME(record.Value, queryType)
					answers = append(answers, cnameAnswers...)
					break
				}
			}
		}
	}

	response := &DNSResponse{
		Name:     name,
		Type:     queryType,
		Answers:  answers,
		Received: len(answers) > 0,
		SentAt:   time.Now(),
	}

	d.ResponseLog = append(d.ResponseLog, response)
	d.Responses++

	if len(answers) == 0 {
		d.Errors++
	}

	fmt.Printf("[DNS] %s: Query %s %s from %s -> %d answers\n",
		d.DeviceID, name, queryType, sourceIP, len(answers))

	return response
}

func (d *DNSProtocol) resolveCNAME(cname, queryType string) []*DNSRecord {
	var answers []*DNSRecord
	for _, zone := range d.Zones {
		for _, record := range zone.Records {
			if record.Name == cname && record.Type == queryType {
				answers = append(answers, record)
			}
		}
	}
	return answers
}

func (d *DNSProtocol) GetZones() []*DNSZone {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var zones []*DNSZone
	for _, zone := range d.Zones {
		zones = append(zones, zone)
	}

	return zones
}

func (d *DNSProtocol) FormatZones() string {
	zones := d.GetZones()
	if len(zones) == 0 {
		return "No DNS zones configured"
	}

	var result string
	result += "DNS Zones:\n"
	result += "----------\n"

	for _, zone := range zones {
		result += fmt.Sprintf("Zone: %s\n", zone.Name)
		if len(zone.Records) == 0 {
			result += "  No records\n"
			continue
		}
		result += fmt.Sprintf("%-30s %-8s %-16s %-6s\n", "Name", "Type", "Value", "TTL")
		result += "--------------------------------------------------------\n"
		for _, record := range zone.Records {
			result += fmt.Sprintf("%-30s %-8s %-16s %-6d\n",
				record.Name,
				record.Type,
				record.Value,
				record.TTL,
			)
		}
	}

	return result
}

func (d *DNSProtocol) FormatStats() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result string
	result += fmt.Sprintf("DNS Statistics:\n")
	result += fmt.Sprintf("---------------\n")
	result += fmt.Sprintf("  Queries: %d\n", d.Queries)
	result += fmt.Sprintf("  Responses: %d\n", d.Responses)
	result += fmt.Sprintf("  Errors: %d\n", d.Errors)
	if d.Queries > 0 {
		result += fmt.Sprintf("  Success Rate: %.1f%%\n", float64(d.Responses-d.Errors)/float64(d.Queries)*100)
	}
	if len(d.Forwarders) > 0 {
		result += fmt.Sprintf("  Forwarders: %v\n", d.Forwarders)
	}

	return result
}

func (d *DNSProtocol) FormatQueryLog(count int) string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.QueryLog) == 0 {
		return "No DNS query logs"
	}

	var result string
	result += "DNS Query Logs:\n"
	result += "---------------\n"

	start := len(d.QueryLog) - count
	if start < 0 {
		start = 0
	}

	for i := start; i < len(d.QueryLog); i++ {
		query := d.QueryLog[i]
		result += fmt.Sprintf("%s %s %s from %s\n",
			query.ReceivedAt.Format("15:04:05"),
			query.Name,
			query.Type,
			query.SourceIP,
		)
	}

	return result
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (d *DNSProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that DNSProtocol satisfies Handler.
var _ Handler = (*DNSProtocol)(nil)
