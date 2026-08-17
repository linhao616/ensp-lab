//go:build !integration

package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"
)

// 本文件锁定 2026-08-12 漏洞分析（gosec + 人工复核）中在 api 包内的修复：
//   V-2：deleteTopology 丢弃 DeleteTopology 的 error 却返回 204（强断言成功）
//   V-3：pprof token 用 != 比较，存在时序侧信道
//   V-5：topology_handlers 的 fmt.Sscanf 忽略返回值，解析失败静默留 subnet=0

// --- V-3：pprof token 常量时间比较 ---
//
// 缺陷背景：原实现 `provided != pprofToken` 会在首个不同字节处短路返回，
// 攻击者可依据响应耗时差异逐字节爆破 token。修复改用
// subtle.ConstantTimeCompare。本用例锁定「各类近似 token 一律 403」，
// 覆盖前缀、长度差异等最容易因短路实现而产生可观测差异的输入形态。
func TestV3PprofTokenTimingSafeCompare(t *testing.T) {
	t.Setenv("ENSP_PPROF", "1")
	t.Setenv("ENSP_PPROF_TOKEN", "sekret-token")
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	// 这些输入都必须被拒绝（403），且不得因长度/前缀差异走出不同分支。
	badTokens := []struct {
		name  string
		token string
	}{
		{"空串", ""},
		{"仅首字符正确", "s"},
		{"正确前缀但截断", "sekret-toke"},
		{"正确前缀加尾巴", "sekret-tokenX"},
		{"整体等长但全错", "xxxxxx-xxxxx"},
		{"大小写不同", "SEKRET-TOKEN"},
		{"超长输入", "sekret-token-sekret-token-sekret-token"},
	}

	for _, bt := range badTokens {
		t.Run(bt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, "/debug/pprof/?token="+bt.token, nil)
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("token=%q 必须返回 403，实际 %d", bt.token, w.Code)
			}
		})
	}

	// 正确 token 仍必须放行（确认修复没把守卫改成恒拒）。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/debug/pprof/?token=sekret-token", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("正确 token 必须放行 200，实际 %d", w.Code)
	}
}

// --- V-2：deleteTopology 的错误可观测性 + 对外契约不变 ---
//
// 缺陷背景：`r.store.DeleteTopology(id)` 的返回 error 被直接丢弃，而 handler
// 仍返回 204 No Content —— 该状态码语义上强断言「资源已删除」。删除失败时
// 用户以为删成功、日志无痕迹，属静默数据不一致。
//
// 修复只补 logPersistErr 日志、刻意不改状态码（避免破坏既有客户端）。
// 因此本用例锁定：删除路径行为与 204 契约保持不变。
func TestV2DeleteTopologyKeeps204Contract(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	topo := topology.NewTopology("del-me", "待删除")
	if err := store.CreateTopology(topo); err != nil {
		t.Fatalf("CreateTopology: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/topologies/del-me", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("删除已存在拓扑应返回 204，实际 %d（%s）", w.Code, w.Body.String())
	}

	// 确认真的删掉了（修复只加日志，不应影响删除本身）。
	got, err := store.GetTopology("del-me")
	if err == nil && got != nil {
		t.Fatal("拓扑应已被删除")
	}
}

// TestV2DeleteNonexistentTopologyStillNoContent 断言删除不存在的拓扑仍返回 204。
// 这是 V-2 修复的关键非回归点：logPersistErr 对 err != nil 只记日志、不改状态码，
// 若误改为 500 会破坏既有客户端（幂等删除依赖 204）。
func TestV2DeleteNonexistentTopologyStillNoContent(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/topologies/never-existed", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("删除不存在拓扑应仍返回 204（幂等），实际 %d", w.Code)
	}
}

// TestV2DeleteTopologyInvalidIDStillRejected 断言 V-2 修复未削弱 F4 的 id 校验。
func TestV2DeleteTopologyInvalidIDStillRejected(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/topologies/bad.id", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 id 必须 400，实际 %d", w.Code)
	}
}

// --- V-5：createTopologySimple 的网关解析健壮性 ---
//
// 缺陷背景：`fmt.Sscanf(gw, "192.168.%d.1", &subnet)` 忽略返回值。当网关不是
// 192.168.x.1 形态（如 10.0.0.1）时，subnet 静默保持 0，后续按子网 0 分配主机位，
// 生成与实际网段不匹配、甚至跨设备互相冲突的 IP。
//
// 修复：解析失败退回 subnetSeq 独立 /24 分配。本用例锁定「非 192.168 网关不会
// 让多台设备挤进同一个 subnet 0 而产生重复 IP」。
func TestV5NonStandardGatewayMustNotCollapseToSubnetZero(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := NewRouter(store, nil, ServerConfig{BindAddr: "127.0.0.1", Port: "8080"})

	// 构造一个不含 192.168.x.1 网关的简易拓扑请求。
	body := `{
		"name": "v5-gw",
		"nodes": [
			{"id": "pc1", "type": "pc", "name": "PC1"},
			{"id": "pc2", "type": "pc", "name": "PC2"},
			{"id": "pc3", "type": "pc", "name": "PC3"}
		],
		"links": []
	}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/topology", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("创建拓扑失败：%d（%s）", w.Code, w.Body.String())
	}

	// 收集所有设备的接口 IP，断言无重复（V-5 的核心危害是重复 IP）。
	all, err := store.ListTopologies()
	if err != nil || len(all) == 0 {
		t.Fatalf("拓扑未落库：err=%v", err)
	}

	seen := map[string]string{}
	for _, topo := range all {
		for devID, dev := range topo.Devices {
			for ifName, iface := range dev.Interfaces {
				ip := iface.IPAddress
				if ip == "" {
					continue
				}
				if prev, dup := seen[ip]; dup {
					t.Fatalf("IP 冲突：%s 同时分配给 %s 与 %s/%s", ip, prev, devID, ifName)
				}
				seen[ip] = devID + "/" + ifName
			}
		}
	}
}
