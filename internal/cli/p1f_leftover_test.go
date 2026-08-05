package cli

// p1f_leftover_test.go —— P1-F 两处遗留修复的回归验证（L1 + L2）。
//
// L1：undo isis 移除 IS-IS 配置（含结构化字段与 isis:* 写盘键）。
// L2：OSPF/BGP 配置随 Serialize/Load 落盘不丢（既有缺口，对齐 isis 持久化做法）。
//
// 直接通过 ExecuteCommandOn + ParseCommand 驱动纯逻辑，不依赖网络/引擎。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestUndoISISRemovesConfig 验证 undo isis 完整清理 ISIS 配置（L1）。
func TestUndoISISRemovesConfig(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	// 进入 IS-IS 视图并真实配置
	runOn(r, topology.DeviceRouter, "isis 1")
	runOn(r, topology.DeviceRouter, "network level-1-2")
	runOn(r, topology.DeviceRouter, "import-route static")

	if r.ISIS == nil || !r.ISIS.Enabled {
		t.Fatalf("precondition: ISIS should be enabled before undo, got Enabled=%v", r.ISIS != nil && r.ISIS.Enabled)
	}
	if _, ok := r.DeviceConfig["isis:enabled"]; !ok {
		t.Fatalf("precondition: isis:enabled key should be mirrored to DeviceConfig")
	}

	// 回到系统视图（undo 仅系统视图处理），执行 undo isis <pid>
	runOn(r, topology.DeviceRouter, "quit")
	if r.CurrentView != ViewSystem {
		t.Fatalf("quit should return to system view, got %s", r.CurrentView)
	}
	out := runOn(r, topology.DeviceRouter, "undo isis 1")
	if !strings.Contains(out, "ISIS process 1 removed") {
		t.Errorf("undo isis 1 should report removed, got: %q", out)
	}

	// 结构化字段应复位
	if r.ISIS.Enabled {
		t.Errorf("ISIS.Enabled should be false after undo, got %v", r.ISIS.Enabled)
	}
	if r.ISIS.ProcessID != 0 {
		t.Errorf("ISIS.ProcessID should be 0 after undo, got %d", r.ISIS.ProcessID)
	}
	if len(r.ISIS.ImportRoutes) != 0 {
		t.Errorf("ISIS.ImportRoutes should be empty after undo, got %v", r.ISIS.ImportRoutes)
	}
	// 写盘键应清理
	for k := range r.DeviceConfig {
		if strings.HasPrefix(k, "isis:") {
			t.Errorf("isis:* key %q should be deleted after undo", k)
		}
	}
}

// TestOSPFPersistAcrossReload 验证 OSPF 配置随 Serialize/Load 落盘不丢（L2）。
func TestOSPFPersistAcrossReload(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "ospf 1 area 0")
	cfg := r.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.OSPF == nil || !reloaded.OSPF.Enabled {
		t.Fatalf("OSPF should persist after reload, got Enabled=%v", reloaded.OSPF != nil && reloaded.OSPF.Enabled)
	}
	if reloaded.OSPF.ProcessID != 1 {
		t.Errorf("OSPF ProcessID should be 1 after reload, got %d", reloaded.OSPF.ProcessID)
	}
	if reloaded.OSPF.AreaID != 0 {
		t.Errorf("OSPF AreaID should be 0 after reload, got %d", reloaded.OSPF.AreaID)
	}
}

// TestBGPPersistAcrossReload 验证 BGP 配置（含邻居）随 Serialize/Load 落盘不丢（L2）。
func TestBGPPersistAcrossReload(t *testing.T) {
	r := NewCLIStateWithType(topology.DeviceRouter)
	runOn(r, topology.DeviceRouter, "system-view")
	runOn(r, topology.DeviceRouter, "bgp 100 router-id 1.1.1.1")
	runOn(r, topology.DeviceRouter, "peer 2.2.2.2 200")
	cfg := r.SerializeToDeviceConfigData()

	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceRouter, cfg, "R1")
	if reloaded.BGP == nil || !reloaded.BGP.Enabled {
		t.Fatalf("BGP should persist after reload, got Enabled=%v", reloaded.BGP != nil && reloaded.BGP.Enabled)
	}
	if reloaded.BGP.ASNumber != 100 {
		t.Errorf("BGP ASNumber should be 100 after reload, got %d", reloaded.BGP.ASNumber)
	}
	if reloaded.BGP.RouterID != "1.1.1.1" {
		t.Errorf("BGP RouterID should be 1.1.1.1 after reload, got %q", reloaded.BGP.RouterID)
	}
	nb, ok := reloaded.BGP.Neighbors["2.2.2.2"]
	if !ok {
		t.Fatalf("BGP neighbor 2.2.2.2 should persist after reload")
	}
	if nb.RemoteAS != 200 {
		t.Errorf("BGP neighbor RemoteAS should be 200 after reload, got %d", nb.RemoteAS)
	}
	if !nb.EBGP {
		t.Errorf("BGP neighbor 2.2.2.2 should be EBGP (remote-as 200 != 100) after reload")
	}
}
