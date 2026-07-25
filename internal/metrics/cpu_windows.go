//go:build windows

package metrics

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPU 返回本进程累计占用的 CPU 时间（纳秒）与当前墙钟时间（纳秒）。
// Windows 通过 GetProcessTimes 取得内核态+用户态时间，精度到 100ns。
func processCPU() (cpuNs int64, wallNs int64) {
	var creation, exit, kernel, user windows.Filetime
	h, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, time.Now().UnixNano()
	}
	_ = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	cpuNs = user.Nanoseconds() + kernel.Nanoseconds()
	wallNs = time.Now().UnixNano()
	return
}
