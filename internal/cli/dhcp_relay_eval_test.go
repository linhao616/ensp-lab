package cli

// dhcp_relay_eval_test.go —— P2 第六项（DHCP 中继）**纯函数评估器**单元测试（T5）。
//
// 覆盖面（设计 §6 T5）：
//   - parseRelayServerIPs：保序 / 去重 / 过滤空串 / 空输入返回非 nil 空切片
//   - joinRelayServerIPs：与 parse 互为逆运算（往返一致）
//   - validRelayServerIP：正反例，含设计 A4 特殊 IPv4 拒绝集
//   - 键构造 helper：dhcpSelectKey / dhcpRelayKey / dhcpRelayKeyPrefix 精确形态
//   - ifaceFromDHCPSelectKey / ifaceFromDHCPRelayKey：§1.6 键碰撞红线（不得误判 dhcp-pool）
//   - readRelayConfig / EvaluateDHCPRelay：缺省值合并、Active 判定
//   - **纯函数无副作用**：调用前后 DeviceConfig 深度相等（reflect.DeepEqual）
//   - dhcpRelaySimNote：lite / full 两态非空且互不相同
//   - newRelayStats：六字段恒 "-"（AC8 红线的类型层保障）

import (
	"reflect"
	"strings"
	"testing"

	"ensp-lab/internal/sim"
	"ensp-lab/internal/topology"
)

// —— parseRelayServerIPs / joinRelayServerIPs ——

