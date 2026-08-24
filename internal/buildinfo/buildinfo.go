// Package buildinfo 是构建元信息的「单一事实源」（single source of truth）。
//
// 背景：此前版本信息散落两处——cmd/server/main.go 里的 main.version/main.buildTime
// 由 ldflags 注入但只用于启动日志；internal/api/router.go 里另有一份同名变量，
// 从未被任何 ldflags 注入，而 /version 端点返回的恰恰是它 → 该端点永远报告
// version="dev"、build_time="unknown"。本包把两份合并为一份，ldflags 只注入这里，
// main 与 api 都从这里读，杜绝「注入了但没人用 / 用了但没注入」。
//
// 五个包级变量由构建期 ldflags 注入，例如：
//
//	go build -ldflags "-X 'ensp-lab/internal/buildinfo.Version=v1.2.3' ..." ./cmd/server
//
// 未经注入（即绕过 make / build.ps1 直接 go build）时它们保持默认值，
// Init() 会据此把 Stale 置为 true，从而在启动日志与 /version 中明示
// 「你跑的是一份来路不明的产物」。
package buildinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 由 ldflags 注入的构建元信息。默认值代表「未经正规构建流程」。
//
// 注意：这些变量必须是包级 string 且不可为常量，否则 -X 无法写入。
// Dirty 之所以是 string 而非 bool，同样是因为 -X 只能注入字符串。
var (
	// Version 为 git describe 结果，如 v0.9.0-1-gca8aa87；未注入时为 "dev"。
	Version string = defaultVersion
	// BuildTime 为 UTC 构建时刻（RFC3339），如 2026-02-11T03:04:05Z；未注入时为 "unknown"。
	BuildTime string = defaultBuildTime
	// Commit 为构建时的 git 短 SHA；未注入时为 "unknown"。
	Commit string = defaultCommit
	// Dirty 表示构建时工作树是否有未提交改动（"true"/"false" 字符串）。
	Dirty string = defaultDirty
	// Stale 表示「当前运行的二进制可能已落后于源码」，由 Init() 计算得出。
	Stale bool = false
	// StaleReason 为 Stale 的人类可读成因；不陈旧时为空串。
	StaleReason string = ""
)

const (
	defaultVersion   = "dev"
	defaultBuildTime = "unknown"
	defaultCommit    = "unknown"
	defaultDirty     = "false"

	// gitTimeout 限制单条 git 子进程耗时，避免 git 卡死拖慢服务启动。
	gitTimeout = 2 * time.Second
)

// initOnce 保证陈旧自检在进程生命周期内只执行一次（结果缓存在包级变量里）。
var initOnce sync.Once

// Init 执行运行期「陈旧产物」自检，并把结论写入 Stale / StaleReason。
//
// 该函数应在进程启动早期调用一次，重复调用无副作用（sync.Once 保护）。
// 它**不会**阻塞或中断启动：任何内部异常都被 recover 吞掉，最坏情况是
// 维持 Stale=false（宁可漏报也不误杀已正确构建的部署产物）。
//
// 判定规则（按序短路）：
//  1. BuildTime 仍是默认值 → 说明绕过了 make/build.ps1 直接 go build → 陈旧。
//  2. git 不可用、或不在 git 工作区、或注入的 Commit 在本仓库中不存在
//     （典型为「部署产物被放到别处运行」）→ 无从判断 → 不置 Stale，仅记录原因。
//  3. 运行时 HEAD 与注入的 Commit 不一致 → 二进制落后于当前分支 → 陈旧。
//  4. 工作树有未提交改动 → 源码已变更而二进制未重建 → 陈旧。
func Init() {
	initOnce.Do(detectStale)
}

// IsDirtyBuild 返回构建时工作树是否为脏（把注入的字符串转成 bool）。
func IsDirtyBuild() bool {
	return strings.EqualFold(strings.TrimSpace(Dirty), "true")
}

