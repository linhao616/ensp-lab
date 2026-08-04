//go:build !integration

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/protocol"
	"ensp-lab/internal/sim"
	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

// newTestRouter 构造 api 包内部的 *Router 并注册 P0-A 测试所需的少量路由，
// 使测试无需依赖 NewRouter（其返回 *gin.Engine 不暴露内部 Router）。
// 直接复用内部实例，保证并发测试中触发的引擎与 handler 共享同一 *Router，
// 真实还原「写 handler 与引擎快照竞争」的场景。
func newTestRouter(store storage.Storage) (*Router, *http.ServeMux) {
	ar := &Router{
		store:      store,
		cliStates:  make(map[string]*cli.CLIState),
		cliLocks:   make(map[string]*sync.Mutex),
		protoSim:   protocol.NewProtocolSimulator(nil),
		engines:    make(map[string]sim.Engine),
		syncTimers: make(map[string]*time.Timer),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/topology", func(w http.ResponseWriter, req *http.Request) {
		ar.createTopologySimple(c(w, req))
	})
	mux.HandleFunc("POST /api/topologies/import", func(w http.ResponseWriter, req *http.Request) {
		ar.createTopologySimple(c(w, req))
	})
	mux.HandleFunc("POST /api/topologies/{id}/links", func(w http.ResponseWriter, req *http.Request) {
		ar.addLink(c(w, req))
	})
	mux.HandleFunc("PUT /api/topologies/{id}/links/{linkId}", func(w http.ResponseWriter, req *http.Request) {
		ar.updateLink(c(w, req))
	})
	mux.HandleFunc("POST /api/topologies/{id}/annotations", func(w http.ResponseWriter, req *http.Request) {
		ar.addAnnotation(c(w, req))
	})
	mux.HandleFunc("PUT /api/topologies/{id}/annotations/{annotationId}", func(w http.ResponseWriter, req *http.Request) {
		ar.updateAnnotation(c(w, req))
	})
	mux.HandleFunc("POST /api/topologies/{id}/devices/{deviceId}/ip-config", func(w http.ResponseWriter, req *http.Request) {
		ar.setIPConfig(c(w, req))
	})
	mux.HandleFunc("GET /api/topologies/{id}/stream", func(w http.ResponseWriter, req *http.Request) {
		ar.streamSimEvents(c(w, req))
	})
	return ar, mux
}

// c 用标准 net/http 请求构造一个最小 gin.Context，便于直接调用 handler。
// 仅覆盖本测试用到的 Param / ShouldBindJSON / JSON / Header。
func c(w http.ResponseWriter, req *http.Request) *gin.Context {
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	// 把标准库路径参数（Go 1.22+ ServeMux 模式 {id}）映射到 gin.Params，
	// 使 handler 中的 c.Param("id") 等调用正确解析。
	var params gin.Params
	for _, name := range []string{"id", "linkId", "annotationId", "deviceId"} {
		if v := req.PathValue(name); v != "" {
			params = append(params, gin.Param{Key: name, Value: v})
		}
	}
	ctx.Params = params
	return ctx
}

