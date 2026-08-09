package cli

// p2_gre_qa_test.go —— P2 第七项（GRE 隧道）集成验收测试（T5，下半：AC7–AC12）+ 共享断言 helper。
//
// 覆盖 PRD §5 验收标准：
//   AC7  display gre tunnel 汇总 + 输出确定性（连续 10 次字节级一致）
//   AC8  诚实占位（CRITICAL 红线）：5 运行态字段恒 "-"，State 不得裸 Up
//   AC9  隧道状态诚实派生（Line protocol 本地派生 + 无硬编码 Up）
//   AC10 undo 语义完整（键被删除而非留空串 / 级联清理 / undo interface Tunnel 零回归）
//   AC11 能力守卫（配置命令按 l3Devices() 守卫；display 只读任意设备可读；capabilities.go 零改动）
//   AC12 架构基线合规（静态断言：state.go 无 GRE 结构体、无 state.GRE 残留、不 import internal/protocol）
//
// AC1–AC6 见 p2_gre_test.go。

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// —— 共享断言 helper（p2_gre_test.go 亦复用）——

// greFieldLine 返回 display 输出中以 label 开头的「标签 : 值」行（去空白），未找到返回 ""。
func greFieldLine(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, label) {
			continue
		}
		// 必须是 `label<空格填充>: value` 形态，避免前缀误命中
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, label))
		if strings.HasPrefix(rest, ":") {
			return trimmed
		}
	}
	return ""
}

// greFieldValueIs 断言 label 行的值恰好等于 want。
func greFieldValueIs(out, label, want string) bool {
	line := greFieldLine(out, label)
	if line == "" {
		return false
	}
	idx := strings.Index(line, ":")
	if idx < 0 {
		return false
	}
	return strings.TrimSpace(line[idx+1:]) == want
}

// hasFieldNamedGRE 反射自证 CLIState 上不存在名为 GRE 的字段（P0-2 结构体事实源已删）。
func hasFieldNamedGRE(st *CLIState) bool {
	rt := reflect.TypeOf(*st)
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Name == "GRE" {
			return true
		}
	}
	return false
}

// readCLISource 读取 internal/cli 下指定源文件内容（静态断言用）。
func readCLISource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// —— AC7：汇总表 + 输出确定性 ——

func TestAC7GRESummaryTableAndDeterminism(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")

	// 故意乱序创建，验证输出按接口名升序
	specs := []struct{ iface, src, dst, key string }{
		{"Tunnel0/0/3", "", "", ""},
		{"Tunnel0/0/1", "202.1.1.1", "202.2.2.2", "1234"},
		{"Tunnel0/0/2", "202.1.1.1", "203.3.3.3", ""},
	}
	for _, s := range specs {
		runOn(st, dt, "interface "+s.iface)
		runOn(st, dt, "tunnel-protocol gre")
		if s.src != "" {
			runOn(st, dt, "source "+s.src)
		}
		if s.dst != "" {
			runOn(st, dt, "destination "+s.dst)
		}
		if s.key != "" {
			runOn(st, dt, "gre key "+s.key)
		}
		runOn(st, dt, "quit")
	}

	out := runOn(st, dt, "display gre tunnel")

	// 3 行数据行，按名称升序
	i1 := strings.Index(out, "Tunnel0/0/1")
	i2 := strings.Index(out, "Tunnel0/0/2")
	i3 := strings.Index(out, "Tunnel0/0/3")
	if i1 < 0 || i2 < 0 || i3 < 0 {
		t.Fatalf("summary missing tunnels\n---\n%s", out)
	}
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("tunnels not in ascending order (idx %d,%d,%d)\n---\n%s", i1, i2, i3, out)
	}
	if !strings.Contains(out, "Total: 3") {
		t.Errorf("summary missing `Total: 3`\n---\n%s", out)
	}
	// 未配 key 的隧道显 "-" 不显 "0"
	row2 := greSummaryRowOf(out, "Tunnel0/0/2")
	if strings.Contains(row2, " 0 ") {
		t.Errorf("Tunnel0/0/2 unset key rendered as 0: %q", row2)
	}

	// 输出确定性：连续 10 次字节级完全一致（证明消除 map 随机遍历）
	for i := 0; i < 10; i++ {
		if again := runOn(st, dt, "display gre tunnel"); again != out {
			t.Fatalf("display gre tunnel not deterministic at iteration %d:\n--- first ---\n%s\n--- got ---\n%s", i, out, again)
		}
	}
}

