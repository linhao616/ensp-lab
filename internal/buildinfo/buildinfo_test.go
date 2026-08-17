package buildinfo

import (
	"strings"
	"testing"
)

// saveState 保存并返回一个恢复函数，避免各用例互相污染包级变量。
func saveState(t *testing.T) {
	t.Helper()
	v, bt, c, d, s, sr := Version, BuildTime, Commit, Dirty, Stale, StaleReason
	t.Cleanup(func() {
		Version, BuildTime, Commit, Dirty, Stale, StaleReason = v, bt, c, d, s, sr
	})
}

// TestDetectStaleMissingBuildTime 覆盖最关键的一条规则：
// 未注入构建时间（= 绕过 make / build.ps1 直接 go build）必须判定为陈旧。
// 这正是「用户跑的是修复前的旧产物」那类幽灵 bug 的拦截点。
func TestDetectStaleMissingBuildTime(t *testing.T) {
	saveState(t)

	BuildTime = defaultBuildTime
	Commit = defaultCommit
	Stale = false
	StaleReason = ""

	detectStale()

	if !Stale {
		t.Fatalf("BuildTime 为默认值时必须判定为陈旧，实际 Stale=false")
	}
	if !strings.Contains(StaleReason, "构建信息未注入") {
		t.Errorf("StaleReason 应说明构建信息未注入，实际为 %q", StaleReason)
	}
}

// TestDetectStaleForeignCommitDoesNotFalsePositive 覆盖误报防线：
// 若注入的 Commit 在当前仓库中不存在（典型为二进制被拷到别处运行），
// 应放弃判断而非武断地报陈旧——否则正常部署产物会被误杀。
// git 不可用时同样应保持 Stale=false，因此本用例在任何环境下结论一致。
func TestDetectStaleForeignCommitDoesNotFalsePositive(t *testing.T) {
	saveState(t)

	BuildTime = "2026-01-01T00:00:00Z"
	// 一个几乎不可能存在于本仓库的对象名。
	Commit = "0000000"
	Stale = false
	StaleReason = ""

	detectStale()

	if Stale {
		t.Fatalf("构建提交不属于当前仓库时不应判定陈旧，reason=%q", StaleReason)
	}
	if StaleReason == "" {
		t.Errorf("跳过判断时应留下可读原因，实际为空")
	}
}

// TestIsDirtyBuild 校验 ldflags 注入的字符串到 bool 的转换足够宽容。
func TestIsDirtyBuild(t *testing.T) {
	saveState(t)

	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{" true ", true},
		{"false", false},
		{"", false},
		{"unknown", false},
	}

	for _, tc := range cases {
		Dirty = tc.in
		if got := IsDirtyBuild(); got != tc.want {
			t.Errorf("IsDirtyBuild(Dirty=%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestInitIsIdempotent 确认 Init 只执行一次：重复调用不得改写已有结论。
func TestInitIsIdempotent(t *testing.T) {
	saveState(t)

	Init()
	first, firstReason := Stale, StaleReason

	// 人为篡改输入；若 Init 未被 sync.Once 保护，第二次调用会得出不同结论。
	BuildTime = defaultBuildTime
	Init()

	if Stale != first || StaleReason != firstReason {
		t.Errorf("Init 应幂等，第二次调用后 Stale=%v reason=%q（首次为 %v / %q）",
			Stale, StaleReason, first, firstReason)
	}
}
