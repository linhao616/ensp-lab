// display_abbrev_test.go 锁死 display 子命令的 VRP 前缀缩写语义（v0.12.1 修复）。
//
// 背景缺陷：normalizeDisplaySubCmd 此前仅支持白名单枚举缩写（int/cu/ver 等），
// `dis aa`（display 子命令集中 aa 是 aaa 的唯一前缀）误报 unknown command 'dis'，
// 与华为 VRP「唯一前缀合法缩写、多前缀歧义报错」语义不符。修复：白名单未命中时
// 走 resolveKeyword 前缀匹配——唯一→展开；多→ambiguous（命中 parser 既有 case）；
// 零→原样落 unknown。
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestDisplaySubCommandsNoDrift 防漂移：displaySubCommands 必须覆盖 displayRegistry 全部键，
// 否则新注册的 display 子命令无法被前缀缩写命中。
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
			t.Errorf("displaySubCommands 缺少 displayRegistry 键 %q——新增 display 子命令须同步追加（缩写/补全/执行三处一致）", k)
		}
	}
}

// TestDisplayAbbrevUniquePrefix 唯一前缀缩写应展开为完整子命令（与真机一致）。
func TestDisplayAbbrevUniquePrefix(t *testing.T) {
	cases := []struct {
		args []string
		want string // 输出中必须出现的特征
	}{
		{[]string{"aa"}, "No AAA configuration"}, // dis aa -> display aaa（本次修复核心）
		{[]string{"ar"}, "IP Address"},           // dis ar -> display arp（ARP 表表头）
		{[]string{"in"}, "GigabitEthernet"},      // dis in -> display interface
		{[]string{"link-q"}, "link quality"},     // dis link-q -> display link-quality（注册表新增键同样可缩写）
	}
	for _, tc := range cases {
		st := NewCLIStateWithType(topology.DeviceRouter)
		out := ExecuteCommandOn(st, &Command{Command: "dis", Args: tc.args}, topology.DeviceRouter)
		if strings.Contains(out, "unknown command") || strings.Contains(out, "Ambiguous") {
			t.Errorf("dis %s 应展开执行而非报错，got: %q", strings.Join(tc.args, " "), out)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("dis %s 输出缺特征 %q，got: %q", strings.Join(tc.args, " "), tc.want, out)
		}
	}
}

// TestDisplayAbbrevAmbiguous 多前缀缩写应报 VRP 风格歧义错误。
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

// TestDisplayAbbrevWhitelistKept 白名单缩写与完整子命令行为不变（无回归）。
func TestDisplayAbbrevWhitelistKept(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"int", "brief"}, "PHY: Physical"},       // dis int brief 保持
		{[]string{"cu"}, "sysname"},                       // dis cu 保持（白名单，输出 VRP 风格配置快照）
		{[]string{"interface", "brief"}, "PHY: Physical"}, // 完整子命令保持
		{[]string{"aaa"}, "No AAA configuration"},         // 完整子命令保持
	} {
		out := ExecuteCommandOn(st, &Command{Command: "dis", Args: tc.args}, topology.DeviceRouter)
		if !strings.Contains(out, tc.want) {
			t.Errorf("dis %s 输出缺特征 %q，got: %q", strings.Join(tc.args, " "), tc.want, out)
		}
	}
}

// TestDisplayAbbrevUnknown 零命中缩写仍落 unknown command（保持兜底）。
func TestDisplayAbbrevUnknown(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	out := ExecuteCommandOn(st, &Command{Command: "dis", Args: []string{"zzz-not-exist"}}, topology.DeviceRouter)
	if !strings.Contains(out, "unknown command") {
		t.Errorf("dis zzz 应报 unknown command，got: %q", out)
	}
}
