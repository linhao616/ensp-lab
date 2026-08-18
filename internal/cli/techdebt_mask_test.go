package cli

// techdebt_mask_test.go —— 既有技术债回归测试：
//   1. prefixToSubnet 此前为有类近似（仅 /8 /16 /24 /32），/30 误算 255.255.255.0；
//      现改为按位精确计算。
//   2. `display interface <if>` 详情的 "Internet Address is" 行曾把 config 键的
//      "IP MASK" 整串当 IP 后又补 "/Mask"，造成掩码重复渲染；现拆出 IP 与 Mask。

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestPrefixToSubnetExact 验证子网掩码按前缀长度精确计算（含非整类边界）。
func TestPrefixToSubnetExact(t *testing.T) {
	cases := []struct {
		prefix int
		want   string
	}{
		{0, "0.0.0.0"},
		{8, "255.0.0.0"},
		{16, "255.255.0.0"},
		{24, "255.255.255.0"},
		{30, "255.255.255.252"}, // 此前误算为 255.255.255.0
		{31, "255.255.255.254"},
		{32, "255.255.255.255"},
		{-1, "0.0.0.0"},         // 越界下限收敛
		{33, "255.255.255.255"}, // 越界上限收敛
	}
	for _, c := range cases {
		if got := prefixToSubnet(c.prefix); got != c.want {
			t.Errorf("prefixToSubnet(%d) = %q, want %q", c.prefix, got, c.want)
		}
	}
}

// TestDisplayInterfaceIPMaskNoDuplicate 验证物理口配置点分掩码后，
// "Internet Address is" 行仅渲染一次掩码（不复读）。
func TestDisplayInterfaceIPMaskNoDuplicate(t *testing.T) {
	const iface = "GigabitEthernet0/0/1"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "interface "+iface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view failed: %s", out)
	}
	if out := runOn(st, topology.DeviceRouter, "ip address 10.0.0.1 255.255.255.252"); strings.HasPrefix(out, "Error") {
		t.Fatalf("ip address failed: %s", out)
	}
	out := runOn(st, topology.DeviceRouter, "display interface "+iface)
	if !strings.Contains(out, "Internet Address is 10.0.0.1/255.255.255.252") {
		t.Errorf("display interface missing correct IP line\n---\n%s", out)
	}
	if strings.Contains(out, "255.255.255.252/255.255.255.252") {
		t.Errorf("mask rendered twice (duplication bug not fixed)\n---\n%s", out)
	}
}

// TestDisplayInterfaceIPPrefixMaskNoDuplicate 验证前缀长度形态（/30）同样不重复，
// 且 prefixToSubnet 修正后不影响其它内部消费路径。
func TestDisplayInterfaceIPPrefixMaskNoDuplicate(t *testing.T) {
	const iface = "GigabitEthernet0/0/2"
	st := NewCLIStateWithType(topology.DeviceRouter)
	runOn(st, topology.DeviceRouter, "system-view")
	if out := runOn(st, topology.DeviceRouter, "interface "+iface); strings.HasPrefix(out, "Error") {
		t.Fatalf("enter interface view failed: %s", out)
	}
	if out := runOn(st, topology.DeviceRouter, "ip address 10.0.0.5 30"); strings.HasPrefix(out, "Error") {
		t.Fatalf("ip address failed: %s", out)
	}
	out := runOn(st, topology.DeviceRouter, "display interface "+iface)
	if !strings.Contains(out, "Internet Address is 10.0.0.5/30") {
		t.Errorf("display interface missing prefix-form IP line\n---\n%s", out)
	}
	if strings.Contains(out, "/30/30") || strings.Contains(out, "10.0.0.5 30/30") {
		t.Errorf("mask rendered twice (duplication bug not fixed)\n---\n%s", out)
	}
}