func TestAC7GRESummaryEmptyState(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	out := runOn(st, dt, "display gre tunnel")
	if !strings.Contains(out, "No GRE tunnel configured") {
		t.Errorf("empty summary = %q, want `No GRE tunnel configured`", out)
	}
}

// greSummaryRowOf 返回汇总表中属于 iface 的那一行。
func greSummaryRowOf(out, iface string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), iface) {
			return line
		}
	}
	return ""
}

// —— AC8：诚实占位 CRITICAL 红线 ——

func TestAC8GREHonestPlaceholders(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	runOn(st, dt, "keepalive")
	runOn(st, dt, "quit")

	detail := runOn(st, dt, "display interface "+greTestIface)
	summary := runOn(st, dt, "display gre tunnel")

	// ① 两处输出均含诚实注记
	for name, out := range map[string]string{"display interface": detail, "display gre tunnel": summary} {
		if !strings.Contains(out, "无真实报文封装/解封装与对端协商") {
			t.Errorf("%s missing honest simulation note\n---\n%s", name, out)
		}
	}

	// ② 5 个运行态字段值恒 "-"：不得匹配 \d+，不得匹配 Reachable|Unreachable|Active
	digits := regexp.MustCompile(`\d`)
	fake := regexp.MustCompile(`(?i)\b(Reachable|Unreachable|Active)\b`)
	for _, label := range []string{
		"Keepalive sent", "Keepalive received",
		"Packets encapsulated", "Packets decapsulated", "Peer reachability",
	} {
		line := greFieldLine(detail, label)
		if line == "" {
			t.Errorf("runtime stat %q missing from display\n---\n%s", label, detail)
			continue
		}
		val := strings.TrimSpace(line[strings.Index(line, ":")+1:])
		if val != "-" {
			t.Errorf("runtime stat %q = %q, want %q (AC8 红线)", label, val, "-")
		}
		if digits.MatchString(val) {
			t.Errorf("runtime stat %q = %q contains fabricated digits (AC8 红线)", label, val)
		}
		if fake.MatchString(val) {
			t.Errorf("runtime stat %q = %q contains fabricated status word (AC8 红线)", label, val)
		}
	}

	// ③ 汇总表 State 列不得裸 Up
	row := greSummaryRowOf(summary, greTestIface)
	if row == "" {
		t.Fatalf("summary row missing\n---\n%s", summary)
	}
	if regexp.MustCompile(`\bUp\s*$`).MatchString(strings.TrimRight(row, " \t")) {
		t.Errorf("summary State column is bare `Up` — must carry `*` qualifier (AC8 红线): %q", row)
	}
	if !strings.Contains(row, "*") {
		t.Errorf("summary State column missing `*` marker: %q", row)
	}
	if !strings.Contains(summary, "未与对端协商") {
		t.Errorf("summary missing State derivation footnote\n---\n%s", summary)
	}
}

// —— AC9：隧道状态诚实派生 ——

func TestAC9GRELineProtocolHonestState(t *testing.T) {
	dt := topology.DeviceRouter

	// ① 仅 tunnel-protocol gre，未配 source/destination → DOWN + 原因
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "quit")
	out := runOn(st, dt, "display interface "+greTestIface)
	lp := greFieldLine(out, "Line protocol current state")
	if !strings.Contains(lp, "DOWN") || !strings.Contains(lp, "source/destination not configured") {
		t.Errorf("incomplete tunnel Line protocol = %q, want DOWN + reason", lp)
	}

	// ② 补齐 source + destination → UP 且带诚实限定语，不得裸 UP
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	runOn(st, dt, "quit")
	out = runOn(st, dt, "display interface "+greTestIface)
	lp = greFieldLine(out, "Line protocol current state")
	if !strings.Contains(lp, "UP") {
		t.Errorf("complete tunnel Line protocol = %q, want UP", lp)
	}
	if !strings.Contains(lp, "peer not verified") {
		t.Errorf("complete tunnel Line protocol = %q, want honest qualifier `peer not verified`", lp)
	}
	// 断言不存在「裸 UP 无限定语」
	if strings.TrimSpace(strings.SplitN(lp, ":", 2)[1]) == "UP" {
		t.Errorf("Line protocol must not be bare UP: %q", lp)
	}
}

