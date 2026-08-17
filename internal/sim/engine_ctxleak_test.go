package sim

import (
	"testing"

	"ensp-lab/internal/topology"
)

// 本文件锁定 2026-08-12 漏洞分析 V-1（context / cancelFunc 泄漏）的修复。
//
// 缺陷背景：
//   nsxEngine 在 NewNSxEngine 与 Start 中各调用一次 context.WithCancel，
//   把 cancel 存入 e.cancelFunc 字段，但**全文件从未调用过它**。
//   （对照 gont_emulator.go:216 有正确的 e.cancelFunc() 调用。）
//
//   后果：每次 Start() 都在 context.Background() 上挂一个永不释放的子节点，
//   反复启停会持续累积；构造失败路径同样泄漏。go vet 的 lostcancel 分析器
//   不报此类问题（cancel 被赋值给结构体字段即视为已逃逸），是 gosec G118 发现的。
//
// 断言策略：直接检查 cancelCtx.Err()。修复前 Stop() 后 Err() 恒为 nil；
// 修复后必须为 context.Canceled。这是「泄漏已消除」的必要条件。

// TestV1StopMustCancelContext 断言 Stop() 会取消 Start() 派生的 context。
func TestV1StopMustCancelContext(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	e, ok := eng.(*nsxEngine)
	if !ok {
		t.Fatalf("期望 *nsxEngine，实际 %T", eng)
	}

	eng.Start()

	e.mu.RLock()
	ctx := e.cancelCtx
	e.mu.RUnlock()
	if ctx == nil {
		t.Fatal("Start 后 cancelCtx 不应为 nil")
	}
	if cerr := ctx.Err(); cerr != nil {
		t.Fatalf("Start 后 context 不应已取消，实际 Err()=%v", cerr)
	}

	eng.Stop()

	// 修复前此处恒为 nil（cancelFunc 从未被调用）→ 用例失败。
	if cerr := ctx.Err(); cerr == nil {
		t.Fatal("回归：Stop() 未取消 context —— cancelFunc 泄漏（V-1）已被回退")
	}
}

// TestV1RepeatedStartStopMustNotLeakContexts 断言反复启停时，每一轮的 context
// 都被取消，不会累积泄漏。这是 V-1 影响面最大的场景（泄漏随启停次数线性增长）。
func TestV1RepeatedStartStopMustNotLeakContexts(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	e, ok := eng.(*nsxEngine)
	if !ok {
		t.Fatalf("期望 *nsxEngine，实际 %T", eng)
	}

	const rounds = 5
	for i := 0; i < rounds; i++ {
		eng.Start()

		e.mu.RLock()
		ctx := e.cancelCtx
		e.mu.RUnlock()
		if ctx == nil {
			t.Fatalf("第 %d 轮：Start 后 cancelCtx 为 nil", i+1)
		}

		eng.Stop()

		if cerr := ctx.Err(); cerr == nil {
			t.Fatalf("第 %d 轮：Stop() 后 context 未取消 —— 该轮 context 泄漏", i+1)
		}
	}
}

// TestV1StopIsStillIdempotent 断言新增的 cancel 调用没有破坏 Stop 的幂等性
// （重复 Stop 必须安全，不得 panic）。这是修复的非回归护栏。
func TestV1StopIsStillIdempotent(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}

	eng.Start()
	eng.Stop()
	eng.Stop() // 第二次必须直接返回，不得 panic（context.CancelFunc 本身幂等）
	eng.Stop()
}

// TestV1StopBeforeStartIsSafe 断言未 Start 就 Stop 不会 panic。
// 修复引入了「持锁取出 cancelFunc、锁外调用」的写法，此处覆盖 cancelFunc
// 可能为 nil 或引擎未启动的分支。
func TestV1StopBeforeStartIsSafe(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	eng.Stop() // started=false，应直接返回
}

// TestV1NewEngineNilTopoStillRejected 断言构造失败路径（V-1 的第二处泄漏点，
// 已在 NewNSxEngine 的 build 失败分支补 cancel()）未改变对外错误契约。
func TestV1NewEngineNilTopoStillRejected(t *testing.T) {
	var nilTopo *topology.Topology
	if _, err := NewNSxEngine(nilTopo); err == nil {
		t.Fatal("nil 拓扑必须返回错误")
	}
}
