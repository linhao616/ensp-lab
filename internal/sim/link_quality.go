package sim

import "math/rand/v2"

// 链路质量模型（v0.12）
//
// 把拓扑链路上配置的「单向延迟 delay」与「丢包率 loss」折算成端到端
// Ping 的可观测行为。设计取舍：
//
//   - 引擎为同步转发，ns-x 网络本身不消耗配置的链路延迟（Link.Delay 此前
//     只被 tracePath 用于 traceroute 展示），因此这里按路径累加后再叠加到
//     wall-clock RTT 上，不存在重复计时。
//   - 丢包采用「路径累积」语义：P = 1 - Π(1 - p_i)，即每跳独立丢包、
//     端到端存活概率为逐跳存活概率之积。这与真实链路串联的直觉一致，
//     且与下发顺序无关。
//   - 本文件只放纯函数 + 一个可注入的随机源，不读写引擎状态，便于单测
//     用确定值验证边界，避免把概率行为写进不可控的集成路径。
const (
	lossPctMin = 0.0
	lossPctMax = 100.0
)

// linkQualityEdge 是 BFS 邻接表上一条边携带的质量属性。
// 同一对设备间存在多条平行链路时沿用既有「后者覆盖」语义（Links 为切片，
// 遍历顺序确定），保证 delay 与 loss 取自同一条链路而不互相错配。
type linkQualityEdge struct {
	delay int
	loss  float64
}

// clampLossPct 把丢包率夹到 [0,100]，防御脏数据（历史 JSON / 手工改盘）。
func clampLossPct(pct float64) float64 {
	if pct < lossPctMin {
		return lossPctMin
	}
	if pct > lossPctMax {
		return lossPctMax
	}
	return pct
}

// EndToEndLossProb 按路径累积语义把逐跳丢包率（百分比 0~100）折算为
// 端到端丢包概率（0~1）：P = 1 - Π(1 - p_i / 100)。
// 空路径返回 0（无链路 = 不丢包）。
func EndToEndLossProb(hopLossPct []float64) float64 {
	survive := 1.0
	for _, p := range hopLossPct {
		survive *= 1 - clampLossPct(p)/100
	}
	if survive <= 0 {
		return 1
	}
	if survive >= 1 {
		return 0
	}
	return 1 - survive
}

// ShouldDrop 判定单个探测包是否被丢弃。
// sample 由调用方提供，须落在 [0,1)；测试可注入固定值获得确定行为。
func ShouldDrop(prob, sample float64) bool {
	if prob <= 0 {
		return false
	}
	if prob >= 1 {
		return true
	}
	return sample < prob
}

// SumOneWayDelayMs 累加路径上各跳链路的单向延迟（毫秒），负值按 0 计。
func SumOneWayDelayMs(hopDelays []int) int {
	total := 0
	for _, d := range hopDelays {
		if d > 0 {
			total += d
		}
	}
	return total
}

// RoundTripDelayMs 把单向延迟累加值折算成 RTT 附加量：探测包需走去、回两程。
func RoundTripDelayMs(hopDelays []int) float64 {
	return float64(2 * SumOneWayDelayMs(hopDelays))
}

// lossSampler 是丢包判定的随机源，默认取 math/rand/v2 的全局并发安全实现。
// 与 dbgSimOut 同为「可注入的包级变量」，仅在包内测试中替换以获得确定序列。
var lossSampler = rand.Float64
