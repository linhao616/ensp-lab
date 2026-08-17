package cli

// ipv6_display_test.go —— IPv6 展示层 + 快照挂载（T03）验收测试。
//
// 覆盖（设计 §3.2 T03 验收，全部走 ExecuteCommandOn + ParseCommand 纯逻辑驱动）：
//   - AC4 ①–⑤ display ipv6 interface brief（升序 / Protocol "-" / 空态 / 字节级确定 / simNote）；
//   - AC5 ①–⑥ display ipv6 interface <if>（真实配置态 / 运行态 "-" / 无效接口 /
//     C3 link-local 双分支：有真实 MAC 键 → fe80::<EUI64> 真实计算，无 MAC → "-"）；
//   - AC7 ①–⑥ display ipv6 routing-table（Direct+Static / 无动态 / RelayNextHop-TunnelID "-" /
//     空态 / 字节级确定 / 前缀**数值**升序 + P1-1 目标过滤）；
//   - AC8 ①–③ display current-configuration IPv6 块 + save→reload 键级往返 + 快照字节级一致；
//   - AC11b display 任意设备可读（PC/Server/Switch 可读 display ipv6 不报能力拒绝）；
//   - AC13 display ripng / display ospfv3（配置态真实 + 运行态 "-" + 注记）；
//   - AC12 ② 键解析精确性：仅 IPv4 键接口（interface:<if>:ip）不得出现在 IPv6 简表。
//
// 🔴 A1 红线：本文件键断言一律走 ipv6_eval.go 的精确 key helper / 精确前缀扫描，
// 严禁任何基于子串的模糊键匹配（AC12 ④ 静态断言覆盖 ipv6_*.go）。

import (
	"fmt"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// ipv6TestDisplayState 构造已配置 IPv6 的 Router 状态（直接写键，专注展示层只读语义；
// 配置命令族语义已由 ipv6_cmd_test.go 覆盖）。
//
// 同时植入 A1 碰撞样本：interface:GigabitEthernet0/0/2:ip 是纯 IPv4 键，与
// interface:<if>:ipv6-* 共享 ":ip" 子串——展示层必须用精确中缀 ":ipv6-" 将其隔离，
// 不得出现在任何 IPv6 输出中（AC12 ②）。
func ipv6TestDisplayState() *CLIState {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/0", ipv6FieldEnable)] = "true"
	st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/0", ipv6FieldAddress)] = "2001:db8::1/64"
	st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable)] = "true"
	st.DeviceConfig["interface:GigabitEthernet0/0/2:ip"] = "10.0.0.1"
	return st
}

