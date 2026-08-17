package cli

// ipv6_eval_test.go —— P2 第九项（IPv6 基础，华为 VRP 课程 43/44）工程师纯函数单测（T01）。
//
// 覆盖设计 T01 验收：
//   - AC3  纯函数 golden 断言（地址/前缀校验、压缩幂等、展开、类型、EUI-64 双格式、网络推导）
//   - AC12 键 helper 精确匹配（interface:GE0/0/1:ip 与 :ipv6-address 互不误判）辅助
//   - A3   静态路由多键形态双段解析（含 nexthop 冒号用例）
//   - AC13 纯函数无副作用（调用前后 DeviceConfig deep-equal）
//   - AC12 ①/② 收集器精确命中（IPv4 键 / 全局键 / 异族键不误判）

import (
	"reflect"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— AC3：纯函数 golden 断言 ——

func TestAC3ValidateIPv6Address(t *testing.T) {
	valid := []string{"2001:db8::1", "fe80::1", "ff02::1", "::1", "::", "2001:db8:0:0:0:0:0:1", "FC00::1"}
	for _, s := range valid {
		if err := ValidateIPv6Address(s); err != nil {
			t.Errorf("ValidateIPv6Address(%q) = %v, want nil", s, err)
		}
	}

	invalid := []string{
		"2001:db8::gg",      // 非法十六进制
		"2001:db8::1%eth0",  // zone（A10，AC3 断言）
		"::ffff:1.2.3.4",    // IPv4-mapped（A10 收敛）
		"::1.2.3.4",         // IPv4-compatible（A10 收敛）
		"2001:db8::1/64",    // 地址带前缀不是纯地址
		"",                  // 空串
		"not-an-address",    // 非地址
	}
	for _, s := range invalid {
		if err := ValidateIPv6Address(s); err == nil {
			t.Errorf("ValidateIPv6Address(%q) = nil, want error", s)
		} else if !strings.HasPrefix(err.Error(), "Error: Invalid IPv6 address") {
			t.Errorf("ValidateIPv6Address(%q) error = %q, want ErrIPv6InvalidAddress prefix", s, err.Error())
		}
	}
}

func TestAC3ValidateIPv6Prefix(t *testing.T) {
	valid := []string{"2001:db8::1/64", "2001:db8::/0", "2001:db8::1/128", "fe80::1/10"}
	for _, p := range valid {
		if err := ValidateIPv6Prefix(p); err != nil {
			t.Errorf("ValidateIPv6Prefix(%q) = %v, want nil", p, err)
		}
	}

	invalid := []string{
		"2001:db8::1/129", // 前缀长度越界（AC3）
		"2001:db8::1",     // 缺前缀（AC3）
		"2001:db8::gg/64", // 地址非法
		"/64",             // 缺地址
		"2001:db8::1/",    // 缺长度
		"2001:db8::1/-1",  // 负长度
		"2001:db8::1/abc", // 非数字长度
	}
	for _, p := range invalid {
		if err := ValidateIPv6Prefix(p); err == nil {
			t.Errorf("ValidateIPv6Prefix(%q) = nil, want error", p)
		}
	}
}

func TestAC3CompressIPv6(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},   // 全展开 → 压缩（AC3）
		{"2001:db8:0:0:0:0:0:1", "2001:db8::1"},                       // 全零段压缩（AC3，:: 仅一次）
		{"2001:db8::1", "2001:db8::1"},                                 // 幂等（AC3）
		{"2001:0DB8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},    // 大小写归一
		{"fe80:0000:0000:0000:0000:0000:0000:0001", "fe80::1"},         // link-local 压缩
		{"::", "::"},                                                   // 未指定
		{"::1", "::1"},                                                 // 回环
	}
	for _, c := range cases {
		if got := CompressIPv6(c.in); got != c.want {
			t.Errorf("CompressIPv6(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 幂等性：压缩后再压缩不变（AC3「幂等」）
	if got := CompressIPv6(CompressIPv6("2001:0db8:0000:0000:0000:0000:0000:0001")); got != "2001:db8::1" {
		t.Errorf("CompressIPv6 idempotency broken: %q", got)
	}
}

func TestAC3ExpandIPv6(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2001:db8::1", "2001:0db8:0000:0000:0000:0000:0000:0001"}, // AC3
		{"::", "0000:0000:0000:0000:0000:0000:0000:0000"},
		{"::1", "0000:0000:0000:0000:0000:0000:0000:0001"},
		{"fe80::1", "fe80:0000:0000:0000:0000:0000:0000:0001"},
	}
	for _, c := range cases {
		if got := ExpandIPv6(c.in); got != c.want {
			t.Errorf("ExpandIPv6(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAC3IPv6AddressType(t *testing.T) {
	cases := []struct {
		in   string
		want IPv6AddrType
	}{
		{"fe80::1", IPv6AddrLinkLocal},
		{"fe80::", IPv6AddrLinkLocal},   // fe80::/10
		{"ff02::1", IPv6AddrMulticast},  // ff00::/8
		{"ff00::", IPv6AddrMulticast},
		{"::1", IPv6AddrLoopback},
		{"::", IPv6AddrUnspecified},
		{"2001:db8::1", IPv6AddrGlobalUnicast},
		{"fc00::1", IPv6AddrUniqueLocal}, // fc00::/7（P1-3 类型判定入 P0，不 panic 且归类合理，AC3）
		{"fd12:3456::1", IPv6AddrUniqueLocal},
	}
	for _, c := range cases {
		if got := IPv6AddressType(c.in); got != c.want {
			t.Errorf("IPv6AddressType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 非法输入不 panic（AC3）
	_ = IPv6AddressType("not-an-address")
}

func TestAC3IPv6EUI64InterfaceID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"00e0-fc12-0aaa", "02e0:fcff:fe12:0aaa"},   // 连字符形态 + 翻转 U/L 位（AC3）
		{"00e0fc120aaa", "02e0:fcff:fe12:0aaa"},     // 无分隔形态（C9，AC3）
		{"00E0-FC12-0AAA", "02e0:fcff:fe12:0aaa"},   // 大小写不敏感（C9，AC3）
		{"00-0C-29-01-02-03", "020c:29ff:fe01:0203"}, // VRP 划线 MAC 形态（宽容分隔）
		{"00:0c:29:01:02:03", "020c:29ff:fe01:0203"}, // 冒号分隔形态（宽容分隔）
	}
	for _, c := range cases {
		got, err := EUI64InterfaceID(c.in)
		if err != nil {
			t.Errorf("EUI64InterfaceID(%q) error = %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("EUI64InterfaceID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 非法 MAC
	for _, bad := range []string{"", "00e0-fc12-0aa", "00e0-fc12-0aag", "00e0fc120aaa00"} {
		if _, err := EUI64InterfaceID(bad); err == nil {
			t.Errorf("EUI64InterfaceID(%q) = nil error, want error", bad)
		}
	}
}

func TestAC3IPv6NetworkFromPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2001:db8::1/64", "2001:db8::"},   // AC3
		{"2001:db8:2::1/64", "2001:db8:2::"},
		{"2001:db8::1/128", "2001:db8::1"}, // /128 网络即自身
		{"fe80::1/10", "fe80::"},           // 链路本地 /10 网络
	}
	for _, c := range cases {
		got, err := NetworkFromPrefix(c.in)
		if err != nil {
			t.Errorf("NetworkFromPrefix(%q) error = %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NetworkFromPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := NetworkFromPrefix("bad"); err == nil {
		t.Error("NetworkFromPrefix(bad) = nil error, want error")
	}
}

// —— 键 helper 精确匹配（AC12 ① 辅助，A1 红线）——

func TestAC12IPv6KeyHelpersExactMatch(t *testing.T) {
	// 键构造 helper 逐字精确
	cases := []struct{ got, want string }{
		{ipv6KeyPrefix(), "ipv6:"},
		{ipv6GlobalKey(), "ipv6:enabled"},
		{ipv6IfaceKey("GE0/0/1", ipv6FieldEnable), "interface:GE0/0/1:ipv6-enable"},
		{ipv6IfaceKey("GE0/0/1", ipv6FieldAddress), "interface:GE0/0/1:ipv6-address"},
		{ipv6RouteStaticPrefix(), "ipv6:route-static:"},
		{ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2"), "ipv6:route-static:2001:db8:2::/64:2001:db8:1::2"},
		{ipv6RIPngKey("1"), "ipv6:ripng:1:enabled"},
		{ipv6RIPngIfaceKey("GE0/0/1", "1"), "interface:GE0/0/1:ripng-1-enable"},
		{ipv6OSPFv3Key("1"), "ipv6:ospfv3:1:enabled"},
		{ipv6OSPFv3IfaceKey("GE0/0/1", "1"), "interface:GE0/0/1:ospfv3-1-area"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("key helper = %q, want %q", c.got, c.want)
		}
	}

	// ifaceFromIPv6Key 精确中缀 :ipv6-：IPv4 键 interface:...:ip 必须不匹配（AC12 ①）
	// （IPv4 键与 IPv6 键共享 ":ip" 子串，但 IPv4 键不含字面 ":ipv6-"）
	if iface, field, ok := ifaceFromIPv6Key("interface:GE0/0/1:ip"); ok {
		t.Errorf("ifaceFromIPv6Key(IPv4 key) matched iface=%q field=%q — must not match (A1)", iface, field)
	}
	// 合法 IPv6 键精确命中
	if iface, field, ok := ifaceFromIPv6Key("interface:GE0/0/1:ipv6-address"); !ok || iface != "GE0/0/1" || field != "address" {
		t.Errorf("ifaceFromIPv6Key(ipv6-address) = (%q, %q, %v), want (GE0/0/1, address, true)", iface, field, ok)
	}
	if iface, field, ok := ifaceFromIPv6Key("interface:GE0/0/1:ipv6-enable"); !ok || iface != "GE0/0/1" || field != "enable" {
		t.Errorf("ifaceFromIPv6Key(ipv6-enable) = (%q, %q, %v), want (GE0/0/1, enable, true)", iface, field, ok)
	}
	// 非 interface: 前缀 / 空字段 / 含 ':' 字段均不匹配
	for _, k := range []string{
		"ipv6:enabled",
		"interface:GE0/0/1:ipv6-",
		"interface:GE0/0/1:ipv6-a:b",
		"interface:",
	} {
		if _, _, ok := ifaceFromIPv6Key(k); ok {
			t.Errorf("ifaceFromIPv6Key(%q) matched — must not match", k)
		}
	}

	// 接口 RIPng / OSPFv3 键解析 helper（T04 undo 复用）
	if iface, pid, ok := ifaceFromIPv6RIPngKey("interface:GE0/0/1:ripng-1-enable"); !ok || iface != "GE0/0/1" || pid != "1" {
		t.Errorf("ifaceFromIPv6RIPngKey = (%q, %q, %v), want (GE0/0/1, 1, true)", iface, pid, ok)
	}
	if _, _, ok := ifaceFromIPv6RIPngKey("interface:GE0/0/1:ipv6-enable"); ok {
		t.Error("ifaceFromIPv6RIPngKey matched ipv6-enable — must not match")
	}
	if iface, pid, ok := ifaceFromIPv6OSPFv3Key("interface:GE0/0/1:ospfv3-1-area"); !ok || iface != "GE0/0/1" || pid != "1" {
		t.Errorf("ifaceFromIPv6OSPFv3Key = (%q, %q, %v), want (GE0/0/1, 1, true)", iface, pid, ok)
	}
	if _, _, ok := ifaceFromIPv6OSPFv3Key("interface:GE0/0/1:ipv6-address"); ok {
		t.Error("ifaceFromIPv6OSPFv3Key matched ipv6-address — must not match")
	}
}

// —— A3 双段解析（多键形态静态路由键，AC12 ② 专项）——

func TestAC12ParseIPv6RouteStaticKey(t *testing.T) {
	// 核心用例：nexthop 含冒号（AC12 ②）
	prefix, nexthop, ok := parseIPv6RouteStaticKey("ipv6:route-static:2001:db8:2::/64:2001:db8:1::2")
	if !ok || prefix != "2001:db8:2::/64" || nexthop != "2001:db8:1::2" {
		t.Errorf("parseIPv6RouteStaticKey = (%q, %q, %v), want (2001:db8:2::/64, 2001:db8:1::2, true)", prefix, nexthop, ok)
	}

	// 单段 next hop（::1 无冒号）
	prefix, nexthop, ok = parseIPv6RouteStaticKey("ipv6:route-static:2001:db8:3::/64:2001:db8::1")
	if !ok || prefix != "2001:db8:3::/64" || nexthop != "2001:db8::1" {
		t.Errorf("parseIPv6RouteStaticKey = (%q, %q, %v), want (2001:db8:3::/64, 2001:db8::1, true)", prefix, nexthop, ok)
	}

	// 长度段多位数字
	prefix, nexthop, ok = parseIPv6RouteStaticKey("ipv6:route-static:2001:db8::/128:2001:db8::1")
	if !ok || prefix != "2001:db8::/128" || nexthop != "2001:db8::1" {
		t.Errorf("parseIPv6RouteStaticKey = (%q, %q, %v), want (2001:db8::/128, 2001:db8::1, true)", prefix, nexthop, ok)
	}

	// 非路由键必须不被误判为路由键（AC12 ②）
	for _, k := range []string{
		"ipv6:enabled",                                                    // 全局使能键
		"interface:GE0/0/1:ipv6-address",                                  // 接口地址键
		"ipv6:route-static:2001:db8:2::/64",                               // 缺 nexthop
		"ipv6:route-static:2001:db8:2::/64:",                              // nexthop 空
		"ipv6:route-static:bad",                                           // 无 '/'
		"ipv6:route-static:2001:db8::/xx:2001:db8::1",                     // 长度段非数字
		"ipv6:route-static:2001:db8::/1234:2001:db8::1",                   // 长度段 4 位
		"ipv6:ripng:1:enabled",                                            // RIPng 键
		"ipv6:ospfv3:1:enabled",                                           // OSPFv3 键
	} {
		if p, nh, ok := parseIPv6RouteStaticKey(k); ok {
			t.Errorf("parseIPv6RouteStaticKey(%q) = (%q, %q, true) — must not be a route key", k, p, nh)
		}
	}
}

// —— 收集器精确命中（AC12 ①/②，A1 红线）——

func TestAC12IPv6CollectorsExactMatch(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	// 构造 AC12 ①–③ 并存状态：IPv4 键 / IPv6 键 / 全局键 / 静态路由键 / 异族键
	dc := map[string]string{
		"interface:GigabitEthernet0/0/1:ip":                     "10.0.0.1 255.255.255.0", // IPv4 键（含 :ip 子串）
		"interface:GigabitEthernet0/0/1:ipv6-address":           "2001:db8::1/64",         // IPv6 键
		"interface:GigabitEthernet0/0/2:ipv6-enable":            "true",                   // IPv6 使能键
		"ipv6:enabled":                                          "true",                   // 全局键
		"ipv6:route-static:2001:db8:2::/64:2001:db8:1::2":       "true",                   // 多键形态路由键
		"interface:Bridge-Aggregation1:lag:mode":                "manual",                 // 异族键（gre 历史教训同源）
		"ipv6:ripng:1:enabled":                                  "true",
		"ipv6:ospfv3:1:enabled":                                 "true",
		"interface:GigabitEthernet0/0/1:ripng-1-enable":         "true",
		"interface:GigabitEthernet0/0/1:ospfv3-1-area":          "0",
	}
	st.DeviceConfig = dc

	// ① 接口扫描：只命中 ipv6-address / ipv6-enable 两个接口，IPv4 键与异族键不误判
	ifaces := collectIPv6Interfaces(st)
	wantIfaces := []string{"GigabitEthernet0/0/1", "GigabitEthernet0/0/2"}
	if !reflect.DeepEqual(ifaces, wantIfaces) {
		t.Errorf("collectIPv6Interfaces = %v, want %v (IPv4/异族键不得误判)", ifaces, wantIfaces)
	}

	// ② 静态路由扫描：精确前缀 + 双段解析，只命中路由键
	routes := collectIPv6RouteStatics(st)
	if len(routes) != 1 {
		t.Fatalf("collectIPv6RouteStatics = %v, want exactly 1 route", routes)
	}
	if routes[0].Prefix != "2001:db8:2::/64" || routes[0].NextHop != "2001:db8:1::2" {
		t.Errorf("collectIPv6RouteStatics[0] = %+v, want Prefix=2001:db8:2::/64 NextHop=2001:db8:1::2", routes[0])
	}

	// RIPng / OSPFv3 PID 收集
	if pids := collectRIPngPIDs(st); !reflect.DeepEqual(pids, []string{"1"}) {
		t.Errorf("collectRIPngPIDs = %v, want [1]", pids)
	}
	if pids := collectOSPFv3PIDs(st); !reflect.DeepEqual(pids, []string{"1"}) {
		t.Errorf("collectOSPFv3PIDs = %v, want [1]", pids)
	}

	// 值非 "true" 的路由键不计入
	st.DeviceConfig["ipv6:route-static:2001:db8:9::/64:2001:db8::9"] = "false"
	if routes := collectIPv6RouteStatics(st); len(routes) != 1 {
		t.Errorf("route with value != true must be excluded, got %v", routes)
	}
}

// —— AC13：纯函数无副作用（调用前后 DeviceConfig deep-equal）——

func TestAC13IPv6PureFunctionsNoSideEffects(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig = map[string]string{
		"interface:GigabitEthernet0/0/1:ipv6-address": "2001:db8::1/64",
		"ipv6:enabled":                                "true",
		"sysname":                                     "R1",
	}
	before := cloneDeviceConfig(t, st)

	// 连续两次调用结果一致 + 不改写任何键
	inputs := []string{"2001:db8::1", "fe80::1", "ff02::1", "::1", "::", "fc00::1"}
	for _, in := range inputs {
		_ = ValidateIPv6Address(in)
		_ = ValidateIPv6Prefix(in + "/64")
		_ = CompressIPv6(in)
		_ = ExpandIPv6(in)
		_ = IPv6AddressType(in)
		if _, err := NetworkFromPrefix(in + "/64"); err != nil {
			t.Errorf("NetworkFromPrefix(%s/64) error = %v", in, err)
		}
	}
	_, _ = EUI64InterfaceID("00e0-fc12-0aaa")
	_ = SimulatedLinkLocal("00e0-fc12-0aaa")
	_ = ipv6SimNote()
	_ = readIPv6AddressView(st, "GigabitEthernet0/0/1")
	_ = collectIPv6Interfaces(st)
	_ = collectIPv6RouteStatics(st)
	_ = collectRIPngPIDs(st)
	_ = collectOSPFv3PIDs(st)
	_, _, _ = ifaceFromIPv6Key("interface:GigabitEthernet0/0/1:ipv6-address")
	_, _, _ = parseIPv6RouteStaticKey("ipv6:route-static:2001:db8:2::/64:2001:db8:1::2")

	if !reflect.DeepEqual(st.DeviceConfig, before) {
		t.Errorf("pure function calls mutated DeviceConfig:\nbefore=%v\nafter =%v", before, st.DeviceConfig)
	}
}

// —— 附加：ipv6SimNote 两态非空 ——

func TestIPv6SimNoteNonEmpty(t *testing.T) {
	note := ipv6SimNote()
	if note == "" {
		t.Fatal("ipv6SimNote() must not be empty (P0-7 / AC9)")
	}
	if !strings.Contains(note, "IPv6") {
		t.Errorf("ipv6SimNote() = %q, want IPv6-related note", note)
	}
}

// cloneDeviceConfig 深拷贝 DeviceConfig（断言无副作用用）。
func cloneDeviceConfig(t *testing.T, st *CLIState) map[string]string {
	t.Helper()
	out := make(map[string]string, len(st.DeviceConfig))
	for k, v := range st.DeviceConfig {
		out[k] = v
	}
	return out
}

// —— 只读视图派生（C3 双分支）——

func TestReadIPv6AddressViewLinkLocalDualBranch(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig = map[string]string{
		"interface:GigabitEthernet0/0/1:ipv6-enable":  "true",
		"interface:GigabitEthernet0/0/1:ipv6-address": "2001:db8::1/64",
	}

	// 无 MAC 键 → LinkLocal 恒 "-"（C3 / AC9）
	view := readIPv6AddressView(st, "GigabitEthernet0/0/1")
	if view.Enable != true || view.Address != "2001:db8::1/64" || view.HasMAC {
		t.Errorf("view = %+v, want enable+address, no MAC", view)
	}
	if view.LinkLocal != IPv6StatPlaceholder {
		t.Errorf("LinkLocal without MAC = %q, want %q (严禁伪造 fe80::)", view.LinkLocal, IPv6StatPlaceholder)
	}

	// 有真实 MAC 键 → fe80::<EUI64> 真实计算（C3 例外）
	st.DeviceConfig["interface:GigabitEthernet0/0/1:mac"] = "00e0-fc12-0aaa"
	view = readIPv6AddressView(st, "GigabitEthernet0/0/1")
	if !view.HasMAC {
		t.Error("HasMAC must be true when mac key exists")
	}
	if view.LinkLocal != "fe80::02e0:fcff:fe12:0aaa" {
		t.Errorf("LinkLocal with MAC = %q, want fe80::02e0:fcff:fe12:0aaa (真实推导)", view.LinkLocal)
	}

	// 未使能接口
	view = readIPv6AddressView(st, "GigabitEthernet0/0/2")
	if view.Enable || view.Address != "" {
		t.Errorf("unconfigured view = %+v, want empty", view)
	}
}
