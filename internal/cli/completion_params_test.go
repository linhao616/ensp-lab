package cli

import "testing"

// TestCompleteDisAaaParams 验证 dis aaa <space> 列出二级子命令参数（参数级补全主体）。
func TestCompleteDisAaaParams(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	cands := Complete(state, SplitCommandTokens("dis aaa "))
	want := []string{"configuration", "statistics", "online-user", "local-user", "domain"}
	for _, w := range want {
		if !containsCand(cands, w) {
			t.Fatalf("dis aaa <space> should suggest %q, got %v", w, cands)
		}
	}
}

// TestCompleteDisAaaPrefix 验证 dis aaa <prefix> 唯一前缀自动补、无匹配返回空。
func TestCompleteDisAaaPrefix(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	// 'c' -> configuration（唯一前缀）
	cands := Complete(state, SplitCommandTokens("dis aaa c"))
	if len(cands) != 1 || cands[0] != "configuration" {
		t.Fatalf("dis aaa c -> want [configuration], got %v", cands)
	}
	// 'l' -> local-user（唯一前缀）
	cands = Complete(state, SplitCommandTokens("dis aaa l"))
	if len(cands) != 1 || cands[0] != "local-user" {
		t.Fatalf("dis aaa l -> want [local-user], got %v", cands)
	}
	// 'x' -> 无匹配（返回空，前端不补全）
	cands = Complete(state, SplitCommandTokens("dis aaa x"))
	if len(cands) != 0 {
		t.Fatalf("dis aaa x -> want no candidates, got %v", cands)
	}
}

// TestCompleteDisAaaLocalUserValue 验证 dis aaa local-user <space> 列出已配用户名（StateProvider）。
func TestCompleteDisAaaLocalUserValue(t *testing.T) {
	state := &CLIState{
		CurrentView: ViewUser,
		DeviceConfig: map[string]string{
			"aaa:local-user:admin:password":   "secret123",
			"aaa:local-user:admin:privilege":  "15",
			"aaa:local-user:operator:password": "secret456",
		},
	}
	cands := Complete(state, SplitCommandTokens("dis aaa local-user "))
	if !containsCand(cands, "admin") || !containsCand(cands, "operator") {
		t.Fatalf("dis aaa local-user <space> should suggest configured users, got %v", cands)
	}
	// 前缀过滤：'a' -> admin
	cands = Complete(state, SplitCommandTokens("dis aaa local-user a"))
	if len(cands) != 1 || cands[0] != "admin" {
		t.Fatalf("dis aaa local-user a -> want [admin], got %v", cands)
	}
}

// TestCompleteDisIpParams 验证 dis ip <space> 列出二级子命令。
func TestCompleteDisIpParams(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	cands := Complete(state, SplitCommandTokens("dis ip "))
	want := []string{"interface", "pool", "routing-table"}
	for _, w := range want {
		if !containsCand(cands, w) {
			t.Fatalf("dis ip <space> should suggest %q, got %v", w, cands)
		}
	}
	// dis ip interface <space> -> 接口名（嵌套值槽位）
	state.Interfaces = map[string]*InterfaceConfig{"GigabitEthernet0/0/1": {}}
	cands = Complete(state, SplitCommandTokens("dis ip interface "))
	if !containsCand(cands, "GigabitEthernet0/0/1") {
		t.Fatalf("dis ip interface <space> should suggest interface names, got %v", cands)
	}
}

// TestCompleteDisInterfaceParams 验证 dis interface <space> 建议 brief 与接口名（混合槽位）。
func TestCompleteDisInterfaceParams(t *testing.T) {
	state := &CLIState{
		CurrentView: ViewUser,
		Interfaces: map[string]*InterfaceConfig{
			"GigabitEthernet0/0/1": {},
			"Vlanif1":              {},
		},
	}
	cands := Complete(state, SplitCommandTokens("dis interface "))
	if !containsCand(cands, "brief") || !containsCand(cands, "GigabitEthernet0/0/1") {
		t.Fatalf("dis interface <space> should suggest 'brief' and interface names, got %v", cands)
	}
	// 'b' -> brief（唯一前缀）
	cands = Complete(state, SplitCommandTokens("dis interface b"))
	if len(cands) != 1 || cands[0] != "brief" {
		t.Fatalf("dis interface b -> want [brief], got %v", cands)
	}
}

