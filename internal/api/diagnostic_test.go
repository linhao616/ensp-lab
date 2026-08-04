//go:build !integration

package api

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"
)

// newDiagTestRouter 在 newTestRouter 基础上注册 P1-D 诊断网关路由与电源路由，
// 复用同一 *Router，使 handler 与引擎共享状态（与真实运行态一致）。
func newDiagTestRouter(store storage.Storage) (*Router, *http.ServeMux) {
	ar, mux := newTestRouter(store)
	mux.HandleFunc("POST /api/topologies/{id}/devices/{deviceId}/power", func(w http.ResponseWriter, req *http.Request) {
		ar.powerDevice(c(w, req))
	})
	mux.HandleFunc("POST /api/diagnostic/{id}/ping", func(w http.ResponseWriter, req *http.Request) {
		ar.diagnosticPing(c(w, req))
	})
	mux.HandleFunc("POST /api/diagnostic/{id}/traceroute", func(w http.ResponseWriter, req *http.Request) {
		ar.diagnosticTraceroute(c(w, req))
	})
	mux.HandleFunc("POST /api/diagnostic/{id}/dns", func(w http.ResponseWriter, req *http.Request) {
		ar.diagnosticDNS(c(w, req))
	})
	return ar, mux
}

// buildTwoPCTopology 在存储中构造由单链路连接、已开机且接口 up 的两台 PC，
// 供诊断网关的成功态测试。
func buildTwoPCTopology(t *testing.T, store storage.Storage, id string) {
	t.Helper()
	topo := topology.NewTopology(id, "diag-2pc")
	mk := func(did, name, ip string) *topology.Device {
		d := &topology.Device{ID: did, Name: name, Type: topology.DevicePC, Status: topology.StatusRunning}
		d.InitializeDefaults()
		d.Interfaces["Ethernet0"].IPAddress = ip
		d.Interfaces["Ethernet0"].SubnetMask = "255.255.255.0"
		d.Interfaces["Ethernet0"].Status = "up"
		return d
	}
	topo.AddDevice(mk("pc1", "PC1", "192.168.1.2"))
	topo.AddDevice(mk("pc2", "PC2", "192.168.1.3"))
	topo.AddLink(&topology.Link{
		ID: "l1", SourceDevice: "pc1", SourcePort: "Ethernet0",
		TargetDevice: "pc2", TargetPort: "Ethernet0", LinkType: topology.LinkTypeBusiness,
	})
	if err := store.CreateTopology(topo); err != nil {
		t.Fatalf("CreateTopology: %v", err)
	}
}

// powerOff 把指定设备置为未开机（clone + 回写，避免直接改写共享指针）。
func powerOff(t *testing.T, store storage.Storage, topoID, devID string) {
	t.Helper()
	topo, err := store.GetTopology(topoID)
	if err != nil || topo == nil {
		t.Fatalf("GetTopology: %v", err)
	}
	topo = topo.Clone()
	if d, ok := topo.GetDevice(devID); ok {
		d.Status = topology.StatusPowerOff
	}
	if err := store.UpdateTopology(topo); err != nil {
		t.Fatalf("UpdateTopology: %v", err)
	}
}

// --- 响应结构 ---

type diagPingResp struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	RTT     struct {
		Min  float64 `json:"min"`
		Avg  float64 `json:"avg"`
		Max  float64 `json:"max"`
		Loss float64 `json:"loss"`
	} `json:"rtt"`
}

type diagTraceResp struct {
	Reachable bool `json:"reachable"`
	Hops      []struct {
		Hop    int     `json:"hop"`
		IP     string  `json:"ip"`
		Device string  `json:"device"`
		RTT    float64 `json:"rtt"`
	} `json:"hops"`
}

type diagDNSResp struct {
	IP    string   `json:"ip"`
	IPs   []string `json:"ips"`
	Error string   `json:"error"`
}