// TestIPv6DisplayAC4InterfaceBrief 验证 AC4 ①–⑤（display ipv6 interface brief）。
func TestIPv6DisplayAC4InterfaceBrief(t *testing.T) {
	st := ipv6TestDisplayState()
	out := runOn(st, topology.DeviceRouter, "display ipv6 interface brief")

	// ① 表头 + 数据行（与实现同格式 %-20s %-10s %-10s 自洽计算，PRD §4.2 字段语义）。
	header := fmt.Sprintf("%-20s %-10s %-10s %s", "Interface", "Physical", "Protocol", "IPv6 Address")
	wantRow0 := fmt.Sprintf("%-20s %-10s %-10s %s", "GigabitEthernet0/0/0", "up", "-", "2001:db8::1/64")
	wantRow1 := fmt.Sprintf("%-20s %-10s %-10s %s", "GigabitEthernet0/0/1", "up", "-", "-")
	for _, want := range []string{header, wantRow0, wantRow1} {
		if !strings.Contains(out, want) {
			t.Errorf("AC4① brief missing %q\n---\n%s", want, out)
		}
	}
	// ① 接口名升序（P1-2）。
	i0, i1 := strings.Index(out, "GigabitEthernet0/0/0"), strings.Index(out, "GigabitEthernet0/0/1")
	if i0 < 0 || i1 < 0 || i0 > i1 {
		t.Errorf("AC4① brief rows not ascending: GE0/0/0@%d GE0/0/1@%d\n---\n%s", i0, i1, out)
	}
	// ② Protocol 列恒 "-"（诚实占位）：逐数据行按空白切分，第 3 列必须为 "-"。
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "GigabitEthernet0/0/") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Errorf("AC4② brief row %q too few fields", line)
			continue
		}
		if fields[2] != "-" {
			t.Errorf("AC4② brief Protocol column for %q must be '-', got %q", fields[0], fields[2])
		}
	}
	// ③ 空态：无任何 ipv6 键 → Info: No IPv6 address configured. + 注记。
	stEmpty := NewCLIStateWithType(topology.DeviceRouter)
	outEmpty := runOn(stEmpty, topology.DeviceRouter, "display ipv6 interface brief")
	if !strings.HasPrefix(outEmpty, InfoNoIPv6Address+"\n") {
		t.Errorf("AC4③ empty brief want %q prefix, got:\n%s", InfoNoIPv6Address, outEmpty)
	}
	if !strings.Contains(outEmpty, ipv6SimNote()) {
		t.Errorf("AC4③ empty brief must append ipv6SimNote, got:\n%s", outEmpty)
	}
	// ④ 10× 字节级确定（无 map 随机遍历）。
	first := runOn(st, topology.DeviceRouter, "display ipv6 interface brief")
	for i := 0; i < 10; i++ {
		if got := runOn(st, topology.DeviceRouter, "display ipv6 interface brief"); got != first {
			t.Fatalf("AC4④ brief not deterministic at iter %d:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
	// ⑤ 末尾恒附 ipv6SimNote。
	if !strings.HasSuffix(out, ipv6SimNote()) {
		t.Errorf("AC4⑤ brief must end with ipv6SimNote, tail=%q", tail(out, 60))
	}
	// AC12 ②：仅 IPv4 键接口不得出现在 IPv6 简表。
	if strings.Contains(out, "GigabitEthernet0/0/2") {
		t.Errorf("AC12② IPv4-only interface must NOT appear in ipv6 brief\n---\n%s", out)
	}
	// 缩写形态与全称字节级一致（normalizeIPv6DisplaySub / resolveKeyword 双保险）。
	if abbr := runOn(st, topology.DeviceRouter, "display ipv6 int bri"); abbr != first {
		t.Errorf("AC4 'display ipv6 int bri' must equal full form\n--- full ---\n%s\n--- abbr ---\n%s", first, abbr)
	}
	if bare := runOn(st, topology.DeviceRouter, "display ipv6"); bare != first {
		t.Errorf("AC13 bare 'display ipv6' must equal brief (A13)\n--- brief ---\n%s\n--- bare ---\n%s", first, bare)
	}
}

// TestIPv6DisplayAC5InterfaceDetail 验证 AC5 ①–⑥（display ipv6 interface <if>）。
func TestIPv6DisplayAC5InterfaceDetail(t *testing.T) {
	st := ipv6TestDisplayState()
	out := runOn(st, topology.DeviceRouter, "display ipv6 interface GigabitEthernet0/0/0")

	// ①–③ 真实配置态 + 运行态诚实占位（PRD §4.3 逐字）。
	for _, want := range []string{
		"GigabitEthernet0/0/0 current state : up", // 真实管理态（缺省 up）
		"Line protocol current state : -",
		"IPv6 is enable, link-local address is -",
		"  Global unicast address(es):",
		"    2001:db8::1, subnet is 2001:db8::/64", // NetworkFromPrefix 推导
		"  Joined group address(es):",
		"    -",
		"  MTU is 1500 bytes",
		"  ND DAD is enabled, number of DAD attempts : -",
		"  ND reachable time : - (ms)",
		"  ND retransmit interval : - (ms)",
		"  IPv6 Packet statistics:",
		"    InReceives: -    InErrors: -    InDiscards: -",
		"    OutRequests: -   OutDiscards: -",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AC5 detail missing %q\n---\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, ipv6SimNote()) {
		t.Errorf("AC5 detail must end with ipv6SimNote, tail=%q", tail(out, 60))
	}

	// ④ 无效接口 → ErrIPv6InvalidInterface 逐字。
	bad := runOn(st, topology.DeviceRouter, "display ipv6 interface GigabitEthernet9/9/9")
	if want := fmt.Sprintf(ErrIPv6InvalidInterface, "GigabitEthernet9/9/9"); bad != want {
		t.Errorf("AC5④ invalid interface want %q, got %q", want, bad)
	}

	// ⑤ 未使能接口（enable 缺失）→ "IPv6 is not enable"，诚实不伪造。
	stNoEnable := NewCLIStateWithType(topology.DeviceRouter)
	stNoEnable.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)] = "2001:db8::1/64"
	outNoEnable := runOn(stNoEnable, topology.DeviceRouter, "display ipv6 interface GigabitEthernet0/0/1")
	if !strings.Contains(outNoEnable, "IPv6 is not enable, link-local address is -") {
		t.Errorf("AC5⑤ not-enable branch want 'IPv6 is not enable', got:\n%s", outNoEnable)
	}

	// ⑥ C3 link-local 双分支：有真实 MAC 键 → fe80::<EUI64> 真实计算（EUI-64，非伪造）。
	stMac := NewCLIStateWithType(topology.DeviceRouter)
	stMac.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable)] = "true"
	stMac.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)] = "2001:db8::1/64"
	stMac.DeviceConfig["interface:GigabitEthernet0/0/1:mac"] = "00e0-fc12-0aaa"
	outMac := runOn(stMac, topology.DeviceRouter, "display ipv6 interface GigabitEthernet0/0/1")
	if want := "IPv6 is enable, link-local address is fe80::02e0:fcff:fe12:0aaa"; !strings.Contains(outMac, want) {
		t.Errorf("AC5⑥ with real MAC want %q, got:\n%s", want, outMac)
	}

	// ⑥' 无 MAC 键 → link-local "-"（上例 st 已覆盖，此处再锁一次状态不变性）。
	if !strings.Contains(out, "IPv6 is enable, link-local address is -") {
		t.Errorf("AC5⑥' no-MAC must show '-', got:\n%s", out)
	}

	// 10× 字节级确定。
	first := runOn(st, topology.DeviceRouter, "display ipv6 interface GigabitEthernet0/0/0")
	for i := 0; i < 10; i++ {
		if got := runOn(st, topology.DeviceRouter, "display ipv6 interface GigabitEthernet0/0/0"); got != first {
			t.Fatalf("AC5 detail not deterministic at iter %d", i)
		}
	}
}

