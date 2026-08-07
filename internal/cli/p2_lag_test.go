package cli

// p2_lag_test.go —— P2 #5 链路聚合端到端集成测试（T05，对齐验收 AC1/AC2/AC3/AC7/AC11/AC12）。
//
// 通过 ExecuteCommandOn + ParseCommand（复用 p1f_test.go 的 runOn）真实驱动命令分发，
// 覆盖：
//   AC1  手工模式建 Eth-Trunk 并加成员、display 列正确；
//   AC2  save → reload → display current-configuration / display eth-trunk 复现；
//   AC3  display link-aggregation summary 不出现用户未配置的 Bridge-Aggregation（幽灵组根因已修复）；
//   AC7  LACP 静态模式 active 选举 maxActive 截断 + 端口优先级小者优先；
//   AC11 能力矩阵：PC/路由器执行 eth-trunk/trunkport/load-balance/link-aggregation 应报错；
//   AC12 诚实占位：Partner 块、PortState/Weight/计数填 "-"（lagPlaceholder），无编造值。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestAC1ManualEthTrunkCreateAndDisplay 手工模式建组 + 加成员 + display 列正确。
func TestAC1ManualEthTrunkCreateAndDisplay(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	if out := runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1"); strings.Contains(out, "Error") {
		t.Fatalf("create Eth-Trunk 1 failed: %q", out)
	}
	if out := runOn(s, topology.DeviceSwitch, "mode manual load-balance"); strings.Contains(out, "Error") {
		t.Errorf("set mode failed: %q", out)
	}
	if out := runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/1"); strings.Contains(out, "Error") {
		t.Errorf("add member GE0/0/1 failed: %q", out)
	}
	if out := runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/2"); strings.Contains(out, "Error") {
		t.Errorf("add member GE0/0/2 failed: %q", out)
	}

	// 成员归属键已写入单一事实源。
	if s.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"] != "1" {
		t.Errorf("GE0/0/1 not joined trunk: %q", s.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"])
	}
	if s.DeviceConfig["interface:GigabitEthernet0/0/2:eth-trunk"] != "1" {
		t.Errorf("GE0/0/2 not joined trunk: %q", s.DeviceConfig["interface:GigabitEthernet0/0/2:eth-trunk"])
	}

	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1")
	if !strings.Contains(out, "WorkingMode: Normal") {
		t.Errorf("manual mode should show 'WorkingMode: Normal', got: %q", out)
	}
	if !strings.Contains(out, "GigabitEthernet0/0/1") || !strings.Contains(out, "GigabitEthernet0/0/2") {
		t.Errorf("both members should be listed, got: %q", out)
	}
	if !strings.Contains(out, "Operate status: up") {
		t.Errorf("two up members → Operate status should be up, got: %q", out)
	}
}

// TestAC2SaveReloadReproduce save→reload 后 display eth-trunk 与 display current-configuration
// 完整复现聚合配置（验证 buildSavedLAGConfig / buildSavedLAGInterfaceConfig 与 LoadFromDeviceConfigData 重建分支）。
func TestAC2SaveReloadReproduce(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1")
	runOn(s, topology.DeviceSwitch, "mode manual load-balance")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/2")
	// 非缺省 load-balance（src-dst-mac ≠ 缺省 src-dst-ip）→ 应出现在保存配置中。
	runOn(s, topology.DeviceSwitch, "load-balance src-dst-mac")
	runOn(s, topology.DeviceSwitch, "save")
	runOn(s, topology.DeviceSwitch, "y")

	cfg := s.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(topology.DeviceSwitch, cfg, "SW")

	// ① 关键键经序列化往返持久化不丢。
	if reloaded.DeviceConfig["interface:Eth-Trunk1:lag:exists"] != "true" {
		t.Errorf("Eth-Trunk1 exists key lost after reload")
	}
	if reloaded.DeviceConfig["interface:Eth-Trunk1:lag:load-balance"] != "src-dst-mac" {
		t.Errorf("load-balance lost after reload: %q", reloaded.DeviceConfig["interface:Eth-Trunk1:lag:load-balance"])
	}
	if reloaded.DeviceConfig["interface:GigabitEthernet0/0/1:eth-trunk"] != "1" {
		t.Errorf("member GE0/0/1 join lost after reload")
	}

	// ② display eth-trunk 复现。
	out := runOn(reloaded, topology.DeviceSwitch, "display eth-trunk 1")
	if !strings.Contains(out, "WorkingMode: Normal") {
		t.Errorf("reload display eth-trunk broken: %q", out)
	}
	if !strings.Contains(out, "GigabitEthernet0/0/1") || !strings.Contains(out, "GigabitEthernet0/0/2") {
		t.Errorf("reload display lost members: %q", out)
	}

	// ③ display current-configuration 复现成员 eth-trunk 行与 load-balance 差异值。
	cur := runOn(reloaded, topology.DeviceSwitch, "display current-configuration")
	if !strings.Contains(cur, "eth-trunk 1") {
		t.Errorf("current-configuration missing member 'eth-trunk 1': %q", cur)
	}
	if !strings.Contains(cur, "load-balance src-dst-mac") {
		t.Errorf("current-configuration missing 'load-balance src-dst-mac': %q", cur)
	}
}

