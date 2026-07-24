// system_handlers.go 提供基础系统信息端点。
//
//   - GET /health  返回服务健康状态、平台与当前 engine 数量
//   - GET /version 返回构建版本、构建时间、状态
//
// 这两个端点是排查服务是否正常启动的最快入口。
package api

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

func (r *Router) health(c *gin.Context) {
	r.engMu.Lock()
	engineCount := len(r.engines)
	r.engMu.Unlock()

	status := gin.H{
		"status":       "ok",
		"platform":     runtime.GOOS,
		"engine_count": engineCount,
		"timestamp":    time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, status)
}

func (r *Router) version(c *gin.Context) {
	r.engMu.Lock()
	engineCount := len(r.engines)
	r.engMu.Unlock()

	info := gin.H{
		"version":      version,
		"build_time":   buildTime,
		"status":       "ok",
		"platform":     runtime.GOOS,
		"engine_count": engineCount,
		"timestamp":    time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, info)
}