func decodePing(t *testing.T, body string) diagPingResp {
	t.Helper()
	var r diagPingResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode ping resp: %v (body=%s)", err, body)
	}
	return r
}

func decodeTrace(t *testing.T, body string) diagTraceResp {
	t.Helper()
	var r diagTraceResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode trace resp: %v (body=%s)", err, body)
	}
	return r
}

func decodeDNS(t *testing.T, body string) diagDNSResp {
	t.Helper()
	var r diagDNSResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode dns resp: %v (body=%s)", err, body)
	}
	return r
}

// TestDiagnosticPingSuccess 验证两转发设备 ping 成功：success=true、loss=0、
// 返回结构化 rtt 统计（不再依赖前端造假）。
func TestDiagnosticPingSuccess(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-ping-ok")

	// 使用拓扑内设备 ID（pc2）作为目标：既走真实内部 ping，又不触发 V-03
	// 外部诊断门控（isTopologyDevice 命中），与改动前 dst=IP 的 happy-path 等价。
	body := `{"src":"pc1","dst":"pc2","count":4}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-ping-ok/ping", body)
	if resp.code != http.StatusOK {
		t.Fatalf("ping success: want 200, got %d %s", resp.code, resp.body)
	}
	r := decodePing(t, resp.body)
	if !r.Success {
		t.Fatalf("ping success: expected success=true, got %+v (output=%q)", r, r.Output)
	}
	if r.RTT.Loss != 0 {
		t.Errorf("ping success: expected loss=0, got %v", r.RTT.Loss)
	}
	if r.RTT.Max < r.RTT.Min {
		t.Errorf("ping success: max < min: %+v", r.RTT)
	}
	// output 应为 Details 换行拼接，非空。
	if r.Output == "" {
		t.Errorf("ping success: output should not be empty")
	}
}

// TestDiagnosticPingUnreachable 验证对拓扑外 IP ping 失败（100% 丢包、success=false、
// rtt 置 0），如实报告而非伪造可达。
//
// 注：目标 203.0.113.99 为文档保留网段（TEST-NET-3），不可路由必然 100% 丢包；
// 设置 ENS_DIAG_ALLOW_EXTERNAL=1 仅临时放行"拓扑外目标"以走通 happy-path 断言，
// 不削弱 V-03 门控（见 TestDiagnosticExternalTargetForbidden 的正向 403 断言）。
func TestDiagnosticPingUnreachable(t *testing.T) {
	t.Setenv("ENS_DIAG_ALLOW_EXTERNAL", "1")
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-ping-loss")

	body := `{"src":"pc1","dst":"203.0.113.99","count":4}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-ping-loss/ping", body)
	if resp.code != http.StatusOK {
		t.Fatalf("ping unreachable: want 200, got %d %s", resp.code, resp.body)
	}
	r := decodePing(t, resp.body)
	if r.Success {
		t.Fatalf("ping unreachable: expected success=false, got %+v", r)
	}
	if r.RTT.Loss != 100 {
		t.Errorf("ping unreachable: expected loss=100, got %v", r.RTT.Loss)
	}
	if r.RTT.Min != 0 || r.RTT.Avg != 0 || r.RTT.Max != 0 {
		t.Errorf("ping unreachable: expected rtt all 0, got %+v", r.RTT)
	}
}

