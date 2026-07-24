package protocol

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type SMTPProtocol struct {
	Enabled  bool
	DeviceID string
	Port     int
	Domain   string
	Emails   []*EmailMessage
	mu       sync.RWMutex
}

type EmailMessage struct {
	ID      string
	From    string
	To      string
	Subject string
	Body    string
	Status  string
	SentAt  time.Time
}

func NewSMTPProtocol(deviceID string) *SMTPProtocol {
	return &SMTPProtocol{
		DeviceID: deviceID,
		Enabled:  false,
		Port:     25,
		Domain:   "",
		Emails:   []*EmailMessage{},
	}
}

func (s *SMTPProtocol) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Enabled = true
}

func (s *SMTPProtocol) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Enabled = false
}

func (s *SMTPProtocol) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Port = port
}

func (s *SMTPProtocol) SetDomain(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Domain = domain
}

func (s *SMTPProtocol) SendEmail(from, to, subject, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Emails = append(s.Emails, &EmailMessage{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		From:    from,
		To:      to,
		Subject: subject,
		Body:    body,
		Status:  "sent",
		SentAt:  time.Now(),
	})
}

func (s *SMTPProtocol) GetStatus() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb strings.Builder
	sb.WriteString("SMTP Configuration:\n")
	sb.WriteString(fmt.Sprintf("  Status: %s\n", map[bool]string{true: "Enabled", false: "Disabled"}[s.Enabled]))
	sb.WriteString(fmt.Sprintf("  Port: %d\n", s.Port))
	sb.WriteString(fmt.Sprintf("  Domain: %s\n", s.Domain))
	if len(s.Emails) > 0 {
		sb.WriteString("  Emails Sent:\n")
		for _, email := range s.Emails {
			sb.WriteString(fmt.Sprintf("    From: %s, To: %s, Subject: %s, Status: %s\n",
				email.From, email.To, email.Subject, email.Status))
		}
	}
	return sb.String()
}

// HandlePacket implements the protocol.Handler interface.
//
// This is a stub: the protocol currently does not participate in
// packet-level simulation. When the protocol gains simulation
// support, replace this with real handling logic that parses
// pkt.Payload and returns follow-up packets.
func (s *SMTPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that SMTPProtocol satisfies Handler.
var _ Handler = (*SMTPProtocol)(nil)
