// errors.go 提供 API 层统一的安全错误响应辅助。
//
// 安全目标（F7）：所有 5xx 及存储派生 404 的内部错误详情（含文件路径、
// 堆栈线索等）只经 internal/logging 记入服务端日志，绝不以原文回显给客户端；
// 客户端仅收到泛化文案，避免内部信息泄露辅助攻击者。
//
// 注意：400 类校验错误（validateXxx 产生）属面向用户的安全/格式提示，
// 仍直接返回具体文案（见各 handler），不在本约定内。
package api

import (
	"ensp-lab/internal/logging"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// clientError 向客户端返回泛化错误文案（不含内部细节），同时将完整错误
// 以 Error 级别记入日志（internal/logging），便于服务端排障。
//
//   - status: 透传给客户端的 HTTP 状态码（通常 5xx，或存储派生 404）。
//   - publicMsg: 仅含面向用户的泛化文案，不泄露内部实现细节。
//   - cause: 内部原始错误；为 nil 时仅返回 publicMsg，不写日志。
func clientError(c *gin.Context, status int, publicMsg string, cause error) {
	if cause != nil {
		logging.Error(publicMsg, zap.Error(cause))
	}
	c.JSON(status, gin.H{"error": publicMsg})
}
