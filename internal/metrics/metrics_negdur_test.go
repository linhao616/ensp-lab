package metrics

import (
	"testing"
	"time"
)

// 本文件锁定 2026-08-12 漏洞分析 V-4（负 duration 转 uint64 回绕）的修复。
//
// 缺陷背景：
//   RecordRebuild 原实现为 ns := uint64(dur.Nanoseconds())。Nanoseconds() 返回
//   int64，负值转 uint64 会回绕成约 1.8e19（2^64 + n）。而 rebuildBusyNs 是
//   atomic.AddUint64 的累加量、lastRebuildNs 是 StoreUint64 的最新值，二者
//   都只增不减、无自愈路径 —— 一次负值即永久污染指标，/api/system/status
//   会显示天文数字的重建耗时，直接摧毁该指标的可用性。
//
// 触发条件：时钟回拨，或调用方以零值/未初始化的 start 时间计算 duration。

// TestV4NegativeDurationMustNotWrapAround 断言负耗时被夹断为 0，
// 而不是回绕成天文数字。
func TestV4NegativeDurationMustNotWrapAround(t *testing.T) {
	c := NewCollector()

	c.RecordRebuild(-5 * time.Second)

	snap := c.Snapshot()

	// 修复前：uint64(-5e9) ≈ 1.8446744e19 ns ≈ 1.8e13 ms → 断言失败。
	// 修复后：夹断为 0。
	if snap.RebuildLastMs < 0 || snap.RebuildLastMs > 1 {
		t.Fatalf("回归：负耗时未被夹断，RebuildLastMs=%v（期望 0）", snap.RebuildLastMs)
	}
	if snap.RebuildBusyMs < 0 || snap.RebuildBusyMs > 1 {
		t.Fatalf("回归：负耗时污染累加值，RebuildBusyMs=%v（期望 0）", snap.RebuildBusyMs)
	}
}

// TestV4NegativeDurationMustNotPoisonLaterValues 断言一次负值不会污染
// 后续正常记录 —— 这是该缺陷「不可自愈」特性的核心验证。
func TestV4NegativeDurationMustNotPoisonLaterValues(t *testing.T) {
	c := NewCollector()

	c.RecordRebuild(-1 * time.Hour) // 异常值
	c.RecordRebuild(10 * time.Millisecond)
	c.RecordRebuild(20 * time.Millisecond)

	snap := c.Snapshot()

	// 累加值应约为 30ms，而不是被 1.8e19 淹没。
	if snap.RebuildBusyMs < 25 || snap.RebuildBusyMs > 35 {
		t.Fatalf("回归：累加值被负值污染，RebuildBusyMs=%v（期望约 30）", snap.RebuildBusyMs)
	}
	if snap.RebuildLastMs < 15 || snap.RebuildLastMs > 25 {
		t.Fatalf("最新值异常，RebuildLastMs=%v（期望约 20）", snap.RebuildLastMs)
	}
}

// TestV4NormalDurationUnchanged 断言修复未改变正常路径的行为（非回归护栏）。
func TestV4NormalDurationUnchanged(t *testing.T) {
	c := NewCollector()

	c.RecordRebuild(50 * time.Millisecond)

	snap := c.Snapshot()
	if snap.RebuildLastMs < 45 || snap.RebuildLastMs > 55 {
		t.Fatalf("正常耗时记录异常，RebuildLastMs=%v（期望约 50）", snap.RebuildLastMs)
	}
}

// TestV4ZeroDurationIsSafe 断言零耗时（边界）不触发异常。
func TestV4ZeroDurationIsSafe(t *testing.T) {
	c := NewCollector()
	c.RecordRebuild(0)

	snap := c.Snapshot()
	if snap.RebuildLastMs != 0 {
		t.Fatalf("零耗时应记为 0，实际 %v", snap.RebuildLastMs)
	}
}
