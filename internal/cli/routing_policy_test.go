// routing_policy_test.go 锁死 P0-2 路由策略（route-policy / filter-policy）行为：
// ① 进入节点视图 + if-match/apply 子句 + display + quit；
// ② filter-policy 三种上下文（BGP/ISIS 视图 + 系统视图协议域限定）；
// ③ import-route ... route-policy 关联；④ 语法错误拒绝；⑤ 视图感知补全。
package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// exec 是测试用的便捷执行器（dt 固定 router）。
func execRP(t *testing.T, st *CLIState, cmd, argStr string) string {
	t.Helper()
	args := strings.Fields(argStr)
	return ExecuteCommandOn(st, &Command{Command: cmd, Args: args}, topology.DeviceRouter)
}

func TestRoutePolicyLifecycle(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")

	// 进入节点视图
	out := execRP(t, st, "route-policy", "RP1 permit node 10")
	if !strings.Contains(out, "Enter route-policy RP1 node 10") {
		t.Fatalf("进入 route-policy 视图失败: %q", out)
	}
	if st.CurrentView != ViewRoutePolicy {
		t.Fatalf("CurrentView 应为 ViewRoutePolicy，实为 %q", st.CurrentView)
	}

	// if-match / apply
	if out := execRP(t, st, "if-match", "ip-prefix RP-IP"); !strings.Contains(out, "if-match ip-prefix RP-IP") {
		t.Fatalf("if-match 失败: %q", out)
	}
	if out := execRP(t, st, "apply", "cost 100"); !strings.Contains(out, "apply cost 100") {
		t.Fatalf("apply 失败: %q", out)
	}

	// 持久化键
	if st.DeviceConfig[rpNodeKey("RP1", 10)+"action"] != "permit" {
		t.Fatalf("action 键未写入: %v", st.DeviceConfig)
	}

	// quit 回系统视图
	execRP(t, st, "quit", "")
	if st.CurrentView != ViewSystem {
		t.Fatalf("quit 后应为 ViewSystem，实为 %q", st.CurrentView)
	}

	// display route-policy <name>
	out = execRP(t, st, "display", "route-policy RP1")
	for _, want := range []string{"node 10", "if-match ip-prefix RP-IP", "apply cost 100"} {
		if !strings.Contains(out, want) {
			t.Fatalf("display route-policy RP1 缺 %q，got:\n%s", want, out)
		}
	}

	// display route-policy（无参列出全部）
	out = execRP(t, st, "display", "route-policy")
	if !strings.Contains(out, "RP1") {
		t.Fatalf("display route-policy 应列出 RP1，got:\n%s", out)
	}
}

func TestRoutePolicyWrongSyntax(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")
	// 缺 action
	if out := execRP(t, st, "route-policy", "RP1"); !strings.Contains(out, "usage") {
		t.Fatalf("缺 action 应报 usage，got: %q", out)
	}
	// 缺 node
	if out := execRP(t, st, "route-policy", "RP1 permit"); !strings.Contains(out, "usage") {
		t.Fatalf("缺 node 应报 usage，got: %q", out)
	}
	// 非 permit/deny
	if out := execRP(t, st, "route-policy", "RP1 allow node 10"); !strings.Contains(out, "usage") {
		t.Fatalf("非法 action 应报 usage，got: %q", out)
	}
}

func TestFilterPolicyBGPView(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")
	execRP(t, st, "bgp", "65001")
	// BGP 视图内 filter-policy acl import
	out := execRP(t, st, "filter-policy", "2000 import")
	if !strings.Contains(out, "bgp import") {
		t.Fatalf("filter-policy BGP 失败: %q", out)
	}
	if st.DeviceConfig["bgp:filter-policy:import:kind"] != "acl" ||
		st.DeviceConfig["bgp:filter-policy:import:value"] != "2000" {
		t.Fatalf("BGP filter-policy 键未写入: %v", st.DeviceConfig)
	}
	out = execRP(t, st, "display", "filter-policy")
	if !strings.Contains(out, "filter-policy acl 2000 (bgp:import)") {
		t.Fatalf("display filter-policy 缺特征，got:\n%s", out)
	}
}

func TestFilterPolicyISISView(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")
	execRP(t, st, "isis", "1")
	out := execRP(t, st, "filter-policy", "ip-prefix RP1 export")
	if !strings.Contains(out, "isis export") {
		t.Fatalf("filter-policy ISIS 失败: %q", out)
	}
	if st.DeviceConfig["isis:filter-policy:export:kind"] != "ip-prefix" ||
		st.DeviceConfig["isis:filter-policy:export:value"] != "RP1" {
		t.Fatalf("ISIS filter-policy 键未写入: %v", st.DeviceConfig)
	}
	out = execRP(t, st, "display", "filter-policy isis")
	if !strings.Contains(out, "filter-policy ip-prefix RP1 (isis:export)") {
		t.Fatalf("display filter-policy isis 缺特征，got:\n%s", out)
	}
}

func TestFilterPolicySystemViewProtocol(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")
	// 系统视图必须显式协议域（OSPF/RIP 无独立子视图）
	out := execRP(t, st, "filter-policy", "2000 ospf import")
	if !strings.Contains(out, "ospf import") {
		t.Fatalf("filter-policy 系统视图失败: %q", out)
	}
	if st.DeviceConfig["ospf:filter-policy:import:kind"] != "acl" ||
		st.DeviceConfig["ospf:filter-policy:import:value"] != "2000" {
		t.Fatalf("OSPF filter-policy 键未写入: %v", st.DeviceConfig)
	}
	// 缺协议域应报错
	if out := execRP(t, st, "filter-policy", "2000 import"); !strings.Contains(out, "usage") {
		t.Fatalf("系统视图缺协议域应报 usage，got: %q", out)
	}
	// 非法协议域应报错
	if out := execRP(t, st, "filter-policy", "2000 bogus import"); !strings.Contains(out, "unsupported protocol") {
		t.Fatalf("非法协议域应报 unsupported，got: %q", out)
	}
}

func TestImportRouteRoutePolicy(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	execRP(t, st, "system-view", "")
	execRP(t, st, "isis", "1")
	out := execRP(t, st, "import-route", "static route-policy RP1")
	if !strings.Contains(out, "route-policy RP1") {
		t.Fatalf("import-route route-policy 失败: %q", out)
	}
	if st.DeviceConfig["isis:import-route:route-policy"] != "RP1" {
		t.Fatalf("import-route route-policy 键未写入: %v", st.DeviceConfig)
	}
}

func TestRoutePolicyCompletionViewAware(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceRouter)
	st.CurrentView = ViewRoutePolicy
	st.RoutePolicyName = "RP1"
	st.RoutePolicyNode = 10
	cands := Complete(st, SplitCommandTokens("if"))
	if !containsCand(cands, "if-match") {
		t.Fatalf("route-policy 视图 'if' 应补全 if-match，got: %v", cands)
	}
	cands = Complete(st, SplitCommandTokens("ap"))
	if !containsCand(cands, "apply") {
		t.Fatalf("route-policy 视图 'ap' 应补全 apply，got: %v", cands)
	}
}

func TestRoutePolicyNotOnL2Switch(t *testing.T) {
	st := NewCLIStateWithType(topology.DeviceSwitch)
	execRP(t, st, "system-view", "")
	out := ExecuteCommandOn(st, &Command{Command: "route-policy", Args: []string{"RP1", "permit", "node", "10"}}, topology.DeviceSwitch)
	if !strings.Contains(out, "not supported") {
		t.Fatalf("二层交换机应拒绝 route-policy，got: %q", out)
	}
}
