package cli

// display_interface_brief_test.go —— `display interface brief` 真机对齐回归测试。
//
// 背景（用户实测缺陷）：旧实现自造了 `Rate` / `Description` 两列并加了一条 72 字符
// 破折号分隔线，真机华为 VRP 都没有；且 `Protocol` 表头宽 8 而取值只有 `up` 两字符，
// 右侧留白过大，用户把后面的 `10G`（Rate 列）误读成 Protocol 的取值。
//
// 本文件把真机 oracle 固化为断言：
//   - 图例块逐行存在，且不存在破折号分隔线 / Rate / Description
//   - 表头 token 起始列固定为 0/28/34/43/49/58/68
//   - 数据行与表头逐列对齐（含右对齐列的右边界）
//   - 诚实占位：InUti/OutUti 恒为 0%，inErrors/outErrors 恒为 0，且无随机数字
//   - 零回归：Tunnel 口协议态短态 + Tunnel 脚注仅在存在 Tunnel 口时出现
//   - 输出顺序确定（map 迭代顺序不得泄漏到输出）

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// briefHeaderColumns 是真机 `display interface brief` 表头各 token 的起始列（0-based）。
var briefHeaderColumns = []struct {
	token string
	col   int
}{
	{"Interface", 0},
	{"PHY", 28},
	{"Protocol", 34},
	{"InUti", 43},
	{"OutUti", 49},
	{"inErrors", 58},
	{"outErrors", 68},
}

// briefLegendLines 是真机 brief 输出开头图例块的逐行文案。
var briefLegendLines = []string{
	"PHY: Physical",
	"*down: administratively down",
	"^down: standby",
	"(l): loopback",
	"(s): spoofing",
	"(b): BFD down",
	"(e): ETHOAM down",
	"(d): Dampening Suppressed",
	"(p): port alarm down",
	"(dl): DLDP down",
	"InUti/OutUti: input utility rate/output utility rate",
}

// newBriefRouter 构造一台配置了一个物理口与一个 LoopBack 口的路由器（无 Tunnel 口）。
func newBriefRouter(t *testing.T) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceRouter)
	if out := runOn(st, topology.DeviceRouter, "system-view"); strings.HasPrefix(out, "Error") {
		t.Fatalf("system-view failed: %s", out)
	}
	if out := runOn(st, topology.DeviceRouter, "interface GigabitEthernet0/0/0"); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view failed: %s", out)
	}
	if out := runOn(st, topology.DeviceRouter, "ip address 10.0.0.1 255.255.255.0"); strings.HasPrefix(out, "Error") {
		t.Fatalf("ip address failed: %s", out)
	}
	runOn(st, topology.DeviceRouter, "quit")
	if out := runOn(st, topology.DeviceRouter, "interface LoopBack0"); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter LoopBack0 view failed: %s", out)
	}
	runOn(st, topology.DeviceRouter, "quit")
	return st
}

// briefBody 剥掉图例块与表头，返回数据行（不含 Tunnel 脚注）。
func briefBody(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	headerIdx := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "Interface") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		t.Fatalf("brief output has no header line:\n%s", out)
	}
	body := make([]string, 0, len(lines))
	for _, ln := range lines[headerIdx+1:] {
		if strings.HasPrefix(ln, "*") {
			continue // Tunnel 脚注
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		body = append(body, ln)
	}
	return body
}