// TestIPv6DisplayAC7RoutingTable 验证 AC7 ①–⑥（display ipv6 routing-table）。
func TestIPv6DisplayAC7RoutingTable(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/0", ipv6FieldEnable)] = "true"
	st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/0", ipv6FieldAddress)] = "2001:db8::1/64"
	// 关键排序样本：2001:db8:2::/64 与 2001:db8:10::/64——
	// 字符串比较会把 10::（'1'=0x31）排到 2::（'2'=0x32）之前，数值比较必须相反（AC7 ⑥）。
	st.DeviceConfig[ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2")] = "true"
	st.DeviceConfig[ipv6RouteStaticKey("2001:db8:10::/64", "2001:db8:1::3")] = "true"
	out := runOn(st, topology.DeviceRouter, "display ipv6 routing-table")

	// ① 表头 + 计数 + Direct/Static 块（PRD §4.4 逐字）。
	for _, want := range []string{
		"Route Flags: R - relay, D - download to fib",
		"Routing Table : Public",
		"         Destinations : 3        Routes : 3",
		"Destination  : 2001:db8::",
		"NextHop      : 2001:db8::1",
		"Preference   : 0",
		"Protocol     : Direct",
		"Interface    : GigabitEthernet0/0/0",
		"Destination  : 2001:db8:2::",
		"NextHop      : 2001:db8:1::2",
		"Preference   : 60",
		"Protocol     : Static",
		"Interface    : NULL0",
		"Destination  : 2001:db8:10::",
		"NextHop      : 2001:db8:1::3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AC7① routing-table missing %q\n---\n%s", want, out)
		}
	}
	// ③ RelayNextHop / TunnelID 恒 "-"（诚实占位）。
	if !strings.Contains(out, "RelayNextHop : -") || !strings.Contains(out, "TunnelID     : -") {
		t.Errorf("AC7③ RelayNextHop/TunnelID must be '-'\n---\n%s", out)
	}
	// ② 无动态协议条目。
	for _, dyn := range []string{"Protocol     : OSPF", "Protocol     : RIPng", "Protocol     : BGP", "Protocol     : ISIS", "Protocol     : OSPFv3"} {
		if strings.Contains(out, dyn) {
			t.Errorf("AC7② dynamic protocol %q must NOT appear\n---\n%s", dyn, out)
		}
	}
	// ⑥ 前缀**数值**升序：2:: 必须在 10:: 之前。
	i2, i10 := strings.Index(out, "Destination  : 2001:db8:2::"), strings.Index(out, "Destination  : 2001:db8:10::")
	if i2 < 0 || i10 < 0 || i2 > i10 {
		t.Errorf("AC7⑥ numeric prefix sort violated (2:: must precede 10::)\n---\n%s", out)
	}
	// ⑤ 10× 字节级确定。
	first := runOn(st, topology.DeviceRouter, "display ipv6 routing-table")
	for i := 0; i < 10; i++ {
		if got := runOn(st, topology.DeviceRouter, "display ipv6 routing-table"); got != first {
			t.Fatalf("AC7⑤ routing-table not deterministic at iter %d", i)
		}
	}
	if !strings.HasSuffix(out, ipv6SimNote()) {
		t.Errorf("AC7 routing-table must end with ipv6SimNote, tail=%q", tail(out, 60))
	}

	// ④ 空态。
	stEmpty := NewCLIStateWithType(topology.DeviceRouter)
	outEmpty := runOn(stEmpty, topology.DeviceRouter, "display ipv6 routing-table")
	if !strings.HasPrefix(outEmpty, InfoNoIPv6Route+"\n") || !strings.Contains(outEmpty, ipv6SimNote()) {
		t.Errorf("AC7④ empty routing-table want %q + note, got:\n%s", InfoNoIPv6Route, outEmpty)
	}

	// P1-1 目标过滤：2001:db8:2::/64 只留 1 条，且不残留 10:: 与直连 ::。
	outFiltered := runOn(st, topology.DeviceRouter, "display ipv6 routing-table 2001:db8:2::/64")
	if !strings.Contains(outFiltered, "Destinations : 1        Routes : 1") {
		t.Errorf("P1-1 filter count want 1, got:\n%s", outFiltered)
	}
	if !strings.Contains(outFiltered, "Destination  : 2001:db8:2::") {
		t.Errorf("P1-1 filter must keep 2001:db8:2::, got:\n%s", outFiltered)
	}
	if strings.Contains(outFiltered, "2001:db8:10::") || strings.Contains(outFiltered, "Destination  : 2001:db8::") {
		t.Errorf("P1-1 filter must drop 10:: and direct route, got:\n%s", outFiltered)
	}
}