// TestAC9NoHardcodedTunnelUpInCLI 断言：不存在针对 **Tunnel 口** 的硬编码协议 up 状态。
//
// 断言范围严格限定于 Tunnel 语义（AC9③）：
//   - 静态：GRE 自有三件套 gre_*.go 内不得出现 Status:"up" / "Protocol":"Up" 字面量；
//   - 运行期（更强、非恒真）：创建 Tunnel 口后不得落任何 :protocol 键，
//     协议态必须 display 期由 greLineProtocolState 派生。
//
// 说明：internal/protocol/protocol.go:1388 属包外死代码，本期不改，不纳入断言范围；
// parser.go 中面向物理口的通用 Status:"up" 亦不在本 AC 范围内。
func TestAC9NoHardcodedTunnelUpInCLI(t *testing.T) {
	// ① 静态：GRE 自有文件零硬编码
	bad := regexp.MustCompile(`"Protocol":\s*"Up"|Status:\s*"up"`)
	for _, f := range []string{"gre_eval.go", "gre_cmd.go", "gre_display.go"} {
		src := readCLISource(t, f)
		for i, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // 反面教材注释允许提及
			}
			if bad.MatchString(line) {
				t.Errorf("%s:%d hardcoded tunnel up status: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}

	// ② 运行期：Tunnel 口创建后不得落 :protocol 键（协议态一律 display 期派生）
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	for k, v := range st.DeviceConfig {
		if strings.HasPrefix(k, "interface:"+greTestIface+":") && strings.HasSuffix(k, ":protocol") {
			t.Errorf("Tunnel interface persisted protocol state key %q=%q — must be derived at display time (AC9③)", k, v)
		}
	}
	// 仅 source/destination 缺配时，派生协议态必须是 DOWN（证明没有硬编码 up 兜底）
	if lp := EvaluateGRE(st, greTestIface).LineProtocol; !strings.Contains(lp, "DOWN") {
		t.Errorf("incomplete tunnel derived protocol = %q, want DOWN (no hardcoded up)", lp)
	}
}

// —— AC10：undo 语义完整 ——

func TestAC10GREUndoDeletesKeys(t *testing.T) {
	dt := topology.DeviceRouter
	cases := []struct {
		undo  string
		field string
	}{
		{"undo source", "source"},
		{"undo destination", "destination"},
		{"undo gre key", "key"},
		{"undo keepalive", "keepalive"},
	}
	for _, c := range cases {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "interface "+greTestIface)
		runOn(st, dt, "tunnel-protocol gre")
		runOn(st, dt, "source 202.1.1.1")
		runOn(st, dt, "destination 202.2.2.2")
		runOn(st, dt, "gre key 1234")
		runOn(st, dt, "keepalive period 10 retry-times 4")

		k := greKey(greTestIface, c.field)
		if _, ok := st.DeviceConfig[k]; !ok {
			t.Fatalf("precondition: key %q not set", k)
		}
		runOn(st, dt, c.undo)
		// 键必须被删除而非留空串
		if v, ok := st.DeviceConfig[k]; ok {
			t.Errorf("%q left key %q present with value %q — want deleted", c.undo, k, v)
		}
	}

	// undo keepalive 必须连带删除 period / retry 三键
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "keepalive period 10 retry-times 4")
	runOn(st, dt, "undo keepalive")
	for _, f := range []string{"keepalive", "keepalive-period", "keepalive-retry"} {
		if _, ok := st.DeviceConfig[greKey(greTestIface, f)]; ok {
			t.Errorf("undo keepalive left key gre-%s", f)
		}
	}
}

func TestAC10GREUndoTunnelProtocolCascades(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	runOn(st, dt, "gre key 1234")
	runOn(st, dt, "keepalive")

	runOn(st, dt, "undo tunnel-protocol")

	// 级联清理该口 gre- 精确前缀全部键
	if keys := greKeysOf(st, greTestIface); len(keys) != 0 {
		t.Errorf("undo tunnel-protocol left gre keys %v", keys)
	}
	if _, ok := st.DeviceConfig[tunnelProtocolKey(greTestIface)]; ok {
		t.Error("undo tunnel-protocol left tunnel-protocol key")
	}
	// 隧道从汇总表消失
	runOn(st, dt, "quit")
	out := runOn(st, dt, "display gre tunnel")
	if strings.Contains(out, greTestIface) {
		t.Errorf("tunnel still listed after undo tunnel-protocol\n---\n%s", out)
	}
}

