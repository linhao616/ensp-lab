package sim

// link_quality_test.go —— v0.12 链路质量模拟：引擎侧验收测试。
//
// 覆盖三类：
//  1. 纯函数数学（端到端丢包累积 / 丢包判定 / 延迟折算），用固定值锁死语义；
//  2. Ping 行为校准（配置延迟必须体现在 RTT，配置丢包必须体现在实际丢包），
//     其中丢包用注入的确定随机源验证，避免概率测试抖动；
//  3. 首跳延迟回归锁：tracePath 此前把 src->path[0] 的链路延迟硬记为 0，
//     导致两节点直连拓扑上 delay 配置完全不生效，本文件锁死修复后的行为。

import (
	"testing"

	"ensp-lab/internal/topology"
)

// —— 1. 纯函数 ——

func TestEndToEndLossProb(t *testing.T) {
	cases := []struct {
		name string
		hops []float64
		want float64
	}{
		{"空路径不丢包", nil, 0},
		{"全零不丢包", []float64{0, 0, 0}, 0},
		{"单跳100%全丢", []float64{100}, 1},
		{"单跳10%", []float64{10}, 0.1},
		{"两跳50%累积为75%", []float64{50, 50}, 0.75},
		{"三跳10%累积", []float64{10, 10, 10}, 0.271},
		{"任一跳100%即全丢", []float64{1, 100, 1}, 1},
		{"越界值被夹取", []float64{-5, 105}, 1},
		{"负值按0计", []float64{-5, 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EndToEndLossProb(tc.hops)
			if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("EndToEndLossProb(%v) = %v, want %v", tc.hops, got, tc.want)
			}
		})
	}
}

func TestShouldDrop(t *testing.T) {
	cases := []struct {
		name   string
		prob   float64
		sample float64
		want   bool
	}{
		{"概率0永不丢", 0, 0, false},
		{"概率0即便样本为0也不丢", 0, 0.999, false},
		{"概率1必丢", 1, 0.999, true},
		{"概率超过1必丢", 1.5, 0.999, true},
		{"样本小于概率则丢", 0.5, 0.4999, true},
		{"样本等于概率不丢（左闭右开）", 0.5, 0.5, false},
		{"样本大于概率不丢", 0.5, 0.6, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldDrop(tc.prob, tc.sample); got != tc.want {
				t.Fatalf("ShouldDrop(%v, %v) = %v, want %v", tc.prob, tc.sample, got, tc.want)
			}
		})
	}
}

func TestDelayAggregation(t *testing.T) {
	if got := SumOneWayDelayMs(nil); got != 0 {
		t.Fatalf("SumOneWayDelayMs(nil) = %d, want 0", got)
	}
	if got := SumOneWayDelayMs([]int{5, 10, 15}); got != 30 {
		t.Fatalf("SumOneWayDelayMs = %d, want 30", got)
	}
	// 负值（脏数据）按 0 计，不得倒扣总延迟。
	if got := SumOneWayDelayMs([]int{5, -100, 15}); got != 20 {
		t.Fatalf("SumOneWayDelayMs with negative = %d, want 20", got)
	}
	// RTT 需经历去、回两程。
	if got := RoundTripDelayMs([]int{5, 10, 15}); got != 60 {
		t.Fatalf("RoundTripDelayMs = %v, want 60", got)
	}
}

// —— 2. Ping 行为校准 ——

// newTwoPCTopoWithQuality 在 newTwoPCTopo 基础上给唯一链路配置延迟与丢包。
func newTwoPCTopoWithQuality(delayMs int, lossPct float64) *topology.Topology {
	topo := newTwoPCTopo()
	for _, l := range topo.Links {
		l.Delay = delayMs
		l.Loss = lossPct
	}
	return topo
}

// TestPingAccumulatesLinkDelay 验证配置的单向链路延迟按往返累加进 RTT。
// 同时是首跳延迟修复的回归锁：两节点直连拓扑只有「首跳」一段链路，
// 修复前 delays[0] 恒为 0，本用例必然失败。
func TestPingAccumulatesLinkDelay(t *testing.T) {
	const delayMs = 25
	eng, err := NewNSxEngine(newTwoPCTopoWithQuality(delayMs, 0))
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	res, err := eng.Ping("pc1", "192.168.1.3")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res.Received == 0 {
		t.Fatalf("expected reachable, got %+v", res)
	}
	// 单向 25ms -> 往返至少 50ms。
	const wantMin = float64(2 * delayMs)
	for i, rtt := range res.RTTMs {
		if rtt < wantMin {
			t.Fatalf("RTT sample %d = %.2f ms, want >= %.0f ms (2 x one-way %d ms)", i, rtt, wantMin, delayMs)
		}
	}
}

// TestPingWithoutDelayStaysFast 反向对照：未配置延迟时不得凭空加时延。
func TestPingWithoutDelayStaysFast(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	res, err := eng.Ping("pc1", "192.168.1.3")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res.Received == 0 {
		t.Fatalf("expected reachable, got %+v", res)
	}
	// CI（共享 CPU 的 Ubuntu runner）上 ns-x 事件循环基线 RTT 可达 ~60ms，
	// 绝对上界取 500ms 仅验证「未配置延迟时不凭空增加量级级延迟」；
	// 「配置延迟确实生效」由 TestPingWithDelayAddsRTT 的下界断言（RTT >= 2*delay）保证。
	const wantMax = 500.0
	for i, rtt := range res.RTTMs {
		if rtt >= wantMax {
			t.Fatalf("RTT sample %d = %.2f ms without configured delay, want < %.0f ms", i, rtt, wantMax)
		}
	}
}

