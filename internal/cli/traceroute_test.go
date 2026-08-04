package cli

import (
	"strings"
	"testing"

	"ensp-lab/internal/sim"
)

// TestFormatEnginePingSuccess 验证真实可达结果渲染出 min/avg/max 与丢包统计。
func TestFormatEnginePingSuccess(t *testing.T) {
	res := &sim.PingResult{
		Sent:     4,
		Received: 4,
		Lost:     0,
		RTTMs:    []float64{1.0, 2.0, 3.0, 2.0},
		Details: []string{
			"64 bytes from 10.0.3.2: icmp_seq=1 ttl=64 time=1.00 ms",
			"64 bytes from 10.0.3.2: icmp_seq=2 ttl=64 time=2.00 ms",
			"64 bytes from 10.0.3.2: icmp_seq=3 ttl=64 time=3.00 ms",
			"64 bytes from 10.0.3.2: icmp_seq=4 ttl=64 time=2.00 ms",
		},
	}
	out := FormatEnginePing(res, "10.0.3.2")
	if !strings.Contains(out, "4 received") {
		t.Errorf("expected '4 received' in output:\n%s", out)
	}
	if !strings.Contains(out, "0% packet loss") {
		t.Errorf("expected '0%% packet loss' in output:\n%s", out)
	}
	if !strings.Contains(out, "min/avg/max = 1.00/2.00/3.00") {
		t.Errorf("expected min/avg/max line in output:\n%s", out)
	}
}

// TestFormatEnginePingUnreachable 验证全丢包时渲染不可达，不伪造成功。
func TestFormatEnginePingUnreachable(t *testing.T) {
	res := &sim.PingResult{
		Sent:     4,
		Received: 0,
		Lost:     4,
		Details: []string{
			"Request timeout for icmp_seq 1",
			"Request timeout for icmp_seq 2",
		},
	}
	out := FormatEnginePing(res, "203.0.113.99")
	if !strings.Contains(out, "destination unreachable") {
		t.Errorf("expected 'destination unreachable' in output:\n%s", out)
	}
	if !strings.Contains(out, "100% packet loss") {
		t.Errorf("unreachable result must report 100%% loss:\n%s", out)
	}
}

// TestFormatEngineTraceroutePath 验证多跳路径渲染出每跳设备与延迟。
func TestFormatEngineTraceroutePath(t *testing.T) {
	res := &sim.TracerouteResult{
		TargetIP: "10.0.3.2",
		MaxTTL:   30,
		Reached:  true,
		Hops: []sim.TracerouteHop{
			{Hop: 1, DeviceID: "sw1", IP: "10.0.1.254", DelayMs: 1},
			{Hop: 2, DeviceID: "r2", IP: "10.0.2.1", DelayMs: 2},
			{Hop: 3, DeviceID: "sw2", IP: "10.0.2.254", DelayMs: 3},
			{Hop: 4, DeviceID: "pc1", IP: "10.0.3.2", DelayMs: 1},
		},
	}
	out := FormatEngineTraceroute(res, 30)
	for _, dev := range []string{"sw1", "r2", "sw2", "pc1"} {
		if !strings.Contains(out, dev) {
			t.Errorf("expected hop device %s in output:\n%s", dev, out)
		}
	}
	if !strings.Contains(out, "Trace complete.") {
		t.Errorf("expected 'Trace complete.' in output:\n%s", out)
	}
}

// TestFormatEngineTracerouteUnreachable 验证不可达渲染超时星号，不伪造路径。
func TestFormatEngineTracerouteUnreachable(t *testing.T) {
	res := &sim.TracerouteResult{
		TargetIP: "203.0.113.99",
		MaxTTL:   30,
		Reached:  false,
		Hops:     nil,
	}
	out := FormatEngineTraceroute(res, 30)
	if !strings.Contains(out, "Request timed out.") {
		t.Errorf("expected 'Request timed out.' in output:\n%s", out)
	}
	if strings.Contains(out, "Trace complete.") {
		t.Errorf("unreachable trace must not report complete:\n%s", out)
	}
}
