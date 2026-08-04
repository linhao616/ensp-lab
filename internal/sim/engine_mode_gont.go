//go:build linux && gont

// Package sim: 在启用 gont 的 Linux 构建中，引擎运行于真实网络命名空间，
// 使用真实协议栈收发报文，因此 engine_mode 标记为 "full"。
package sim

// EngineModeName 返回当前构建的引擎能力级别。
// 注意：包内已存在 EngineMode 类型（标识 nsx/gont 后端），为避免命名冲突，
// 此处函数命名为 EngineModeName。启用 gont 的 Linux 构建 = 真实引擎（full）。
func EngineModeName() string {
	return "full"
}
