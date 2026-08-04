//go:build integration

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ensp-lab/internal/storage"
	"ensp-lab/internal/testutil"
	"ensp-lab/internal/topology"
)

func TestAPIEndToEnd(t *testing.T) {
	t.Parallel()

	testutil.CheckNetNSLimit(t)
	testutil.CleanupStaleNetNS(t)

	rm := testutil.NewResourceMonitor(t)
	rm.Start()
	defer rm.Stop()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	t.Cleanup(func() {
		runtime.GC()
	})

	createBody := []byte(`{
		"name": "TestTopo-E2E",
		"nodes": [
			{"id": "h1", "type": "pc"},
			{"id": "h2", "type": "pc"}
		],
		"links": [
			{"source_device": "h1", "source_port": "eth0", "target_device": "h2", "target_port": "eth0"}
		]
	}`)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create topology: expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("Failed to unmarshal create response: %v", err)
	}

	topoID := createResp["id"].(string)
	if topoID == "" {
		t.Fatal("Topology ID is empty")
	}

	t.Logf("Created topology: %s", topoID)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/topology/"+topoID+"/ping?src=h1&dst=h2", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Ping: expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var pingResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &pingResp); err != nil {
		t.Fatalf("Failed to unmarshal ping response: %v", err)
	}

	t.Logf("Ping result: sent=%v, received=%v", pingResp["sent"], pingResp["received"])

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/topologies/"+topoID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete topology: expected status 204, got %d: %s", w.Code, w.Body.String())
	}

	t.Logf("Deleted topology: %s", topoID)
}

func TestAPIConcurrentTopologyOperations(t *testing.T) {
	t.Parallel()

	testutil.CheckNetNSLimit(t)

	rm := testutil.NewResourceMonitor(t)
	rm.Start()
	defer rm.Stop()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	t.Cleanup(func() {
		runtime.GC()
	})

	const numGoroutines = 2
	const numTopologiesPerGoroutine = 1

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numTopologiesPerGoroutine; j++ {
				topoName := fmt.Sprintf("ConcurrentTopo-%d-%d", id, j)
				createBody := []byte(fmt.Sprintf(`{
					"name": "%s",
					"nodes": [
						{"id": "h1", "type": "pc"},
						{"id": "h2", "type": "pc"}
					],
					"links": [
						{"source_device": "h1", "source_port": "eth0", "target_device": "h2", "target_port": "eth0"}
					]
				}`, topoName))

				w := httptest.NewRecorder()
				req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader(createBody))
				req.Header.Set("Content-Type", "application/json")
				r.ServeHTTP(w, req)

				if w.Code != http.StatusCreated {
					t.Errorf("Goroutine %d, Topo %d: create failed, status %d: %s", id, j, w.Code, w.Body.String())
					continue
				}

				var createResp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
					t.Errorf("Goroutine %d, Topo %d: failed to unmarshal create response: %v", id, j, err)
					continue
				}

				topoID := createResp["id"].(string)

				w = httptest.NewRecorder()
				req, _ = http.NewRequest(http.MethodDelete, "/api/topologies/"+topoID, nil)
				r.ServeHTTP(w, req)

				if w.Code != http.StatusNoContent {
					t.Errorf("Goroutine %d, Topo %d: delete failed, status %d: %s", id, j, w.Code, w.Body.String())
				}
			}
		}(i)
	}

	wg.Wait()

	topos, err := store.ListTopologies()
	if err != nil {
		t.Fatalf("Failed to list topologies: %v", err)
	}
	if len(topos) != 0 {
		t.Errorf("Expected 0 topologies after cleanup, got %d", len(topos))
	}
}

func TestFileStorageReadWrite(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "ensp-test-storage")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := storage.NewFileStorage(tmpDir)

	testTopo := topology.NewTopology("test-file-topo", "Test File Storage")

	h1 := &topology.Device{ID: "h1", Name: "Host1", Type: topology.DevicePC}
	h1.InitializeDefaults()
	testTopo.AddDevice(h1)

	h2 := &topology.Device{ID: "h2", Name: "Host2", Type: topology.DevicePC}
	h2.InitializeDefaults()
	testTopo.AddDevice(h2)

	if err := store.CreateTopology(testTopo); err != nil {
		t.Fatalf("Failed to create topology: %v", err)
	}

	loadedTopo, err := store.GetTopology("test-file-topo")
	if err != nil {
		t.Fatalf("Failed to get topology: %v", err)
	}
	if loadedTopo == nil {
		t.Fatal("Topology is nil after load")
	}

	if loadedTopo.Name != testTopo.Name {
		t.Errorf("Name mismatch: expected %q, got %q", testTopo.Name, loadedTopo.Name)
	}
	if loadedTopo.DeviceCount() != testTopo.DeviceCount() {
		t.Errorf("Device count mismatch: expected %d, got %d", testTopo.DeviceCount(), loadedTopo.DeviceCount())
	}

	loadedTopo.Name = "Updated name"
	if err := store.UpdateTopology(loadedTopo); err != nil {
		t.Fatalf("Failed to update topology: %v", err)
	}

	reloadedTopo, err := store.GetTopology("test-file-topo")
	if err != nil {
		t.Fatalf("Failed to get updated topology: %v", err)
	}
	if reloadedTopo.Name != "Updated name" {
		t.Errorf("Name not updated: expected %q, got %q", "Updated name", reloadedTopo.Name)
	}

	if err := store.DeleteTopology("test-file-topo"); err != nil {
		t.Fatalf("Failed to delete topology: %v", err)
	}

	_, err = store.GetTopology("test-file-topo")
	if err != nil {
		t.Fatalf("Unexpected error getting deleted topology: %v", err)
	}

	filePath := filepath.Join(tmpDir, "test-file-topo.json")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("Topology file should be deleted, but exists: %s", filePath)
	}
}

