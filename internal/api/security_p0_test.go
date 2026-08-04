//go:build !integration

package api

// P0 安全加固（F4/F5/F7/F8/F10）回归测试。
//
// 这批测试覆盖本轮 P0 改动的安全门控：
//   - F4  validateTopoID 统一校验（getTopology / deleteTopology / exportTopology）
//   - F5  导出文件名净化（sanitizeForFilename + export Content-Disposition）
//   - F7  内部错误泛化响应（5xx 不回显内部细节；400 校验错误仍面向用户）
//   - F8  CORS AllowHeaders 去除 Authorization
//   - F10 pprof token 守卫（未带 token 403 / 带 token 200 / 关闭时 404）
//
// 风格沿用项目既有单元测试：直接用 gin.Context 调 handler（绕过 URL 解析以便注入
// 原始非法 :id），或通过 NewRouter + httptest 验证中间件栈挂载（CORS/F10 必须走
// 真实路由栈）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/protocol"
	"ensp-lab/internal/sim"
	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
	// 与 cmd/server/main.go 一致：blank import 使 net/http/pprof 的处理器注册到
	// DefaultServeMux，从而 F10 测试中 gin.WrapF(http.DefaultServeMux.ServeHTTP)
	// 能真正返回 pprof 页面（200）。不引入新依赖。
	_ "net/http/pprof"
)

// errTopologyStore 是仅 GetTopology 返回预设错误的 Storage 桩，用于稳定触发
// exportTopology / getQueueDepth 的 5xx 泛化路径（MemoryStorage 永不返回 error）。
type errTopologyStore struct {
	getErr error
}

func (s *errTopologyStore) GetTopology(id string) (*topology.Topology, error) {
	return nil, s.getErr
}
func (s *errTopologyStore) ListTopologies() ([]*topology.Topology, error) { return nil, nil }
func (s *errTopologyStore) CreateTopology(t *topology.Topology) error     { return nil }
func (s *errTopologyStore) UpdateTopology(t *topology.Topology) error     { return nil }
func (s *errTopologyStore) DeleteTopology(id string) error                { return nil }
func (s *errTopologyStore) AddPacketEvent(topologyID string, e *storage.PacketEventRecord) error {
	return nil
}
func (s *errTopologyStore) ListPacketEvents(topologyID string, limit int) ([]*storage.PacketEventRecord, error) {
	return nil, nil
}
func (s *errTopologyStore) ClearPacketEvents(topologyID string) error { return nil }
func (s *errTopologyStore) Flush() error                              { return nil }
func (s *errTopologyStore) StartAutoSave(ctx context.Context, interval time.Duration) {
}
func (s *errTopologyStore) StorageDir() string { return "" }

// newSecTestRouter 构造裸 *Router（不挂 gin 中间件），供直接调 handler 使用。
func newSecTestRouter(store storage.Storage) *Router {
	return &Router{
		store:      store,
		cliStates:  make(map[string]*cli.CLIState),
		cliLocks:   make(map[string]*sync.Mutex),
		protoSim:   protocol.NewProtocolSimulator(nil),
		engines:    make(map[string]sim.Engine),
		syncTimers: make(map[string]*time.Timer),
	}
}

// serveTopoHandler 以显式 id 参数直接调用拓扑级 :id handler，绕过 URL 解析，
// 便于把原始非法 id（含 .. / \ 控制字符）喂给 F4 守卫做边界验证。
func serveTopoHandler(handler func(*gin.Context), id string) (int, string, http.Header) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/x", nil)
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	handler(ctx)
	return w.Code, w.Body.String(), w.Header()
}

// --- F4：validateTopoID 统一校验守卫（handler 边界） ---