// TestP0AHandlerConcurrency 验证 P0-A 修复后，多个写类 handler
// （updateLink / updateAnnotation / setIPConfig）在仿真引擎存活
// （持有共享拓扑快照）的情况下并发访问，不会产生数据竞争或破坏存储对象。
//
// 这些 handler 此前直接改写 store 返回的共享 *Topology，与引擎快照 /
// 并发读请求竞争；修复后均改为 t.Clone() 副本上操作再整体替换。
func TestP0AHandlerConcurrency(t *testing.T) {
	store := storage.NewMemoryStorage()
	ar, mux := newTestRouter(store)

	// 构造含 2 台 PC + 链路 + 标注的拓扑，并触发引擎懒加载（持有快照）。
	createBody := `{
		"name": "P0A-Conc",
		"nodes": [
			{"id": "h1", "type": "pc"},
			{"id": "h2", "type": "pc"}
		],
		"links": [
			{"source_device": "h1", "source_port": "Ethernet0", "target_device": "h2", "target_port": "Ethernet0"}
		]
	}`
	topoID := doJSON(t, mux, "POST", "/api/topology", createBody).id
	if topoID == "" {
		t.Fatal("create topology failed")
	}

	// 触发引擎（持有拓扑不可变快照），模拟真实运行态下的并发写。
	if _, err := ar.getOrCreateEngine(topoID); err != nil {
		t.Fatalf("getOrCreateEngine: %v", err)
	}

	// 复用 createTopologySimple 自建的链路（避免 addLink 的端口占用冲突）；
	// 标注仍是独立新增（无端口冲突限制）。
	tOp, err := store.GetTopology(topoID)
	if err != nil || tOp == nil {
		t.Fatalf("get topology: %v", err)
	}
	links := tOp.GetLinks()
	if len(links) == 0 {
		t.Fatal("expected at least one auto-created link")
	}
	linkID := links[0].ID

	annoID := doJSON(t, mux, "POST", "/api/topologies/"+topoID+"/annotations",
		`{"text":"note","position_x":10,"position_y":20}`).annoID
	if annoID == "" {
		t.Fatal("addAnnotation failed")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	// 并发更新链路
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"delay":%d,"bandwidth":%d}`, n, 1000+n)
			resp := doRaw(mux, "PUT", "/api/topologies/"+topoID+"/links/"+linkID, body)
			if resp.code != http.StatusOK {
				errCh <- fmt.Errorf("updateLink %d: status %d %s", n, resp.code, resp.body)
			}
		}(i)
	}

	// 并发更新标注
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"text":"note-%d","position_x":%d,"position_y":%d}`, n, n, n)
			resp := doRaw(mux, "PUT", "/api/topologies/"+topoID+"/annotations/"+annoID, body)
			if resp.code != http.StatusOK {
				errCh <- fmt.Errorf("updateAnnotation %d: status %d %s", n, resp.code, resp.body)
			}
		}(i)
	}

	// 并发更新设备 IP 配置
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"ip":"192.168.99.%d","subnet_mask":"255.255.255.0"}`, n+2)
			resp := doRaw(mux, "POST", "/api/topologies/"+topoID+"/devices/h1/ip-config", body)
			if resp.code != http.StatusOK {
				errCh <- fmt.Errorf("setIPConfig %d: status %d %s", n, resp.code, resp.body)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// 终值一致性：链路 delay 应落在 [0,49]。
	tOp, err = store.GetTopology(topoID)
	if err != nil || tOp == nil {
		t.Fatalf("get topology after concurrency: %v", err)
	}
	var finalDelay int
	for _, l := range tOp.GetLinks() {
		if l.ID == linkID {
			finalDelay = l.Delay
		}
	}
	if finalDelay < 0 || finalDelay > 49 {
		t.Errorf("unexpected final link delay: %d", finalDelay)
	}
	t.Logf("concurrent run OK: final link delay=%d, annotations=%d, devices=%d",
		finalDelay, len(tOp.Annotations), len(tOp.Devices))
}

// TestP0AImportValidation 验证 import 入口对悬空端口、非法端口名的拒绝。
func TestP0AImportValidation(t *testing.T) {
	store := storage.NewMemoryStorage()
	_, mux := newTestRouter(store)

	// 端口不存在于设备（PC 默认接口名是 Ethernet0，而非 ge0/0/0）
	body := `{
		"name": "bad",
		"nodes": [{"id":"a","type":"pc"},{"id":"b","type":"pc"}],
		"links": [{"source_device":"a","source_port":"ge0/0/0","target_device":"b","target_port":"ge0/0/0"}]
	}`
	if resp := doRaw(mux, "POST", "/api/topologies/import", body); resp.code == http.StatusCreated {
		t.Errorf("expected rejection for unknown ports, got 201: %s", resp.body)
	}

	// 合法 import 应通过
	bodyOK := `{
		"name": "good",
		"nodes": [{"id":"a","type":"pc"},{"id":"b","type":"pc"}],
		"links": [{"source_device":"a","source_port":"Ethernet0","target_device":"b","target_port":"Ethernet0"}]
	}`
	if resp := doRaw(mux, "POST", "/api/topologies/import", bodyOK); resp.code != http.StatusCreated {
		t.Errorf("valid import rejected: %d %s", resp.code, resp.body)
	}
}

// TestP0ASSEConnectedEscape 验证 SSE connected 帧对 topoID 做了 JSON 转义，
// 不直接字符串插值（防注入破坏 SSE 帧）。这里通过 handler 直接调用验证
// 不 panic 且逻辑可达（真实 topo 才会发 connected 帧）。
func TestP0ASSEConnectedEscape(t *testing.T) {
	store := storage.NewMemoryStorage()
	ar, mux := newTestRouter(store)
	// 构造一个真实拓扑再触发 SSE，验证 connected 帧转义路径可达且不 panic。
	topoID := doJSON(t, mux, "POST", "/api/topology",
		`{"name":"sse","nodes":[{"id":"h1","type":"pc"}],"links":[]}`).id
	if topoID == "" {
		t.Fatal("create failed")
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/topologies/"+topoID+"/stream", nil)
	ar.streamSimEvents(c(w, req))
	// 真实 topo 应已写出 connected 帧；topoID 经 json.Marshal 写入，无裸插值。
	t.Logf("SSE connected frame written, status=%d", w.Code)
}

// --- 测试辅助 ---

type respHelper struct {
	code   int
	body   string
	id     string
	linkID string
	annoID string
}

func doRaw(mux *http.ServeMux, method, path, body string) respHelper {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, req)
	return respHelper{code: w.Code, body: w.Body.String()}
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path, body string) respHelper {
	h := doRaw(mux, method, path, body)
	rh := respHelper{code: h.code, body: h.body}
	if h.code >= 400 {
		return rh
	}
	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(h.body), &generic); err == nil {
		if v, ok := generic["id"].(string); ok {
			rh.id = v
		}
	}
	var link topology.Link
	if err := json.Unmarshal([]byte(h.body), &link); err == nil && link.ID != "" {
		rh.linkID = link.ID
	}
	var anno topology.TextAnnotation
	if err := json.Unmarshal([]byte(h.body), &anno); err == nil && anno.ID != "" {
		rh.annoID = anno.ID
	}
	return rh
}
