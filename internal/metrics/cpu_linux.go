//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// processCPU 返回本进程累计占用的 CPU 时间（纳秒）与当前墙钟时间（纳秒）。
// Linux 读取 /proc/self/stat 的 utime+stime 字段（第 14、15 个以空格分隔的字段，
// 0-based 索引 13、14）。该字段以 USER_HZ（固定 100）为单位，因此乘以 1e7 转为纳秒。
func processCPU() (cpuNs int64, wallNs int64) {
	data, err := os.ReadFile("/proc/self/stat")
	if err == nil {
		s := string(data)
		// comm 字段可能含空格/括号，使用最后一个 ')' 之后的内容解析，更稳健。
		if idx := strings.LastIndex(s, ")"); idx >= 0 {
			fields := strings.Fields(s[idx+1:])
			// 0-based：state=0, ppid=1, ... utime=12, stime=13
			if len(fields) > 13 {
				if ut, e1 := strconv.ParseInt(fields[12], 10, 64); e1 == nil {
					if st, e2 := strconv.ParseInt(fields[13], 10, 64); e2 == nil {
						// USER_HZ = 100 → 1 tick = 1e7 ns
						cpuNs = (ut + st) * (1e9 / 100)
					}
				}
			}
		}
	}
	wallNs = time.Now().UnixNano()
	return
}