func TestF4ValidateTopoIDGuardHandlers(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := newSecTestRouter(store)

	// 各类非法 :id（含 .. / \ 控制字符 超长 特殊字符），三个拓扑级入口都应 400。
	illegal := []string{
		"../etc/passwd",
		"a/b",
		`a\b`,
		"a" + string(rune(0)) + "b",   // 控制字符 NUL
		"a" + string(rune(1)) + "b",   // 控制字符 SOH
		strings.Repeat("a", 100),      // 超长（>64）
		"bad.id",                      // 点
		"bad id",                      // 空格
		"bad$id",                      // 特殊字符
		"../../etc",                   // 多层穿越
	}
	for _, id := range illegal {
		if code, _, _ := serveTopoHandler(r.getTopology, id); code != http.StatusBadRequest {
			t.Errorf("getTopology id=%q: want 400, got %d", id, code)
		}
		if code, _, _ := serveTopoHandler(r.deleteTopology, id); code != http.StatusBadRequest {
			t.Errorf("deleteTopology id=%q: want 400, got %d", id, code)
		}
		if code, _, _ := serveTopoHandler(r.exportTopology, id); code != http.StatusBadRequest {
			t.Errorf("exportTopology id=%q: want 400, got %d", id, code)
		}
	}

	// 合法 id（不存在）不应被 400 拦截：get→404 / delete→204 / export→404。
	valid := []string{"validtopo1", "a-b_c1", "Topo_123", "abc123"}
	for _, id := range valid {
		if code, _, _ := serveTopoHandler(r.getTopology, id); code == http.StatusBadRequest {
			t.Errorf("getTopology valid id=%q: unexpected 400", id)
		}
		if code, _, _ := serveTopoHandler(r.deleteTopology, id); code == http.StatusBadRequest {
			t.Errorf("deleteTopology valid id=%q: unexpected 400", id)
		}
		if code, _, _ := serveTopoHandler(r.exportTopology, id); code == http.StatusBadRequest {
			t.Errorf("exportTopology valid id=%q: unexpected 400", id)
		}
	}
}

// TestF4ValidateTopoIDGuardViaRouter 验证 F4 守卫已真正挂到 gin 路由上
// （非法单段 :id 经真实路由到达 handler 仍返回 400）。
func TestF4ValidateTopoIDGuardViaRouter(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	cases := []string{"bad.id", "bad id", strings.Repeat("a", 100)}
	for _, id := range cases {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/topologies/"+id, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET /api/topologies/%q via router: want 400, got %d (%s)", id, w.Code, w.Body.String())
		}
	}
}

// --- F5：导出文件名净化 ---

func TestF5SanitizeForFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a/b\\c:d*e", "abcde"},
		{"Topo_1-2", "Topo_1-2"},
		{"!!!", ""},
		{"../etc", "etc"},
		{"with space", "withspace"},
		{"", ""},
	}
	for _, c := range cases {
		got := sanitizeForFilename(c.in)
		if got != c.want {
			t.Errorf("sanitizeForFilename(%q) = %q, want %q", c.in, got, c.want)
		}
		// 净化结果只允许 [A-Za-z0-9_-]。
		for _, r := range got {
			ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') || r == '_' || r == '-'
			if !ok {
				t.Errorf("sanitizeForFilename(%q) = %q contains illegal char %q", c.in, got, string(r))
			}
		}
	}
}

