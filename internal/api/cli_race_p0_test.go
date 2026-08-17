//go:build !integration

package api

// executeCLI 共享指针原地改写竞态（2026-08-12 发布前审计 P0-R1）回归测试。
//
// 背景：store.GetTopology 返回 FileStorage/MemoryStorage 内部的共享 *Topology
// 指针。修复前 executeCLI 直接在该共享对象上执行
//
//	device.ConfigData = ...
//	device.Interfaces["Ethernet0"] = ...   // 或 delete(...)
//
// 而 FileStorage.StartAutoSave 的后台 goroutine 每 interval（默认 5s）调用
// Flush → Topology.Clone → cloneDevice，会在 t.mu.RLock 下遍历同一个
// device.Interfaces map。两者无共同锁 ⇒ 并发 map 读写 ⇒ Go 运行时抛出
// "concurrent map read and map write" fatal error，且该 throw 无法被
// gin.Recovery() 捕获，进程直接退出。
//
// 本机无 gcc、跑不了 -race，故采用「逻辑并发断言」：不去制造真实竞态，而是
// 直接验证竞态的**必要条件已被消除** —— 即 executeCLI 不再触碰共享指针所指
// 对象，改为深拷贝后写入。只要该不变式成立，上述 fatal 路径即不可达。
//
// 断言口径：
//  1. 命令执行后，执行前抓取的共享 *Device 必须**零改动**（ConfigData 仍为
//     nil、Interfaces 仍为空）——证明没有原地写。
//  2. 同时 store 中的最新拓扑必须**已含改动**——证明持久化没被削弱（修复不是
//     简单地把写操作删掉）。
//  3. 拓扑级指针本身必须已被替换（新指针 != 旧指针）——证明走的是
//     Clone + UpdateTopology 通道，与其余写类 handler 约定一致。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ensp-lab/internal/storage"
	"ensp-lab/internal/topology"

	"github.com/gin-gonic/gin"
)

// invokeExecuteCLI 以显式 id/deviceId 参数直接调用 executeCLI，绕过路由解析。
func invokeExecuteCLI(r *Router, topoID, deviceID, command string) (int, string) {
	body, _ := json.Marshal(map[string]string{"command": command})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = req
	ctx.Params = gin.Params{
		{Key: "id", Value: topoID},
		{Key: "deviceId", Value: deviceID},
	}
	r.executeCLI(ctx)
	return w.Code, w.Body.String()
}

func TestP0R1CLIMustNotMutateSharedTopologyInPlace(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := newSecTestRouter(store)

	const topoID = "cli-race-p0"
	const devID = "pc1"

	topo := topology.NewTopology(topoID, "cli-race-p0")
	topo.Devices[devID] = &topology.Device{
		ID:         devID,
		Name:       "PC1",
		Type:       topology.DevicePC, // 终端设备：updateDeviceInterfaces 会写 Ethernet0
		Status:     topology.StatusRunning,
		Interfaces: map[string]*topology.Interface{},
	}
	if err := store.CreateTopology(topo); err != nil {
		t.Fatalf("CreateTopology failed: %v", err)
	}

	// 抓取共享指针 —— 等价于后台 AutoSave goroutine / 并发只读 handler 手上
	// 持有的那个对象。修复后它必须在整条 CLI 写路径中保持只读。
	sharedTopo, err := store.GetTopology(topoID)
	if err != nil || sharedTopo == nil {
		t.Fatalf("GetTopology failed: err=%v topo=%v", err, sharedTopo)
	}
	sharedDev, ok := sharedTopo.GetDevice(devID)
	if !ok {
		t.Fatalf("device %q not found in shared topology", devID)
	}
	if sharedDev.ConfigData != nil {
		t.Fatalf("precondition broken: shared device ConfigData should start nil")
	}
	if len(sharedDev.Interfaces) != 0 {
		t.Fatalf("precondition broken: shared device Interfaces should start empty, got %d",
			len(sharedDev.Interfaces))
	}

	// 执行一条最普通的命令。注意：即便是只读类命令也会记录历史并触发回写，
	// 因此这条路径足以覆盖 updateDeviceInterfaces 的 map 写入。
	code, respBody := invokeExecuteCLI(r, topoID, devID, "display version")
	if code != http.StatusOK {
		t.Fatalf("executeCLI status = %d, want 200; body=%s", code, respBody)
	}

	// --- 断言 1：共享对象零改动（竞态必要条件已消除） ---
	if sharedDev.ConfigData != nil {
		t.Errorf("REGRESSION: executeCLI 原地改写了共享 Device.ConfigData"+
			"（应改写深拷贝副本）；ConfigData=%+v", sharedDev.ConfigData)
	}
	if len(sharedDev.Interfaces) != 0 {
		t.Errorf("REGRESSION: executeCLI 原地写入了共享 Device.Interfaces map"+
			"（与后台 Flush/Clone 并发读同一 map 会触发不可恢复 fatal）；keys=%v",
			ifaceKeys(sharedDev.Interfaces))
	}

	// --- 断言 2：持久化未被削弱 ---
	latest, err := store.GetTopology(topoID)
	if err != nil || latest == nil {
		t.Fatalf("GetTopology after CLI failed: err=%v topo=%v", err, latest)
	}
	latestDev, ok := latest.GetDevice(devID)
	if !ok {
		t.Fatalf("device %q missing from persisted topology", devID)
	}
	if latestDev.ConfigData == nil {
		t.Errorf("CLI 状态未落盘：持久化后的 Device.ConfigData 仍为 nil")
	}
	if _, exists := latestDev.Interfaces["Ethernet0"]; !exists {
		t.Errorf("CLI 状态未落盘：持久化后的终端设备缺少 Ethernet0 接口，keys=%v",
			ifaceKeys(latestDev.Interfaces))
	}

	// --- 断言 3：走的是 Clone + UpdateTopology 通道（指针已替换） ---
	if latest == sharedTopo {
		t.Errorf("REGRESSION: 拓扑指针未被替换，说明 executeCLI 仍在共享对象上" +
			"原地改写，而非 Clone→UpdateTopology")
	}
	if latestDev == sharedDev {
		t.Errorf("REGRESSION: 设备指针未被替换，深拷贝未生效")
	}
}

