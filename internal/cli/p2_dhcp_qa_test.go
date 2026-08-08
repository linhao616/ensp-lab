package cli

// p2_dhcp_qa_test.go —— P2 第六项（DHCP 中继）端到端 QA 回归（T6）。
//
// 覆盖 PRD 验收标准 AC7 / AC8 / AC10 / AC11 与设计 §1.6 键碰撞专项、T0 迁移回归面：
//   AC7  输出确定性（连续 10 次字节级一致）+ save→reload 往返完整复现
//   AC8  诚实占位红线：转发统计 / Fwd 列 / Source IP 未配恒 "-"，全输出附注记，
//        且**不得出现任何数字型统计或 Reachable/Up/Active 之类臆造可达性词**
//   AC10 a=二层 Switch 既有 dhcp enable/pool 行为逐字不变
//        b=display 任意设备可读（PC/Server 上不得出现 "is not supported on"）
//        c=全量既有测试零回归（由 go test ./... 保证，本文件补关键面）
//   AC11 静态断言：state.go 无中继内嵌结构体、评估器不 import internal/protocol、
//        capabilities.go / tools.go 零改动痕迹（"dhcp" 仍为 switchAndL3）
//   §1.6 键碰撞专项：dhcp-pool 与 dhcp-select / dhcp-relay:* 全链路互不干扰
//   T0   迁移回归面：系统视图 dhcp enable/disable/pool 行为不变

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— AC7：输出确定性 ——

func TestAC7DisplayDeterministic(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	// 乱序配置 3 个中继接口，每个多台服务器
	for _, iface := range []string{"GigabitEthernet0/0/2", "GigabitEthernet0/0/0", "GigabitEthernet0/0/1"} {
		runOn(st, topology.DeviceRouter, "interface "+iface)
		runOn(st, topology.DeviceRouter, "dhcp select relay")
		runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.3")
		runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
		runOn(st, topology.DeviceRouter, "dhcp relay information enable")
		runOn(st, topology.DeviceRouter, "quit")
	}

	for _, raw := range []string{
		"display dhcp relay all",
		"display dhcp relay interface GigabitEthernet0/0/1",
		"display current-configuration",
	} {
		first := runOn(st, topology.DeviceRouter, raw)
		for i := 1; i < 10; i++ {
			if got := runOn(st, topology.DeviceRouter, raw); got != first {
				t.Fatalf("%q not byte-identical at iteration %d\n--- first ---\n%s\n--- got ---\n%s",
					raw, i, first, got)
			}
		}
	}
}

// —— AC7：save → reload 往返 ——

func TestAC7SaveReloadRoundTrip(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+iface)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.2")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")
	runOn(st, topology.DeviceRouter, "dhcp relay information strategy keep")
	runOn(st, topology.DeviceRouter, "dhcp relay source-ip 10.2.2.254")

	before := runOn(st, topology.DeviceRouter, "display current-configuration")
	for _, want := range []string{
		" dhcp select relay",
		" dhcp relay server-ip 10.1.1.1",
		" dhcp relay server-ip 10.1.1.2",
		" dhcp relay information enable",
		" dhcp relay information strategy keep",
		" dhcp relay source-ip 10.2.2.254",
	} {
		if !strings.Contains(before, want) {
			t.Errorf("current-configuration missing %q\n---\n%s", want, before)
		}
	}
	// 保序：server-ip 行按配置序
	if strings.Index(before, "server-ip 10.1.1.1") > strings.Index(before, "server-ip 10.1.1.2") {
		t.Error("server-ip lines not in configuration order")
	}

	// save → 新 state 回填 DeviceConfig（LoadFromDeviceConfigData 零改动路径）
	data := st.SerializeToDeviceConfigData()
	restored := NewCLIStateWithType(topology.DeviceRouter)
	restored.LoadFromDeviceConfigData(data)

	// 键级往返
	for _, k := range []string{
		dhcpSelectKey(iface),
		dhcpRelayKey(iface, dhcpRelayFieldServerIPs),
		dhcpRelayKey(iface, dhcpRelayFieldOption82),
		dhcpRelayKey(iface, dhcpRelayFieldStrategy),
		dhcpRelayKey(iface, dhcpRelayFieldSourceIP),
	} {
		if restored.DeviceConfig[k] != st.DeviceConfig[k] {
			t.Errorf("key %q not restored: %q != %q", k, restored.DeviceConfig[k], st.DeviceConfig[k])
		}
	}
	// 配置语义往返
	if got, want := readRelayConfig(restored, iface), readRelayConfig(st, iface); got.Mode != want.Mode ||
		strings.Join(got.ServerIPs, ",") != strings.Join(want.ServerIPs, ",") ||
		got.Option82 != want.Option82 || got.Option82Strategy != want.Option82Strategy ||
		got.SourceIP != want.SourceIP {
		t.Errorf("config not restored:\ngot  %+v\nwant %+v", got, want)
	}
	// reload 后 display 仍完整复现中继段（独立输出通道兜底）
	after := runOn(restored, topology.DeviceRouter, "display current-configuration")
	for _, want := range []string{
		"interface " + iface,
		" dhcp select relay",
		" dhcp relay server-ip 10.1.1.1",
		" dhcp relay server-ip 10.1.1.2",
		" dhcp relay information enable",
		" dhcp relay information strategy keep",
		" dhcp relay source-ip 10.2.2.254",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("post-reload current-configuration missing %q\n---\n%s", want, after)
		}
	}
	// display dhcp relay 在 reload 后同样可读
	detail := runOn(restored, topology.DeviceRouter, "display dhcp relay interface "+iface)
	if !strings.Contains(detail, "10.1.1.1") || strings.Contains(detail, "Error:") {
		t.Errorf("post-reload detail broken:\n%s", detail)
	}
}

func TestAC7SavedConfigOmitsDefaultStrategy(t *testing.T) {
	// 设计 A5：strategy 等于生效缺省值 replace 时不落盘（VRP 只输出差异值）。
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+iface)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay information strategy replace")

	lines := buildSavedDHCPRelayInterfaceConfig(st, iface)
	if strings.Contains(lines, "information strategy") {
		t.Errorf("default strategy must not be persisted, got:\n%s", lines)
	}
	if !strings.Contains(lines, " dhcp select relay") {
		t.Errorf("select line missing:\n%s", lines)
	}
	// 未配 option82 时不输出 enable 行
	if strings.Contains(lines, "information enable") {
		t.Errorf("option82 default must not be persisted, got:\n%s", lines)
	}
	// 但 display 仍显示生效缺省值 replace（A5）
	detail := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+iface)
	if !strings.Contains(detail, "Option82 strategy") || !strings.Contains(detail, DefaultOption82Strategy) {
		t.Errorf("display must show effective default strategy:\n%s", detail)
	}
}

