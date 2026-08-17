package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// TestDisplayRegistryCoreCommands 锁死迁移完整性：核心 display 子命令必须已在注册表。
// 若某 handler 被误删或注册遗漏，本测试立即失败（对应原 parser.go 巨型 switch 的 case）。
func TestDisplayRegistryCoreCommands(t *testing.T) {
	core := []string{
		"this", "interface", "ip", "arp", "vxlan", "bgp", "nat", "routing-table",
		"ipv6", "ospf", "vrrp", "stp", "acl", "version", "vlan", "mac-address",
		"users", "bfd", "vrf", "pbr", "gre", "aaa", "local-user", "domain",
		"qos", "dot1x", "radius", "netflow", "lldp", "memory", "cpu-usage",
		"current-configuration", "diagnostic-information", "saved-configuration",
		"history-command", "m-lag", "mlag", "port", "port-vlan", "port-security",
		"eth-trunk", "link-aggregation", "isis", "ripng", "ospfv3", "dhcp",
		"snmp", "ssh", "ntp", "ipsec", "startup", "syslog", "sysname",
		"temperature", "clock", "device", "stp",
	}
	for _, k := range core {
		if _, ok := displayRegistry[k]; !ok {
			t.Errorf("displayRegistry 缺少核心子命令 %q（迁移遗漏）", k)
		}
	}
}

// TestDisplayRegistryDispatchParity 在多种设备类型下跑一批 dis 命令，校验：
// ① 不 panic；② 输出非空；③ 部分命令含预期子串（行为与原内联 switch 一致）。
func TestDisplayRegistryDispatchParity(t *testing.T) {
	type tc struct {
		dt   topology.DeviceType
		args []string
		want string // 期望出现的子串（空串表示仅校验非空）
	}
	cases := []tc{
		{topology.DeviceRouter, []string{"arp"}, "IP Address"},
		{topology.DeviceRouter, []string{"vxlan", "tunnel"}, "VXLAN Tunnel"},
		{topology.DeviceRouter, []string{"version"}, ""},
		{topology.DeviceL3Switch, []string{"ip", "interface", "brief"}, ""},
		{topology.DeviceL3Switch, []string{"ip", "routing-table"}, ""},
		{topology.DeviceRouter, []string{"bgp", "peer"}, ""},
		{topology.DeviceRouter, []string{"ospf", "peer"}, ""},
		{topology.DeviceRouter, []string{"this"}, ""},
		{topology.DeviceRouter, []string{"current-configuration"}, ""},
		{topology.DeviceRouter, []string{"history-command"}, ""},
		{topology.DevicePC, []string{"arp"}, "IP Address"},
		{topology.DeviceVTEP, []string{"vxlan", "tunnel"}, "VXLAN Tunnel"},
		{topology.DeviceFirewall, []string{"nat", "server"}, ""},
		{topology.DeviceRouter, []string{"ipv6", "interface", "brief"}, ""},
		{topology.DeviceRouter, []string{"vlan"}, ""},
		{topology.DeviceRouter, []string{"mac-address"}, ""},
		{topology.DeviceRouter, []string{"users"}, ""},
		{topology.DeviceRouter, []string{"aaa"}, ""},
		{topology.DeviceRouter, []string{"local-user"}, ""},
		{topology.DeviceRouter, []string{"domain"}, ""},
		{topology.DeviceRouter, []string{"gre"}, ""},
		{topology.DeviceRouter, []string{"stp"}, ""},
		{topology.DeviceRouter, []string{"vrrp"}, ""},
		{topology.DeviceRouter, []string{"acl", "configuration"}, "ACL Configuration"},
		{topology.DeviceRouter, []string{"bfd"}, ""},
		{topology.DeviceRouter, []string{"vrf"}, ""},
	}
	for _, c := range cases {
		st := NewCLIStateWithType(c.dt)
		cmd := &Command{Command: "display", Args: c.args}
		var out string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("dis %s 在设备 %v 下 panic: %v", strings.Join(c.args, " "), c.dt, r)
				}
			}()
			out = ExecuteCommandOn(st, cmd, c.dt)
		}()
		if strings.TrimSpace(out) == "" {
			t.Errorf("dis %s 在设备 %v 下返回空输出", strings.Join(c.args, " "), c.dt)
			continue
		}
		if c.want != "" && !strings.Contains(out, c.want) {
			t.Errorf("dis %s 在设备 %v 下缺少期望子串 %q，got:\n%s", strings.Join(c.args, " "), c.dt, c.want, out)
		}
	}
}

// TestDisplayDispatchUnknown 未知 dis 子命令应返回 unknown command（而非空输出/panic）。
func TestDisplayDispatchUnknown(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	out := ExecuteCommandOn(st, &Command{Command: "display", Args: []string{"zzz-not-exist"}}, topology.DeviceRouter)
	if !strings.Contains(out, "unknown command") {
		t.Errorf("未知 dis 子命令应提示 unknown command，got: %q", out)
	}
}

// TestDisplayDispatchNeedsArgs 无参数的 dis 应提示 need args。
func TestDisplayDispatchNeedsArgs(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	out := ExecuteCommandOn(st, &Command{Command: "display", Args: []string{}}, topology.DeviceRouter)
	if !strings.Contains(out, "need args") {
		t.Errorf("dis 无参数应提示 need args，got: %q", out)
	}
}

// TestDisplayKeyCollision 端口安全粘滞 MAC 等十六进制串不得误伤 display 分发。
func TestDisplayKeyCollision(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	// 注入一个含粘滞 MAC 的 DeviceConfig 键（模拟端口安全场景），
	// 校验 dis arp 分发不受其影响（键碰撞红线：禁用 Contains 模糊扫描）。
	st.DeviceConfig["interface:GigabitEthernet0/0/1:port-security:sticky-mac:00e0-fc12-0aaa"] = "1"
	out := ExecuteCommandOn(st, &Command{Command: "display", Args: []string{"arp"}}, topology.DeviceRouter)
	if !strings.Contains(out, "IP Address") {
		t.Errorf("含粘滞 MAC 键时 dis arp 分发异常，got:\n%s", out)
	}
}