// TestPingFullLossDropsAll 验证 loss=100 时全部 echo 判丢（不依赖随机源）。
func TestPingFullLossDropsAll(t *testing.T) {
	eng, err := NewNSxEngine(newTwoPCTopoWithQuality(0, 100))
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	res, err := eng.Ping("pc1", "192.168.1.3")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res.Received != 0 || res.Lost != res.Sent {
		t.Fatalf("loss=100%% expected all lost, got sent=%d received=%d lost=%d", res.Sent, res.Received, res.Lost)
	}
}

// TestPingPartialLossDeterministic 用注入的确定随机序列验证部分丢包：
// loss=50% 时样本 <0.5 判丢、>=0.5 判通，4 个 echo 应恰好 2 通 2 丢。
func TestPingPartialLossDeterministic(t *testing.T) {
	samples := []float64{0.1, 0.9, 0.2, 0.8} // 丢、通、丢、通
	idx := 0
	orig := lossSampler
	lossSampler = func() float64 {
		v := samples[idx%len(samples)]
		idx++
		return v
	}
	defer func() { lossSampler = orig }()

	eng, err := NewNSxEngine(newTwoPCTopoWithQuality(0, 50))
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	res, err := eng.Ping("pc1", "192.168.1.3")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res.Received != 2 || res.Lost != 2 {
		t.Fatalf("expected 2 received / 2 lost with injected samples %v, got received=%d lost=%d (details=%v)",
			samples, res.Received, res.Lost, res.Details)
	}
	if idx != 4 {
		t.Fatalf("lossSampler should be consulted once per delivered echo, called %d times", idx)
	}
}

// TestPingZeroLossNeverConsultsSampler 验证零丢包配置下不进入概率分支
// （ShouldDrop 短路），避免无谓的随机数消耗与行为噪声。
func TestPingZeroLossNeverConsultsSampler(t *testing.T) {
	calls := 0
	orig := lossSampler
	lossSampler = func() float64 {
		calls++
		return 0 // 若被采纳会导致全丢，用于放大错误
	}
	defer func() { lossSampler = orig }()

	eng, err := NewNSxEngine(newTwoPCTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()
	eng.Start()

	res, err := eng.Ping("pc1", "192.168.1.3")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if res.Received == 0 {
		t.Fatalf("zero-loss link must stay reachable, got %+v", res)
	}
	if calls != 0 {
		t.Fatalf("lossSampler must not be consulted when loss=0, called %d times", calls)
	}
}

// —— 3. tracePath 首跳延迟/丢包回归锁 ——

// TestTracePathFirstHopQuality 验证 tracePath 首跳取到真实入链路的
// delay/loss（修复前恒为 0），且 delays/losses 与 path 同索引对齐。
func TestTracePathFirstHopQuality(t *testing.T) {
	topo := newTwoPCTopoWithQuality(7, 3.5)
	eng, err := NewNSxEngine(topo)
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()

	nsx, ok := eng.(*nsxEngine)
	if !ok {
		t.Fatalf("expected *nsxEngine, got %T", eng)
	}
	path, delays, losses, connected := nsx.tracePath("pc1", "pc2")
	if !connected {
		t.Fatalf("pc1 -> pc2 should be connected")
	}
	if len(path) != 1 || path[0] != "pc2" {
		t.Fatalf("path = %v, want [pc2]", path)
	}
	if len(delays) != len(path) || len(losses) != len(path) {
		t.Fatalf("delays/losses must align with path: path=%d delays=%d losses=%d", len(path), len(delays), len(losses))
	}
	if delays[0] != 7 {
		t.Fatalf("first hop delay = %d, want 7 (首跳链路延迟不得被丢弃)", delays[0])
	}
	if losses[0] != 3.5 {
		t.Fatalf("first hop loss = %v, want 3.5", losses[0])
	}
}

// TestTracerouteReportsFirstHopDelay 验证 traceroute 首跳延迟同步修正：
// 三跳拓扑 l1 延迟 1ms，首跳应如实上报 1 而非 0。
func TestTracerouteReportsFirstHopDelay(t *testing.T) {
	eng, err := NewNSxEngine(newThreeHopTopo())
	if err != nil {
		t.Fatalf("NewNSxEngine: %v", err)
	}
	defer eng.Stop()

	res, err := eng.Traceroute("r1", "10.0.3.2", 30)
	if err != nil {
		t.Fatalf("Traceroute: %v", err)
	}
	if len(res.Hops) == 0 {
		t.Fatalf("expected hops, got %+v", res)
	}
	// newThreeHopTopo: l1(r1-sw1)=1, l2(sw1-r2)=2, l3(r2-sw2)=3, l4(sw2-pc1)=1
	wantDelays := []float64{1, 2, 3, 1}
	if len(res.Hops) != len(wantDelays) {
		t.Fatalf("expected %d hops, got %d", len(wantDelays), len(res.Hops))
	}
	for i, want := range wantDelays {
		if res.Hops[i].DelayMs != want {
			t.Errorf("hop %d (%s) DelayMs = %v, want %v", i+1, res.Hops[i].DeviceID, res.Hops[i].DelayMs, want)
		}
	}
}
