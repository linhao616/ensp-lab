package cli

// iface_sort_test.go —— 接口名「自然序」排序回归测试。
//
// 背景（Review 发现的保真度缺陷）：旧实现同类接口内按字符串字典序排序，
// 导致 GigabitEthernet0/0/24 排在 GigabitEthernet0/0/3 之前（字符 '2' < '3'）。
// 真机华为 VRP 按接口编号的**数值**排序，正确顺序应为 0/0/2 → 0/0/3 → 0/0/24。
//
// 本文件锁定：
//   - naturalLess 的逐段数值比较语义（含前导零、超长编号、纯字符串等边界）
//   - sortInterfaceNames 的「类别优先 + 同类自然序」两级排序
//   - display interface brief 与 display ip interface brief 的实际输出顺序一致

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestNaturalLessOrdering 逐对断言 naturalLess 的比较语义。
func TestNaturalLessOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want bool // 期望 naturalLess(a, b)
		desc string
	}{
		// 核心缺陷场景：数字段必须按数值比较，而不是字典序。
		{"GigabitEthernet0/0/3", "GigabitEthernet0/0/24", true, "3 < 24（字典序会判反）"},
		{"GigabitEthernet0/0/24", "GigabitEthernet0/0/3", false, "24 > 3"},
		{"GigabitEthernet0/0/2", "GigabitEthernet0/0/10", true, "2 < 10（字典序会判反）"},
		{"GigabitEthernet0/0/10", "GigabitEthernet0/0/2", false, "10 > 2"},
		{"GigabitEthernet0/0/9", "GigabitEthernet0/0/10", true, "9 < 10（跨位数）"},
		// 相同名称。
		{"GigabitEthernet0/0/1", "GigabitEthernet0/0/1", false, "相等不小于"},
		// 前导槽位数值比较优先于后续槽位。
		{"GigabitEthernet0/0/1", "GigabitEthernet0/1/0", true, "第二段 0 < 1"},
		{"GigabitEthernet1/0/0", "GigabitEthernet0/0/1", false, "第一段 1 > 0"},
		// 前导零不影响数值比较。
		{"Eth0/0/007", "Eth0/0/8", true, "007 == 7 < 8"},
		{"Eth0/0/08", "Eth0/0/8", false, "08 == 8，不小于"},
		{"Eth0/0/8", "Eth0/0/08", false, "8 == 08，不小于"},
		// 超长编号不溢出（不走 strconv.Atoi 路径）。
		{"Eth0/0/99999999999999999999", "Eth0/0/100000000000000000000", true, "超 int64 位数比较"},
		// 非数字段按字符串比较。
		{"Ethernet0", "GigabitEthernet0", true, "E < G"},
		{"LoopBack0", "LoopBack1", true, "同前缀比数字"},
		{"Vlanif10", "Vlanif9", false, "10 > 9"},
		{"Vlanif9", "Vlanif10", true, "9 < 10"},
		// 前缀关系：短的在前。
		{"Eth0/0/1", "Eth0/0/1x", true, "前缀更短者在前"},
		{"Eth0/0/1x", "Eth0/0/1", false, "更长者在后"},
		// 数字段 vs 非数字段。
		{"Eth1", "Etha", true, "'1'(0x31) < 'a'(0x61)"},
	}
	for _, c := range cases {
		if got := naturalLess(c.a, c.b); got != c.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v —— %s", c.a, c.b, got, c.want, c.desc)
		}
	}
}

// TestNaturalLessIsStrictWeakOrdering naturalLess 必须是严格弱序，
// 否则 sort.Slice 行为未定义（可能 panic 或产生不确定结果）。
func TestNaturalLessIsStrictWeakOrdering(t *testing.T) {
	names := []string{
		"GigabitEthernet0/0/0", "GigabitEthernet0/0/1", "GigabitEthernet0/0/2",
		"GigabitEthernet0/0/3", "GigabitEthernet0/0/10", "GigabitEthernet0/0/24",
		"GigabitEthernet0/1/0", "GigabitEthernet1/0/0",
		"Eth0/0/007", "Eth0/0/7", "Ethernet0", "LoopBack0", "LoopBack1",
		"Vlanif1", "Vlanif9", "Vlanif10", "Tunnel0/0/1", "",
	}
	// ① 反自反性：naturalLess(x, x) 恒 false。
	for _, n := range names {
		if naturalLess(n, n) {
			t.Errorf("naturalLess(%q, %q) = true，违反反自反性", n, n)
		}
	}
	// ② 反对称性：不能同时 a<b 且 b<a。
	for _, a := range names {
		for _, b := range names {
			if naturalLess(a, b) && naturalLess(b, a) {
				t.Errorf("naturalLess(%q,%q) 与 naturalLess(%q,%q) 同时为 true，违反反对称性", a, b, b, a)
			}
		}
	}
	// ③ 传递性：a<b 且 b<c ⇒ a<c。
	for _, a := range names {
		for _, b := range names {
			if !naturalLess(a, b) {
				continue
			}
			for _, c := range names {
				if naturalLess(b, c) && !naturalLess(a, c) {
					t.Errorf("传递性失败: %q<%q, %q<%q, 但 !(%q<%q)", a, b, b, c, a, c)
				}
			}
		}
	}
}