// TestIPv6DisplayAC8Snapshot 验证 AC8 ①–③（current-configuration IPv6 块 + 持久化往返）。
func TestIPv6DisplayAC8Snapshot(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	// 与 PRD §4.5 操作流一致走命令族。
	runOn(st, dt, "system-view")
	runOn(st, dt, "ipv6")
	runOn(st, dt, "interface GigabitEthernet0/0/1")
	runOn(st, dt, "ipv6 enable")
	runOn(st, dt, "ipv6 address 2001:db8::1/64")
	runOn(st, dt, "quit")
	runOn(st, dt, "ipv6 route-static 2001:db8:2::/64 2001:db8:1::2")

	// ① display current-configuration 输出 IPv6 块（P0-12 / PRD §4.5）。
	before := runOn(st, dt, "display current-configuration")
	for _, want := range []string{
		"interface GigabitEthernet0/0/1",
		" ipv6 enable",
		" ipv6 address 2001:db8::1/64",
		"ipv6 route-static 2001:db8:2::/64 2001:db8:1::2",
	} {
		if !strings.Contains(before, want) {
			t.Errorf("AC8① current-configuration missing %q\n---\n%s", want, before)
		}
	}
	// ① 全局 ipv6:enabled 由既有 formatProtocolBlocks 输出（保留不改）。
	if !strings.Contains(before, "protocol-status") || !strings.Contains(before, " ipv6 enable") {
		t.Errorf("AC8① current-configuration missing global ipv6 enable in protocol-status block\n---\n%s", before)
	}

	// ② save→reload 键级往返（Serialize/Load 零改动路径）。
	cfg := st.SerializeToDeviceConfigData()
	restored := NewCLIStateFromDeviceConfig(dt, cfg, "R1")
	for _, k := range []string{
		ipv6GlobalKey(),
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable),
		ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress),
		ipv6RouteStaticKey("2001:db8:2::/64", "2001:db8:1::2"),
	} {
		if restored.DeviceConfig[k] != st.DeviceConfig[k] {
			t.Errorf("AC8② key %q not restored: %q != %q", k, restored.DeviceConfig[k], st.DeviceConfig[k])
		}
	}

	// ③ reload 后 display current-configuration 完整复现 IPv6 块（独立通道兜底）。
	after := runOn(restored, dt, "display current-configuration")
	for _, want := range []string{
		"interface GigabitEthernet0/0/1",
		" ipv6 enable",
		" ipv6 address 2001:db8::1/64",
		"ipv6 route-static 2001:db8:2::/64 2001:db8:1::2",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("AC8③ post-reload current-configuration missing %q\n---\n%s", want, after)
		}
	}

	// ③ 快照字节级确定（10×）。
	first := runOn(st, dt, "display current-configuration")
	for i := 0; i < 10; i++ {
		if got := runOn(st, dt, "display current-configuration"); got != first {
			t.Fatalf("AC8③ snapshot not deterministic at iter %d", i)
		}
	}
}