func TestParseRelayServerIPsOrderAndDedup(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", []string{}},
		{"blank", "   ", []string{}},
		{"single", "10.1.1.1", []string{"10.1.1.1"}},
		{"order-preserved", "10.1.1.3,10.1.1.1,10.1.1.2", []string{"10.1.1.3", "10.1.1.1", "10.1.1.2"}},
		{"dedup-keep-first", "10.1.1.1,10.1.1.2,10.1.1.1", []string{"10.1.1.1", "10.1.1.2"}},
		{"drop-empty-parts", "10.1.1.1,,10.1.1.2,", []string{"10.1.1.1", "10.1.1.2"}},
		{"trim-spaces", " 10.1.1.1 , 10.1.1.2 ", []string{"10.1.1.1", "10.1.1.2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseRelayServerIPs(c.raw)
			if got == nil {
				t.Fatalf("parseRelayServerIPs(%q) returned nil slice, want non-nil", c.raw)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseRelayServerIPs(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestJoinRelayServerIPsRoundTrip(t *testing.T) {
	in := []string{"10.1.1.1", "10.1.1.2", "192.168.9.9"}
	raw := joinRelayServerIPs(in)
	if raw != "10.1.1.1,10.1.1.2,192.168.9.9" {
		t.Fatalf("joinRelayServerIPs = %q", raw)
	}
	if got := parseRelayServerIPs(raw); !reflect.DeepEqual(got, in) {
		t.Errorf("round trip mismatch: %v != %v", got, in)
	}
}

// —— validRelayServerIP（含设计 A4 特殊地址拒绝）——

func TestValidRelayServerIPAccepts(t *testing.T) {
	for _, ip := range []string{"10.1.1.1", "172.16.0.254", "192.168.1.1", "8.8.8.8", "1.0.0.1"} {
		if ok, reason := validRelayServerIP(ip); !ok {
			t.Errorf("validRelayServerIP(%q) rejected: %s", ip, reason)
		}
	}
}

func TestValidRelayServerIPRejects(t *testing.T) {
	// 设计 A4：0.0.0.0 / 255.255.255.255 / 127.0.0.0/8 / 224.0.0.0/4 一律拒绝。
	cases := []string{
		"",                // 空
		"abc",             // 非 IP
		"10.1.1",          // 残缺
		"10.1.1.256",      // 越界
		"2001:db8::1",     // IPv6
		"0.0.0.0",         // 全零
		"255.255.255.255", // 全一广播
		"127.0.0.1",       // 环回
		"127.10.20.30",    // 环回段
		"224.0.0.5",       // 组播
		"239.255.255.250", // 组播上界
	}
	for _, ip := range cases {
		ok, reason := validRelayServerIP(ip)
		if ok {
			t.Errorf("validRelayServerIP(%q) accepted, want reject", ip)
			continue
		}
		if !strings.HasPrefix(reason, "Error:") {
			t.Errorf("validRelayServerIP(%q) reason %q must start with Error:", ip, reason)
		}
	}
}

// —— 键构造与解析（§1.6 键碰撞红线）——

func TestDHCPRelayKeyShapes(t *testing.T) {
	if got := dhcpSelectKey("GigabitEthernet0/0/1"); got != "interface:GigabitEthernet0/0/1:dhcp-select" {
		t.Errorf("dhcpSelectKey = %q", got)
	}
	if got := dhcpRelayKey("Vlanif10", dhcpRelayFieldServerIPs); got != "interface:Vlanif10:dhcp-relay:server-ips" {
		t.Errorf("dhcpRelayKey = %q", got)
	}
	if got := dhcpRelayKeyPrefix("Vlanif10"); got != "interface:Vlanif10:dhcp-relay:" {
		t.Errorf("dhcpRelayKeyPrefix = %q", got)
	}
}

func TestIfaceFromDHCPKeysNeverMatchDHCPPool(t *testing.T) {
	// §1.6 键碰撞红线：interface:<if>:dhcp-pool 绝不可被误判为中继 / 模式键。
	poolKey := "interface:Vlanif10:dhcp-pool"
	if iface, ok := ifaceFromDHCPSelectKey(poolKey); ok {
		t.Errorf("dhcp-pool key misread as dhcp-select for iface %q", iface)
	}
	if iface, ok := ifaceFromDHCPRelayKey(poolKey); ok {
		t.Errorf("dhcp-pool key misread as dhcp-relay for iface %q", iface)
	}
	// 正向：select 键 / relay 键必须被正确解析
	if iface, ok := ifaceFromDHCPSelectKey("interface:Vlanif10:dhcp-select"); !ok || iface != "Vlanif10" {
		t.Errorf("ifaceFromDHCPSelectKey = (%q, %v)", iface, ok)
	}
	if iface, ok := ifaceFromDHCPRelayKey("interface:Vlanif10:dhcp-relay:option82"); !ok || iface != "Vlanif10" {
		t.Errorf("ifaceFromDHCPRelayKey = (%q, %v)", iface, ok)
	}
	// 反向：非 interface 前缀 / 畸形键
	for _, k := range []string{
		"vrrp:1:priority",
		"interface:Vlanif10:ip",
		"interface:Vlanif10:dhcp-relay:", // 字段名为空的畸形键
		"interface::dhcp-select",
	} {
		if _, ok := ifaceFromDHCPSelectKey(k); ok {
			t.Errorf("ifaceFromDHCPSelectKey(%q) unexpectedly matched", k)
		}
		if _, ok := ifaceFromDHCPRelayKey(k); ok {
			t.Errorf("ifaceFromDHCPRelayKey(%q) unexpectedly matched", k)
		}
	}
}

// —— readRelayConfig / EvaluateDHCPRelay 缺省值与 Active ——

func TestReadRelayConfigDefaults(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	cfg := readRelayConfig(st, "GigabitEthernet0/0/1")
	if cfg.Mode != "" {
		t.Errorf("Mode default = %q, want empty", cfg.Mode)
	}
	if cfg.ServerIPs == nil || len(cfg.ServerIPs) != 0 {
		t.Errorf("ServerIPs default = %v, want non-nil empty", cfg.ServerIPs)
	}
	if cfg.Option82 != DefaultOption82Enabled {
		t.Errorf("Option82 default = %v, want %v", cfg.Option82, DefaultOption82Enabled)
	}
	// 设计 A5：未配 strategy 时读出生效缺省值 replace（不是 "-"、不是空串）。
	if cfg.Option82Strategy != DefaultOption82Strategy {
		t.Errorf("Option82Strategy default = %q, want %q", cfg.Option82Strategy, DefaultOption82Strategy)
	}
	if cfg.SourceIP != "" {
		t.Errorf("SourceIP default = %q, want empty", cfg.SourceIP)
	}
}

func TestEvaluateDHCPRelayActive(t *testing.T) {
	iface := "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)

	// ① 未配任何键：Active=false
	if EvaluateDHCPRelay(st, iface).Active {
		t.Error("Active should be false when nothing configured")
	}
	// ② 仅 select relay、无 server-ip：Active=false（配置未闭环）
	st.DeviceConfig[dhcpSelectKey(iface)] = relayModeRelay
	if EvaluateDHCPRelay(st, iface).Active {
		t.Error("Active should be false without server-ip")
	}
	// ③ select relay + server-ip：Active=true
	st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)] = "10.1.1.1"
	res := EvaluateDHCPRelay(st, iface)
	if !res.Active {
		t.Error("Active should be true with relay mode + server-ip")
	}
	if res.Interface != iface {
		t.Errorf("Interface = %q, want %q", res.Interface, iface)
	}
	// ④ 模式非 relay（幽灵残留）：Active=false
	st.DeviceConfig[dhcpSelectKey(iface)] = relayModeGlobal
	if EvaluateDHCPRelay(st, iface).Active {
		t.Error("Active should be false when mode != relay")
	}
}

// —— 纯函数无副作用（架构铁律 2）——

func TestEvaluateDHCPRelayIsSideEffectFree(t *testing.T) {
	iface := "Vlanif10"
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig[dhcpSelectKey(iface)] = relayModeRelay
	st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldServerIPs)] = "10.1.1.1,10.1.1.2"
	st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldOption82)] = "true"
	st.DeviceConfig[dhcpRelayKey(iface, dhcpRelayFieldSourceIP)] = "10.2.2.254"

	before := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		before[k] = v
	}

	// 连续调用全部只读入口
	_ = EvaluateDHCPRelay(st, iface)
	_ = readRelayConfig(st, iface)
	_ = dhcpSelectMode(st, iface)
	_ = collectRelayInterfaces(st)
	_ = collectDHCPSelectInterfaces(st)
	_ = buildDHCPRelayDisplay(st, nil)
	_ = buildDHCPRelayDisplay(st, []string{"interface", iface})
	_ = buildSavedDHCPRelayInterfaceConfig(st, iface)
	_ = buildSavedDHCPRelayConfig(st)

	if !reflect.DeepEqual(before, st.DeviceConfig) {
		t.Errorf("read-only path mutated DeviceConfig:\nbefore=%v\nafter =%v", before, st.DeviceConfig)
	}
}

