package api

// link_quality_sync_test.go —— v0.12 链路质量模拟：CLI -> topology.Link 同步层验收。
//
// 核心锁死两条设计约束（见 link_quality_sync.go 顶部说明）：
//  1. 两端取较大值：结果与命令下发顺序无关（确定性）、语义悲观；
//  2. 按命令触发而非全量同步：REST PUT /api/link 设置过的 delay
//     不得被任意无关 CLI 命令清零（历史回归模式）。

import (
	"testing"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/topology"
)

// lqTopo 构造 r1(GE0/0/1) <-> r2(GE0/0/2) 单链路拓扑。
func lqTopo() *topology.Topology {
	t := topology.NewTopology("lq", "link-quality")
	r1 := &topology.Device{ID: "r1", Name: "R1", Type: topology.DeviceRouter, Status: topology.StatusRunning}
	r1.InitializeDefaults()
	r2 := &topology.Device{ID: "r2", Name: "R2", Type: topology.DeviceRouter, Status: topology.StatusRunning}
	r2.InitializeDefaults()
	t.AddDevice(r1)
	t.AddDevice(r2)
	t.AddLink(&topology.Link{
		ID:           "l1",
		SourceDevice: "r1", SourcePort: "GigabitEthernet0/0/1",
		TargetDevice: "r2", TargetPort: "GigabitEthernet0/0/2",
		LinkType: topology.LinkTypeBusiness,
	})
	return t
}

// lqStateWith 构造一个已在指定接口上配好 delay/loss 的 CLIState。
// 走真实 CLI 命令而非直接塞键，确保键格式与生产路径一致。
func lqStateWith(t *testing.T, iface string, cmds ...string) *cli.CLIState {
	t.Helper()
	st := cli.NewCLIStateWithType(topology.DeviceRouter)
	run := func(raw string) string {
		return cli.ExecuteCommandOn(st, cli.ParseCommand(raw), topology.DeviceRouter)
	}
	run("system-view")
	run("interface " + iface)
	for _, c := range cmds {
		if out := run(c); out == "" {
			t.Fatalf("command %q produced empty output", c)
		}
	}
	return st
}

// TestSyncLinkQualityWritesLinkAttrs 验证单端配置能落到链路属性上。
func TestSyncLinkQualityWritesLinkAttrs(t *testing.T) {
	topo := lqTopo()
	states := map[string]*cli.CLIState{
		"r1": lqStateWith(t, "GigabitEthernet0/0/1", "delay 20", "loss 0.5"),
	}
	if !syncLinkQualityForInterface(topo, "r1", "GigabitEthernet0/0/1", states) {
		t.Fatalf("expected changed=true on first sync")
	}
	link := topo.Links[0]
	if link.Delay != 20 || link.Loss != 0.5 {
		t.Fatalf("link attrs = (delay %d, loss %v), want (20, 0.5)", link.Delay, link.Loss)
	}
	// 幂等：同样配置再同步一次不应报告变化（避免无谓引擎重建）。
	if syncLinkQualityForInterface(topo, "r1", "GigabitEthernet0/0/1", states) {
		t.Fatalf("second sync with identical config should report changed=false")
	}
}

// TestSyncLinkQualityTwoEndsTakeMax 验证两端各配时取较大值，且与下发顺序无关。
func TestSyncLinkQualityTwoEndsTakeMax(t *testing.T) {
	states := map[string]*cli.CLIState{
		"r1": lqStateWith(t, "GigabitEthernet0/0/1", "delay 10", "loss 2"),
		"r2": lqStateWith(t, "GigabitEthernet0/0/2", "delay 30", "loss 0.5"),
	}
	// 从 r1 侧触发同步。
	topo1 := lqTopo()
	syncLinkQualityForInterface(topo1, "r1", "GigabitEthernet0/0/1", states)
	// 从 r2 侧触发同步。
	topo2 := lqTopo()
	syncLinkQualityForInterface(topo2, "r2", "GigabitEthernet0/0/2", states)

	for name, topo := range map[string]*topology.Topology{"from-r1": topo1, "from-r2": topo2} {
		link := topo.Links[0]
		if link.Delay != 30 {
			t.Errorf("%s: delay = %d, want 30 (两端取 max)", name, link.Delay)
		}
		if link.Loss != 2 {
			t.Errorf("%s: loss = %v, want 2 (两端取 max)", name, link.Loss)
		}
	}
}