func TestFileStorageConcurrentAccess(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "ensp-test-concurrent")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := storage.NewFileStorage(tmpDir)

	const numGoroutines = 3
	const numTopologies = 2
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numTopologies; j++ {
				topoID := fmt.Sprintf("concurrent-%d-%d", id, j)
				topo := topology.NewTopology(topoID, fmt.Sprintf("Concurrent %d-%d", id, j))
				h1 := &topology.Device{ID: fmt.Sprintf("h1-%d-%d", id, j), Name: fmt.Sprintf("Host1-%d-%d", id, j), Type: topology.DevicePC}
				h1.InitializeDefaults()
				topo.AddDevice(h1)

				if err := store.CreateTopology(topo); err != nil {
					t.Errorf("Goroutine %d, Topo %d: create failed: %v", id, j, err)
					continue
				}

				time.Sleep(time.Millisecond * 5)

				_, err := store.GetTopology(topoID)
				if err != nil {
					t.Errorf("Goroutine %d, Topo %d: get failed: %v", id, j, err)
				}

				time.Sleep(time.Millisecond * 5)

				if err := store.DeleteTopology(topoID); err != nil {
					t.Errorf("Goroutine %d, Topo %d: delete failed: %v", id, j, err)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestAPITopologyNotFound(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/topologies/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/topologies/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}
}

func TestAPIInvalidRequests(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader([]byte(`{invalid json}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid JSON, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/topology/test/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for missing src/dst, got %d", w.Code)
	}
}

func TestAPISimStatus(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/sim/status", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("Failed to unmarshal status: %v", err)
	}

	t.Logf("Engine mode: %v", status["mode"])
}

// TestCLIConcurrentSameDevice 验证「同一设备的并发 CLI 串行执行」：
// 8 个 goroutine 对同一设备各发 5 条 CLI 命令（共 40 条），结束后再发
// display history-command 校验历史恰好 40 条、无丢失。本测试需在
// `go test -race` 下运行，才能真正验证 per-device 锁消除了数据竞争。
func TestCLIConcurrentSameDevice(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryStorage()
	var staticFS fs.FS
	r := NewRouter(store, staticFS, ServerConfig{})

	// 建一个含 router 设备的拓扑（createTopologySimple 接受 nodes/links）。
	createBody := []byte(`{
		"name": "ConcurrentCLI",
		"nodes": [{"id": "r1", "type": "router"}],
		"links": []
	}`)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create topology: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var createResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	topoID := createResp["id"].(string)

	const goroutines = 8
	const cmdsPerGoroutine = 5
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines*cmdsPerGoroutine)

	postCLI := func(command string) {
		body := []byte(fmt.Sprintf(`{"command":%q}`, command))
		rw := httptest.NewRecorder()
		q, _ := http.NewRequest(http.MethodPost,
			"/api/topologies/"+topoID+"/devices/r1/cli", bytes.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rw, q)
		if rw.Code != http.StatusOK {
			errCh <- fmt.Errorf("cli %q: status %d: %s", command, rw.Code, rw.Body.String())
		}
	}

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < cmdsPerGoroutine; j++ {
				postCLI("display version")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// 并发结束后查询历史，应恰好 40 条（8×5），且无重复丢失。
	rw := httptest.NewRecorder()
	q, _ := http.NewRequest(http.MethodPost,
		"/api/topologies/"+topoID+"/devices/r1/cli",
		bytes.NewReader([]byte(`{"command":"display history-command 1000"}`)))
	q.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rw, q)
	if rw.Code != http.StatusOK {
		t.Fatalf("history-command: status %d: %s", rw.Code, rw.Body.String())
	}
	var histResp map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &histResp); err != nil {
		t.Fatalf("unmarshal history resp: %v", err)
	}
	output, _ := histResp["output"].(string)
	got := strings.Count(output, "display version")
	want := goroutines * cmdsPerGoroutine
	if got != want {
		t.Errorf("history entries: got %d, want %d (some commands were lost under concurrency)", got, want)
	}
}