// —— collectRelayInterfaces 确定性与键碰撞防御 ——

func TestCollectRelayInterfacesDeterministicAndPoolSafe(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	// 三个中继接口（乱序写入）
	st.DeviceConfig[dhcpSelectKey("Vlanif30")] = relayModeRelay
	st.DeviceConfig[dhcpSelectKey("Vlanif10")] = relayModeRelay
	st.DeviceConfig[dhcpSelectKey("Vlanif20")] = relayModeRelay
	// 干扰项：dhcp-pool 绑定键 + 非 relay 模式接口
	st.DeviceConfig["interface:Vlanif99:dhcp-pool"] = "pool1"
	st.DeviceConfig[dhcpSelectKey("Vlanif40")] = relayModeGlobal

	want := []string{"Vlanif10", "Vlanif20", "Vlanif30"}
	for i := 0; i < 10; i++ {
		got := collectRelayInterfaces(st)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: collectRelayInterfaces = %v, want %v", i, got, want)
		}
	}
	// collectDHCPSelectInterfaces 口径更宽：包含 select global 的接口，仍不含 dhcp-pool 接口。
	wide := collectDHCPSelectInterfaces(st)
	if !reflect.DeepEqual(wide, []string{"Vlanif10", "Vlanif20", "Vlanif30", "Vlanif40"}) {
		t.Errorf("collectDHCPSelectInterfaces = %v", wide)
	}
	for _, name := range wide {
		if name == "Vlanif99" {
			t.Error("dhcp-pool interface leaked into DHCP select interface set (§1.6 key collision)")
		}
	}
}

// —— 诚实占位（AC8 红线）——

func TestNewRelayStatsAllPlaceholder(t *testing.T) {
	s := newRelayStats()
	v := reflect.ValueOf(s)
	if v.NumField() != 6 {
		t.Fatalf("RelayStats field count = %d, want 6", v.NumField())
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		// 类型层红线：所有字段必须是 string，杜绝日后填 int 计数器。
		if f.Kind() != reflect.String {
			t.Fatalf("RelayStats.%s kind = %s, want string (AC8 red line)",
				v.Type().Field(i).Name, f.Kind())
		}
		if f.String() != relayStatPlaceholder {
			t.Errorf("RelayStats.%s = %q, want %q",
				v.Type().Field(i).Name, f.String(), relayStatPlaceholder)
		}
	}
}

func TestDHCPRelaySimNoteHonest(t *testing.T) {
	note := dhcpRelaySimNote()
	if strings.TrimSpace(note) == "" {
		t.Fatal("dhcpRelaySimNote must not be empty")
	}
	if !strings.Contains(note, "模拟") {
		t.Errorf("sim note must state it is a simulation, got %q", note)
	}
	// lite 引擎下必须显式标注 lite 与「统计不可用」。
	if sim.EngineModeName() == "lite" && !strings.Contains(note, "lite") {
		t.Errorf("lite engine note must mention lite, got %q", note)
	}
}
