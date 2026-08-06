package cli

// vrrp_eval_test.go —— VRRP 纯函数评估器单元测试（T05，对齐 AC4 / AC5 / AC6）。
//
// 直接构造 CLIState + DeviceConfig 键，覆盖：
//   EvaluateVRRP：Initialize / 虚拟 IP 拥有者(Master) / 本地静态假设(Master) / track 降优先级；
//   CompareVRRPPriority：高优先级胜 / 同优先级比接口 IP / 255 拥有者胜 / 确定性 tie-break；
//   vrrpSameSubnet：同网段 / 不同网段 / 接口无 IP / 非法 virtual-ip；
//   AC6 纯函数无副作用：连续两次调用结果一致、不改写 state.DeviceConfig。

import (
	"sort"
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

// snapshotDeviceConfig 把 DeviceConfig 序列化为可比较的排序字符串（用于无副作用断言）。
func snapshotDeviceConfig(state *CLIState) string {
	keys := make([]string, 0, len(state.DeviceConfig))
	for k := range state.DeviceConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(state.DeviceConfig[k])
		b.WriteString("\n")
	}
	return b.String()
}

// TestEvaluateVRRPInitialize 无 virtual-ip 键 → 未配齐，Role=Initialize。
func TestEvaluateVRRPInitialize(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	res := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	if res.Configured {
		t.Errorf("expected Configured=false, got true")
	}
	if res.Role != "Initialize" {
		t.Errorf("expected Role=Initialize, got %q", res.Role)
	}
	if res.Reason != "VRRP group not configured" {
		t.Errorf("unexpected Reason: %q", res.Reason)
	}
}

// TestEvaluateVRRPOwnerMaster Priority=255（虚拟 IP 拥有者）→ Master, IsOwner=true。
func TestEvaluateVRRPOwnerMaster(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "virtual-ip")] = "192.168.1.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "priority")] = "255"
	res := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	if !res.Configured {
		t.Fatalf("expected Configured=true")
	}
	if res.Role != "Master" {
		t.Errorf("expected Role=Master, got %q", res.Role)
	}
	if !res.IsOwner {
		t.Errorf("expected IsOwner=true for priority 255")
	}
	if res.Reason != "Virtual IP owner (priority 255)" {
		t.Errorf("unexpected Reason: %q", res.Reason)
	}
	if res.Priority != 255 {
		t.Errorf("expected Priority=255, got %d", res.Priority)
	}
}

// TestEvaluateVRRPStaticMaster 普通优先级 → 本地静态假设 Master。
func TestEvaluateVRRPStaticMaster(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "virtual-ip")] = "192.168.1.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "priority")] = "120"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "preempt")] = "disable"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "advertise")] = "2"
	res := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	if res.Role != "Master" {
		t.Errorf("expected Role=Master, got %q", res.Role)
	}
	if res.IsOwner {
		t.Errorf("expected IsOwner=false for priority 120")
	}
	if !strings.Contains(res.Reason, "Local static assumption") {
		t.Errorf("expected honest reason, got %q", res.Reason)
	}
	if res.Priority != 120 || res.EffectivePriority != 120 {
		t.Errorf("expected Priority/EffectivePriority=120, got %d/%d", res.Priority, res.EffectivePriority)
	}
	if res.Preempt {
		t.Errorf("expected Preempt=false (preempt-mode disable)")
	}
	if res.Advertise != 2 {
		t.Errorf("expected Advertise=2, got %d", res.Advertise)
	}
}

// TestEvaluateVRRPTrackReduced 被跟踪接口 Down → 有效优先级下降（缺省 reduced=10）。
func TestEvaluateVRRPTrackReduced(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "virtual-ip")] = "192.168.1.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "priority")] = "120"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "track-iface")] = "GigabitEthernet0/0/2"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "track-reduced")] = "30"
	// 被跟踪口未 Down → 有效优先级 = 配置优先级。
	resUp := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	if resUp.EffectivePriority != 120 {
		t.Errorf("track iface up: expected EffectivePriority=120, got %d", resUp.EffectivePriority)
	}
	// 被跟踪口 Down → 有效优先级 = 120 - 30 = 90。
	s.DeviceConfig["interface:GigabitEthernet0/0/2:status"] = "Down"
	resDown := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	if resDown.EffectivePriority != 90 {
		t.Errorf("track iface down: expected EffectivePriority=90, got %d", resDown.EffectivePriority)
	}
}