// TestDisplayInterfaceBriefLegendAndHeader 断言图例块 + 表头列位，并断言旧的
// 自造列（Rate / Description）与破折号分隔线已彻底移除。
func TestDisplayInterfaceBriefLegendAndHeader(t *testing.T) {
	st := newBriefRouter(t)
	out := runOn(st, topology.DeviceRouter, "display interface brief")

	for _, want := range briefLegendLines {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("brief 图例块缺少 %q\n---\n%s", want, out)
		}
	}
	// 图例块必须位于表头之前。
	legendIdx := strings.Index(out, "PHY: Physical")
	headerIdx := strings.Index(out, "Interface  ")
	if legendIdx < 0 || headerIdx < 0 || legendIdx > headerIdx {
		t.Fatalf("图例块必须出现在表头之前, legendIdx=%d headerIdx=%d\n---\n%s", legendIdx, headerIdx, out)
	}

	// 真机没有破折号分隔线，也没有 Rate / Description 列。
	for _, forbidden := range []string{"----", "Rate", "Description"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("brief 输出不应再包含 %q（真机无此内容）\n---\n%s", forbidden, out)
		}
	}

	header := ""
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "Interface") {
			header = ln
			break
		}
	}
	if header == "" {
		t.Fatalf("未找到表头行\n---\n%s", out)
	}
	for _, c := range briefHeaderColumns {
		if got := strings.Index(header, c.token); got != c.col {
			t.Errorf("表头 token %q 起始列 = %d, want %d\nheader=%q", c.token, got, c.col, header)
		}
	}
}

// TestDisplayInterfaceBriefRowAlignment 断言数据行与表头逐列对齐，
// 且诚实占位值（0% / 0）落在正确的右对齐位置。
func TestDisplayInterfaceBriefRowAlignment(t *testing.T) {
	st := newBriefRouter(t)
	out := runOn(st, topology.DeviceRouter, "display interface brief")
	body := briefBody(t, out)
	if len(body) == 0 {
		t.Fatalf("brief 无数据行\n---\n%s", out)
	}
	for _, row := range body {
		if len(row) != 77 {
			t.Errorf("数据行宽度 = %d, want 77（列格式串固定宽度）: %q", len(row), row)
			continue
		}
		// PHY 列左对齐，起始列 28。
		if phy := strings.TrimSpace(row[28:33]); phy != "up" && phy != "down" {
			t.Errorf("PHY 列 = %q, want up/down: %q", phy, row)
		}
		// Protocol 列左对齐，起始列 34，宽 8。
		if proto := strings.TrimSpace(row[34:42]); proto == "" {
			t.Errorf("Protocol 列为空: %q", row)
		}
		// InUti / OutUti 右对齐，右边界分别是 47 / 54。
		if got := row[43:48]; got != "   0%" {
			t.Errorf("InUti 列 = %q, want %q: %q", got, "   0%", row)
		}
		if got := row[49:55]; got != "    0%" {
			t.Errorf("OutUti 列 = %q, want %q: %q", got, "    0%", row)
		}
		// inErrors / outErrors 右对齐，右边界分别是 65 / 76。
		if got := row[56:66]; got != "         0" {
			t.Errorf("inErrors 列 = %q, want 右对齐的 0: %q", got, row)
		}
		if got := row[67:77]; got != "         0" {
			t.Errorf("outErrors 列 = %q, want 右对齐的 0: %q", got, row)
		}
	}
}

// TestDisplayInterfaceBriefHonestZeroCounters 诚实占位红线：
// lite 引擎无真实数据平面，利用率与错误计数必须恒为零值，不得出现编造的非零数字。
func TestDisplayInterfaceBriefHonestZeroCounters(t *testing.T) {
	st := newBriefRouter(t)
	first := runOn(st, topology.DeviceRouter, "display interface brief")
	second := runOn(st, topology.DeviceRouter, "display interface brief")
	if first != second {
		t.Fatalf("两次 brief 输出不一致（疑似随机数字/不确定顺序）:\nfirst=\n%s\nsecond=\n%s", first, second)
	}
	for _, row := range briefBody(t, first) {
		tail := row[43:]
		for _, digit := range "123456789" {
			if strings.ContainsRune(tail, digit) {
				t.Errorf("统计列出现非零数字 %q（违反诚实占位红线）: %q", string(digit), row)
				break
			}
		}
	}
}

