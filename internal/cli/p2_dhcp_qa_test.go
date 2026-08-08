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
