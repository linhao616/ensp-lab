package sim

import (
	"bytes"
	"strings"
	"testing"
)

// TestDbgSimRateLimit 验证：即便开启 ENSP_DEBUG，dbgSim 也不会无限制刷日志。
// 一次窗口内大量调用应被限流到 dbgSimMaxPerWindow 行以内，避免重演“1GB 日志拖死 HTTP”。
//
// 注意：输出目标通过包级变量 dbgSimOut 注入到 bytes.Buffer，避免直接重定向 os.Stdout
// 到 pipe 导致缓冲写满时阻塞（早期版本曾因此死锁）。
func TestDbgSimRateLimit(t *testing.T) {
	oldDebug := debugSim
	debugSim = true
	oldOut := dbgSimOut
	var buf bytes.Buffer
	dbgSimOut = &buf
	defer func() {
		debugSim = oldDebug
		dbgSimOut = oldOut
	}()

	dbgSim("DEBUG: first line\n")
	for i := 0; i < 1000; i++ {
		dbgSim("DEBUG: spam %d\n", i)
	}

	lines := strings.Count(buf.String(), "\n")
	if lines > dbgSimMaxPerWindow+5 {
		t.Fatalf("rate limit not effective: emitted %d lines, expected <= %d", lines, dbgSimMaxPerWindow+5)
	}
	t.Logf("dbgSim emitted %d lines (cap ~%d) — rate limit OK", lines, dbgSimMaxPerWindow)
}
