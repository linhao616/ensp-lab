package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type HTTPRequest struct {
	Method     string
	Path       string
	Headers    map[string]string
	Body       string
	SourceIP   string
	ReceivedAt time.Time
}

type HTTPResponse struct {
	StatusCode int
	StatusText string
	Headers    map[string]string
	Body       string
	SentAt     time.Time
}

type HTTPServer struct {
	Port          int
	Enabled       bool
	UseTLS        bool
	DocumentRoot  string
	Requests      int
	Responses     int
	BytesReceived int
	BytesSent     int
	StartedAt     time.Time
	LastRequestAt time.Time
}

type HTTPProtocol struct {
	Enabled     bool
	DeviceID    string
	Servers     map[int]*HTTPServer
	RequestLog  []*HTTPRequest
	ResponseLog []*HTTPResponse
	mu          sync.RWMutex
}

func NewHTTPProtocol(deviceID string) *HTTPProtocol {
	return &HTTPProtocol{
		Enabled:     false,
		DeviceID:    deviceID,
		Servers:     make(map[int]*HTTPServer),
		RequestLog:  []*HTTPRequest{},
		ResponseLog: []*HTTPResponse{},
	}
}

func (h *HTTPProtocol) Enable() {
	h.mu.Lock()
	h.Enabled = true
	h.mu.Unlock()
}

func (h *HTTPProtocol) Disable() {
	h.mu.Lock()
	h.Enabled = false
	h.mu.Unlock()
}

func (h *HTTPProtocol) StartServer(port int, useTLS bool, documentRoot string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.Servers[port] = &HTTPServer{
		Port:          port,
		Enabled:       true,
		UseTLS:        useTLS,
		DocumentRoot:  documentRoot,
		Requests:      0,
		Responses:     0,
		BytesReceived: 0,
		BytesSent:     0,
		StartedAt:     time.Now(),
		LastRequestAt: time.Time{},
	}

	proto := "HTTP"
	if useTLS {
		proto = "HTTPS"
	}

	fmt.Printf("[HTTP] %s: %s server started on port %d\n", h.DeviceID, proto, port)
}

func (h *HTTPProtocol) StopServer(port int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.Servers, port)
	fmt.Printf("[HTTP] %s: Server stopped on port %d\n", h.DeviceID, port)
}

func (h *HTTPProtocol) IsServerRunning(port int) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	server, exists := h.Servers[port]
	return exists && server.Enabled
}

func (h *HTTPProtocol) HandleRequest(sourceIP, method, path string, headers map[string]string, body string) *HTTPResponse {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.Enabled {
		return &HTTPResponse{
			StatusCode: 503,
			StatusText: "Service Unavailable",
			Body:       "HTTP service is disabled",
			SentAt:     time.Now(),
		}
	}

	request := &HTTPRequest{
		Method:     method,
		Path:       path,
		Headers:    headers,
		Body:       body,
		SourceIP:   sourceIP,
		ReceivedAt: time.Now(),
	}

	h.RequestLog = append(h.RequestLog, request)

	var serverPort int
	for port, server := range h.Servers {
		if server.Enabled {
			serverPort = port
			server.Requests++
			server.BytesReceived += len(body)
			server.LastRequestAt = time.Now()
			break
		}
	}

	response := h.generateResponse(method, path, headers)
	response.SentAt = time.Now()

	h.ResponseLog = append(h.ResponseLog, response)

	if serverPort != 0 {
		if server, exists := h.Servers[serverPort]; exists {
			server.Responses++
			server.BytesSent += len(response.Body)
		}
	}

	fmt.Printf("[HTTP] %s: %s %s -> %d from %s\n", h.DeviceID, method, path, response.StatusCode, sourceIP)

	return response
}

func (h *HTTPProtocol) generateResponse(method, path string, headers map[string]string) *HTTPResponse {
	response := &HTTPResponse{
		Headers: make(map[string]string),
	}

	response.Headers["Server"] = "ensp-lab HTTP Server"
	response.Headers["Date"] = time.Now().Format(time.RFC1123)
	response.Headers["Content-Type"] = "text/html"

	switch {
	case path == "/" || path == "/index.html":
		response.StatusCode = 200
		response.StatusText = "OK"
		response.Body = `<!DOCTYPE html>
<html>
<head><title>ensp-lab Server</title></head>
<body>
<h1>Welcome to ensp-lab HTTP Server</h1>
<p>This is a simulated HTTP server for network training.</p>
</body>
</html>`

	case path == "/status":
		response.StatusCode = 200
		response.StatusText = "OK"
		response.Headers["Content-Type"] = "application/json"
		response.Body = `{"status": "running", "server": "ensp-lab"}`

	case path == "/404":
		response.StatusCode = 404
		response.StatusText = "Not Found"
		response.Body = "<html><body><h1>404 Not Found</h1></body></html>"

	case method == "HEAD":
		response.StatusCode = 200
		response.StatusText = "OK"
		response.Body = ""

	default:
		response.StatusCode = 200
		response.StatusText = "OK"
		response.Body = fmt.Sprintf("<html><body><h1>%s %s</h1><p>Path: %s</p></body></html>", method, path, path)
	}

	return response
}

func (h *HTTPProtocol) GetServers() []*HTTPServer {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var servers []*HTTPServer
	for _, server := range h.Servers {
		servers = append(servers, server)
	}

	return servers
}

func (h *HTTPProtocol) FormatServers() string {
	servers := h.GetServers()
	if len(servers) == 0 {
		return "No HTTP servers running"
	}

	var result string
	result += "HTTP Servers:\n"
	result += "-------------\n"

	for _, server := range servers {
		proto := "HTTP"
		if server.UseTLS {
			proto = "HTTPS"
		}
		result += fmt.Sprintf("  * %s on port %d\n", proto, server.Port)
		result += fmt.Sprintf("    - Requests: %d, Responses: %d\n", server.Requests, server.Responses)
		result += fmt.Sprintf("    - Bytes Received: %d, Bytes Sent: %d\n", server.BytesReceived, server.BytesSent)
		result += fmt.Sprintf("    - Started: %s\n", server.StartedAt.Format("2006-01-02 15:04:05"))
		if !server.LastRequestAt.IsZero() {
			result += fmt.Sprintf("    - Last Request: %s\n", server.LastRequestAt.Format("2006-01-02 15:04:05"))
		}
	}

	return result
}

func (h *HTTPProtocol) FormatLogs(count int) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.RequestLog) == 0 {
		return "No HTTP request logs"
	}

	var result string
	result += "HTTP Request Logs:\n"
	result += "------------------\n"

	start := len(h.RequestLog) - count
	if start < 0 {
		start = 0
	}

	for i := start; i < len(h.RequestLog); i++ {
		req := h.RequestLog[i]
		result += fmt.Sprintf("%s %s %s from %s\n",
			req.ReceivedAt.Format("15:04:05"),
			req.Method,
			req.Path,
			req.SourceIP,
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
func (h *HTTPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that HTTPProtocol satisfies Handler.
var _ Handler = (*HTTPProtocol)(nil)
