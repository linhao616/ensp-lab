package cli

// p2_gre_test.go —— P2 第七项（GRE 隧道，华为 VRP 课程 69）集成验收测试（T5，上半：AC1–AC6）。
//
// 覆盖 PRD §5 验收标准：
//   AC1  接口视图分派 + DeviceConfig 单一事实源写入；旧结构体事实源已废弃
//   AC2  save → reload 持久化贯通（本期最大价值点，现状 100% 丢失）
//   AC3  旧自造系统视图命令与旧展示已下线，且无残留写入路径
//   AC4  IPv4 合法性校验（net.ParseIP + To4 口径）
//   AC5  前置条件 / 视图 / 接口类型守卫（失败必须不写键）
//   AC6  display interface Tunnel<x> 忠实展示（未配 key 显 "-" 而非 "0"）
//
// AC7–AC12 见 p2_gre_qa_test.go。
// 全部经 runOn(state, dt, raw) 驱动，不依赖网络与真实引擎。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

const greTestIface = "Tunnel0/0/1"

// newGRERouter 构造一台已进入 greTestIface 接口视图的路由器。
func newGRERouter(t *testing.T) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "interface "+greTestIface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view failed: %s", out)
	}
	if st.CurrentView != ViewInterface || st.CurrentSub != greTestIface {
		t.Fatalf("unexpected view state: view=%v sub=%q", st.CurrentView, st.CurrentSub)
	}
	return st
}

// newGRETunnelReady 构造一台已 `tunnel-protocol gre` 的路由器（source/destination 前置已满足）。
func newGRETunnelReady(t *testing.T) *CLIState {
	t.Helper()
	st := newGRERouter(t)
	if out := runOn(st, topology.DeviceRouter, "tunnel-protocol gre"); strings.HasPrefix(out, "Error") {
		t.Fatalf("tunnel-protocol gre failed: %s", out)
	}
	return st
}

// newGRETunnelRouter 构造一台已进入指定 Tunnel 接口视图的路由器（QA 测试复用）。
func newGRETunnelRouter(t *testing.T, iface string) *CLIState {
	t.Helper()
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "interface "+iface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view %s failed: %s", iface, out)
	}
	if st.CurrentView != ViewInterface || st.CurrentSub != iface {
		t.Fatalf("unexpected view state: view=%v sub=%q", st.CurrentView, st.CurrentSub)
	}
	return st
}

// configureFullGRE 通过 CLI 完整配置一条 GRE 隧道（tunnel-protocol/source/destination/key）。
func configureFullGRE(t *testing.T, st *CLIState, iface, src, dst, key string) {
	t.Helper()
	for _, step := range []struct {
		cmd string
	}{
		{"tunnel-protocol gre"},
		{"source " + src},
		{"destination " + dst},
		{"gre key " + key},
	} {
		if out := runOn(st, topology.DeviceRouter, step.cmd); strings.HasPrefix(out, "Error") {
			t.Fatalf("configureFullGRE %s failed at %q: %s", iface, step.cmd, out)
		}
	}
}

