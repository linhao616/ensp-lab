//go:build !windows && !linux

package metrics

import "time"

// processCPU 在非 Windows/Linux 平台无法获取进程 CPU 时间，返回 0。
func processCPU() (cpuNs int64, wallNs int64) {
	return 0, time.Now().UnixNano()
}
