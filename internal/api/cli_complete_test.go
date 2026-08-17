package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"ensp-lab/internal/cli"
	"ensp-lab/internal/storage"
)

// TestCompleteCLIEndpoint 验证 /cli/complete 端点：路由可达、委托 cli.Complete、
// 候选随视图变化、且零副作用（不改动设备会话视图，AC4）。
func TestCompleteCLIEndpoint(t *testing.T) {
	store := storage.NewMemoryStorage()
	ar, mux := newTestRouter(store)
	mux.HandleFunc("POST /api/topologies/{id}/devices/{deviceId}/cli/complete", func(w http.ResponseWriter, req *http.Request) {
		ar.completeCLI(c(w, req))
	})

	topoID := doJSON(t, mux, "POST", "/api/topology", `{
		"name": "complete-test",
		"nodes": [{"id": "r1", "type": "router"}]
	}`).id
	if topoID == "" {
		t.Fatal("create topology failed")
	}
	devPath := "/api/topologies/" + topoID + "/devices/r1/cli/complete"

	// dis ipv -> 注册表驱动补全出 ipv6
	rec := doRaw(mux, "POST", devPath, `{"view":"user","sub":"","input":"dis ipv"}`)
	if rec.code != http.StatusOK {
		t.Fatalf("complete status = %d, body=%s", rec.code, rec.body)
	}
	var resp struct {
		Candidates []string `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(rec.body), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.body)
	}
	if !containsStr(resp.Candidates, "ipv6") {
		t.Fatalf("dis ipv should complete to ipv6, got %v", resp.Candidates)
	}

	// system 视图下 inter -> interface
	rec2 := doRaw(mux, "POST", devPath, `{"view":"system","sub":"","input":"inter"}`)
	var resp2 struct {
		Candidates []string `json:"candidates"`
	}
	json.Unmarshal([]byte(rec2.body), &resp2)
	if !containsStr(resp2.Candidates, "interface") {
		t.Fatalf("system view 'inter' should complete to 'interface', got %v", resp2.Candidates)
	}

	// 零副作用：请求携带 view=system 不应改写共享会话状态（completeCLI 还原视图）。
	if st, ok := ar.cliStates["r1"]; ok {
		if st.CurrentView != cli.ViewUser {
			t.Fatalf("completeCLI mutated shared view to %q, expected %q (side-effect violation)", st.CurrentView, cli.ViewUser)
		}
	}
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
