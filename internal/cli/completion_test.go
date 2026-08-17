package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompletionNoDrift 锁死「配置视图关键字表」与 parser.go 实际处理的命令不漂移：
// 表中每个 token 必须是 parser.go 中某个 case 的首别名（否则视为该命令已失效，
// 补全表却仍在推荐 -> 立即失败，逼维护者同步更新）。
func TestCompletionNoDrift(t *testing.T) {
	content, err := os.ReadFile("parser.go")
	if err != nil {
		// 测试在包目录内运行；退一步找仓库内 parser.go
		alt, aerr := os.ReadFile(filepath.Join("..", "cli", "parser.go"))
		if aerr != nil {
			t.Fatalf("read parser.go: %v / %v", err, aerr)
		}
		content = alt
	}
	parser := string(content)

	tables := [][]string{
		userViewCommands, systemViewCommands, interfaceViewCommands,
		aaaViewCommands, bgpViewCommands, aclViewCommands, vtyViewCommands,
		dhcpPoolViewCommands, isisViewCommands, mstRegionViewCommands, mlagViewCommands,
	}
	for _, tbl := range tables {
		for _, kw := range tbl {
			needle := fmt.Sprintf("case %q", kw)
			if !strings.Contains(parser, needle) {
				t.Errorf("completion keyword %q not found as a case label in parser.go (drift?)", kw)
			}
		}
	}
}

// contains checks membership (exact, case-sensitive) in a candidate slice.
func containsCand(cands []string, want string) bool {
	for _, c := range cands {
		if c == want {
			return true
		}
	}
	return false
}

// TestCompletionDisplayPrefix 验证 dis 上下文按前缀过滤注册表 key（AC1: ipv+Tab→ipv6）。
func TestCompletionDisplayPrefix(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	cands := Complete(state, SplitCommandTokens("dis ipv"))
	if len(cands) != 1 || cands[0] != "ipv6" {
		t.Fatalf("dis ipv -> want [ipv6], got %v", cands)
	}

	// 多候选：dis ip 应同时匹配 ip / ipsec / ipv6（均以前缀 ip 开头）
	cands = Complete(state, SplitCommandTokens("dis ip"))
	if !containsCand(cands, "ip") || !containsCand(cands, "ipsec") || !containsCand(cands, "ipv6") {
		t.Fatalf("dis ip should include ip/ipsec/ipv6, got %v", cands)
	}

	// 结尾空格 -> 列出全部 display 子命令
	cands = Complete(state, SplitCommandTokens("dis "))
	if len(cands) == 0 || !containsCand(cands, "ipv6") || !containsCand(cands, "arp") {
		t.Fatalf("dis <space> should list all display keys, got %v", cands)
	}
}

// TestCompletionViewAware 验证候选随视图变化（AC3）。
func TestCompletionViewAware(t *testing.T) {
	// system 视图：inter -> interface
	sys := &CLIState{CurrentView: ViewSystem}
	cands := Complete(sys, SplitCommandTokens("inter"))
	if !containsCand(cands, "interface") {
		t.Fatalf("system view 'inter' should complete to 'interface', got %v", cands)
	}

	// user 视图：sy -> system-view
	usr := &CLIState{CurrentView: ViewUser}
	cands = Complete(usr, SplitCommandTokens("sy"))
	if !containsCand(cands, "system-view") {
		t.Fatalf("user view 'sy' should complete to 'system-view', got %v", cands)
	}

	// user 视图不应推荐需 system 视图才合法的关键字（如 ospf）
	if containsCand(Complete(usr, SplitCommandTokens("osp")), "ospf") {
		// ospf 在 user 视图表 userViewCommands 中未列出，若命中说明表错了
		t.Fatalf("user view should NOT suggest 'ospf' (not a user-view command)")
	}
}

// TestCompletionInterfaceName 验证接口名补全（真实接口列表，状态感知）。
func TestCompletionInterfaceName(t *testing.T) {
	state := &CLIState{
		CurrentView: ViewSystem,
		Interfaces: map[string]*InterfaceConfig{
			"GigabitEthernet0/0/1": {},
			"GigabitEthernet0/0/2": {},
			"Vlanif1":              {},
		},
	}

	// system 视图：interface Gi -> 两个 GE 接口
	cands := Complete(state, SplitCommandTokens("interface Gi"))
	if !containsCand(cands, "GigabitEthernet0/0/1") || !containsCand(cands, "GigabitEthernet0/0/2") {
		t.Fatalf("interface Gi should complete GE names, got %v", cands)
	}
	if containsCand(cands, "Vlanif1") {
		t.Fatalf("interface Gi should NOT match Vlanif1, got %v", cands)
	}

	// dis int Gi -> 同样接口名补全（归一化 int->interface）
	cands = Complete(state, SplitCommandTokens("dis int Gi"))
	if !containsCand(cands, "GigabitEthernet0/0/1") {
		t.Fatalf("dis int Gi should complete GE names, got %v", cands)
	}
}

// TestCompletionUnknown 验证无匹配时返回空（不 panic、不编造）。
func TestCompletionUnknown(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	cands := Complete(state, SplitCommandTokens("zzz"))
	if len(cands) != 0 {
		t.Fatalf("unknown prefix should yield no candidates, got %v", cands)
	}
}