// TestSortInterfaceNamesNaturalOrder 断言两级排序：
// 类别优先（LoopBack → Vlanif → 物理口），同类内按自然序。
func TestSortInterfaceNamesNaturalOrder(t *testing.T) {
	// 故意打乱输入，且包含 Review 点名的 0/0/2 / 0/0/3 / 0/0/10 / 0/0/24 混排。
	got := []string{
		"GigabitEthernet0/0/24",
		"Vlanif10",
		"GigabitEthernet0/0/3",
		"LoopBack1",
		"GigabitEthernet0/0/10",
		"Vlanif2",
		"GigabitEthernet0/0/2",
		"LoopBack0",
		"GigabitEthernet0/0/1",
	}
	want := []string{
		// 类别 0：LoopBack
		"LoopBack0",
		"LoopBack1",
		// 类别 1：Vlanif（2 必须在 10 之前）
		"Vlanif2",
		"Vlanif10",
		// 类别 2：物理口（1 → 2 → 3 → 10 → 24，数值序）
		"GigabitEthernet0/0/1",
		"GigabitEthernet0/0/2",
		"GigabitEthernet0/0/3",
		"GigabitEthernet0/0/10",
		"GigabitEthernet0/0/24",
	}
	sortInterfaceNames(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortInterfaceNames 顺序错误:\n got = %v\nwant = %v", got, want)
	}
}

// TestSortInterfaceNamesIsDeterministic 多次排序不同初始排列必须收敛到同一结果。
func TestSortInterfaceNamesIsDeterministic(t *testing.T) {
	base := []string{
		"GigabitEthernet0/0/2", "GigabitEthernet0/0/24", "GigabitEthernet0/0/3",
		"GigabitEthernet0/0/10", "LoopBack0", "Vlanif10", "Vlanif2",
	}
	first := append([]string(nil), base...)
	sortInterfaceNames(first)

	// 反序输入。
	reversed := append([]string(nil), base...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	sortInterfaceNames(reversed)
	if !reflect.DeepEqual(first, reversed) {
		t.Errorf("反序输入结果不一致:\n%v\nvs\n%v", first, reversed)
	}

	// 字典序输入。
	lexi := append([]string(nil), base...)
	sort.Strings(lexi)
	sortInterfaceNames(lexi)
	if !reflect.DeepEqual(first, lexi) {
		t.Errorf("字典序输入结果不一致:\n%v\nvs\n%v", first, lexi)
	}
}

// newNaturalOrderRouter 构造一台配置了多个数字编号接口的路由器，
// 接口按乱序创建，用于验证输出顺序由排序决定而非创建顺序。
func newNaturalOrderRouter(t *testing.T) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	// 乱序创建：24 → 3 → 10 → 2，若排序失效则输出会保留字典序或创建序。
	for _, name := range []string{
		"GigabitEthernet0/0/24",
		"GigabitEthernet0/0/3",
		"GigabitEthernet0/0/10",
		"GigabitEthernet0/0/2",
	} {
		if out := runOn(st, topology.DeviceRouter, "interface "+name); strings.HasPrefix(out, "Error") {
			t.Fatalf("enter interface %s failed: %s", name, out)
		}
		runOn(st, topology.DeviceRouter, "quit")
	}
	return st
}

// briefInterfaceOrder 从 display interface brief 输出中抽取接口名顺序。
func briefInterfaceOrder(t *testing.T, out string) []string {
	t.Helper()
	names := make([]string, 0, 8)
	for _, row := range briefBody(t, out) {
		names = append(names, strings.TrimSpace(row[:27]))
	}
	return names
}

// numericSuffixOrder 从接口名列表中筛出指定前缀的项，返回其相对顺序。
func numericSuffixOrder(names []string, prefix string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

// TestDisplayInterfaceBriefNaturalOrder display interface brief 的实际输出必须是自然序。
func TestDisplayInterfaceBriefNaturalOrder(t *testing.T) {
	st := newNaturalOrderRouter(t)
	out := runOn(st, topology.DeviceRouter, "display interface brief")
	order := numericSuffixOrder(briefInterfaceOrder(t, out), "GigabitEthernet0/0/")
	want := []string{
		"GigabitEthernet0/0/2",
		"GigabitEthernet0/0/3",
		"GigabitEthernet0/0/10",
		"GigabitEthernet0/0/24",
	}
	// 设备自带的默认接口（0/0/0、0/0/1 等）也在列表中，只校验目标接口的相对顺序。
	filtered := make([]string, 0, len(want))
	for _, n := range order {
		for _, w := range want {
			if n == w {
				filtered = append(filtered, n)
				break
			}
		}
	}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("display interface brief 接口顺序非自然序:\n got = %v\nwant = %v\n---\n%s", filtered, want, out)
	}
}

// TestDisplayIPInterfaceBriefNaturalOrder display ip interface brief 必须与
// display interface brief 使用同一排序口径（共用 sortInterfaceNames）。
func TestDisplayIPInterfaceBriefNaturalOrder(t *testing.T) {
	st := newNaturalOrderRouter(t)
	out := runOn(st, topology.DeviceRouter, "display ip interface brief")
	seen := make([]string, 0, 4)
	want := []string{
		"GigabitEthernet0/0/2",
		"GigabitEthernet0/0/3",
		"GigabitEthernet0/0/10",
		"GigabitEthernet0/0/24",
	}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(strings.SplitN(strings.TrimSpace(line), " ", 2)[0])
		for _, w := range want {
			if name == w {
				seen = append(seen, name)
				break
			}
		}
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("display ip interface brief 接口顺序非自然序:\n got = %v\nwant = %v\n---\n%s", seen, want, out)
	}
}