// TestDisplayInterfaceBriefTunnelZeroRegression 零回归：
// Tunnel 口协议态仍走 greLineProtocolBrief 的诚实短态，且脚注仅在存在 Tunnel 口时出现。
func TestDisplayInterfaceBriefTunnelZeroRegression(t *testing.T) {
	const footnote = "* Tunnel protocol state is derived from local configuration only."

	// ① 无 Tunnel 口：不得出现脚注。
	plain := runOn(newBriefRouter(t), topology.DeviceRouter, "display interface brief")
	if strings.Contains(plain, footnote) {
		t.Errorf("无 Tunnel 口时不应出现脚注\n---\n%s", plain)
	}

	// ② 有 Tunnel 口但未配 source/destination：Protocol 短态为 down，且出现脚注。
	st := newBriefRouter(t)
	if out := runOn(st, topology.DeviceRouter, "interface "+greTestIface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter tunnel view failed: %s", out)
	}
	runOn(st, topology.DeviceRouter, "quit")
	out := runOn(st, topology.DeviceRouter, "display interface brief")
	if !strings.Contains(out, footnote) {
		t.Errorf("存在 Tunnel 口时应追加脚注\n---\n%s", out)
	}
	tunnelRow := ""
	for _, row := range briefBody(t, out) {
		if strings.HasPrefix(row, greTestIface) {
			tunnelRow = row
			break
		}
	}
	if tunnelRow == "" {
		t.Fatalf("brief 未列出 Tunnel 口 %s\n---\n%s", greTestIface, out)
	}
	if got := strings.TrimSpace(tunnelRow[34:42]); got != "down" {
		t.Errorf("未配 source/destination 的 Tunnel 口 Protocol = %q, want down: %q", got, tunnelRow)
	}

	// ③ 配齐 source/destination 后短态转为 up*，且仍能放进 %-8s 列宽。
	if out := runOn(st, topology.DeviceRouter, "interface "+greTestIface); strings.HasPrefix(out, "Error") {
		t.Fatalf("re-enter tunnel view failed: %s", out)
	}
	runOn(st, topology.DeviceRouter, "tunnel-protocol gre")
	runOn(st, topology.DeviceRouter, "source 10.0.0.1")
	runOn(st, topology.DeviceRouter, "destination 10.0.0.2")
	runOn(st, topology.DeviceRouter, "quit")
	out = runOn(st, topology.DeviceRouter, "display interface brief")
	for _, row := range briefBody(t, out) {
		if !strings.HasPrefix(row, greTestIface) {
			continue
		}
		if got := strings.TrimSpace(row[34:42]); got != "up*" {
			t.Errorf("配齐后的 Tunnel 口 Protocol = %q, want up*: %q", got, row)
		}
		if len(row) != 77 {
			t.Errorf("Tunnel 数据行宽度 = %d, want 77（up* 必须放得进 %%-8s）: %q", len(row), row)
		}
	}
}

// TestDisplayInterfaceBriefDeterministicOrder 断言输出顺序确定：
// LoopBack → Vlanif → 其余物理口，同类按**自然序**（编号数值序）升序，
// 与 display ip interface brief 共用 sortInterfaceNames，口径一致。
//
// 注意：这里必须用 naturalLess 而非字符串 `<` 比较。真机 VRP 按接口编号数值排序，
// 字典序会把 GigabitEthernet0/0/24 判在 0/0/3 之前（'2' < '3'），与真机不符。
func TestDisplayInterfaceBriefDeterministicOrder(t *testing.T) {
	st := newBriefRouter(t)
	out := runOn(st, topology.DeviceRouter, "display interface brief")
	body := briefBody(t, out)
	if len(body) < 2 {
		t.Fatalf("数据行不足以校验顺序\n---\n%s", out)
	}
	prevCat, prevName := -1, ""
	for _, row := range body {
		name := strings.TrimSpace(row[:27])
		cat := ifaceCategory(name)
		if cat < prevCat {
			t.Errorf("接口类别顺序错乱: %q(cat=%d) 出现在 cat=%d 之后\n---\n%s", name, cat, prevCat, out)
		}
		// 同类内后一个接口不得「自然序小于」前一个。
		if cat == prevCat && prevName != "" && naturalLess(name, prevName) {
			t.Errorf("同类接口未按自然序升序: %q 出现在 %q 之后\n---\n%s", name, prevName, out)
		}
		prevCat, prevName = cat, name
	}
}