// TestCompleteConfigInterfaceValue 验证 system 视图 interface <prefix> 补全接口名。
func TestCompleteConfigInterfaceValue(t *testing.T) {
	state := &CLIState{
		CurrentView: ViewSystem,
		Interfaces: map[string]*InterfaceConfig{
			"GigabitEthernet0/0/1": {},
			"GigabitEthernet0/0/2": {},
		},
	}
	cands := Complete(state, SplitCommandTokens("interface Gi"))
	if !containsCand(cands, "GigabitEthernet0/0/1") || !containsCand(cands, "GigabitEthernet0/0/2") {
		t.Fatalf("system interface Gi should complete GE names, got %v", cands)
	}
}

// TestCompleteConfigLocalUserValue 验证 aaa 视图 local-user <space> 补全已配用户名。
func TestCompleteConfigLocalUserValue(t *testing.T) {
	state := &CLIState{
		CurrentView: ViewAAA,
		DeviceConfig: map[string]string{
			"aaa:local-user:admin:password": "secret123",
		},
	}
	cands := Complete(state, SplitCommandTokens("local-user "))
	if !containsCand(cands, "admin") {
		t.Fatalf("aaa local-user <space> should suggest configured user 'admin', got %v", cands)
	}
}

// TestCompleteDisAaaQuestionMarkContract 锁死 ? 热键契约：
// 后端 completeParams 按“下一 token”计算候选，故 ? 触发前前端必须补尾随空格。
// 无空格的 "dis aaa" 会被当作“补全当前 token”，仅返回 ["aaa"]（而非 aaa 的子参数）；
// 只有 "dis aaa "（尾随空格）才返回二级参数列表——这正是 ? 分支要给 input 追加空格的原因。
func TestCompleteDisAaaQuestionMarkContract(t *testing.T) {
	state := &CLIState{CurrentView: ViewUser}
	// 无尾随空格：补全当前 token "aaa" 自身，不应列出子参数。
	noSpace := Complete(state, SplitCommandTokens("dis aaa"))
	if len(noSpace) != 1 || noSpace[0] != "aaa" {
		t.Fatalf("dis aaa (no trailing space) -> want [aaa] (complete current token), got %v", noSpace)
	}
	// 有尾随空格：列出 aaa 的二级参数（? 热键的真实查询形态）。
	withSpace := Complete(state, SplitCommandTokens("dis aaa "))
	want := []string{"configuration", "statistics", "online-user", "local-user", "domain"}
	for _, w := range want {
		if !containsCand(withSpace, w) {
			t.Fatalf("dis aaa <space> (as ? sends) should suggest %q, got %v", w, withSpace)
		}
	}
}

// TestCompletionParamNoDrift 锁死参数级候选与执行器实际识别的 token 零漂移：
// 每个 keyword 槽位的静态候选必须是对应 display 子命令实际可识别的 token。
func TestCompletionParamNoDrift(t *testing.T) {
	// display aaa 实际识别的二级子命令（regAaaDisplay switch 分支）。
	aaaRecognized := map[string]bool{
		"configuration": true, "statistics": true, "online-user": true,
		"local-user": true, "domain": true,
	}
	for _, p := range displayParamSpecs["aaa"].Params {
		for _, c := range p.Candidates {
			if !aaaRecognized[c] {
				t.Errorf("display aaa param candidate %q not recognized by regAaaDisplay (drift?)", c)
			}
		}
	}
	// display ip 实际识别的二级子命令（regIpDisplay ipSubKW）。
	ipRecognized := map[string]bool{"interface": true, "pool": true, "routing-table": true}
	for _, p := range displayParamSpecs["ip"].Params {
		for _, c := range p.Candidates {
			if !ipRecognized[c] {
				t.Errorf("display ip param candidate %q not recognized by regIpDisplay (drift?)", c)
			}
		}
	}
	// display interface 实际识别：brief 关键字（regInterfaceDisplay）+ 任意接口名
	for _, p := range displayParamSpecs["interface"].Params {
		for _, c := range p.Candidates {
			if c != "brief" {
				t.Errorf("display interface param candidate %q unexpected (only 'brief' is static)", c)
			}
		}
	}
}
