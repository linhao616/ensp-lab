package cli

// p1f_qa_verify_test.go —— 独立回归验证（QA 严过关）。
// 覆盖工程师 p1f_leftover_test.go 未触及的边界：
// L1 安全/空复位、L2 多 area / 未配置空态 / 无邻居 BGP / 协议键互不冲突共存。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// L1 边界：从未配置 ISIS 时裸 undo isis / undo isis <pid> 必须安全、不 panic、保持 disabled。
func TestQAUndoISISSafeWhenNotConfigured(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	if out := runOn(r, topology.DeviceRouter, "undo isis"); !strings.Contains(out, "ISIS process removed") {
		t.Errorf("bare undo isis should report removed, got %q", out)
	}
	if r.ISIS == nil || r.ISIS.Enabled {
		t.Errorf("ISIS must stay disabled (non-nil) after undo, got Enabled=%v", r.ISIS != nil && r.ISIS.Enabled)
	}
	if out := runOn(r, topology.DeviceRouter, "undo isis 5"); !strings.Contains(out, "ISIS process 5 removed") {
		t.Errorf("undo isis 5 should report removed, got %q", out)
	}
	for k := range r.DeviceConfig {
		if strings.HasPrefix(k, "isis:") {
			t.Errorf("unexpected isis:* key %q after undo on unconfigured device", k)
		}
	}
}

// L1 边界：undo isis 完整复位全部结构化字段与所有 isis:* 键。
func TestQAUndoISISFullReset(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-1-2")
	runOn(r, topology.DeviceRouter, "import-route static")
	runOn(r, topology.DeviceRouter, "import-route direct")
	runOn(r, topology.DeviceRouter, "quit")
	runOn(r, topology.DeviceRouter, "undo isis 1")
	if r.ISIS.Enabled || r.ISIS.ProcessID != 0 || r.ISIS.NetworkType != "level-2" || len(r.ISIS.ImportRoutes) != 0 {
		t.Errorf("undo isis did not fully reset: %+v", r.ISIS)
	}
	for k := range r.DeviceConfig {
		if strings.HasPrefix(k, "isis:") {
			t.Errorf("isis:* key %q should be cleared", k)
		}
	}
}

// L2 边界：OSPF 多 area（area id != 0）serialize→load 不丢。
func TestQAOSPFMultiAreaPersist(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "ospf 5 area 100")
	cfg := r.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if !reloaded.OSPF.Enabled || reloaded.OSPF.ProcessID != 5 || reloaded.OSPF.AreaID != 100 {
		t.Errorf("OSPF multi-area reload mismatch: Enabled=%v PID=%d Area=%d", reloaded.OSPF.Enabled, reloaded.OSPF.ProcessID, reloaded.OSPF.AreaID)
	}
}

// L2 边界：从未配置 OSPF 时 serialize→load 应保持不启用（无 ospf:* 键）。
func TestQAOSPFNoConfigStaysEmpty(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	cfg := r.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.OSPF.Enabled {
		t.Errorf("OSPF should stay disabled after reload when never configured")
	}
	for k := range reloaded.DeviceConfig {
		if strings.HasPrefix(k, "ospf:") {
			t.Errorf("unexpected ospf:* key %q", k)
		}
	}
}

// L2 边界：BGP 无邻居 serialize→load 存活且 Neighbors 为非空 map（不 panic）。
func TestQABGPNoNeighborPersist(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "bgp 65001 router-id 9.9.9.9")
	cfg := r.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if !reloaded.BGP.Enabled || reloaded.BGP.ASNumber != 65001 || reloaded.BGP.RouterID != "9.9.9.9" {
		t.Errorf("BGP no-neighbor reload mismatch: %+v", reloaded.BGP)
	}
	if reloaded.BGP.Neighbors == nil {
		t.Errorf("BGP.Neighbors should be non-nil map after reload")
	}
}

// L2 边界：isis:/ospf:/bgp: 镜像键互不破坏，三协议共存 serialize→load 全存活。
func TestQAProtocolKeysNoConflict(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-1-2")
	runOn(r, topology.DeviceRouter, "quit")
	runOn(r, topology.DeviceRouter, "ospf 1 area 0")
	runOn(r, topology.DeviceRouter, "bgp 100 router-id 1.1.1.1")
	runOn(r, topology.DeviceRouter, "peer 2.2.2.2 200")
	cfg := r.SerializeToDeviceConfigData()
	for _, want := range []string{"isis:enabled", "ospf:enabled", "bgp:enabled", "bgp:peer-ips"} {
		if _, ok := cfg.Interfaces[want]; !ok {
			t.Errorf("expected mirror key %q present before reload", want)
		}
	}
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if !reloaded.ISIS.Enabled || reloaded.ISIS.ProcessID != 1 {
		t.Errorf("ISIS lost after mixed reload")
	}
	if !reloaded.OSPF.Enabled || reloaded.OSPF.ProcessID != 1 {
		t.Errorf("OSPF lost after mixed reload")
	}
	if !reloaded.BGP.Enabled || reloaded.BGP.ASNumber != 100 {
		t.Errorf("BGP lost after mixed reload")
	}
	nb, ok := reloaded.BGP.Neighbors["2.2.2.2"]
	if !ok || !nb.EBGP || nb.RemoteAS != 200 {
		t.Errorf("BGP neighbor EBGP lost after mixed reload: %+v", nb)
	}
}
