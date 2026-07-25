package sim

import (
	"testing"

	"ensp-lab/internal/topology"
)

// newTwoPCTopo 构造一个由单条链路连接的两台 PC 的拓扑，便于验证 ping 与重建。
func newTwoPCTopo() *topology.Topology {
	t := topology.NewTopology("t1", "test")
	pc1 := &topology.Device{ID: "pc1", Name: "PC1", Type: topology.DevicePC, Status: topology.StatusRunning}
	pc1.InitializeDefaults()
	pc1.Interfaces["Ethernet0"].IPAddress = "192.168.1.2"
	pc1.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	pc1.Interfaces["Ethernet0"].Status = "up"

	pc2 := &topology.Device{ID: "pc2", Name: "PC2", Type: topology.DevicePC, Status: topology.StatusRunning}
	pc2.InitializeDefaults()
	pc2.Interfaces["Ethernet0"].IPAddress = "192.168.1.3"
	pc2.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
	pc2.Interfaces["Ethernet0"].Status = "up"

	t.AddDevice(pc1)
	t.AddDevice(pc2)
	t.AddLink(&topology.Link{
		ID:           "l1",
		SourceDevice: "pc1", SourcePort: "Ethernet0",
		TargetDevice: "pc2", TargetPort: "Ethernet0",
		LinkType:     topology.LinkTypeBusiness,
	})
	return t
}

// withIP 返回 pc2 使用指定 IP 的拓扑副本（链路/位置不变）。
func withPC2IP(base *topology.Topology, ip string) *topology.Topology {
	t := base.Clone()
	d := t.Devices["pc2"]
	d.Interfaces["Ethernet0"].IPAddress = ip
	return t
}

// TestEngineRebuildPropagates 验证 B1：在拓扑变更后调用 Rebuild，引擎的
// 内部图状态应反映最新拓扑——旧 IP 不再可达，新 IP 可达。
func TestEngineRebuildPropagates(t *testing.T) {
	base := newTwoPCTopo()
	eng, err := NewNSxEngine(base)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	// 重建前：pc2 的旧 IP 应当可达。
	if res, err := eng.Ping("pc1", "192.168.1.3"); err != nil {
		t.Fatalf("ping before rebuild: %v", err)
	} else if res == nil || res.Received == 0 {
		t.Fatalf("ping before rebuild: expected reachable, got %+v", res)
	}

	// 变更拓扑：pc2 的 IP 改为 10.0.0.3，并重建引擎。
	if err := eng.Rebuild(withPC2IP(base, "10.0.0.3")); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	// 重建后：旧 IP 不再可达（引擎视图已更新）。
	if res, err := eng.Ping("pc1", "192.168.1.3"); err != nil {
		t.Fatalf("ping old ip after rebuild: %v", err)
	} else if res != nil && res.Received > 0 {
		t.Fatalf("ping old ip after rebuild: expected unreachable, got %+v", res)
	}

	// 重建后：新 IP 可达（确认重建生效，而非简单断连）。
	if res, err := eng.Ping("pc1", "10.0.0.3"); err != nil {
		t.Fatalf("ping new ip after rebuild: %v", err)
	} else if res == nil || res.Received == 0 {
		t.Fatalf("ping new ip after rebuild: expected reachable, got %+v", res)
	}
}

// TestEngineRebuildConcurrentSafe 验证 B2：并发地 Rebuild 与 Ping 不应
// panic 或死锁。引擎通过 atomic.Value 快照隔离读写，共享 *Topology 由
// API 层持有，引擎只持有私有深拷贝。
func TestEngineRebuildConcurrentSafe(t *testing.T) {
	base := newTwoPCTopo()
	eng, err := NewNSxEngine(base)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func(n int) {
			for i := 0; i < 15; i++ {
				if i%2 == 0 {
					// 交替重建为可达 IP 的拓扑，制造高频快照替换（避免 3s ping 超时拖慢）。
					_ = eng.Rebuild(withPC2IP(base, "192.168.1.4"))
				} else {
					_, _ = eng.Ping("pc1", "192.168.1.4")
				}
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	// 若能走到这里未 panic/死锁，即说明并发 Rebuild+Ping 安全。
}

// TestEngineStopRestart 验证 B3：被 Stop 的引擎可以再次 Start 而不 panic。
// 修复前 Stop 会关闭 eventCh/pendingEvents 且不重建，再次 Start 会向已关闭
// 通道发送导致 panic；修复后 Start 重建通道，生命周期可正常复用。
func TestEngineStopRestart(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	eng.Start()
	eng.Stop()
	eng.Start() // 不应 panic
	eng.Stop()
	eng.Stop() // 幂等，第二次 Stop 应安全返回
}
