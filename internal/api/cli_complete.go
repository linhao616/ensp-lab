// cli_complete.go 提供 CLI Tab 补全端点（F11 修复：cli.Complete 后端早已就绪，
// 但 HTTP 端点此前从未接线，导致前端补全每次 404、cli_complete_test 编译不过）。
//
//   - POST /api/topologies/:id/devices/:deviceId/cli/complete
//
// 语义：纯只读、零副作用。补全候选完全由后端 cli.Complete 计算（设计 §3 / AC1~AC4），
// 前端不持有第二份命令字典，杜绝与 parser 漂移。为支持「在指定视图下补全」
// （如前端已知当前停在 system 视图），请求携带 view/sub，completeCLI 临时把共享
// CLIState 切换到该视图计算候选，算完即还原，绝不改写设备会话的真实视图（AC4）。
// 同一设备串行化（复用 executeCLI 的设备锁），避免与并发 executeCLI 争抢同一
// CLIState 的视图字段而导致竞态。
package api

import (
	"net/http"

	"ensp-lab/internal/cli"

	"github.com/gin-gonic/gin"
)

// cliCompleteRequest 是 /cli/complete 的请求体。
//   - View：补全时所处视图（"user"/"system"/...），映射到 cli.ViewType；空则沿用共享会话当前视图。
//   - Sub：视图子标识（如 AAA 方案名），可选；空则不强制切换。
//   - Input：当前已输入的命令行文本（含或不含结尾空格，由 cli.SplitCommandTokens 区分）。
type cliCompleteRequest struct {
	View  string `json:"view"`
	Sub   string `json:"sub"`
	Input string `json:"input"`
}

// completeCLI 计算 CLI 补全候选（只读、零副作用）。
func (r *Router) completeCLI(c *gin.Context) {
	id := c.Param("id")
	deviceId := c.Param("deviceId")

	// 同一设备串行化：与 executeCLI 共用设备锁，避免并发读写同一 CLIState 的视图字段。
	devMu := r.deviceCLIMutex(deviceId)
	devMu.Lock()
	defer devMu.Unlock()

	var req cliCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dt := r.lookupDeviceType(id, deviceId)
	state := r.getOrInitCLIState(id, deviceId, dt)

	// 临时切换到请求视图计算候选，算完还原，保证零副作用（AC4）。
	origView := state.CurrentView
	origSub := state.CurrentSub
	if req.View != "" {
		state.CurrentView = cli.ViewType(req.View)
	}
	if req.Sub != "" {
		state.CurrentSub = req.Sub
	}

	tokens := cli.SplitCommandTokens(req.Input)
	candidates := cli.Complete(state, tokens)

	// 还原共享会话视图，避免污染设备真实 CLI 会话（防御性，即便上面未切换也显式还原）。
	state.CurrentView = origView
	state.CurrentSub = origSub

	if candidates == nil {
		candidates = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"candidates": candidates,
		"view":       origView,
	})
}