// greKeysOf 返回该接口全部 gre-* 键（用于「不写任何键」断言）。
func greKeysOf(st *CLIState, iface string) []string {
	prefix := greKeyPrefix(iface)
	out := make([]string, 0)
	for k := range st.DeviceConfig {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// —— AC1：接口视图分派 + 事实源写入 ——

func TestAC1GREInterfaceViewSingleSourceOfTruth(t *testing.T) {
	for _, dt := range []topology.DeviceType{topology.DeviceRouter, topology.DeviceL3Switch} {
		st := NewCLIStateWithType(dt)
		runOn(st, dt, "system-view")
		runOn(st, dt, "interface "+greTestIface)

		for _, cmd := range []string{
			"tunnel-protocol gre",
			"source 202.1.1.1",
			"destination 202.2.2.2",
		} {
			if out := runOn(st, dt, cmd); out != "" {
				t.Errorf("[%s] %q = %q, want silent success (配置成功静默)", dt, cmd, out)
			}
		}

		want := map[string]string{
			"interface:" + greTestIface + ":tunnel-protocol": "gre",
			"interface:" + greTestIface + ":gre-source":      "202.1.1.1",
			"interface:" + greTestIface + ":gre-destination": "202.2.2.2",
		}
		for k, v := range want {
			if got := st.DeviceConfig[k]; got != v {
				t.Errorf("[%s] DeviceConfig[%q] = %q, want %q", dt, k, got, v)
			}
		}
	}
}

// TestAC1NoStructFactSource 反向断言：旧结构体事实源已废弃（静态断言由 AC12 补齐）。
func TestAC1NoStructFactSource(t *testing.T) {
	st := newGRETunnelReady(t)
	runOn(st, topology.DeviceRouter, "source 202.1.1.1")

	// CLIState 上不得再存在 GRE 结构体字段（编译期已保证；此处做运行期反射自证）
	if hasFieldNamedGRE(st) {
		t.Error("CLIState still exposes a GRE struct field — P0-2 结构体事实源应已删除")
	}
}

// —— AC2：save → reload 持久化贯通（本期最大价值点）——

func TestAC2GRESaveReloadRoundTrip(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	for _, cmd := range []string{
		// 掩码形态严格照 PRD §4.1 样例（点分十进制），与 §4.4 期望输出一致
		"ip address 10.0.0.1 255.255.255.252",
		"source 202.1.1.1",
		"destination 202.2.2.2",
		"gre key 1234",
		"keepalive period 5 retry-times 3",
	} {
		if out := runOn(st, dt, cmd); strings.HasPrefix(out, "Error") {
			t.Fatalf("%q = %q", cmd, out)
		}
	}

	// 记录 reload 前的 GRE 键集
	beforeKeys := map[string]string{}
	for k, v := range st.DeviceConfig {
		if strings.HasPrefix(k, greKeyPrefix(greTestIface)) || k == tunnelProtocolKey(greTestIface) {
			beforeKeys[k] = v
		}
	}
	if len(beforeKeys) == 0 {
		t.Fatal("no GRE keys written before save — test premise broken")
	}

	// save → reload 往返
	cfg := st.SerializeToDeviceConfigData()
	reloaded := NewCLIStateFromDeviceConfig(dt, cfg, "R1")

	// ① 键集逐键完全一致
	for k, v := range beforeKeys {
		if got := reloaded.DeviceConfig[k]; got != v {
			t.Errorf("after reload DeviceConfig[%q] = %q, want %q (配置丢失)", k, got, v)
		}
	}
	afterCount := 0
	for k := range reloaded.DeviceConfig {
		if strings.HasPrefix(k, greKeyPrefix(greTestIface)) || k == tunnelProtocolKey(greTestIface) {
			afterCount++
		}
	}
	if afterCount != len(beforeKeys) {
		t.Errorf("GRE key count after reload = %d, want %d", afterCount, len(beforeKeys))
	}

	// ② display interface 复现 source / destination / key / keepalive
	detail := runOn(reloaded, dt, "display interface "+greTestIface)
	for _, sub := range []string{"202.1.1.1", "202.2.2.2", "1234", "period 5", "retry-times 3"} {
		if !strings.Contains(detail, sub) {
			t.Errorf("reloaded display interface missing %q\n---\n%s", sub, detail)
		}
	}

	// ③ display current-configuration 复现 §4.4 全部 6 行
	cur := runOn(reloaded, dt, "display current-configuration")
	for _, line := range []string{
		"interface " + greTestIface,
		"ip address 10.0.0.1 255.255.255.252",
		"tunnel-protocol gre",
		"source 202.1.1.1",
		"destination 202.2.2.2",
		"gre key 1234",
		"keepalive period 5 retry-times 3",
	} {
		if !strings.Contains(cur, line) {
			t.Errorf("reloaded current-configuration missing %q\n---\n%s", line, cur)
		}
	}

	// ④ §4.4 固定顺序：ip address → tunnel-protocol → source → destination → gre key → keepalive
	assertGRELineOrder(t, cur, []string{
		"interface " + greTestIface,
		" ip address 10.0.0.1 255.255.255.252",
		" tunnel-protocol gre",
		" source 202.1.1.1",
		" destination 202.2.2.2",
		" gre key 1234",
		" keepalive period 5 retry-times 3",
	})
}

// assertGRELineOrder 断言给定片段在输出中按序出现（PRD §4.4 固定顺序）。
func assertGRELineOrder(t *testing.T, out string, ordered []string) {
	t.Helper()
	prev := -1
	for _, frag := range ordered {
		idx := strings.Index(out, frag)
		if idx < 0 {
			t.Errorf("current-configuration missing %q", frag)
			return
		}
		if idx < prev {
			t.Errorf("current-configuration line out of order: %q appears before previous fragment\n---\n%s", frag, out)
			return
		}
		prev = idx
	}
}

// TestAC2GREDefaultsNotRedundantlyPersisted 验证「缺省值不冗余落盘」口径。
func TestAC2GREDefaultsNotRedundantlyPersisted(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	// 裸 keepalive：不显式指定 period/retry
	if out := runOn(st, dt, "keepalive"); strings.HasPrefix(out, "Error") {
		t.Fatalf("keepalive = %q", out)
	}
	if _, ok := st.DeviceConfig[greKey(greTestIface, "keepalive-period")]; ok {
		t.Error("bare `keepalive` must not persist default period (差异值口径)")
	}
	cur := runOn(st, dt, "display current-configuration")
	if !strings.Contains(cur, " keepalive\n") {
		t.Errorf("current-configuration should contain bare ` keepalive`\n---\n%s", cur)
	}
	if strings.Contains(cur, "keepalive period 5 retry-times 3") {
		t.Error("default keepalive params must not be redundantly emitted")
	}
	// 未配 key / checksum → 不输出对应行
	if strings.Contains(cur, "gre key") {
		t.Error("unconfigured gre key must not appear in current-configuration")
	}
	if strings.Contains(cur, "gre checksum") {
		t.Error("unconfigured gre checksum must not appear in current-configuration")
	}
}

// —— AC3：旧自造命令与旧展示已下线 ——

func TestAC3LegacySystemViewGRECommandRetired(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	dt := topology.DeviceRouter
	runOn(st, dt, "system-view")

	out := runOn(st, dt, "gre Tunnel0/0/1 202.1.1.1 202.2.2.2")
	// ① 返回含 Tunnel interface view 引导的 Error
	if !strings.HasPrefix(out, "Error:") {
		t.Errorf("legacy system-view gre = %q, want Error: prefix", out)
	}
	if !strings.Contains(out, "Tunnel interface view") {
		t.Errorf("legacy system-view gre = %q, want guidance containing %q", out, "Tunnel interface view")
	}
	// 严禁自造欢快文案
	if strings.Contains(strings.ToLower(out), "created") {
		t.Errorf("legacy command must not report success, got %q", out)
	}
	// ② 断言无任何 gre- 键被写入
	for k := range st.DeviceConfig {
		if strings.Contains(k, ":gre-") || strings.HasSuffix(k, ":tunnel-protocol") {
			t.Errorf("legacy system-view gre wrote key %q — must write nothing (AC3)", k)
		}
	}
}

func TestAC3LegacyDisplayGRERedirects(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	runOn(st, dt, "quit")

	legacy := runOn(st, dt, "display gre")
	modern := runOn(st, dt, "display gre tunnel")

	// C6：旧形态重定向到新实现，二者输出完全一致
	if legacy != modern {
		t.Errorf("`display gre` must redirect to `display gre tunnel`:\nlegacy=%q\nmodern=%q", legacy, modern)
	}
	if !strings.Contains(legacy, "GRE tunnel information") {
		t.Errorf("`display gre` = %q, want summary title", legacy)
	}
	// 旧 map 随机遍历实现的特征字段（未配 key 显 0）必须消失
	if strings.Contains(legacy, "Key: 0") {
		t.Errorf("`display gre` still emits legacy `Key: 0` field:\n%s", legacy)
	}
}

// —— AC4：IPv4 合法性校验 ——

func TestAC4GREEndpointIPValidation(t *testing.T) {
	dt := topology.DeviceRouter
	invalid := []string{"300.1.1.1", "10.1.1", "abc", "10.1.1.1/24", "2001:db8::1"}
	for _, bad := range invalid {
		for _, field := range []string{"source", "destination"} {
			st := newGRETunnelReady(t)
			out := runOn(st, dt, field+" "+bad)
			if !strings.HasPrefix(out, "Error:") {
				t.Errorf("%s %s = %q, want Error:", field, bad, out)
			}
			if !strings.Contains(out, "Invalid IP address") &&
				!strings.Contains(out, "not a valid tunnel address") {
				t.Errorf("%s %s = %q, want invalid-address diagnostic", field, bad, out)
			}
			// 键未被写入或污染
			if v, ok := st.DeviceConfig[greKey(greTestIface, field)]; ok {
				t.Errorf("%s %s wrote key with value %q — must not persist invalid input", field, bad, v)
			}
		}
	}

	valid := []string{"202.1.1.1", "172.16.0.254"}
	for _, good := range valid {
		st := newGRETunnelReady(t)
		if out := runOn(st, dt, "source "+good); out != "" {
			t.Errorf("source %s = %q, want silent success", good, out)
		}
		if got := st.DeviceConfig[greKey(greTestIface, "source")]; got != good {
			t.Errorf("gre-source = %q, want %q", got, good)
		}
	}
}

// TestAC4GREInterfaceNameFormAccepted 验证 C3 双形态：接口名如实存、绝不推导 IP。
func TestAC4GREInterfaceNameFormAccepted(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	if out := runOn(st, dt, "source GigabitEthernet0/0/1"); out != "" {
		t.Errorf("source <interface> = %q, want silent success (C3 双形态)", out)
	}
	got := st.DeviceConfig[greKey(greTestIface, "source")]
	if got != "GigabitEthernet0/0/1" {
		t.Errorf("gre-source = %q, want verbatim interface name (绝不推导 IP)", got)
	}
	// display 原样回显
	runOn(st, dt, "destination 202.2.2.2")
	detail := runOn(st, dt, "display interface "+greTestIface)
	if !strings.Contains(detail, "Tunnel source GigabitEthernet0/0/1") {
		t.Errorf("display must echo interface-name form verbatim\n---\n%s", detail)
	}
}

// —— AC5：前置条件 / 视图 / 接口类型守卫 ——

func TestAC5GREPrerequisiteGuard(t *testing.T) {
	dt := topology.DeviceRouter

	// ① 未 tunnel-protocol gre 就 source → 报错引导，且不写键
	st := newGRERouter(t)
	out := runOn(st, dt, "source 202.1.1.1")
	if !strings.Contains(out, "tunnel-protocol gre") {
		t.Errorf("source without prerequisite = %q, want guidance containing %q", out, "tunnel-protocol gre")
	}
	if keys := greKeysOf(st, greTestIface); len(keys) != 0 {
		t.Errorf("prerequisite failure wrote keys %v — must write nothing (AC5①)", keys)
	}

	// ② 非 Tunnel 口执行 tunnel-protocol gre → 拒绝
	st2 := NewCLIStateWithType(dt)
	runOn(st2, dt, "system-view")
	runOn(st2, dt, "interface GigabitEthernet0/0/1")
	out = runOn(st2, dt, "tunnel-protocol gre")
	if !strings.Contains(out, "only supported on Tunnel interfaces") {
		t.Errorf("tunnel-protocol on physical iface = %q, want Tunnel-only diagnostic", out)
	}
	if got, ok := st2.DeviceConfig[tunnelProtocolKey("GigabitEthernet0/0/1")]; ok {
		t.Errorf("wrote tunnel-protocol key %q on physical interface", got)
	}

	// ③ 系统视图执行 source → 视图拒绝
	st3 := NewCLIStateWithType(dt)
	runOn(st3, dt, "system-view")
	out = runOn(st3, dt, "source 202.1.1.1")
	if !strings.HasPrefix(out, "Error:") {
		t.Errorf("system-view source = %q, want Error:", out)
	}

	// ④ 缺参 → usage
	st4 := newGRETunnelReady(t)
	for cmd, wantSub := range map[string]string{
		"source":      "usage:",
		"destination": "usage:",
		"gre key":     "usage:",
	} {
		if out := runOn(st4, dt, cmd); !strings.Contains(out, wantSub) {
			t.Errorf("%q = %q, want contains %q", cmd, out, wantSub)
		}
	}
}

// TestAC5GRESameAddressRejected 验证 C5：source 与 destination 同址拒绝。
func TestAC5GRESameAddressRejected(t *testing.T) {
	dt := topology.DeviceRouter

	// destination 与已配 source 同址
	st := newGRETunnelReady(t)
	runOn(st, dt, "source 202.1.1.1")
	out := runOn(st, dt, "destination 202.1.1.1")
	if out != errGRESameAddr {
		t.Errorf("same-address destination = %q, want %q", out, errGRESameAddr)
	}
	if _, ok := st.DeviceConfig[greKey(greTestIface, "destination")]; ok {
		t.Error("rejected same-address destination must not be persisted")
	}

	// 反向：source 与已配 destination 同址
	st2 := newGRETunnelReady(t)
	runOn(st2, dt, "destination 202.2.2.2")
	out = runOn(st2, dt, "source 202.2.2.2")
	if out != errGRESameAddr {
		t.Errorf("same-address source = %q, want %q", out, errGRESameAddr)
	}
	if _, ok := st2.DeviceConfig[greKey(greTestIface, "source")]; ok {
		t.Error("rejected same-address source must not be persisted")
	}

	// 接口名形态不比对（C3：绝不推导 IP）
	st3 := newGRETunnelReady(t)
	runOn(st3, dt, "source GigabitEthernet0/0/1")
	if out := runOn(st3, dt, "destination GigabitEthernet0/0/1"); out != "" {
		t.Errorf("interface-name form must not trigger same-address check, got %q", out)
	}
}

// —— AC6：display interface Tunnel<x> 忠实展示 ——

func TestAC6GREDisplayInterfaceFields(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")
	runOn(st, dt, "gre key 1234")
	runOn(st, dt, "keepalive period 5 retry-times 3")

	out := runOn(st, dt, "display interface "+greTestIface)
	for _, sub := range []string{
		"Tunnel source 202.1.1.1, destination 202.2.2.2",
		"Tunnel protocol/transport GRE/IP",
		"--- Tunnel runtime statistics ---",
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("display interface missing %q\n---\n%s", sub, out)
		}
	}
	if !greFieldValueIs(out, "GRE key", "1234") {
		t.Errorf("GRE key field wrong\n---\n%s", out)
	}
	kaLine := greFieldLine(out, "Keepalive")
	for _, sub := range []string{"Enabled", "period 5", "retry-times 3"} {
		if !strings.Contains(kaLine, sub) {
			t.Errorf("Keepalive line %q missing %q", kaLine, sub)
		}
	}
}

// TestAC6GREKeyUnsetShowsDashNotZero 是 P1-1 关键断言（直击旧实现 Key: 0 缺陷）。
func TestAC6GREKeyUnsetShowsDashNotZero(t *testing.T) {
	st := newGRETunnelReady(t)
	dt := topology.DeviceRouter
	runOn(st, dt, "source 202.1.1.1")
	runOn(st, dt, "destination 202.2.2.2")

	out := runOn(st, dt, "display interface "+greTestIface)
	if !greFieldValueIs(out, "GRE key", "-") {
		t.Errorf("unset GRE key must render %q, got line %q", "-", greFieldLine(out, "GRE key"))
	}
	if greFieldValueIs(out, "GRE key", "0") {
		t.Errorf("unset GRE key must NOT render 0 (P1-1)\n---\n%s", out)
	}

	// 对照：显式配置 key 0 时必须显示 0（"" 与 0 可区分，A7）
	runOn(st, dt, "gre key 0")
	out = runOn(st, dt, "display interface "+greTestIface)
	if !greFieldValueIs(out, "GRE key", "0") {
		t.Errorf("explicit `gre key 0` must render 0, got line %q", greFieldLine(out, "GRE key"))
	}
}

func TestAC6GREUnconfiguredTunnelAndBadInterface(t *testing.T) {
	dt := topology.DeviceRouter

	// 未配 GRE 的 Tunnel 口 → 明确提示而非空串
	st := newGRERouter(t)
	out := runOn(st, dt, "display interface "+greTestIface)
	if !strings.Contains(out, infoGREOnIfaceNotCfg) {
		t.Errorf("unconfigured Tunnel display missing %q\n---\n%s", infoGREOnIfaceNotCfg, out)
	}
	// 未配 GRE 时不得输出运行态统计分组（A11③）
	if strings.Contains(out, "--- Tunnel runtime statistics ---") {
		t.Errorf("unconfigured Tunnel must not emit runtime statistics block\n---\n%s", out)
	}

	// 不存在的接口 → 明确 Error
	out = runOn(st, dt, "display interface Tunnel9/9/9")
	if !strings.HasPrefix(out, "Error") {
		t.Errorf("display of nonexistent interface = %q, want Error", out)
	}
}
