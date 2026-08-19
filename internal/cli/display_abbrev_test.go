// display_abbrev_test.go 锁死 display 子命令缩写语义（v0.12.1 修正）。
//
// 语义（与华为 VRP 一致）：
//   - 白名单缩写（int/cu/ver 等官方允许的）-> 展开执行
//   - 多前缀缩写（dis i / dis b）-> Ambiguous command found at '^' position.
//   - 其余未支持缩写（dis aa / dis ar / dis link-q）-> 不静默展开，报
//     unknown command 且回显完整命令（'dis aa'），不误导为 'dis' 本身有问题。
//
// 历史：v0.12.1 首版把"唯一前缀"自动展开（dis aa -> display aaa），
// 用户实测指出 VRP 不静默猜测、应提示 aa 有问题，故修正为上述语义。
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestDisplaySubCommandsNoDrift 防漂移：displaySubCommands 必须覆盖 displayRegistry 全部键，
// 否则歧义检测会漏判（如新增 display 子命令未加入表，缩写歧义被误判为 unknown）。
func TestDisplaySubCommandsNoDrift(t *testing.T) {
	for k := range displayRegistry {
		found := false
		for _, c := range displaySubCommands {
			if c == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("displaySubCommands 缺少 displayRegistry 键 %q——新增 display 子命令须同步追加", k)
		}
	}
}

// TestDisplayAbbrevWhitelist 白名单缩写与完整子命令照常执行（无回归）。
func TestDisplayAbbrevWhitelist(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"int", "brief"}, "PHY: Physical"},       // dis int brief 白名单
		{[]string{"cu"}, "sysname"},                       // dis cu 白名单（VRP 官方缩写）
		{[]string{"interface", "brief"}, "PHY: Physical"}, // 完整子命令
		{[]string{"aaa"}, "No AAA configuration"},         // 完整子命令
	} {
		out := ExecuteCommandOn(st, &Command{Command: "dis", Args: tc.args}, topology.DeviceRouter)
		if !strings.Contains(out, tc.want) {
			t.Errorf("dis %s 输出缺特征 %q，got: %q", strings.Join(tc.args, " "), tc.want, out)
		}
	}
}

// TestDisplayAbbrevAmbiguous 多前缀缩写应报 VRP 风格歧义错误（而非执行）。
func TestDisplayAbbrevAmbiguous(t *testing.T) {
	cases := []struct {
		args []string
	}{
		{[]string{"i"}}, // interface/ip/ipsec/ipv6/isis
		{[]string{"b"}}, // bfd/bgp
		{[]string{"m"}}, // mac-address/memory/m-lag/mlag/mtu
		{[]string{"d"}}, // description/device/dhcp/.../domain/dot1x/duplex
	}
	for _, tc := range cases {
		st := NewCLIStateWithType(topology.DeviceRouter)
		out := ExecuteCommandOn(st, &Command{Command: "dis", Args: tc.args}, topology.DeviceRouter)
		if !strings.Contains(out, "Ambiguous command") {
			t.Errorf("dis %s 应报 Ambiguous command，got: %q", strings.Join(tc.args, " "), out)
		}
	}
}

// TestDisplayAbbrevUnsupported 未支持缩写不得静默展开执行，且报错须指向完整命令
// （含子命令，而非只显示首 token 'dis' 误导）。
func TestDisplayAbbrevUnsupported(t *testing.T) {
	cases := []struct {
		args []string
		want string // 报错中必须出现的完整命令
	}{
		{[]string{"aa"}, "unknown command 'dis aa'"},         // 不展开为 display aaa
		{[]string{"ar"}, "unknown command 'dis ar'"},         // 不展开为 display arp
		{[]string{"in"}, "unknown command 'dis in'"},         // 不展开为 display interface
		{[]string{"link-q"}, "unknown command 'dis link-q'"}, // 不展开为 display link-quality
		{[]string{"zzz"}, "unknown command 'dis zzz'"},       // 零命中兜底
	}
	for _, tc := range cases {
		st := NewCLIStateWithType(topology.DeviceRouter)
		out := ExecuteCommandOn(st, &Command{Command: "dis", Args: tc.args}, topology.DeviceRouter)
		if !strings.Contains(out, tc.want) {
			t.Errorf("dis %s 应报 %q，got: %q", strings.Join(tc.args, " "), tc.want, out)
		}
		// 绝不静默执行：不能出现 AAA/ARP 等实际输出特征
		if strings.Contains(out, "No AAA configuration") || strings.Contains(out, "PHY: Physical") {
			t.Errorf("dis %s 不应静默展开执行，got: %q", strings.Join(tc.args, " "), out)
		}
	}
}

// TestUnknownCommandFullText 非 display 命令的 unknown 报错同样回显完整命令。
func TestUnknownCommandFullText(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	out := ExecuteCommandOn(st, &Command{Command: "foobar", Args: []string{"baz", "1"}}, topology.DeviceRouter)
	if !strings.Contains(out, "unknown command 'foobar baz 1'") {
		t.Errorf("unknown 报错应回显完整命令，got: %q", out)
	}
}
