package protocol

import (
	"fmt"
	"sync"
	"time"

	"ensp-lab/internal/sim"
)

type FTPUser struct {
	Username    string
	Password    string
	HomeDir     string
	Permissions string
}

type FTPFile struct {
	Name     string
	Size     int
	Type     string
	Modified time.Time
}

type FTPServer struct {
	Port           int
	Enabled        bool
	Anonymous      bool
	Users          map[string]*FTPUser
	Files          map[string][]*FTPFile
	Connections    int
	MaxConnections int
	Requests       int
	StartedAt      time.Time
}

type FTPProtocol struct {
	Enabled  bool
	DeviceID string
	Servers  map[int]*FTPServer
	mu       sync.RWMutex
}

func NewFTPProtocol(deviceID string) *FTPProtocol {
	return &FTPProtocol{
		Enabled:  false,
		DeviceID: deviceID,
		Servers:  make(map[int]*FTPServer),
	}
}

func (f *FTPProtocol) Enable() {
	f.mu.Lock()
	f.Enabled = true
	f.mu.Unlock()
}

func (f *FTPProtocol) Disable() {
	f.mu.Lock()
	f.Enabled = false
	f.mu.Unlock()
}

func (f *FTPProtocol) StartServer(port int, anonymous bool, maxConnections int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Servers[port] = &FTPServer{
		Port:           port,
		Enabled:        true,
		Anonymous:      anonymous,
		Users:          make(map[string]*FTPUser),
		Files:          make(map[string][]*FTPFile),
		Connections:    0,
		MaxConnections: maxConnections,
		Requests:       0,
		StartedAt:      time.Now(),
	}

	if anonymous {
		f.Servers[port].Users["anonymous"] = &FTPUser{
			Username:    "anonymous",
			Password:    "",
			HomeDir:     "/",
			Permissions: "read",
		}
		f.Servers[port].Files["/"] = []*FTPFile{
			{Name: "index.html", Size: 1024, Type: "file", Modified: time.Now()},
			{Name: "README.txt", Size: 512, Type: "file", Modified: time.Now()},
			{Name: "images", Size: 0, Type: "dir", Modified: time.Now()},
		}
	}

	fmt.Printf("[FTP] %s: FTP server started on port %d (anonymous: %v)\n", f.DeviceID, port, anonymous)
}

func (f *FTPProtocol) StopServer(port int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.Servers, port)
	fmt.Printf("[FTP] %s: FTP server stopped on port %d\n", f.DeviceID, port)
}

func (f *FTPProtocol) IsServerRunning(port int) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	server, exists := f.Servers[port]
	return exists && server.Enabled
}

func (f *FTPProtocol) AddUser(port int, username, password, homeDir, permissions string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	server, exists := f.Servers[port]
	if !exists {
		return
	}

	server.Users[username] = &FTPUser{
		Username:    username,
		Password:    password,
		HomeDir:     homeDir,
		Permissions: permissions,
	}

	if _, exists := server.Files[homeDir]; !exists {
		server.Files[homeDir] = []*FTPFile{
			{Name: ".", Size: 0, Type: "dir", Modified: time.Now()},
			{Name: "..", Size: 0, Type: "dir", Modified: time.Now()},
		}
	}

	fmt.Printf("[FTP] %s: User %s added to server on port %d\n", f.DeviceID, username, port)
}

func (f *FTPProtocol) Authenticate(port int, username, password string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	server, exists := f.Servers[port]
	if !exists {
		return false
	}

	user, exists := server.Users[username]
	if !exists {
		return false
	}

	if username == "anonymous" && server.Anonymous {
		return true
	}

	return user.Password == password
}

func (f *FTPProtocol) ListFiles(port int, username, path string) []*FTPFile {
	f.mu.RLock()
	defer f.mu.RUnlock()

	server, exists := f.Servers[port]
	if !exists {
		return []*FTPFile{}
	}

	user, exists := server.Users[username]
	if !exists {
		return []*FTPFile{}
	}

	fullPath := user.HomeDir + path
	if files, exists := server.Files[fullPath]; exists {
		return files
	}

	return []*FTPFile{}
}

func (f *FTPProtocol) GetFile(port int, username, path string) *FTPFile {
	f.mu.RLock()
	defer f.mu.RUnlock()

	server, exists := f.Servers[port]
	if !exists {
		return nil
	}

	user, exists := server.Users[username]
	if !exists {
		return nil
	}

	fullPath := user.HomeDir + path
	if files, exists := server.Files[fullPath]; exists {
		for _, file := range files {
			if file.Type == "file" {
				return file
			}
		}
	}

	return nil
}

func (f *FTPProtocol) Connect(port int) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	server, exists := f.Servers[port]
	if !exists || !server.Enabled {
		return "Connection refused: FTP server is not running"
	}

	if server.Connections >= server.MaxConnections {
		return "Connection refused: maximum connections reached"
	}

	server.Connections++
	server.Requests++

	return fmt.Sprintf("Connected to FTP server on port %d\n220 ensp-lab FTP Server ready", port)
}

func (f *FTPProtocol) Disconnect(port int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	server, exists := f.Servers[port]
	if !exists {
		return
	}

	if server.Connections > 0 {
		server.Connections--
	}
}

func (f *FTPProtocol) GetServers() []*FTPServer {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var servers []*FTPServer
	for _, server := range f.Servers {
		servers = append(servers, server)
	}

	return servers
}

func (f *FTPProtocol) FormatServers() string {
	servers := f.GetServers()
	if len(servers) == 0 {
		return "No FTP servers running"
	}

	var result string
	result += "FTP Servers:\n"
	result += "------------\n"

	for _, server := range servers {
		result += fmt.Sprintf("  * Port %d\n", server.Port)
		result += fmt.Sprintf("    - Status: %s\n", boolToString(server.Enabled))
		result += fmt.Sprintf("    - Anonymous Access: %s\n", boolToString(server.Anonymous))
		result += fmt.Sprintf("    - Users: %d\n", len(server.Users))
		result += fmt.Sprintf("    - Current Connections: %d/%d\n", server.Connections, server.MaxConnections)
		result += fmt.Sprintf("    - Total Requests: %d\n", server.Requests)
		result += fmt.Sprintf("    - Started: %s\n", server.StartedAt.Format("2006-01-02 15:04:05"))
	}

	return result
}

func (f *FTPProtocol) FormatFileList(port int, username, path string) string {
	files := f.ListFiles(port, username, path)
	if len(files) == 0 {
		return "No files in directory"
	}

	var result string
	result += fmt.Sprintf("Directory listing for %s:\n", path)
	result += fmt.Sprintf("%-1s %-10s %-12s %s\n", "T", "Size", "Modified", "Name")
	result += "--------------------------------------------\n"

	for _, file := range files {
		fileType := "-"
		if file.Type == "dir" {
			fileType = "d"
		}
		result += fmt.Sprintf("%-1s %-10d %-12s %s\n",
			fileType,
			file.Size,
			file.Modified.Format("01-02 15:04"),
			file.Name,
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
func (f *FTPProtocol) HandlePacket(pkt *sim.Packet) []*sim.Packet {
	return nil
}

// Compile-time assertion that FTPProtocol satisfies Handler.
var _ Handler = (*FTPProtocol)(nil)
