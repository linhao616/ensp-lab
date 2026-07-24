package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type TLSCertificate struct {
	SerialNumber       string
	Subject            string
	Issuer             string
	NotBefore          time.Time
	NotAfter           time.Time
	SignatureAlgorithm string
	KeyLength          int
	Fingerprint        string
}

type TLSProtocol struct {
	Enabled        bool
	DeviceID       string
	Certificates   map[string]*TLSCertificate
	EnabledCiphers []string
	Version        string
	mu             sync.RWMutex
}

func NewTLSProtocol(deviceID string) *TLSProtocol {
	return &TLSProtocol{
		Enabled:      false,
		DeviceID:     deviceID,
		Certificates: make(map[string]*TLSCertificate),
		EnabledCiphers: []string{
			"TLS_AES_256_GCM_SHA384",
			"TLS_AES_128_GCM_SHA256",
			"TLS_CHACHA20_POLY1305_SHA256",
			"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		Version: "TLSv1.3",
	}
}

func (t *TLSProtocol) Enable() {
	t.mu.Lock()
	t.Enabled = true
	t.mu.Unlock()
}

func (t *TLSProtocol) Disable() {
	t.mu.Lock()
	t.Enabled = false
	t.mu.Unlock()
}

func (t *TLSProtocol) AddCertificate(name, subject, issuer string, keyLength int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.Certificates[name] = &TLSCertificate{
		SerialNumber:       fmt.Sprintf("%d", now.UnixNano()),
		Subject:            subject,
		Issuer:             issuer,
		NotBefore:          now,
		NotAfter:           now.Add(365 * 24 * time.Hour),
		SignatureAlgorithm: "SHA256withRSA",
		KeyLength:          keyLength,
		Fingerprint:        fmt.Sprintf("%x", now.UnixNano())[:32],
	}

	fmt.Printf("[TLS] %s: Certificate added for %s\n", t.DeviceID, subject)
}

func (t *TLSProtocol) RemoveCertificate(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.Certificates, name)
	fmt.Printf("[TLS] %s: Certificate removed: %s\n", t.DeviceID, name)
}

func (t *TLSProtocol) GetCertificates() []*TLSCertificate {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var certs []*TLSCertificate
	for _, cert := range t.Certificates {
		certs = append(certs, cert)
	}

	return certs
}

func (t *TLSProtocol) SetVersion(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.Version = version
}

func (t *TLSProtocol) GetVersion() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.Version
}

func (t *TLSProtocol) FormatCertificates() string {
	certs := t.GetCertificates()
	if len(certs) == 0 {
		return "No TLS certificates configured"
	}

	var result string
	result += "TLS Certificates:\n"
	result += "-----------------\n"

	for name, cert := range t.Certificates {
		result += fmt.Sprintf("Certificate: %s\n", name)
		result += fmt.Sprintf("  Serial Number: %s\n", cert.SerialNumber)
		result += fmt.Sprintf("  Subject: %s\n", cert.Subject)
		result += fmt.Sprintf("  Issuer: %s\n", cert.Issuer)
		result += fmt.Sprintf("  Valid From: %s\n", cert.NotBefore.Format("2006-01-02 15:04:05"))
		result += fmt.Sprintf("  Valid Until: %s\n", cert.NotAfter.Format("2006-01-02 15:04:05"))
		result += fmt.Sprintf("  Key Length: %d bits\n", cert.KeyLength)
		result += fmt.Sprintf("  Fingerprint: %s\n", cert.Fingerprint)
		result += "\n"
	}

	return result
}

func (t *TLSProtocol) FormatCiphers() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result string
	result += fmt.Sprintf("TLS Version: %s\n", t.Version)
	result += "Enabled Ciphers:\n"
	result += "----------------\n"

	for _, cipher := range t.EnabledCiphers {
		result += fmt.Sprintf("  * %s\n", cipher)
	}

	return result
}

func (t *TLSProtocol) Handshake(clientHello bool) bool {
	t.mu.RLock()
	enabled := t.Enabled
	t.mu.RUnlock()

	if !enabled {
		return false
	}

	fmt.Printf("[TLS] %s: TLS handshake completed (%s)\n", t.DeviceID, t.Version)
	return true
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (t *TLSProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that TLSProtocol satisfies Handler.
var _ Handler = (*TLSProtocol)(nil)