// TestCompareVRRPPriority 覆盖选举比较规则与 tie-break。
func TestCompareVRRPPriority(t *testing.T) {
	// 高优先级胜。
	if c := CompareVRRPPriority(VRRPGroup{Priority: 120}, VRRPGroup{Priority: 100}); c <= 0 {
		t.Errorf("higher priority should win, got %d", c)
	}
	// 255 拥有者胜（即便对端优先级更高）。
	if c := CompareVRRPPriority(VRRPGroup{Priority: 255}, VRRPGroup{Priority: 254}); c <= 0 {
		t.Errorf("owner(255) should win, got %d", c)
	}
	if c := CompareVRRPPriority(VRRPGroup{Priority: 100}, VRRPGroup{Priority: 255}); c >= 0 {
		t.Errorf("non-owner should lose to owner, got %d", c)
	}
	// 同优先级比接口 IP 大者胜。
	a := VRRPGroup{Priority: 100, InterfaceIP: "192.168.1.2"}
	b := VRRPGroup{Priority: 100, InterfaceIP: "192.168.1.1"}
	if c := CompareVRRPPriority(a, b); c <= 0 {
		t.Errorf("larger interface IP should win on tie, got %d", c)
	}
	if c := CompareVRRPPriority(b, a); c >= 0 {
		t.Errorf("smaller interface IP should lose on tie, got %d", c)
	}
	// 完全相等 → 0（确定性 tie-break）。
	same := VRRPGroup{Priority: 100, InterfaceIP: "192.168.1.1"}
	if c := CompareVRRPPriority(same, same); c != 0 {
		t.Errorf("identical groups should tie (0), got %d", c)
	}
}

// TestVRRPSameSubnet 同网段校验纯函数。
func TestVRRPSameSubnet(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig["interface:GigabitEthernet0/0/1:ip"] = "192.168.1.1 255.255.255.0"
	// 同网段。
	if ok, _, msg := vrrpSameSubnet(s, "GigabitEthernet0/0/1", "192.168.1.254"); !ok {
		t.Errorf("expected same subnet, got error: %q", msg)
	}
	// 不同网段。
	if ok, _, msg := vrrpSameSubnet(s, "GigabitEthernet0/0/1", "10.0.0.1"); ok {
		t.Errorf("expected different subnet rejection, got ok")
	} else if !strings.Contains(msg, "not in the same subnet") {
		t.Errorf("expected 'not in the same subnet' message, got %q", msg)
	}
	// 接口无 IP。
	s2 := NewCLIStateWithType(topology.DeviceRouter)
	if ok, _, msg := vrrpSameSubnet(s2, "GigabitEthernet0/0/1", "192.168.1.254"); ok {
		t.Errorf("expected rejection when interface has no IP")
	} else if !strings.Contains(msg, "no IP address configured") {
		t.Errorf("expected 'no IP address' message, got %q", msg)
	}
	// 非法 virtual-ip。
	if ok, _, msg := vrrpSameSubnet(s, "GigabitEthernet0/0/1", "not-an-ip"); ok {
		t.Errorf("expected rejection for invalid virtual-ip")
	} else if !strings.Contains(msg, "invalid virtual-ip") {
		t.Errorf("expected 'invalid virtual-ip' message, got %q", msg)
	}
}

// TestVRRPPureFunctionNoSideEffects AC6：连续两次 EvaluateVRRP 结果一致且不改写 state/DeviceConfig。
func TestVRRPPureFunctionNoSideEffects(t *testing.T) {
	s := NewCLIStateWithType(topology.DeviceRouter)
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "virtual-ip")] = "192.168.1.254"
	s.DeviceConfig[vrrpKey("GigabitEthernet0/0/1", 1, "priority")] = "120"
	before := snapshotDeviceConfig(s)

	res1 := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	after1 := snapshotDeviceConfig(s)
	res2 := EvaluateVRRP(s, "GigabitEthernet0/0/1", 1)
	after2 := snapshotDeviceConfig(s)

	if after1 != before {
		t.Errorf("EvaluateVRRP mutated DeviceConfig on first call:\nbefore=%q\nafter=%q", before, after1)
	}
	if after2 != after1 {
		t.Errorf("EvaluateVRRP mutated DeviceConfig on second call")
	}
	if res1 != res2 {
		t.Errorf("two consecutive calls returned different results: %+v vs %+v", res1, res2)
	}
}