func TestAC10UndoInterfaceTunnelAndEthTrunkNoRegression(t *testing.T) {
	dt := topology.DeviceRouter

	// ① undo interface Tunnel0/0/1 清理 interface:Tunnel0/0/1:* 全部键
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "ip address 10.0.0.1 30")
	runOn(st, dt, "quit")
	runOn(st, dt, "undo interface "+greTestIface)

	prefix := "interface:" + greTestIface + ":"
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			t.Errorf("undo interface left key %q", k)
		}
	}
	if _, ok := st.Interfaces[greTestIface]; ok {
		t.Error("undo interface left state.Interfaces entry")
	}
	// 幂等：重复执行不 panic
	runOn(st, dt, "undo interface "+greTestIface)

	// ② 既有 undo interface Eth-Trunk 行为逐字不变（零回归）
	sw := NewCLIStateWithType(topology.DeviceSwitch)
	runOn(sw, topology.DeviceSwitch, "system-view")
	runOn(sw, topology.DeviceSwitch, "interface Eth-Trunk 1")
	runOn(sw, topology.DeviceSwitch, "quit")
	got := runOn(sw, topology.DeviceSwitch, "undo interface Eth-Trunk 1")
	if strings.Contains(got, "Tunnel") {
		t.Errorf("Eth-Trunk undo path polluted by Tunnel handling: %q", got)
	}
	if strings.HasPrefix(got, "Error") {
		t.Errorf("undo interface Eth-Trunk 1 = %q, want success (零回归)", got)
	}
}

// —— AC11：能力守卫 ——

func TestAC11aGREConfigDeviceGuard(t *testing.T) {
	denied := []topology.DeviceType{topology.DevicePC, topology.DeviceServer, topology.DeviceSwitch}
	for _, dt := range denied {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "interface "+greTestIface)
		for _, cmd := range []string{
			"tunnel-protocol gre", "source 202.1.1.1",
			"destination 202.2.2.2", "gre key 1", "keepalive",
		} {
			out := runOn(st, dt, cmd)
			if !strings.HasPrefix(out, "Error") {
				t.Errorf("[%s] %q = %q, want rejection", dt, cmd, out)
			}
		}
		for k := range st.DeviceConfig {
			if strings.Contains(k, ":gre-") || strings.HasSuffix(k, ":tunnel-protocol") {
				t.Errorf("[%s] denied device wrote GRE key %q", dt, k)
			}
		}
	}

	allowed := []topology.DeviceType{
		topology.DeviceRouter, topology.DeviceL3Switch,
		topology.DeviceFirewall, topology.DeviceVTEP,
	}
	for _, dt := range allowed {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "interface "+greTestIface)
		if out := runOn(st, dt, "tunnel-protocol gre"); strings.HasPrefix(out, "Error") {
			t.Errorf("[%s] tunnel-protocol gre = %q, want allowed", dt, out)
		}
	}
}

func TestAC11bGREDisplayReadableOnAnyDevice(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DevicePC, topology.DeviceServer} {
		st := NewCLIStateWithType(dt)
		out := runOn(st, dt, "display gre tunnel")
		if strings.Contains(out, "is not supported on") {
			t.Errorf("[%s] display gre tunnel = %q, want read-only pass-through (AC11b)", dt, out)
		}
		if !strings.Contains(out, "No GRE tunnel configured") {
			t.Errorf("[%s] display gre tunnel = %q, want empty-state Info", dt, out)
		}
	}
}

func TestAC11cCapabilitiesUnchanged(t *testing.T) {
	src := readCLISource(t, "capabilities.go")
	// 容忍 gofmt 对齐产生的空白填充，但要求键值绑定关系不变
	if !regexp.MustCompile(`"gre":\s*l3Devices\(\)`).MatchString(src) {
		t.Error(`capabilities.go must keep "gre": l3Devices() unchanged (AC11c)`)
	}
	// 不得为 GRE 新增/重定义设备集
	if strings.Contains(src, "greDevices()") {
		t.Error("capabilities.go must not define a new greDevices() set — reuse l3Devices() (AC11c)")
	}
}