// TestIPv6DisplayAC11bAnyDeviceReadable 验证 AC11b（display 任意设备可读，无能力拒绝）。
func TestIPv6DisplayAC11bAnyDeviceReadable(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer, topology.DeviceSwitch} {
		st := NewCLIStateWithType(dt)
		st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldEnable)] = "true"
		st.DeviceConfig[ipv6IfaceKey("GigabitEthernet0/0/1", ipv6FieldAddress)] = "2001:db8::1/64"
		for _, c := range []string{
			"display ipv6 interface brief",
			"display ipv6 interface GigabitEthernet0/0/1",
			"display ipv6 routing-table",
		} {
			out := runOn(st, dt, c)
			if strings.Contains(out, "not supported") || strings.Contains(out, "unknown command") {
				t.Errorf("AC11b %s %q must be readable without capability rejection, got %q", dt, c, out)
			}
			if !strings.Contains(out, ipv6SimNote()) {
				t.Errorf("AC11b %s %q must append ipv6SimNote, got %q", dt, c, out)
			}
		}
	}
}

// TestIPv6DisplayRIPngOSPFv3 验证 AC13 展示（display ripng / display ospfv3 诚实占位）。
func TestIPv6DisplayRIPngOSPFv3(t *testing.T) {
	// RIPng：配置态真实 + 运行态恒 "-" + 注记。
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.DeviceConfig[ipv6RIPngKey("1")] = "true"
	st.DeviceConfig[ipv6RIPngIfaceKey("GigabitEthernet0/0/1", "1")] = "true"
	out := runOn(st, topology.DeviceRouter, "display ripng")
	for _, want := range []string{
		"RIPng process 1",
		"  State : Running",
		"  Interface GigabitEthernet0/0/1 : Enabled",
		"  Neighbors : -",
		"  Routes : -",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AC13 display ripng missing %q\n---\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, ipv6SimNote()) {
		t.Errorf("AC13 display ripng must end with ipv6SimNote, tail=%q", tail(out, 60))
	}
	// 未配置 → Not configured + 注记。
	stEmpty := NewCLIStateWithType(topology.DeviceRouter)
	outEmpty := runOn(stEmpty, topology.DeviceRouter, "display ripng")
	if !strings.HasPrefix(outEmpty, "RIPng: Not configured\n") || !strings.Contains(outEmpty, ipv6SimNote()) {
		t.Errorf("AC13 empty ripng want 'RIPng: Not configured' + note, got:\n%s", outEmpty)
	}

	// OSPFv3：配置态真实 + 运行态恒 "-" + 注记。
	st3 := NewCLIStateWithType(topology.DeviceRouter)
	st3.DeviceConfig[ipv6OSPFv3Key("1")] = "true"
	st3.DeviceConfig[ipv6OSPFv3IfaceKey("GigabitEthernet0/0/1", "1")] = "0"
	out3 := runOn(st3, topology.DeviceRouter, "display ospfv3 1")
	for _, want := range []string{
		"OSPFv3 process 1",
		"  State : Running",
		"  Interface GigabitEthernet0/0/1 : area 0",
		"  Neighbors : -",
		"  LSAs : -",
	} {
		if !strings.Contains(out3, want) {
			t.Errorf("AC13 display ospfv3 missing %q\n---\n%s", want, out3)
		}
	}
	if !strings.HasSuffix(out3, ipv6SimNote()) {
		t.Errorf("AC13 display ospfv3 must end with ipv6SimNote, tail=%q", tail(out3, 60))
	}
	stEmpty3 := NewCLIStateWithType(topology.DeviceRouter)
	outEmpty3 := runOn(stEmpty3, topology.DeviceRouter, "display ospfv3")
	if !strings.HasPrefix(outEmpty3, "OSPFv3: Not configured\n") || !strings.Contains(outEmpty3, ipv6SimNote()) {
		t.Errorf("AC13 empty ospfv3 want 'OSPFv3: Not configured' + note, got:\n%s", outEmpty3)
	}
}

// tail 返回字符串末尾最多 n 个字符（测试失败信息可读性用）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
