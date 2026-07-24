//go:build linux

package router

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/stv0g/gont/v2/pkg"
)

type Router interface {
	Start() error
	Stop() error
	ApplyOSPFConfig(network string, area string) error
	ApplyBGPConfig(localAS uint32, neighbors []BGPNeighbor) error
	IsRunning() bool
	GetRoutes() ([]RouteInfo, error)
}

type BGPNeighbor struct {
	IP       string
	RemoteAS uint32
}

type RouteInfo struct {
	Destination string
	NextHop     string
	Metric      int
	Protocol    string
}

type FRRRouter struct {
	host      *gont.Host
	name      string
	running   bool
	configDir string
	daemons   map[string]*os.Process
	mu        sync.Mutex
}

func NewFRRRouter(host *gont.Host, name string) *FRRRouter {
	return &FRRRouter{
		host:      host,
		name:      name,
		daemons:   make(map[string]*os.Process),
		configDir: "/etc/frr",
	}
}

func (r *FRRRouter) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return nil
	}

	if err := r.setupConfigDirectory(); err != nil {
		return fmt.Errorf("setup config directory: %w", err)
	}

	if err := r.writeDefaultConfigs(); err != nil {
		return fmt.Errorf("write default configs: %w", err)
	}

	if err := r.startDaemons(); err != nil {
		return fmt.Errorf("start daemons: %w", err)
	}

	r.running = true
	return nil
}

func (r *FRRRouter) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	if err := r.stopDaemons(); err != nil {
		return fmt.Errorf("stop daemons: %w", err)
	}

	r.running = false
	return nil
}

func (r *FRRRouter) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *FRRRouter) ApplyOSPFConfig(network string, area string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return fmt.Errorf("router not running")
	}

	return r.host.Exec(func() error {
		ospfdPath := filepath.Join(r.configDir, "ospfd.conf")

		content, err := os.ReadFile(ospfdPath)
		if err != nil {
			return fmt.Errorf("read ospfd.conf: %w", err)
		}

		config := string(content)

		networkLine := fmt.Sprintf(" network %s area %s", network, area)
		if strings.Contains(config, networkLine) {
			return nil
		}

		config = strings.Replace(config, "router ospf\n", "router ospf\n"+networkLine+"\n", 1)

		if err := os.WriteFile(ospfdPath, []byte(config), 0644); err != nil {
			return fmt.Errorf("write ospfd.conf: %w", err)
		}

		return r.reloadFRR()
	})
}

func (r *FRRRouter) ApplyBGPConfig(localAS uint32, neighbors []BGPNeighbor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return fmt.Errorf("router not running")
	}

	return r.host.Exec(func() error {
		bgpdPath := filepath.Join(r.configDir, "bgpd.conf")

		content, err := os.ReadFile(bgpdPath)
		if err != nil {
			return fmt.Errorf("read bgpd.conf: %w", err)
		}

		config := string(content)

		config = strings.Replace(config,
			fmt.Sprintf("router bgp %d\n", localAS),
			fmt.Sprintf("router bgp %d\n", localAS),
			1)

		for _, neighbor := range neighbors {
			neighborLine := fmt.Sprintf(" neighbor %s remote-as %d", neighbor.IP, neighbor.RemoteAS)
			if strings.Contains(config, neighborLine) {
				continue
			}
			config = strings.Replace(config, "router bgp 65000\n", fmt.Sprintf("router bgp %d\n", localAS)+neighborLine+"\n", 1)
		}

		if err := os.WriteFile(bgpdPath, []byte(config), 0644); err != nil {
			return fmt.Errorf("write bgpd.conf: %w", err)
		}

		return r.reloadFRR()
	})
}

func (r *FRRRouter) reloadFRR() error {
	cmd := exec.Command("vtysh", "-c", "write memory")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vtysh write memory: %w, stderr: %s", err, stderr.String())
	}
	return nil
}

func (r *FRRRouter) GetRoutes() ([]RouteInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil, fmt.Errorf("router not running")
	}

	var routes []RouteInfo
	err := r.host.Exec(func() error {
		cmd := exec.Command("ip", "route", "show")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ip route show: %w", err)
		}

		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}

			route := RouteInfo{
				Destination: parts[0],
			}

			for i := 0; i < len(parts); i++ {
				switch parts[i] {
				case "via":
					if i+1 < len(parts) {
						route.NextHop = parts[i+1]
					}
				case "metric":
					if i+1 < len(parts) {
						if metric, err := strconv.Atoi(parts[i+1]); err == nil {
							route.Metric = metric
						}
					}
				case "proto":
					if i+1 < len(parts) {
						route.Protocol = parts[i+1]
					}
				}
			}

			routes = append(routes, route)
		}

		return nil
	})

	return routes, err
}

func (r *FRRRouter) setupConfigDirectory() error {
	return r.host.Exec(func() error {
		return os.MkdirAll(r.configDir, 0755)
	})
}

func (r *FRRRouter) writeDefaultConfigs() error {
	return r.host.Exec(func() error {
		if err := writeFile(filepath.Join(r.configDir, "daemons"), daemonsConfig); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(r.configDir, "ospfd.conf"), ospfdDefaultConfig); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(r.configDir, "bgpd.conf"), bgpdDefaultConfig); err != nil {
			return err
		}
		return nil
	})
}

func (r *FRRRouter) startDaemons() error {
	var err error
	r.host.Exec(func() error {
		daemons := []string{"zebra", "ospfd", "bgpd"}
		for _, daemon := range daemons {
			cmd := exec.Command("/usr/lib/frr/" + daemon)
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("start %s: %w", daemon, err)
			}
			r.daemons[daemon] = cmd.Process
		}
		return nil
	})
	return err
}

func (r *FRRRouter) stopDaemons() error {
	return r.host.Exec(func() error {
		for name, proc := range r.daemons {
			if proc != nil {
				if err := proc.Kill(); err != nil {
					return fmt.Errorf("kill %s: %w", name, err)
				}
				proc.Wait()
			}
		}
		r.daemons = make(map[string]*os.Process)
		return nil
	})
}

func writeFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0644)
}

var daemonsConfig = []byte(`
zebra=no
bgpd=yes
ospfd=yes
ospf6d=no
ripd=no
ripngd=no
isisd=no
bfdd=no
eigrpd=no
pbrd=no
nhrpd=no
`)

var ospfdDefaultConfig = []byte(`
!
router ospf
!
line vty
!
`)

var bgpdDefaultConfig = []byte(`
!
router bgp 65000
!
line vty
!
`)