// TestP0R1CLINonTerminalDeviceAlsoUsesClone 覆盖非终端设备分支：
// updateDeviceInterfaces 对交换机/路由器执行的是 delete(Interfaces, "Ethernet0")，
// 同样是 map 写操作，必须落在副本上而非共享对象。
func TestP0R1CLINonTerminalDeviceAlsoUsesClone(t *testing.T) {
	store := storage.NewMemoryStorage()
	r := newSecTestRouter(store)

	const topoID = "cli-race-sw"
	const devID = "sw1"

	topo := topology.NewTopology(topoID, "cli-race-sw")
	topo.Devices[devID] = &topology.Device{
		ID:     devID,
		Name:   "SW1",
		Type:   topology.DeviceSwitch, // 非终端：会走 delete 分支
		Status: topology.StatusRunning,
		Interfaces: map[string]*topology.Interface{
			// 历史遗留的 Ethernet0，非终端设备应被清理（但只能清副本）
			"Ethernet0": {Name: "Ethernet0"},
		},
	}
	if err := store.CreateTopology(topo); err != nil {
		t.Fatalf("CreateTopology failed: %v", err)
	}

	sharedTopo, _ := store.GetTopology(topoID)
	sharedDev, ok := sharedTopo.GetDevice(devID)
	if !ok {
		t.Fatalf("device %q not found", devID)
	}

	code, respBody := invokeExecuteCLI(r, topoID, devID, "display version")
	if code != http.StatusOK {
		t.Fatalf("executeCLI status = %d, want 200; body=%s", code, respBody)
	}

	// 共享对象上的 Ethernet0 必须仍在 —— 证明 delete 落在副本上。
	if _, exists := sharedDev.Interfaces["Ethernet0"]; !exists {
		t.Errorf("REGRESSION: executeCLI 在共享 Device.Interfaces 上执行了 delete" +
			"（并发 map 写），应只改写深拷贝副本")
	}

	// 副本上应已清理。
	latest, _ := store.GetTopology(topoID)
	latestDev, ok := latest.GetDevice(devID)
	if !ok {
		t.Fatalf("device %q missing from persisted topology", devID)
	}
	if _, exists := latestDev.Interfaces["Ethernet0"]; exists {
		t.Errorf("非终端设备的历史遗留 Ethernet0 未被清理，keys=%v",
			ifaceKeys(latestDev.Interfaces))
	}
}

// TestP0R2RateLimiterEvictsExpiredWindows 锁死限流器惰性回收：
// 修复前 windows map 只增不删，被大量不同源 IP 访问时单调增长（内存泄漏 /
// 放大型 DoS 面）。修复后每 sweepEveryN 次调用清理一轮过期窗口。
// 注意：不能靠「把 window 设成 1ns」来制造过期 —— Windows 上 time.Now() 的
// 时钟粒度约 0.5~15ms，同一时钟刻度内 now.After(resetAt) 恒为 false，会让测试
// 变成不稳定的时序赌博。这里改为直接把 resetAt 回拨到过去，做确定性断言。
func TestP0R2RateLimiterEvictsExpiredWindows(t *testing.T) {
	rl := newRateLimiter(100, time.Minute)

	// 第一阶段：灌入远超 sweepEveryN 的不同源 IP。
	// n 取 sweepEveryN 的整数倍，使这一阶段结束时 sweepCount 正好归零，
	// 后续清理时机可精确预期。
	const n = sweepEveryN * 3
	for i := 0; i < n; i++ {
		if !rl.allow("10.0.0." + strconv.Itoa(i)) {
			t.Fatalf("allow(%d) 意外被限流：每个新 key 首次访问都应放行", i)
		}
	}

	rl.mu.Lock()
	filled := len(rl.windows)
	// 强制全部过期（等价于「窗口自然到期」，但不依赖真实时钟推进）。
	past := time.Now().Add(-time.Hour)
	for _, w := range rl.windows {
		w.resetAt = past
	}
	rl.mu.Unlock()

	if filled != n {
		t.Fatalf("precondition broken: 灌入 %d 个 key 后 windows=%d", n, filled)
	}

	// 第二阶段：再打 sweepEveryN 次，恰好触发一轮清理。
	for i := 0; i < sweepEveryN; i++ {
		rl.allow("probe")
	}

	rl.mu.Lock()
	size := len(rl.windows)
	rl.mu.Unlock()

	// 清理生效后，n 个过期条目应全部释放，仅剩 probe 自身。
	// 放宽到 4 以容忍实现细节，只要不是 O(n) 单调增长即算通过。
	if size > 4 {
		t.Errorf("REGRESSION: rateLimiter.windows 未回收过期条目，"+
			"灌入 %d 个 key 并全部置为过期后仍残留 %d（应 <= 4）", n, size)
	}

	// 回收不得影响限流语义：同一 key 在正常窗口内超限仍须被拒。
	rl2 := newRateLimiter(2, time.Minute)
	if !rl2.allow("1.2.3.4") || !rl2.allow("1.2.3.4") {
		t.Fatalf("前两次请求应放行")
	}
	if rl2.allow("1.2.3.4") {
		t.Errorf("第三次请求应被限流（limit=2），回收逻辑不应放宽限流判定")
	}
}

func ifaceKeys(m map[string]*topology.Interface) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
