// gap_test.go 是网闸（GAP）设备 CLI 的验收测试（方案 A：完整网闸支持）。
//
// 覆盖：
//   - 视图层级：system → [gap] → [gap-channel-<n>] / [gap-policy-<n>] → quit 回退链
//   - 通道：mapping / enable / display gap channel（Up/Config/未配置）
//   - 策略：permit / enable / display gap policy
//   - 诚实占位：display gap statistics 恒 "-"
//   - 能力守卫：非 gap 设备执行 gap 命令不被消费
//
// 🔴 本文件全部 helper 使用 `gapV` 独占前缀并自包含（同包测试并行维护防重名）。
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// gapVDev 构造网闸设备态。
func gapVDev() *CLIState {
	st := NewCLIStateWithType(topology.DeviceGAP)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	st.DeviceConfig["sysname"] = "GAP1"
	st.DeviceName = "GAP1"
	return st
}

// gapVExec 执行一条命令并返回回显。
func gapVExec(st *CLIState, line string) string {
	return ExecuteCommandOn(st, ParseCommand(line), st.DeviceType)
}

// gapVHas 断言输出包含子串。
func gapVHas(t *testing.T, out, want, step string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("step %s: output missing %q\n---\n%s", step, want, out)
	}
}

// TestGAPViewHierarchy 验证视图层级与 quit 回退链。
func TestGAPViewHierarchy(t *testing.T) {
	st := gapVDev()
	gapVExec(st, "system-view")
	if out := gapVExec(st, "gap"); !strings.Contains(out, "[GAP1-gap]") {
		t.Fatalf("gap entry: %q", out)
	}
	if out := gapVExec(st, "channel 1"); !strings.Contains(out, "[GAP1-gap-channel-1]") {
		t.Fatalf("channel entry: %q", out)
	}
	// 通道子视图 quit 回 [gap] 视图（回显统一 "Return"，断言视图状态）
	gapVExec(st, "quit")
	if st.CurrentView != ViewGAP {
		t.Fatalf("after channel quit, view=%s want gap", st.CurrentView)
	}
	if out := gapVExec(st, "policy 1"); !strings.Contains(out, "[GAP1-gap-policy-1]") {
		t.Fatalf("policy entry: %q", out)
	}
	// 策略子视图 quit 回 [gap]
	gapVExec(st, "quit")
	if st.CurrentView != ViewGAP {
		t.Fatalf("after policy quit, view=%s want gap", st.CurrentView)
	}
	// gap 视图 quit 回 system
	gapVExec(st, "quit")
	if st.CurrentView != ViewSystem {
		t.Errorf("after gap quit, view=%s want system", st.CurrentView)
	}
}

// TestGAPChannelConfigure 验证通道配置 + display 状态（Up）。
func TestGAPChannelConfigure(t *testing.T) {
	st := gapVDev()
	gapVExec(st, "system-view")
	gapVExec(st, "gap")
	gapVExec(st, "channel 1")
	out := gapVExec(st, "mapping tcp 192.168.1.10 8080 <-> 203.0.113.10 8080")
	gapVHas(t, out, "Mapping configured", "mapping")
	out = gapVExec(st, "enable")
	gapVHas(t, out, "Channel enabled", "enable")
	gapVExec(st, "quit")
	gapVExec(st, "quit")
	out = gapVExec(st, "display gap channel")
	gapVHas(t, out, "Channel 1", "channel list")
	gapVHas(t, out, "Up", "channel status")
	gapVHas(t, out, "tcp 192.168.1.10:8080 <-> 203.0.113.10:8080", "mapping text")
}

// TestGAPChannelConfigOnly 验证 mapping 未 enable → Config 状态（不谎报 Up）。
func TestGAPChannelConfigOnly(t *testing.T) {
	st := gapVDev()
	gapVExec(st, "system-view")
	gapVExec(st, "gap")
	gapVExec(st, "channel 2")
	gapVExec(st, "mapping tcp 10.0.0.1 80 <-> 10.0.0.2 80")
	gapVExec(st, "quit")
	gapVExec(st, "quit")
	out := gapVExec(st, "display gap channel")
	gapVHas(t, out, "Channel 2", "channel 2 listed")
	gapVHas(t, out, "Config", "config-only status")
	if strings.Contains(out, "Channel 2    Up") {
		t.Errorf("channel 2 without enable must NOT be Up\n---\n%s", out)
	}
}

// TestGAPPolicyConfigure 验证策略配置 + display。
func TestGAPPolicyConfigure(t *testing.T) {
	st := gapVDev()
	gapVExec(st, "system-view")
	gapVExec(st, "gap")
	gapVExec(st, "policy 1")
	out := gapVExec(st, "permit source 192.168.1.0/24 dest 203.0.113.0/24")
	gapVHas(t, out, "Policy configured", "permit")
	gapVExec(st, "enable")
	gapVExec(st, "quit")
	gapVExec(st, "quit")
	out = gapVExec(st, "display gap policy")
	gapVHas(t, out, "Policy 1", "policy listed")
	gapVHas(t, out, "permit 192.168.1.0/24 -> 203.0.113.0/24", "rule text")
	gapVHas(t, out, "Enable", "enabled flag")
}

// TestGAPStatisticsHonest 验证统计诚实占位。
func TestGAPStatisticsHonest(t *testing.T) {
	st := gapVDev()
	out := gapVExec(st, "display gap statistics")
	gapVHas(t, out, "Forwarded packets : -", "packets placeholder")
	gapVHas(t, out, "Sessions          : -", "sessions placeholder")
}

// TestGAPDeviceGuard 验证非 gap 设备不消费 gap 命令（能力矩阵先行拦截）。
func TestGAPDeviceGuard(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	if st.DeviceConfig == nil {
		st.DeviceConfig = make(map[string]string)
	}
	st.DeviceName = "R1"
	gapVExec(st, "system-view")
	out := gapVExec(st, "gap")
	if strings.Contains(out, "[R1-gap]") {
		t.Errorf("router must not enter gap view, got %q", out)
	}
}