// TestF5ExportFilenameViaRouter 验证导出响应头 Content-Disposition 的文件名
// 仅含安全字符且被 %q 包裹（filename="<id>.json"）。
func TestF5ExportFilenameViaRouter(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	createBody := `{"name":"exp","nodes":[{"id":"h1","type":"pc"},{"id":"h2","type":"pc"}],"links":[{"source_device":"h1","source_port":"Ethernet0","target_device":"h2","target_port":"Ethernet0"}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader([]byte(createBody)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create topology: want 201, got %d %s", w.Code, w.Body.String())
	}
	var cr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cr); err != nil {
		t.Fatalf("decode create resp: %v", err)
	}
	topoID := cr.ID // generateID() 产出 16 位十六进制，全部为安全字符

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/topologies/"+topoID+"/export", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("export: want 200, got %d %s", w2.Code, w2.Body.String())
	}
	cd := w2.Header().Get("Content-Disposition")
	want := fmt.Sprintf("attachment; filename=%q", topoID+".json")
	if cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
	if !strings.HasPrefix(cd, "attachment; filename=\"") || !strings.HasSuffix(cd, "\"") {
		t.Errorf("Content-Disposition not properly %q-wrapped: %q", "%q", cd)
	}
}

// TestF5ExportEmptyIDFallback 验证空 topoID 经 sanitize 后为空时回退为
// "topology"（filename="topology.json"）。空 id 通过 F4 守卫（validateTopoID 允许空）。
func TestF5ExportEmptyIDFallback(t *testing.T) {
	store := storage.NewMemoryStorage()
	// MemoryStorage 允许存储空 id 拓扑（仅测试用桩语义）。
	emptyTopo := topology.NewTopology("", "empty")
	if err := store.CreateTopology(emptyTopo); err != nil {
		t.Fatalf("CreateTopology empty id: %v", err)
	}
	r := newSecTestRouter(store)
	code, _, hdr := serveTopoHandler(r.exportTopology, "")
	if code != http.StatusOK {
		t.Fatalf("export empty id: want 200, got %d", code)
	}
	cd := hdr.Get("Content-Disposition")
	if cd != `attachment; filename="topology.json"` {
		t.Errorf("Content-Disposition = %q, want %q", cd, `attachment; filename="topology.json"`)
	}
}

// --- F7：内部错误泛化响应 ---

func TestF7ErrorGeneralization(t *testing.T) {
	internalMsg := "/var/lib/ensp/topologies/secret.json: no such file or directory"
	store := &errTopologyStore{getErr: errors.New(internalMsg)}
	r := newSecTestRouter(store)

	// exportTopology 触发 5xx 路径 -> 泛化文案，不泄露内部细节。
	code, body, _ := serveTopoHandler(r.exportTopology, "validid")
	if code != http.StatusInternalServerError {
		t.Fatalf("exportTopology 5xx: want 500, got %d (%s)", code, body)
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode 500 resp: %v (body=%s)", err, body)
	}
	if resp.Error != "internal server error" {
		t.Errorf("exportTopology: want generalized msg, got %q", resp.Error)
	}
	if strings.Contains(body, "var/lib") || strings.Contains(body, "no such file") ||
		strings.Contains(body, "secret.json") {
		t.Errorf("exportTopology leaked internal detail: %s", body)
	}

	// getQueueDepth 同样走 clientError 泛化（5xx）。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/sim/queue-depth?topology=validid", nil)
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	r.getQueueDepth(ctx)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("getQueueDepth 5xx: want 500, got %d (%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "var/lib") {
		t.Errorf("getQueueDepth leaked internal detail: %s", w.Body.String())
	}

	// 400 校验错误仍回显面向用户的安全提示（未被泛化）。
	code400, body400, _ := serveTopoHandler(r.exportTopology, "bad/id")
	if code400 != http.StatusBadRequest {
		t.Fatalf("exportTopology invalid id: want 400, got %d", code400)
	}
	if !strings.Contains(body400, "invalid topology id") {
		t.Errorf("400 should echo user-facing validation msg, got %q", body400)
	}
}

// --- F8：CORS AllowHeaders 去除 Authorization ---

func TestF8CORSNoAuthorization(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	// 预检 OPTIONS：带 Origin + Request-Method + Request-Headers(Authorization)。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodOptions, "/api/topologies", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")
	r.ServeHTTP(w, req)

	allow := w.Header().Get("Access-Control-Allow-Headers")
	if strings.Contains(allow, "Authorization") {
		t.Errorf("CORS Allow-Headers must NOT include Authorization, got %q", allow)
	}
	t.Logf("Access-Control-Allow-Headers (preflight) = %q", allow)

	// 带 Authorization 头的普通 GET：其头不应被列入允许列表。
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/topologies", nil)
	req2.Header.Set("Origin", "http://127.0.0.1:8080")
	req2.Header.Set("Authorization", "Bearer xyz")
	r.ServeHTTP(w2, req2)
	allow2 := w2.Header().Get("Access-Control-Allow-Headers")
	if strings.Contains(allow2, "Authorization") {
		t.Errorf("CORS Allow-Headers must NOT include Authorization on GET, got %q", allow2)
	}
}

// --- F10：pprof token 守卫 ---

func TestF10PprofTokenGuard(t *testing.T) {
	// 开启 pprof 并设定固定 token 以便稳定断言。
	t.Setenv("ENSP_PPROF", "1")
	t.Setenv("ENSP_PPROF_TOKEN", "sekret-token")
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	// 无 token -> 403。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("pprof without token: want 403, got %d (%s)", w.Code, w.Body.String())
	}

	// 正确 ?token= -> 200。
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/debug/pprof/?token=sekret-token", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("pprof with ?token: want 200, got %d (%s)", w2.Code, w2.Body.String())
	}

	// 正确 X-Pprof-Token 头 -> 200。
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req3.Header.Set("X-Pprof-Token", "sekret-token")
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("pprof with X-Pprof-Token header: want 200, got %d (%s)", w3.Code, w3.Body.String())
	}

	// 错误 token -> 403。
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest(http.MethodGet, "/debug/pprof/?token=wrong", nil)
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusForbidden {
		t.Errorf("pprof with wrong token: want 403, got %d (%s)", w4.Code, w4.Body.String())
	}
}

// TestF10PprofDisabledByDefault 验证默认（ENSP_PPROF 为空）时 pprof 路由不挂载 -> 404。
func TestF10PprofDisabledByDefault(t *testing.T) {
	t.Setenv("ENSP_PPROF", "")
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("pprof disabled: want 404 (route not mounted), got %d (%s)", w.Code, w.Body.String())
	}
}