// —— AC8：诚实占位红线 ——

func TestAC8ForwardingStatsAlwaysPlaceholder(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+iface)
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")

	detail := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+iface)

	// 六个统计标签的值必须严格是 "-"
	statLabels := []string{
		"DHCP packets forwarded", "DISCOVER forwarded", "OFFER received",
		"REQUEST forwarded", "ACK received", "Server reachability",
	}
	for _, label := range statLabels {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.+?)\s*$`)
		m := re.FindStringSubmatch(detail)
		if m == nil {
			t.Fatalf("stat label %q not found in:\n%s", label, detail)
		}
		if m[1] != relayStatPlaceholder {
			t.Errorf("stat %q = %q, want %q (AC8 red line)", label, m[1], relayStatPlaceholder)
		}
	}
	// 严禁臆造可达性词
	for _, banned := range []string{"Reachable", "Unreachable", "reachable via", "Active", "packets/s"} {
		if strings.Contains(detail, banned) {
			t.Errorf("output contains fabricated runtime word %q:\n%s", banned, detail)
		}
	}
	// 汇总表 Fwd 列恒 "-"
	summary := runOn(st, topology.DeviceRouter, "display dhcp relay all")
	if !strings.Contains(summary, dhcpRelaySimNote()) {
		t.Error("summary missing honest sim note")
	}
	for _, line := range strings.Split(summary, "\n") {
		if !strings.HasPrefix(line, iface) {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(line, " "), relayStatPlaceholder) {
			t.Errorf("summary Fwd column must be %q, row = %q", relayStatPlaceholder, line)
		}
	}
}

func TestAC8SourceIPNeverDerived(t *testing.T) {
	// 拍板 #4：Source IP 未配恒 "-"，绝不推导接口主 IP。
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+iface)
	runOn(st, topology.DeviceRouter, "ip address 192.168.100.254 255.255.255.0")
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")

	detail := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+iface)
	re := regexp.MustCompile(`(?m)^\s*Source IP address\s*:\s*(.+?)\s*$`)
	m := re.FindStringSubmatch(detail)
	if m == nil {
		t.Fatalf("Source IP address label not found:\n%s", detail)
	}
	if m[1] != relayStatPlaceholder {
		t.Errorf("Source IP = %q, want %q (must NOT derive interface primary IP)", m[1], relayStatPlaceholder)
	}
	if strings.Contains(detail, "192.168.100.254") {
		t.Errorf("interface primary IP leaked into relay detail:\n%s", detail)
	}
}

// —— AC10：迁移零回归 + display 任意设备可读 ——

func TestAC10aLegacySwitchDHCPUnchanged(t *testing.T) {
	// 二层 Switch 的既有系统视图 dhcp 行为必须逐字不变。
	st := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(st, topology.DeviceSwitch, "system-view")
	if out := runOn(st, topology.DeviceSwitch, "dhcp enable"); out != "DHCP enabled" {
		t.Errorf("dhcp enable = %q, want %q", out, "DHCP enabled")
	}
	if st.DHCP == nil || !st.DHCP.Enabled {
		t.Error("dhcp enable must set state.DHCP.Enabled")
	}
	if out := runOn(st, topology.DeviceSwitch, "dhcp pool p1"); out != "Enter DHCP pool p1 view" {
		t.Errorf("dhcp pool = %q", out)
	}
	if st.CurrentView != ViewDHCPPool || st.CurrentSub != "p1" {
		t.Errorf("pool view not entered: view=%v sub=%q", st.CurrentView, st.CurrentSub)
	}
	// 池视图内的 dhcp 命令仍走 ViewDHCPPool 分支（T0 新增的 ViewInterface 分派
	// 必须排在 ViewDHCPPool 之后，绝不能截胡池视图）。
	if out := runOn(st, topology.DeviceSwitch, "dhcp bogus"); out != "Error: invalid DHCP pool command" {
		t.Errorf("pool-view dhcp routing broken: %q, want %q", out, "Error: invalid DHCP pool command")
	}
	if st.CurrentView != ViewDHCPPool {
		t.Error("pool view must be retained")
	}
	runOn(st, topology.DeviceSwitch, "quit")
	if out := runOn(st, topology.DeviceSwitch, "dhcp disable"); out != "DHCP disabled" {
		t.Errorf("dhcp disable = %q", out)
	}
	// 系统视图 dhcp 未知子命令行为不变
	if out := runOn(st, topology.DeviceSwitch, "dhcp bogus"); out != "Error: invalid DHCP command" {
		t.Errorf("dhcp bogus = %q, want %q", out, "Error: invalid DHCP command")
	}
}

func TestAC10bDisplayReadableOnAnyDevice(t *testing.T) {
	// 拍板 #5：display dhcp relay 只读，任意设备可读，不得出现能力拒绝文案。
	for _, dt := range []topology.DeviceType{
		topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceSwitch,
		topology.DeviceFirewall, topology.DevicePC, topology.DeviceServer,
	} {
		st := NewCLIStateWithType(dt)
		out := runOn(st, dt, "display dhcp relay all")
		if strings.Contains(out, "is not supported on") {
			t.Errorf("device %s: display must be readable, got %q", dt, out)
		}
		if !strings.Contains(out, infoNoDHCPRelayInterface) {
			t.Errorf("device %s: expected empty-state Info, got %q", dt, out)
		}
		if !strings.Contains(out, dhcpRelaySimNote()) {
			t.Errorf("device %s: missing honest sim note", dt)
		}
	}
}

func TestAC10cInterfaceViewNonRelayDHCPUnchanged(t *testing.T) {
	// 接口视图下 dhcp enable/disable/pool 仍走系统视图口径（迁移不扩大语义）。
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "interface GigabitEthernet0/0/1")
	for _, raw := range []string{"dhcp enable", "dhcp disable", "dhcp pool p1"} {
		if out := runOn(st, topology.DeviceRouter, raw); out != "Error: must be in system view" {
			t.Errorf("%q in interface view = %q, want system-view error", raw, out)
		}
	}
	if st.CurrentView != ViewInterface {
		t.Error("view must not change on rejected commands")
	}
}

// —— AC11：静态架构断言 ——

func TestAC11NoRelayStructInState(t *testing.T) {
	src, err := os.ReadFile("state.go")
	if err != nil {
		t.Fatalf("read state.go: %v", err)
	}
	text := string(src)
	// 架构铁律 1：严禁在 state.go 新增任何 relay 内嵌结构体 / 字段。
	if strings.Contains(text, "Relay") {
		t.Error("state.go must not contain any Relay struct or field (架构铁律 1)")
	}
	// T0：死字段必须已删除。
	if strings.Contains(text, "DHCPSelectMode") {
		t.Error("state.go still declares DHCPSelectMode; T0 requires removal")
	}
}

func TestAC11EvaluatorDoesNotImportProtocol(t *testing.T) {
	for _, f := range []string{"dhcp_relay_eval.go", "dhcp_relay_display.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// 精确匹配 import 声明（带引号的完整包路径），避免误伤注释中的说明文字。
		if strings.Contains(string(src), `"ensp-lab/internal/protocol"`) {
			t.Errorf("%s must not import internal/protocol (架构铁律 2)", f)
		}
	}
}

func TestAC11CapabilitiesUntouched(t *testing.T) {
	// 拍板 #5：capabilities.go 顶层 "dhcp" 保持 switchAndL3()，零改动。
	src, err := os.ReadFile("capabilities.go")
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}
	if !regexp.MustCompile(`"dhcp":\s*switchAndL3\(\)`).MatchString(string(src)) {
		t.Error(`capabilities.go must keep "dhcp": switchAndL3() unchanged`)
	}
	// 中继设备集必须复用 l3Devices()，不得重定义
	for _, f := range []string{"dhcp_relay_eval.go", "dhcp_relay_cmd.go", "dhcp_relay_display.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(b), "func l3Devices") {
			t.Errorf("%s must not redefine l3Devices()", f)
		}
	}
}

func TestAC11NoRandomOrCounterInRelaySources(t *testing.T) {
	// AC8 类型层红线的源码级加固：中继三文件不得引入随机数 / 时间戳等运行态臆造源。
	for _, f := range []string{"dhcp_relay_eval.go", "dhcp_relay_cmd.go", "dhcp_relay_display.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(src)
		for _, banned := range []string{"math/rand", "rand.Intn", "time.Now("} {
			if strings.Contains(text, banned) {
				t.Errorf("%s must not use %q (honest placeholder red line)", f, banned)
			}
		}
	}
}

// —— §1.6 键碰撞专项 ——

func TestKeyCollisionDHCPPoolIsolated(t *testing.T) {
	iface := "Vlanif10"
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	poolKey := "interface:" + iface + ":dhcp-pool"
	st.DeviceConfig[poolKey] = "pool1"

	// ① 只有 dhcp-pool 键时，不得被识别为中继接口（幽灵接口）
	if len(collectRelayInterfaces(st)) != 0 {
		t.Errorf("dhcp-pool key created ghost relay interface: %v", collectRelayInterfaces(st))
	}
	if len(collectDHCPSelectInterfaces(st)) != 0 {
		t.Errorf("dhcp-pool key leaked into select interface set: %v", collectDHCPSelectInterfaces(st))
	}
	// ② display 仍为空态
	if out := buildDHCPRelayDisplay(st, []string{"all"}); !strings.Contains(out, infoNoDHCPRelayInterface) {
		t.Errorf("dhcp-pool must not produce relay rows:\n%s", out)
	}
	// ③ 持久化通道不得为纯 dhcp-pool 接口输出中继段
	if lines := buildSavedDHCPRelayConfig(st); lines != "" {
		t.Errorf("dhcp-pool interface wrongly persisted as relay:\n%s", lines)
	}
	// ④ 级联清理不得删除 dhcp-pool 键
	clearDHCPRelayKeys(st, iface)
	if _, ok := st.DeviceConfig[poolKey]; !ok {
		t.Error("clearDHCPRelayKeys deleted dhcp-pool key (§1.6 red line)")
	}
	// ⑤ 同接口同时存在 pool 与 relay 配置时互不干扰
	st.DeviceConfig[dhcpSelectKey(iface)] = relayModeRelay
	st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)] = "10.1.1.1"
	if got := collectRelayInterfaces(st); len(got) != 1 || got[0] != iface {
		t.Errorf("collectRelayInterfaces = %v, want [%s]", got, iface)
	}
	clearDHCPRelayKeys(st, iface)
	if _, ok := st.DeviceConfig[poolKey]; !ok {
		t.Error("cascade cleanup deleted dhcp-pool key")
	}
	if _, ok := st.DeviceConfig[dhcpSelectKey(iface)]; !ok {
		t.Error("cascade cleanup must not delete dhcp-select key")
	}
	if _, ok := st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)]; ok {
		t.Error("cascade cleanup must delete relay server-ips key")
	}
}

// —— T0 迁移回归面：多接口独立模式 ——

func TestPerInterfaceModeIndependence(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")

	modes := map[string]string{
		"GigabitEthernet0/0/0": relayModeGlobal,
		"GigabitEthernet0/0/1": relayModeInterface,
		"GigabitEthernet0/0/2": relayModeRelay,
	}
	for iface, mode := range modes {
		runOn(st, topology.DeviceRouter, "interface "+iface)
		runOn(st, topology.DeviceRouter, "dhcp select "+mode)
		runOn(st, topology.DeviceRouter, "quit")
	}
	// 每接口一值，互不覆盖（旧全局死字段无法表达的能力）
	for iface, mode := range modes {
		if got := dhcpSelectMode(st, iface); got != mode {
			t.Errorf("iface %s mode = %q, want %q", iface, got, mode)
		}
	}
	// 仅 relay 接口进入中继集合
	got := collectRelayInterfaces(st)
	if len(got) != 1 || got[0] != "GigabitEthernet0/0/2" {
		t.Errorf("collectRelayInterfaces = %v, want [GigabitEthernet0/0/2]", got)
	}
	// 三个接口的 select 行全部落盘
	cfg := runOn(st, topology.DeviceRouter, "display current-configuration")
	for iface, mode := range modes {
		if !strings.Contains(cfg, "interface "+iface) {
			t.Errorf("current-configuration missing interface %s", iface)
		}
		if !strings.Contains(cfg, " dhcp select "+mode) {
			t.Errorf("current-configuration missing 'dhcp select %s' for %s", mode, iface)
		}
	}
}

// ============================================================================
// QA 独立验收补充（严过关 / Yan，2026-08-09）
//
// 说明：以下 TestQA_* 为 QA 独立编写的加硬断言，**不复用**上方工程师/QA 首轮用例的
// 判定路径，用于覆盖首轮遗漏面：
//   AC7  ：Vlanif10 参与的混合接口集合 + 升序断言 + server-ip 配置序断言 + 空态提示
//   AC8  ：6 个转发统计字段的**正则**红线（不匹配 \d、不匹配可达性词）、
//          统计分组整块无数字、汇总表 Fwd 列按列切分断言（非"结尾字符"启发式）、
//          全部 4 种 display dhcp relay* 形态均附注记
//   AC10a：PC / Server 的配置命令拒绝（首轮**完全未覆盖**）+ L2 Switch 的
//          `dhcp relay information enable`（首轮遗漏）+ 三层设备放行
//   AC10b：`display dhcp relay`（无参形态）在 PC / Server 上放行（首轮只测了 all）
//   AC10c：ViewDHCPPool 全部池子命令 + display ip pool 既有行为逐字不变（首轮只测 3 条）
//   AC11 ：中继三文件 import 白名单（零新增依赖的源码级证明）
//   §1.6 ：键碰撞经**命令路径**（而非直调 clearDHCPRelayKeys）验证级联清理不误伤 dhcp-pool
// ============================================================================

// qaRelayStatLabels 是 PRD §4.2 的 6 个转发统计标签（AC8 红线断言面）。
var qaRelayStatLabels = []string{
	"DHCP packets forwarded",
	"DISCOVER forwarded",
	"OFFER received",
	"REQUEST forwarded",
	"ACK received",
	"Server reachability",
}

// qaFabricatedRuntimeWord 匹配「臆造运行态」词汇。仅用于统计字段值域，
// 不用于全文（全文含合法的 `Interface status : Up`，那是本地可判定的真实字段）。
var qaFabricatedRuntimeWord = regexp.MustCompile(`(?i)(reachable|unreachable|\bup\b|\bdown\b|active)`)

// qaAnyDigit 匹配任意数字（统计字段值域内出现即违反 AC8 红线）。
var qaAnyDigit = regexp.MustCompile(`\d`)

// qaStatValue 抽取详情块中某统计标签冒号后的值（去除首尾空白）。
// 找不到标签时 ok=false —— 用于区分「字段缺失」与「字段值错误」。
func qaStatValue(out, label string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.*?)\s*$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// qaNewRelayL3Switch 构造一台已 dhcp enable 的三层交换机（可配 Vlanif），
// 返回时**停留在 system-view**（`interface <name>` 要求 ViewSystem，parser.go:316）。
//
// 注意（QA 踩坑记录）：parser.go:1126 的 `vlan <id>` 分支**不切换视图**，仍是
// ViewSystem；若此处再补一个 `quit`，会命中 parser.go:290 的
// `ViewSystem -> ViewUser` 分支直接退回用户视图，后续 `interface Vlanif10`
// 必然报 "Error: must be in system-view"。故不得调用 quit。
func qaNewRelayL3Switch(t *testing.T) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceL3Switch)
	runOn(st, topology.DeviceL3Switch, "system-view")
	runOn(st, topology.DeviceL3Switch, "dhcp enable")
	runOn(st, topology.DeviceL3Switch, "vlan 10")
	if st.CurrentView != ViewSystem {
		t.Fatalf("qaNewRelayL3Switch: view = %v, want ViewSystem", st.CurrentView)
	}
	return st
}

// —— AC8 红线：转发统计 6 字段正则断言（QA 独立实现）——

func TestQA_AC8_StatFieldsRegexRedLine(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	runOn(st, topology.DeviceRouter, "dhcp enable")
	runOn(st, topology.DeviceRouter, "interface "+iface)
	runOn(st, topology.DeviceRouter, "ip address 192.168.10.254 255.255.255.0")
	runOn(st, topology.DeviceRouter, "dhcp select relay")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.1")
	runOn(st, topology.DeviceRouter, "dhcp relay server-ip 10.1.1.2")
	runOn(st, topology.DeviceRouter, "dhcp relay information enable")

	detail := runOn(st, topology.DeviceRouter, "display dhcp relay interface "+iface)

	// ① 6 个字段必须存在，且值严格为 "-"、不含任何数字、不含臆造可达性词。
	for _, label := range qaRelayStatLabels {
		val, ok := qaStatValue(detail, label)
		if !ok {
			t.Fatalf("AC8: stat label %q missing from detail output:\n%s", label, detail)
		}
		if val != relayStatPlaceholder {
			t.Errorf("AC8 red line: %q = %q, want exactly %q", label, val, relayStatPlaceholder)
		}
		if qaAnyDigit.MatchString(val) {
			t.Errorf("AC8 red line: %q value %q matches \\d (fabricated counter)", label, val)
		}
		if qaFabricatedRuntimeWord.MatchString(val) {
			t.Errorf("AC8 red line: %q value %q matches Reachable|Unreachable|Up|Down|Active", label, val)
		}
	}

	// ② 「--- Forwarding statistics ---」整块内不得出现任何数字。
	//    该断言比逐字段更硬：即便日后有人新增一个统计字段并填数字也会被拦下。
	blockRe := regexp.MustCompile(`(?s)--- Forwarding statistics ---(.*?)\n` + regexp.QuoteMeta(dhcpRelayDetailRule))
	bm := blockRe.FindStringSubmatch(detail)
	if bm == nil {
		t.Fatalf("AC8: forwarding statistics block not found:\n%s", detail)
	}
	if qaAnyDigit.MatchString(bm[1]) {
		t.Errorf("AC8 red line: forwarding statistics block contains digits:\n%s", bm[1])
	}
	if qaFabricatedRuntimeWord.MatchString(bm[1]) {
		t.Errorf("AC8 red line: forwarding statistics block contains runtime words:\n%s", bm[1])
	}

	// ③ 反向对照：Interface status 是**真实**字段，必须仍能显示 Up
	//    （证明 ② 的"整块无数字/无 Up"限定在统计分组内，未被写成恒真断言）。
	if v, ok := qaStatValue(detail, "Interface status"); !ok || v != "Up" {
		t.Errorf("Interface status = %q (ok=%v), want %q — 真实字段不应被占位化", v, ok, "Up")
	}
	// 同理，Server IP address(es) 是真实字段，必须含配置的数字地址。
	if !strings.Contains(detail, "10.1.1.1") || !strings.Contains(detail, "10.1.1.2") {
		t.Errorf("real config fields lost from detail output:\n%s", detail)
	}

	// ④ 汇总表 Fwd 列：按空白切分取末列，恒 "-"；同时校验列数为 7（防列错位）。
	summary := runOn(st, topology.DeviceRouter, "display dhcp relay all")
	rowSeen := false
	for _, line := range strings.Split(summary, "\n") {
		if !strings.HasPrefix(line, iface) {
			continue
		}
		rowSeen = true
		fields := strings.Fields(line)
		if len(fields) != 7 {
			t.Fatalf("AC8: summary row must have 7 columns, got %d: %q", len(fields), line)
		}
		if fields[6] != relayStatPlaceholder {
			t.Errorf("AC8 red line: Fwd column = %q, want %q (row=%q)", fields[6], relayStatPlaceholder, line)
		}
		if qaAnyDigit.MatchString(fields[6]) {
			t.Errorf("AC8 red line: Fwd column %q matches \\d", fields[6])
		}
	}
	if !rowSeen {
		t.Fatalf("AC8: summary row for %s not found:\n%s", iface, summary)
	}
	// 表头末列必须是 Fwd（确认上面取的确实是 Fwd 列，而非碰巧的 Source IP）
	for _, line := range strings.Split(summary, "\n") {
		if strings.HasPrefix(line, "Interface") {
			hf := strings.Fields(line)
			if len(hf) == 0 || hf[len(hf)-1] != "Fwd" {
				t.Errorf("summary header last column = %v, want Fwd", hf)
			}
		}
	}

	// ⑤ 全部 4 种 display dhcp relay* 形态末尾均含诚实注记。
	note := dhcpRelaySimNote()
	empty := NewCLIStateWithType(topology.DeviceRouter)
	runOn(empty, topology.DeviceRouter, "system-view")
	runOn(empty, topology.DeviceRouter, "interface "+iface)
	runOn(empty, topology.DeviceRouter, "quit")
	cases := map[string]string{
		"display dhcp relay":                    runOn(st, topology.DeviceRouter, "display dhcp relay"),
		"display dhcp relay all":                summary,
		"display dhcp relay interface <cfg>":    detail,
		"display dhcp relay all (empty state)":  runOn(empty, topology.DeviceRouter, "display dhcp relay all"),
		"display dhcp relay interface <no-cfg>": runOn(empty, topology.DeviceRouter, "display dhcp relay interface "+iface),
	}
	for name, out := range cases {
		if !strings.Contains(out, note) {
			t.Errorf("AC8: %s output missing dhcpRelaySimNote():\n%s", name, out)
		}
	}
	// 空态提示存在且不是能力拒绝
	if got := cases["display dhcp relay all (empty state)"]; !strings.Contains(got, infoNoDHCPRelayInterface) {
		t.Errorf("AC7/AC8: empty state must show %q, got:\n%s", infoNoDHCPRelayInterface, got)
	}
}

// —— AC7：Vlanif10 参与的确定性与排序 ——

func TestQA_AC7_DeterminismWithVlanifAndOrdering(t *testing.T) {
	st := qaNewRelayL3Switch(t)

	// 空态先断言（配置前）
	if out := runOn(st, topology.DeviceL3Switch, "display dhcp relay all"); !strings.Contains(out, infoNoDHCPRelayInterface) {
		t.Errorf("AC7: empty state must print %q, got:\n%s", infoNoDHCPRelayInterface, out)
	}

	// **倒序**配置 3 个接口，验证 display 仍按接口名升序输出
	type ifCfg struct {
		name    string
		servers []string
	}
	cfgs := []ifCfg{
		{"Vlanif10", []string{"172.16.0.9", "172.16.0.1"}},
		{"GigabitEthernet0/0/2", []string{"10.2.2.9", "10.2.2.1"}},
		{"GigabitEthernet0/0/1", []string{"10.1.1.9", "10.1.1.1"}},
	}
	for _, c := range cfgs {
		if out := runOn(st, topology.DeviceL3Switch, "interface "+c.name); strings.HasPrefix(out, "Error") {
			t.Fatalf("enter interface %s failed: %s", c.name, out)
		}
		if out := runOn(st, topology.DeviceL3Switch, "dhcp select relay"); strings.HasPrefix(out, "Error") {
			t.Fatalf("dhcp select relay on %s failed: %s", c.name, out)
		}
		for _, ip := range c.servers {
			if out := runOn(st, topology.DeviceL3Switch, "dhcp relay server-ip "+ip); strings.HasPrefix(out, "Error") {
				t.Fatalf("server-ip %s on %s failed: %s", ip, c.name, out)
			}
		}
		runOn(st, topology.DeviceL3Switch, "quit")
	}

	// ① 接口按名称升序（G < V），与配置顺序相反 —— 证明不是巧合
	want := []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2", "Vlanif10"}
	if got := collectRelayInterfaces(st); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("AC7: collectRelayInterfaces = %v, want %v", got, want)
	}
	summary := runOn(st, topology.DeviceL3Switch, "display dhcp relay all")
	prev := -1
	for _, name := range want {
		idx := strings.Index(summary, name)
		if idx < 0 {
			t.Fatalf("AC7: summary missing interface %s:\n%s", name, summary)
		}
		if idx <= prev {
			t.Errorf("AC7: interface %s not in ascending position (idx=%d, prev=%d):\n%s", name, idx, prev, summary)
		}
		prev = idx
	}
	if !strings.Contains(summary, "Total: 3 relay interface(s)") {
		t.Errorf("AC7: summary total line wrong:\n%s", summary)
	}

	// ② server-ip 按**配置序**（先配的 .9 必须是 Primary，而非字典序的 .1）
	primaries := map[string]string{
		"GigabitEthernet0/0/1": "10.1.1.9",
		"GigabitEthernet0/0/2": "10.2.2.9",
		"Vlanif10":             "172.16.0.9",
	}
	for _, line := range strings.Split(summary, "\n") {
		for name, wantPrimary := range primaries {
			if !strings.HasPrefix(line, name) {
				continue
			}
			f := strings.Fields(line)
			if len(f) != 7 {
				t.Fatalf("AC7: row %q column count = %d, want 7", line, len(f))
			}
			if f[3] != wantPrimary {
				t.Errorf("AC7: %s Primary Server = %q, want %q (配置序，非字典序)", name, f[3], wantPrimary)
			}
			if f[2] != "2" {
				t.Errorf("AC7: %s Servers count = %q, want 2", name, f[2])
			}
		}
	}
	// 详情块内 server-ip 同样保序
	detail := runOn(st, topology.DeviceL3Switch, "display dhcp relay interface Vlanif10")
	if i9, i1 := strings.Index(detail, "172.16.0.9"), strings.Index(detail, "172.16.0.1"); i9 < 0 || i1 < 0 || i9 > i1 {
		t.Errorf("AC7: detail server-ip order broken (i9=%d i1=%d):\n%s", i9, i1, detail)
	}

	// ③ 连续 10 次字节级完全一致
	for _, raw := range []string{
		"display dhcp relay all",
		"display dhcp relay",
		"display dhcp relay interface Vlanif10",
		"display dhcp relay interface GigabitEthernet0/0/1",
		"display current-configuration",
	} {
		first := runOn(st, topology.DeviceL3Switch, raw)
		for i := 1; i < 10; i++ {
			got := runOn(st, topology.DeviceL3Switch, raw)
			if got != first {
				t.Fatalf("AC7: %q not byte-identical at run %d\n--- first ---\n%s\n--- got ---\n%s", raw, i, first, got)
			}
		}
	}
}

// —— AC10a：配置命令按设备类型守卫（PC / Server / L2 Switch 拒绝）——

func TestQA_AC10a_ConfigCommandsRejectedOnNonL3Devices(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	relayCmds := []string{
		"dhcp select relay",
		"dhcp relay server-ip 10.1.1.1",
		"dhcp relay information enable",
	}

	// ① PC / Server / 二层 Switch：三条配置命令全部被拒，且**一个键都不写**
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer, topology.DeviceSwitch} {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "interface "+iface)
		for _, raw := range relayCmds {
			out := runOn(st, dt, raw)
			if !strings.HasPrefix(out, "Error:") {
				t.Errorf("AC10a: %s on %s = %q, want Error", raw, dt, out)
			}
			if !strings.Contains(out, "not supported") {
				t.Errorf("AC10a: %s on %s = %q, want a capability rejection", raw, dt, out)
			}
		}
		// 二层 Switch 走分支内守卫，文案必须是专用文案（非通用兜底串）
		if dt == topology.DeviceSwitch {
			want := errDHCPRelayNotSupported(string(dt))
			if out := runOn(st, dt, "dhcp relay information enable"); out != want {
				t.Errorf("AC10a: L2 switch information enable = %q, want %q", out, want)
			}
		}
		if _, ok := st.DeviceConfig[dhcpSelectKey(iface)]; ok {
			t.Errorf("AC10a: %s must not write dhcp-select key", dt)
		}
		if n := len(relayKeysOf(st, iface)); n != 0 {
			t.Errorf("AC10a: %s wrote %d relay keys, want 0", dt, n)
		}
	}

	// ② 三层设备正常放行（Router / L3Switch / Firewall），键真实落地
	for _, dt := range []topology.DeviceType{topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceFirewall} {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "dhcp enable")
		if out := runOn(st, dt, "interface "+iface); strings.HasPrefix(out, "Error") {
			t.Fatalf("AC10a: %s cannot enter interface view: %s", dt, out)
		}
		for _, raw := range relayCmds {
			if out := runOn(st, dt, raw); strings.Contains(out, "not supported") || strings.HasPrefix(out, "Error:") {
				t.Errorf("AC10a: %s must allow %q, got %q", dt, raw, out)
			}
		}
		if got := st.DeviceConfig[dhcpSelectKey(iface)]; got != relayModeRelay {
			t.Errorf("AC10a: %s dhcp-select = %q, want relay", dt, got)
		}
		if got := st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)]; got != "10.1.1.1" {
			t.Errorf("AC10a: %s server-ips = %q, want 10.1.1.1", dt, got)
		}
		if got := st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldOption82)]; got != "true" {
			t.Errorf("AC10a: %s option82 = %q, want true", dt, got)
		}
	}

	// ③ 设备集口径 = l3Devices()，且必须包含 VTEP、排除 Switch / PC / Server
	l3 := l3Devices()
	for _, dt := range []topology.DeviceType{
		topology.DeviceRouter, topology.DeviceL3Switch, topology.DeviceFirewall, topology.DeviceVTEP,
	} {
		if !l3[dt] {
			t.Errorf("AC10a: l3Devices() must contain %s", dt)
		}
	}
	for _, dt := range []topology.DeviceType{topology.DeviceSwitch, topology.DevicePC, topology.DeviceServer} {
		if l3[dt] {
			t.Errorf("AC10a: l3Devices() must NOT contain %s", dt)
		}
	}
}

// —— AC10b：display 只读、任意设备可读（含无参形态）——

func TestQA_AC10b_DisplayNoArgReadableOnHosts(t *testing.T) {
	note := dhcpRelaySimNote()
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer} {
		for _, raw := range []string{"display dhcp relay", "display dhcp relay all"} {
			st := NewCLIStateWithType(dt)
			out := runOn(st, dt, raw)
			if strings.Contains(out, "is not supported on") {
				t.Errorf("AC10b: %q on %s must be readable, got %q", raw, dt, out)
			}
			if strings.Contains(out, "not supported") {
				t.Errorf("AC10b: %q on %s must not be capability-rejected, got %q", raw, dt, out)
			}
			if !strings.Contains(out, infoNoDHCPRelayInterface) {
				t.Errorf("AC10b: %q on %s = %q, want empty-state Info", raw, dt, out)
			}
			if !strings.Contains(out, note) {
				t.Errorf("AC10b: %q on %s missing honest sim note, got %q", raw, dt, out)
			}
		}
	}
}

// —— AC10c：二层 Switch 既有 DHCP 池能力逐字不变 ——

func TestQA_AC10c_L2SwitchLegacyDHCPPoolVerbatim(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(st, topology.DeviceSwitch, "system-view")

	if out := runOn(st, topology.DeviceSwitch, "dhcp enable"); out != "DHCP enabled" {
		t.Errorf("AC10c: dhcp enable = %q", out)
	}
	if out := runOn(st, topology.DeviceSwitch, "dhcp pool office"); out != "Enter DHCP pool office view" {
		t.Errorf("AC10c: dhcp pool = %q", out)
	}
	if st.CurrentView != ViewDHCPPool || st.CurrentSub != "office" {
		t.Fatalf("AC10c: pool view not entered: view=%v sub=%q", st.CurrentView, st.CurrentSub)
	}

	// ViewDHCPPool 内全部池子命令（dhcp 前缀形态，逐字断言既有回显）。
	//
	// AC10c 的判定口径是「**逐字不变**」而非「行为正确」：此处锁定的是 a7587b9
	// **之前**（父提交 1c72305）就已存在的既有回显，确保中继改动没有串改二层
	// 交换机的地址池链路。其中 network / lease 两条命令在父提交里就恒返回 usage
	// 错误（parser.go 下标错位，见本文件末尾 QA-FINDING-1），属**既有缺陷**，
	// 不是本次提交引入的回归 —— 因此这里按「不变」锁定其错误回显，一旦有人在
	// 中继改造中顺手改动这两条命令，本用例会立刻失败并要求走独立评审。
	poolCases := []struct{ raw, want string }{
		// 既有缺陷分支（父提交行为一致）：恒 usage 错误
		{"dhcp network 192.168.1.0 mask 255.255.255.0", "Error: usage: network <ip> mask <mask>"},
		{"dhcp lease day 3 hour 6", "Error: usage: lease day <days> [hour <hours>]"},
		// 正常分支：必须成功且回显逐字不变
		{"dhcp gateway-list 192.168.1.254", "Gateway 192.168.1.254 configured"},
		{"dhcp dns-list 8.8.8.8 114.114.114.114", "DNS list configured: 8.8.8.8, 114.114.114.114"},
		{"dhcp excluded-ip-address 192.168.1.1 192.168.1.10", "Excluded IP address: 192.168.1.1-192.168.1.10"},
	}
	for _, c := range poolCases {
		if out := runOn(st, topology.DeviceSwitch, c.raw); out != c.want {
			t.Errorf("AC10c: %q = %q, want %q", c.raw, out, c.want)
		}
		if st.CurrentView != ViewDHCPPool {
			t.Fatalf("AC10c: %q must not leave pool view", c.raw)
		}
	}
	pool := st.DHCP.Pools["office"]
	if pool == nil {
		t.Fatal("AC10c: pool office lost")
	}
	// 正常分支的落库结果
	if pool.Gateway != "192.168.1.254" {
		t.Errorf("AC10c: pool gateway = %q", pool.Gateway)
	}
	if len(pool.DNSList) != 2 || pool.DNSList[0] != "8.8.8.8" || pool.DNSList[1] != "114.114.114.114" {
		t.Errorf("AC10c: pool dns = %v", pool.DNSList)
	}
	if len(pool.ExcludedIPs) != 1 || pool.ExcludedIPs[0] != "192.168.1.1-192.168.1.10" {
		t.Errorf("AC10c: pool excluded = %v", pool.ExcludedIPs)
	}
	// 既有缺陷分支：usage 错误后**不得**产生任何副作用（半写状态比报错更危险）
	if pool.Network != "" || pool.Mask != "" {
		t.Errorf("AC10c: usage 错误后 network/mask 不应落库，got %q/%q", pool.Network, pool.Mask)
	}
	if pool.LeaseTime != "" {
		t.Errorf("AC10c: usage 错误后 lease 不应落库，got %q", pool.LeaseTime)
	}
	// 新增的 ViewInterface 分派绝不能截胡池视图
	if out := runOn(st, topology.DeviceSwitch, "dhcp bogus"); out != "Error: invalid DHCP pool command" {
		t.Errorf("AC10c: pool-view routing hijacked: %q", out)
	}

	// display ip pool 既有行为不受影响（仍可读到该池，且不含中继串扰）
	runOn(st, topology.DeviceSwitch, "quit")
	if out := runOn(st, topology.DeviceSwitch, "display ip pool"); !strings.Contains(out, "office") {
		t.Errorf("AC10c: display ip pool lost pool office:\n%s", out)
	} else if strings.Contains(out, "relay") || strings.Contains(out, "Relay") {
		t.Errorf("AC10c: display ip pool polluted by relay content:\n%s", out)
	}
	if out := runOn(st, topology.DeviceSwitch, "dhcp disable"); out != "DHCP disabled" {
		t.Errorf("AC10c: dhcp disable = %q", out)
	}
}

// —— §1.6 键碰撞：经命令路径验证级联清理不误伤 dhcp-pool ——

func TestQA_KeyCollision_CascadeViaCommandPath(t *testing.T) {
	iface := "Vlanif10"
	poolKey := "interface:" + iface + ":dhcp-pool"

	st := qaNewRelayL3Switch(t)
	if out := runOn(st, topology.DeviceL3Switch, "interface "+iface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter %s failed: %s", iface, out)
	}
	// 既有地址池绑定键（parser.go:2646 口径）与中继键共存于同一接口
	st.DeviceConfig[poolKey] = "office"
	runOn(st, topology.DeviceL3Switch, "dhcp select relay")
	runOn(st, topology.DeviceL3Switch, "dhcp relay server-ip 172.16.0.1")
	runOn(st, topology.DeviceL3Switch, "dhcp relay information enable")
	runOn(st, topology.DeviceL3Switch, "dhcp relay source-ip 172.16.0.254")

	if n := len(relayKeysOf(st, iface)); n != 3 {
		t.Fatalf("setup: relay keys = %d, want 3 (%v)", n, relayKeysOf(st, iface))
	}
	// collectRelayInterfaces 不得因 dhcp-pool 键产生重复 / 幽灵项
	if got := collectRelayInterfaces(st); len(got) != 1 || got[0] != iface {
		t.Fatalf("§1.6: collectRelayInterfaces = %v, want [%s]", got, iface)
	}

	// ① 经命令路径切模式：dhcp select global → 级联清理 relay 键
	if out := runOn(st, topology.DeviceL3Switch, "dhcp select global"); strings.HasPrefix(out, "Error") {
		t.Fatalf("dhcp select global failed: %s", out)
	}
	if got, ok := st.DeviceConfig[poolKey]; !ok || got != "office" {
		t.Errorf("§1.6 red line: dhcp-pool key damaged by cascade cleanup (ok=%v val=%q)", ok, got)
	}
	if got := st.DeviceConfig[dhcpSelectKey(iface)]; got != relayModeGlobal {
		t.Errorf("§1.6: dhcp-select = %q, want global", got)
	}
	if n := len(relayKeysOf(st, iface)); n != 0 {
		t.Errorf("§1.6: cascade cleanup left %d relay keys: %v", n, relayKeysOf(st, iface))
	}
	// 切到 global 后不再是中继接口，display 不得残留幽灵行
	if out := runOn(st, topology.DeviceL3Switch, "display dhcp relay all"); strings.Contains(out, "172.16.0.1") {
		t.Errorf("§1.6: ghost relay config after mode switch:\n%s", out)
	}

	// ② 经命令路径 undo dhcp select → 同样不得误删 dhcp-pool
	runOn(st, topology.DeviceL3Switch, "dhcp select relay")
	runOn(st, topology.DeviceL3Switch, "dhcp relay server-ip 172.16.0.2")
	if out := runOn(st, topology.DeviceL3Switch, "undo dhcp select"); strings.HasPrefix(out, "Error") {
		t.Fatalf("undo dhcp select failed: %s", out)
	}
	if got, ok := st.DeviceConfig[poolKey]; !ok || got != "office" {
		t.Errorf("§1.6 red line: undo dhcp select damaged dhcp-pool key (ok=%v val=%q)", ok, got)
	}
	if _, ok := st.DeviceConfig[dhcpSelectKey(iface)]; ok {
		t.Error("§1.6: undo dhcp select must remove dhcp-select key")
	}
	if n := len(relayKeysOf(st, iface)); n != 0 {
		t.Errorf("§1.6: undo dhcp select left %d relay keys", n)
	}
	// ③ 仅剩 dhcp-pool 键时，中继集合必须为空（不得把地址池接口当中继接口）
	if got := collectRelayInterfaces(st); len(got) != 0 {
		t.Errorf("§1.6: dhcp-pool-only interface treated as relay: %v", got)
	}
	if got := collectDHCPSelectInterfaces(st); len(got) != 0 {
		t.Errorf("§1.6: dhcp-pool-only interface leaked into select set: %v", got)
	}
}

// ============================================================================
// QA-FINDING-1（既有缺陷，**非本次提交引入**，不阻塞 P2 #6 验收）
//
// 位置：internal/cli/parser.go  ViewDHCPPool 分支
//   - 1493 行 `case "network"`：守卫写成 `cmd.Args[1] != "mask"`，但此分支的
//     cmd.Args[0] 恒为 "network"（subCmd 就是从 Args[0] 取的），实参 IP 落在
//     Args[1]、"mask" 落在 Args[2]。整体下标少 1，导致
//     `dhcp network <ip> mask <mask>` **恒**返回 usage 错误；
//     且 1496/1497 行 `pool.Network = cmd.Args[0]` / `pool.Mask = cmd.Args[2]`
//     即便侥幸进到函数体也会把字面量 "network"/"mask" 当数据写库。
//   - 1526 行 `case "lease"`：守卫写成 `cmd.Args[0] != "day"`，而 Args[0] 恒为
//     "lease"，条件**永真** → 该命令 100% 不可达，`dhcp lease day 3 hour 6`
//     恒返回 usage 错误。
//
// 对照：同一分支的 gateway-list / dns-list / excluded-ip-address 均正确使用
// Args[1]/Args[2]，可正常落库 —— 说明这是这两条命令独有的下标错位。
//
// 版本证据：`git show 1c72305:internal/cli/parser.go` 中两处代码逐字相同，
// 故与 a7587b9（DHCP 中继）无因果关系，属存量缺陷。
//
// 影响面：二层交换机地址池的 network/mask/lease 无法通过 `dhcp <sub>` 形态配置，
// `display ip pool` 的 Network / Total / Free 列因此恒为空/0。绕行路径：系统视图
// 的 `ip pool <name> network <ip>`（parser.go:1638）仍可写入 Network（但写不了 Mask）。
//
// 本用例为**特征化测试（characterization test）**：锁定当前行为，使缺陷可见且
// 不被静默改动；修复时应同时更新本用例与 TestQA_AC10c_* 的对应断言。
// ============================================================================

func TestQA_KnownIssue_DHCPPoolNetworkLeaseOffByOne(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(st, topology.DeviceSwitch, "system-view")
	runOn(st, topology.DeviceSwitch, "dhcp enable")
	runOn(st, topology.DeviceSwitch, "dhcp pool office")

	// ① 任何合法写法的 `dhcp network` 都进不去正确分支
	for _, raw := range []string{
		"dhcp network 192.168.1.0 mask 255.255.255.0",
		"dhcp network 10.0.0.0 mask 255.0.0.0",
	} {
		if out := runOn(st, topology.DeviceSwitch, raw); out != "Error: usage: network <ip> mask <mask>" {
			t.Errorf("QA-FINDING-1 已变化：%q = %q（若已修复请同步更新本用例与 AC10c 用例）", raw, out)
		}
	}
	// ② `dhcp lease` 分支恒不可达（守卫条件永真）
	for _, raw := range []string{"dhcp lease day 3 hour 6", "dhcp lease day 7"} {
		if out := runOn(st, topology.DeviceSwitch, raw); out != "Error: usage: lease day <days> [hour <hours>]" {
			t.Errorf("QA-FINDING-1 已变化：%q = %q（若已修复请同步更新本用例与 AC10c 用例）", raw, out)
		}
	}
	// ③ 失败后无半写副作用
	pool := st.DHCP.Pools["office"]
	if pool == nil {
		t.Fatal("pool office lost")
	}
	if pool.Network != "" || pool.Mask != "" || pool.LeaseTime != "" {
		t.Errorf("QA-FINDING-1：usage 错误竟产生副作用 network=%q mask=%q lease=%q",
			pool.Network, pool.Mask, pool.LeaseTime)
	}
	// ④ 源码级定位证据：确认错位下标仍在（供修复者直接定位）
	src, err := os.ReadFile("parser.go")
	if err != nil {
		t.Fatalf("read parser.go: %v", err)
	}
	for _, snippet := range []string{
		`if len(cmd.Args) < 4 || strings.ToLower(cmd.Args[1]) != "mask" {`,
		`if len(cmd.Args) < 2 || strings.ToLower(cmd.Args[0]) != "day" {`,
	} {
		if !strings.Contains(string(src), snippet) {
			t.Logf("QA-FINDING-1 源码片段已变化（可能已修复）：%s", snippet)
		}
	}
}

// —— AC11：中继三文件 import 白名单（零新增依赖的源码级证明）——

func TestQA_AC11_RelaySourcesImportWhitelist(t *testing.T) {
	// 白名单 = Go 标准库 + 本仓 internal/sim。任何第三方 / internal/protocol 出现即失败。
	allowed := map[string]bool{
		`"fmt"`: true, `"net"`: true, `"sort"`: true, `"strings"`: true,
		`"strconv"`: true, `"ensp-lab/internal/sim"`: true,
		`"ensp-lab/internal/topology"`: true,
	}
	importBlock := regexp.MustCompile(`(?s)\nimport \(\n(.*?)\n\)`)
	importLine := regexp.MustCompile(`^\s*(?:[\w.]+\s+)?("[^"]+")\s*$`)

	for _, f := range []string{"dhcp_relay_eval.go", "dhcp_relay_cmd.go", "dhcp_relay_display.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		m := importBlock.FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("%s: import block not found", f)
		}
		found := 0
		for _, line := range strings.Split(m[1], "\n") {
			lm := importLine.FindStringSubmatch(line)
			if lm == nil {
				continue
			}
			found++
			if !allowed[lm[1]] {
				t.Errorf("%s imports %s — 违反零新增依赖 / 架构铁律", f, lm[1])
			}
			if lm[1] == `"ensp-lab/internal/protocol"` {
				t.Errorf("%s must not import internal/protocol", f)
			}
		}
		if found == 0 {
			t.Errorf("%s: import block parsed 0 entries — 断言可能失效", f)
		}
	}
}