// TestAC3NoGhostBridgeAggregation 验证 display link-aggregation summary 不编造用户未配置的
// Bridge-Aggregation<N> 幽灵组（按 agg-family 归类，huawei 组归为 Eth-Trunk<id>）。
func TestAC3NoGhostBridgeAggregation(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/2")

	out := runOn(s, topology.DeviceSwitch, "display link-aggregation summary")
	if !strings.Contains(out, "Eth-Trunk1") {
		t.Errorf("summary should list Eth-Trunk1, got: %q", out)
	}
	if strings.Contains(out, "Bridge-Aggregation") {
		t.Errorf("summary must NOT invent ghost Bridge-Aggregation groups, got: %q", out)
	}
}

// TestAC7LACPMaxActiveTruncation LACP 静态模式下 maxActive 截断 + 端口优先级（因子③）小者优先。
func TestAC7LACPMaxActiveTruncation(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")

	// 设置成员端口 LACP 优先级（因子③）：GE0/0/2 优于 GE0/0/1。
	// 用 quit 回退一层到系统视图（return 会直接回到用户视图，无法再 interface）。
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "lacp priority 100")
	runOn(s, topology.DeviceSwitch, "quit")
	runOn(s, topology.DeviceSwitch, "interface GigabitEthernet0/0/2")
	runOn(s, topology.DeviceSwitch, "lacp priority 50")
	runOn(s, topology.DeviceSwitch, "quit")

	runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1")
	runOn(s, topology.DeviceSwitch, "mode lacp-static")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/2")
	// 第三个成员 + 限制 maxActive=2 触发截断。
	runOn(s, topology.DeviceSwitch, "max active-linknumber 2")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/24")

	// 直接调用评估器（同包）做确定性断言。
	res := EvaluateLAG(s, 1)
	if res.Mode != LAGModeLACP {
		t.Fatalf("mode should be lacp-static, got %q", res.Mode)
	}
	if len(res.ActiveMembers) != 2 {
		t.Fatalf("maxActive=2 should select exactly 2, got %d (%v)", len(res.ActiveMembers), namesOf(res.ActiveMembers))
	}
	if res.UpPortCount != 2 {
		t.Errorf("UpPortCount want 2 got %d", res.UpPortCount)
	}
	// 选举顺序：端口优先级小者优先 → GE0/0/2(pri50) 在 GE0/0/1(pri100) 之前。
	if res.ActiveMembers[0].Name != "GigabitEthernet0/0/2" {
		t.Errorf("active[0] want GE0/0/2 (pri50), got %q", res.ActiveMembers[0].Name)
	}

	// display 反映截断。
	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1")
	if !strings.Contains(out, "Number Of Up Port In Trunk: 2") {
		t.Errorf("display should show 2 up ports after truncation, got: %q", out)
	}
}

// TestAC11CapabilityMatrix 验证能力矩阵守卫：路由器/PC 执行 LAG 命令族被拒（switchDevices 守卫）。
func TestAC11CapabilityMatrix(t *testing.T) {
	lagCmds := []string{
		"eth-trunk 1",
		"trunkport GigabitEthernet0/0/1",
		"load-balance src-dst-ip",
		"link-aggregation mode static",
	}

	router := NewCLIStateWithType(topology.DeviceRouter)
	runOn(router, topology.DeviceRouter, "system-view")
	for _, cmd := range lagCmds {
		if out := runOn(router, topology.DeviceRouter, cmd); !strings.Contains(out, "not supported") {
			t.Errorf("Router should reject %q via capability guard, got: %q", cmd, out)
		}
	}

	pc := NewCLIStateWithType(topology.DevicePC)
	runOn(pc, topology.DevicePC, "system-view")
	if out := runOn(pc, topology.DevicePC, "eth-trunk 1"); !strings.Contains(out, "not supported") {
		t.Errorf("PC should reject eth-trunk via capability guard, got: %q", out)
	}

	// 对照：交换机不应被能力矩阵拒绝（进入接口视图才是正常的视图守卫错误）。
	sw := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(sw, topology.DeviceSwitch, "system-view")
	if out := runOn(sw, topology.DeviceSwitch, "eth-trunk 1"); strings.Contains(out, "not supported") {
		t.Errorf("Switch should NOT capability-reject eth-trunk, got: %q", out)
	}
}

// TestAC12HonestPlaceholder 验证诚实占位：Partner 块整块占位、PortState/Weight/计数填
// lagPlaceholder("-")，绝不列伪造行或填随机数。
func TestAC12HonestPlaceholder(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(s, topology.DeviceSwitch, "system-view")
	runOn(s, topology.DeviceSwitch, "interface Eth-Trunk 1")
	runOn(s, topology.DeviceSwitch, "mode lacp-static")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/1")
	runOn(s, topology.DeviceSwitch, "trunkport GigabitEthernet0/0/2")

	out := runOn(s, topology.DeviceSwitch, "display eth-trunk 1")
	// Partner 块为整块诚实占位公告。
	if !strings.Contains(out, lagPartnerPlaceholder) {
		t.Errorf("Partner block must be honest placeholder, got: %q", out)
	}
	// PortState / Weight / 计数统一填 "-"。
	if !strings.Contains(out, lagPlaceholder) {
		t.Errorf("PortState/Weight/counters must fill %q, got: %q", lagPlaceholder, out)
	}
	// 绝不编造对端 Partner 全零 MAC。
	if strings.Contains(out, "0000-0000-0000") {
		t.Errorf("must not fabricate Partner MAC '0000-0000-0000', got: %q", out)
	}

	// summary 的空单元格同样填占位符。
	sum := runOn(s, topology.DeviceSwitch, "display link-aggregation summary")
	if !strings.Contains(sum, lagPlaceholder) {
		t.Errorf("summary empty cells should contain placeholder %q, got: %q", lagPlaceholder, sum)
	}
}