// —— AC12：架构基线合规（静态断言）——

func TestAC12GREArchitectureBaseline(t *testing.T) {
	// ① state.go 无 GRE 结构体 / 字段残留
	stateSrc := readCLISource(t, "state.go")
	badState := regexp.MustCompile(`GREConfig|^\s*GRE\s+map|Tunnel\w*\s+struct`)
	for i, line := range strings.Split(stateSrc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // 架构铁律注释允许提及 GRE
		}
		if badState.MatchString(line) {
			t.Errorf("state.go:%d still declares GRE/Tunnel struct: %s", i+1, strings.TrimSpace(line))
		}
	}

	// ② 全仓 internal/cli 生产代码无 state.GRE 残留写入路径
	//    （跳过 _test.go：测试文件本身要引用该标识符做断言，否则自我命中）
	files, _ := filepath.Glob("*.go")
	leak := regexp.MustCompile(`\b(state|st|s)\.GRE\b`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src := readCLISource(t, f)
		for i, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if leak.MatchString(line) {
				t.Errorf("%s:%d references removed struct fact source: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}

	// ③ gre_*.go 不得 import internal/protocol，且不得模糊匹配 Contains("gre")
	for _, f := range []string{"gre_eval.go", "gre_cmd.go", "gre_display.go"} {
		src := readCLISource(t, f)
		if strings.Contains(src, `"ensp-lab/internal/protocol"`) {
			t.Errorf("%s must not import internal/protocol (AC12)", f)
		}
		for i, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if regexp.MustCompile(`strings\.Contains\([^,]+,\s*"gre"\)`).MatchString(line) {
				t.Errorf("%s:%d uses fuzzy Contains(\"gre\") — key collision risk (P0-3): %s", f, i+1, trimmed)
			}
		}
	}
}

// TestAC12GREKeyCollisionEndToEnd 是键碰撞专项的端到端版本（P0-3 最高危）。
func TestAC12GREKeyCollisionEndToEnd(t *testing.T) {
	const lagKey = "interface:Bridge-Aggregation1:lag:mode"
	if !strings.Contains(lagKey, "gre") {
		t.Fatalf("test premise broken: %q must contain substring %q", lagKey, "gre")
	}

	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	runOn(st, dt, "system-view")
	st.DeviceConfig[lagKey] = "lacp-static"
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "tunnel-protocol gre")
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "quit")

	// ① 汇总表只含 Tunnel0/0/1，不含 Bridge-Aggregation1
	out := runOn(st, dt, "display gre tunnel")
	if strings.Contains(out, "Bridge-Aggregation1") {
		t.Errorf("summary leaked LAG interface (key collision!)\n---\n%s", out)
	}
	if !strings.Contains(out, greTestIface) {
		t.Errorf("summary missing %s\n---\n%s", greTestIface, out)
	}

	// ② 级联清理后 LAG 键完好无损
	runOn(st, dt, "interface "+greTestIface)
	runOn(st, dt, "undo tunnel-protocol")
	if v, ok := st.DeviceConfig[lagKey]; !ok || v != "lacp-static" {
		t.Errorf("cascade cleanup damaged LAG key: got %q ok=%v, want %q", v, ok, "lacp-static")
	}
}