// TestDiagnosticPingPowerOff 验证源设备未开机时返回 400 + 明确的"未开机"提示。
func TestDiagnosticPingPowerOff(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-ping-off")
	powerOff(t, store, "topo-ping-off", "pc1")

	body := `{"src":"pc1","dst":"192.168.1.3","count":4}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-ping-off/ping", body)
	if resp.code != http.StatusBadRequest {
		t.Fatalf("ping power-off: want 400, got %d %s", resp.code, resp.body)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp.body), &errBody); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errBody.Error == "" || !bytes.Contains([]byte(errBody.Error), []byte("未开机")) {
		t.Errorf("ping power-off: expected '未开机' message, got %q", errBody.Error)
	}
}

// TestDiagnosticTraceroutePath 验证真实路径发现：可达、返回逐跳（含 device/ip/rtt）。
func TestDiagnosticTraceroutePath(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-trace-ok")

	body := `{"src":"pc1","dst":"pc2"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-trace-ok/traceroute", body)
	if resp.code != http.StatusOK {
		t.Fatalf("traceroute path: want 200, got %d %s", resp.code, resp.body)
	}
	r := decodeTrace(t, resp.body)
	if !r.Reachable {
		t.Fatalf("traceroute path: expected reachable=true, got hops=%v", r.Hops)
	}
	if len(r.Hops) < 1 {
		t.Fatalf("traceroute path: expected >=1 hop, got %v", r.Hops)
	}
	for _, h := range r.Hops {
		if h.Device == "" {
			t.Errorf("traceroute path: hop device should not be empty: %+v", h)
		}
	}
	// 末跳应为目标设备 pc2（路径发现真实可达）。
	if last := r.Hops[len(r.Hops)-1]; last.Device != "pc2" {
		t.Errorf("traceroute path: last hop should be pc2, got %s", last.Device)
	}
}

// TestDiagnosticTracerouteUnreachable 验证目标不在拓扑中时如实返回不可达
// （hops 空、reachable=false），不伪造路径。
//
// 同 TestDiagnosticPingUnreachable：临时放行外部目标以走通断言，门控本身由
// TestDiagnosticExternalTargetForbidden 正向覆盖。
func TestDiagnosticTracerouteUnreachable(t *testing.T) {
	t.Setenv("ENS_DIAG_ALLOW_EXTERNAL", "1")
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-trace-loss")

	body := `{"src":"pc1","dst":"203.0.113.99"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-trace-loss/traceroute", body)
	if resp.code != http.StatusOK {
		t.Fatalf("traceroute unreachable: want 200, got %d %s", resp.code, resp.body)
	}
	r := decodeTrace(t, resp.body)
	if r.Reachable {
		t.Fatalf("traceroute unreachable: expected reachable=false, got %+v", r)
	}
	if len(r.Hops) != 0 {
		t.Fatalf("traceroute unreachable: expected empty hops, got %v", r.Hops)
	}
}

// TestDiagnosticTraceroutePowerOff 验证源设备未开机时返回 400。
func TestDiagnosticTraceroutePowerOff(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-trace-off")
	powerOff(t, store, "topo-trace-off", "pc1")

	body := `{"src":"pc1","dst":"pc2"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-trace-off/traceroute", body)
	if resp.code != http.StatusBadRequest {
		t.Fatalf("traceroute power-off: want 400, got %d %s", resp.code, resp.body)
	}
}

// TestDiagnosticDNSLocalOverride 验证本地 host 映射预设时返回真实 IP（确定性，不依赖外网）。
func TestDiagnosticDNSLocalOverride(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-dns-local")

	// 注入本地映射（测试后还原），模拟"拓扑/运维预设的 host 映射"。
	prev := localDNSHosts["host.local"]
	localDNSHosts["host.local"] = "203.0.113.10"
	defer func() { localDNSHosts["host.local"] = prev }()

	body := `{"src":"pc1","domain":"host.local"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-dns-local/dns", body)
	if resp.code != http.StatusOK {
		t.Fatalf("dns local override: want 200, got %d %s", resp.code, resp.body)
	}
	r := decodeDNS(t, resp.body)
	if r.IP != "203.0.113.10" {
		t.Errorf("dns local override: expected ip 203.0.113.10, got %q", r.IP)
	}
}

// TestDiagnosticDNSPowerOff 验证源设备未开机时返回 400。
func TestDiagnosticDNSPowerOff(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-dns-off")
	powerOff(t, store, "topo-dns-off", "pc1")

	body := `{"src":"pc1","domain":"www.example.com"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-dns-off/dns", body)
	if resp.code != http.StatusBadRequest {
		t.Fatalf("dns power-off: want 400, got %d %s", resp.code, resp.body)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(resp.body), &errBody)
	if errBody.Error == "" || !bytes.Contains([]byte(errBody.Error), []byte("未开机")) {
		t.Errorf("dns power-off: expected '未开机' message, got %q", errBody.Error)
	}
}

// TestDiagnosticDNSHonest 验证系统 DNS 失败时如实 404，且绝不以假 IP 糊弄：
// 成功则 ip 必须是合法 IP；失败则 error 字段存在且不应出现编造 ip。
// 该测试不依赖 sandbox 是否联网（两种结果均可接受）。
//
// 临时放行外部 DNS（ENS_DIAG_ALLOW_EXTERNAL=1）以走通真实解析路径；
// 默认禁用下的 403 由 TestDiagnosticExternalTargetForbidden 正向覆盖。
func TestDiagnosticDNSHonest(t *testing.T) {
	t.Setenv("ENS_DIAG_ALLOW_EXTERNAL", "1")
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-dns-net")

	body := `{"src":"pc1","domain":"www.example.com"}`
	resp := doRaw(mux, "POST", "/api/diagnostic/topo-dns-net/dns", body)
	switch resp.code {
	case http.StatusOK:
		r := decodeDNS(t, resp.body)
		if r.IP == "" || net.ParseIP(r.IP) == nil {
			t.Fatalf("dns success but ip invalid/empty: %q", r.IP)
		}
	case http.StatusNotFound:
		r := decodeDNS(t, resp.body)
		if r.Error == "" {
			t.Errorf("dns 404 but no error message (前端无法展示失败原因)")
		}
		if r.IP != "" {
			t.Errorf("dns 404 must not return a fake ip, got %q", r.IP)
		}
	default:
		t.Fatalf("dns: unexpected status %d %s", resp.code, resp.body)
	}
}

// TestDiagnosticExternalTargetForbidden 正向覆盖 V-03 外部诊断门控：在默认安全策略
// （ENS_DIAG_ALLOW_EXTERNAL 未开启）下，对"拓扑外目标"（公网 IP / 公网域名）的诊断
// 必须返回 403，防止服务端被用作网络侦察 / DoS 放大跳板。该断言防止门控被回退。
func TestDiagnosticExternalTargetForbidden(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newDiagTestRouter(store)
	buildTwoPCTopology(t, store, "topo-ext-gate")

	// 拓扑外 IP 的 ping 必须 403。
	respPing := doRaw(mux, "POST", "/api/diagnostic/topo-ext-gate/ping", `{"src":"pc1","dst":"8.8.8.8","count":1}`)
	if respPing.code != http.StatusForbidden {
		t.Fatalf("external ping (default): want 403, got %d %s", respPing.code, respPing.body)
	}

	// 拓扑外 IP 的 traceroute 必须 403。
	respTrace := doRaw(mux, "POST", "/api/diagnostic/topo-ext-gate/traceroute", `{"src":"pc1","dst":"8.8.8.8"}`)
	if respTrace.code != http.StatusForbidden {
		t.Fatalf("external traceroute (default): want 403, got %d %s", respTrace.code, respTrace.body)
	}

	// 公网域名的 DNS 解析必须 403。
	respDNS := doRaw(mux, "POST", "/api/diagnostic/topo-ext-gate/dns", `{"src":"pc1","domain":"example.com"}`)
	if respDNS.code != http.StatusForbidden {
		t.Fatalf("external dns (default): want 403, got %d %s", respDNS.code, respDNS.body)
	}

	// 对照：拓扑内目标（设备 ID）应正常放行（不被误伤）。
	respInternal := doRaw(mux, "POST", "/api/diagnostic/topo-ext-gate/ping", `{"src":"pc1","dst":"pc2","count":1}`)
	if respInternal.code != http.StatusOK {
		t.Fatalf("internal ping (topology device) should be allowed, got %d %s", respInternal.code, respInternal.body)
	}
}
