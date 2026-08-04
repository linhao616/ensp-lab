//go:build !(linux && gont)

// Package sim: 非 gont 构建（Windows/macOS，或不含 gont tag 的 Linux 构建）
// 使用 ns-x 纯 Go 事件驱动仿真，不触及真实协议栈，因此 engine_mode 标记为 "lite"。
package sim

// EngineModeName 返回当前构建的引擎能力级别。
// 注意：包内已存在 EngineMode 类型（标识 nsx/gont 后端），为避免命名冲突，
// 此处函数命名为 EngineModeName。非 gont 构建（ns-x 仿真子集）= lite。
func EngineModeName() string {
	return "lite"
}