// TestSyncLinkQualityUndoResetsToZero 验证 undo 后链路属性回落到 0
// （即「无额外时延与丢包」），不残留旧值。
func TestSyncLinkQualityUndoResetsToZero(t *testing.T) {
	topo := lqTopo()
	topo.Links[0].Delay = 50
	topo.Links[0].Loss = 9

	// r1 侧执行过 delay/loss 后又 undo，DeviceConfig 中已无链路质量键。
	st := lqStateWith(t, "GigabitEthernet0/0/1", "delay 20", "loss 0.5", "undo delay", "undo loss")
	states := map[string]*cli.CLIState{"r1": st}

	if !syncLinkQualityForInterface(topo, "r1", "GigabitEthernet0/0/1", states) {
		t.Fatalf("undo 后应报告 changed=true（50/9 -> 0/0）")
	}
	if topo.Links[0].Delay != 0 || topo.Links[0].Loss != 0 {
		t.Fatalf("undo 后链路属性 = (delay %d, loss %v), want (0, 0)",
			topo.Links[0].Delay, topo.Links[0].Loss)
	}
}

// TestSyncLinkQualityNoLinkOnInterface 未连线接口：配置照样落 DeviceConfig，
// 但没有链路可承载，不算错误也不需重建引擎。
func TestSyncLinkQualityNoLinkOnInterface(t *testing.T) {
	topo := lqTopo()
	states := map[string]*cli.CLIState{
		"r1": lqStateWith(t, "GigabitEthernet0/0/9", "delay 20"),
	}
	if syncLinkQualityForInterface(topo, "r1", "GigabitEthernet0/0/9", states) {
		t.Fatalf("未连线接口不应报告 changed")
	}
	if topo.Links[0].Delay != 0 {
		t.Fatalf("未连线接口的配置不得串到其它链路，got delay=%d", topo.Links[0].Delay)
	}
}

// TestSyncLinkQualityDefensiveArgs 防御性入参。
func TestSyncLinkQualityDefensiveArgs(t *testing.T) {
	topo := lqTopo()
	states := map[string]*cli.CLIState{}
	if syncLinkQualityForInterface(nil, "r1", "GigabitEthernet0/0/1", states) {
		t.Errorf("nil topology 应返回 false")
	}
	if syncLinkQualityForInterface(topo, "", "GigabitEthernet0/0/1", states) {
		t.Errorf("空 deviceID 应返回 false")
	}
	if syncLinkQualityForInterface(topo, "r1", "   ", states) {
		t.Errorf("空接口名应返回 false")
	}
	// states 中无对应设备（极端并发）不应 panic，且视为未配置。
	if syncLinkQualityForInterface(topo, "r1", "GigabitEthernet0/0/1", states) {
		t.Errorf("无 state 时链路已是 0/0，应返回 false")
	}
}

// TestFindLinkByEndpointCaseInsensitive 端口名大小写不敏感（与 CLI 归一化口径一致）。
func TestFindLinkByEndpointCaseInsensitive(t *testing.T) {
	topo := lqTopo()
	if l := findLinkByEndpoint(topo, "r1", "gigabitethernet0/0/1"); l == nil || l.ID != "l1" {
		t.Fatalf("小写端口名应命中 l1，got %+v", l)
	}
	if l := findLinkByEndpoint(topo, "r2", "GIGABITETHERNET0/0/2"); l == nil || l.ID != "l1" {
		t.Fatalf("大写端口名应命中 l1，got %+v", l)
	}
	if l := findLinkByEndpoint(topo, "r1", "GigabitEthernet0/0/2"); l != nil {
		t.Fatalf("r1 上不存在 GE0/0/2 端点，不应命中，got %+v", l)
	}
	if l := findLinkByEndpoint(topo, "r9", "GigabitEthernet0/0/1"); l != nil {
		t.Fatalf("不存在的设备不应命中，got %+v", l)
	}
}

// TestRestSetDelayNotClearedByUnrelatedCommand 是行为回归锁：
// 通过 REST PUT /api/link 设置的 delay，在执行无关 CLI 命令时不得被清零。
// 保障机制 = executeCLI 仅在 cli.IsLinkQualityCommand(cmd) 为真时才同步。
func TestRestSetDelayNotClearedByUnrelatedCommand(t *testing.T) {
	unrelated := []string{"display version", "system-view", "interface GigabitEthernet0/0/1",
		"undo shutdown", "display current-configuration"}
	for _, raw := range unrelated {
		if cli.IsLinkQualityCommand(cli.ParseCommand(raw)) {
			t.Fatalf("%q 被误判为链路质量命令，将导致 REST 设置的 delay 被清零", raw)
		}
	}
	// 正向：链路质量命令必须被识别，否则配置无法生效。
	for _, raw := range []string{"delay 20", "loss 1", "undo delay", "undo loss"} {
		if !cli.IsLinkQualityCommand(cli.ParseCommand(raw)) {
			t.Fatalf("%q 未被识别为链路质量命令，配置将无法同步到链路", raw)
		}
	}
}