// TestAC2GREReloadIPLineViaIndependentChannel 是主理人报告的
// 「reload 后 display current-configuration 丢失 ip address 行」缺陷的**回归锁**。
//
// 根因不是测试污染，而是持久化通道分工：LoadFromDeviceConfigData 只回填
// DeviceConfig，**不会**把 Tunnel 口重建进 state.Interfaces（Tunnel 非设备预置口）。
// 因此 reload 后 Tunnel 块必定由 buildSavedGREConfig 这条独立通道输出，
// 而该通道早期漏写 ip 行（现由 savedInterfaceIPLine 补齐，gre_display.go:250）。
//
// 本测试同时锁住两个不变量，任一被破坏都说明回归：
// ① reload 后 Tunnel 口确实不在 state.Interfaces（独立通道是唯一活跃路径，非偶然命中）
// ② 该通道必须输出 ip address 行，且排在 tunnel-protocol 之前（PRD §4.4 顺序）
func TestAC2GREReloadIPLineViaIndependentChannel(t *testing.T) {
	dt := topology.DeviceRouter
	st := NewCLIStateWithType(dt)
	for _, c := range []string{
		"system-view",
		"interface " + greTestIface,
		"ip address 10.0.0.1 255.255.255.252",
		"tunnel-protocol gre",
		"source 202.1.1.1",
		"destination 202.2.2.2",
		"quit",
	} {
		runOn(st, dt, c)
	}

	reloaded := NewCLIStateFromDeviceConfig(dt, st.SerializeToDeviceConfigData(), "R1")

	// ① 独立通道确为活跃路径（若哪天 reload 改为重建 Tunnel 口，此断言会提醒同步复核 §4.4 顺序）
	if _, ok := reloaded.Interfaces[greTestIface]; ok {
		t.Fatalf("前提变更：reload 后 %s 已被重建进 state.Interfaces，"+
			"buildSavedGREConfig 独立通道不再是活跃路径，请复核 ip address 行与 §4.4 顺序由谁负责输出", greTestIface)
	}

	// ② 独立通道必须补齐 ip address 行（缺失即为主理人报告的原始缺陷复发）
	cur := runOn(reloaded, dt, "display current-configuration")
	const ipLine = " ip address 10.0.0.1 255.255.255.252"
	if !strings.Contains(cur, ipLine) {
		t.Errorf("reload 后 current-configuration 丢失 %q（buildSavedGREConfig 未补齐 ip 行）\n---\n%s", ipLine, cur)
	}
	assertGRELineOrder(t, cur, []string{
		"interface " + greTestIface,
		ipLine,
		" tunnel-protocol gre",
	})

	// ③ savedInterfaceIPLine 纯函数直测：空格形态（GRE save→reload 实际使用的存储形态）
	for _, tc := range []struct{ raw, want string }{
		{"10.0.0.1 255.255.255.252", " ip address 10.0.0.1 255.255.255.252\n"},
		{"", ""},
	} {
		probe := NewCLIStateWithType(dt)
		if tc.raw != "" {
			probe.DeviceConfig["interface:"+greTestIface+":ip"] = tc.raw
		}
		if got := savedInterfaceIPLine(probe, greTestIface); got != tc.want {
			t.Errorf("savedInterfaceIPLine(raw=%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// ④ CIDR 形态：⚠️ 已知缺陷（既有代码，非本期 GRE 引入，QA 记录在案）
	//
	// savedInterfaceIPLine 注释宣称「兼容 <ip>/<prefix> 形态」，但它依赖的
	// prefixToSubnet（tools.go:214）是**有类近似**实现，只分 /8 /16 /24 /32 四档：
	// /30 落进 `prefix >= 24` 分支 → 误还原成 255.255.255.0（正确应为 255.255.255.252）。
	// 这对 GRE 尤其刺眼——点对点隧道惯用 /30，PRD §4.4 样例正是 255.255.255.252。
	//
	// 当前**不可达**：`ip address` 命令以空格形态落盘，GRE save→reload 走不到 CIDR 分支，
	// 故不阻塞本期交付。prefixToSubnet 为 host/PC/parser 共用，修复须连带回归，
	// 已作为独立技术债上报主理人。
	//
	// 此处只断言「IP 段保留 + 输出结构良好」这部分确定成立的性质，
	// 刻意**不断言掩码取值**：既不把错误行为固化成期望，也不制造阻塞交付的红灯。
	probe := NewCLIStateWithType(dt)
	probe.DeviceConfig["interface:"+greTestIface+":ip"] = "10.0.0.1/30"
	got := savedInterfaceIPLine(probe, greTestIface)
	if !strings.HasPrefix(got, " ip address 10.0.0.1 ") || !strings.HasSuffix(got, "\n") {
		t.Errorf("savedInterfaceIPLine(CIDR) = %q, want 形如 \" ip address 10.0.0.1 <mask>\\n\"", got)
	}
	if strings.Contains(got, "/") {
		t.Errorf("savedInterfaceIPLine(CIDR) = %q, 不应残留 \"/\"（须还原为点分掩码形态）", got)
	}
}