// detectStale 是 Init 的实际实现，独立成函数便于单元测试直接调用。
func detectStale() {
	// 自检属于「锦上添花」的开发辅助，绝不允许它把服务搞崩：
	// 任何 panic（如极端环境下 os/exec 异常）都在此吞掉，保持 Stale 原值。
	defer func() {
		if rec := recover(); rec != nil {
			Stale = false
			StaleReason = "陈旧自检异常，已跳过"
		}
	}()

	// 规则 1：没有构建时间戳 = 没走正规构建入口。这是最常见、也最该报的一种陈旧。
	if BuildTime == defaultBuildTime {
		Stale = true
		StaleReason = "构建信息未注入（疑似直接 go build 绕过 make / build.ps1）"
		return
	}

	head, err := gitOutput("rev-parse", "--short", "HEAD")
	if err != nil || head == "" {
		// 规则 2a：git 缺失或当前目录不是 git 工作区（正常的生产部署即如此）。
		StaleReason = "git 不可用，跳过陈旧自检"
		return
	}

	// 规则 2a2：当前目录不是 git 仓库根（git 命令解析到父仓库，典型为开发副本/部署副本）。
	// 此时 HEAD/status 都属于「另一个仓库」，任何比对都是误报 → 跳过。
	topLevel, tlErr := gitOutput("rev-parse", "--show-toplevel")
	cwd, cwdErr := os.Getwd()
	if tlErr == nil && cwdErr == nil && topLevel != "" {
		tl := filepath.Clean(topLevel)
		wd := filepath.Clean(cwd)
		if !sameRepoRoot(tl, wd) {
			StaleReason = "当前目录不是本仓库根（git 解析到父仓库），跳过陈旧自检"
			return
		}
	}

	// 规则 2b：确认注入的 Commit 确实属于当前仓库。
	// 若二进制被拷到「另一个」git 仓库目录下运行，HEAD 必然不同，但那不代表陈旧，
	// 直接比对会误报。用 cat-file 验证血缘，血缘对不上就放弃判断。
	if Commit != defaultCommit {
		if _, err := gitOutput("cat-file", "-e", Commit+"^{commit}"); err != nil {
			StaleReason = "构建提交不属于当前仓库，跳过陈旧自检"
			return
		}
	}

	// 规则 3：二进制构建自某个提交，而当前 HEAD 已经前进/回退。
	if Commit != defaultCommit && head != Commit {
		Stale = true
		StaleReason = "二进制构建自 " + Commit + "，当前 HEAD 为 " + head
		return
	}

	// 规则 4：提交一致，但工作树里还有没提交的改动 → 源码可能已改而未重建。
	// 仅当 Commit 已注入（来自本仓库）时才做工作树检查；未注入（dev 构建，如
	// 开发副本目录下 build.ps1 判定仓库根不匹配而 fallback）时无从对照，跳过——
	// 否则 git status 解析到父仓库会把父仓的脏状态误判为本二进制陈旧。
	if Commit == defaultCommit {
		StaleReason = "未注入提交号（dev 构建），跳过工作树检查"
		return
	}
	status, err := gitOutput("status", "--porcelain")
	if err != nil {
		StaleReason = "git status 执行失败，跳过工作树检查"
		return
	}
	// 忽略 data/ 目录的改动：拓扑 JSON 是运行时数据（用户在 UI 改拓扑即落盘），
	// 属于「数据变更」而非「源码已变更」，不构成陈旧（否则每次改拓扑都误报 stale）。
	// porcelain 固定列格式：XY path（X=暂存状态, Y=工作树状态, 第 3 字节是空格,
	// 路径从第 4 字节开始）。⚠️ 不能用 TrimSpace 处理整行——会剥掉 X 位的前导
	// 空格导致路径偏移（data/ 变成 ata/ 匹配不上）。
	relevant := 0
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if len(line) >= 4 && strings.HasPrefix(line[3:], "data/") {
			continue
		}
		relevant++
	}
	if relevant > 0 {
		Stale = true
		StaleReason = "工作树存在未提交改动，源码可能已变更"
		return
	}
}

// gitOutput 执行一条 git 子命令并返回去除首尾空白的 stdout。
//
// 带 gitTimeout 超时保护；git 不存在、非 git 目录、命令失败均返回 error，
// 调用方据此决定「跳过判断」而非「判定陈旧」。
func gitOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	// 丢弃 stderr：git 在非仓库目录下的报错对调用方没有价值，err 已足够表达失败。
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sameRepoRoot 判断 git 仓库根 topLevel 与当前工作目录 wd 是否指向同一仓库根。
// 两者相等即成立；若 wd 是仓库根的子目录也成立（在仓库内子目录运行仍算本仓库）。
// 不一致时（如开发副本目录下 git 解析到了父仓库）返回 false，调用方跳过 git 自检。
func sameRepoRoot(topLevel, wd string) bool {
	if topLevel == wd {
		return true
	}
	return strings.HasPrefix(wd, topLevel+string(os.PathSeparator))
}
