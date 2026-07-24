package testutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type Stoppable interface {
	Stop()
}

const (
	maxNetNSCount = 50
	maxStaleAge   = 10 * time.Minute
)

type ResourceMonitor struct {
	t               *testing.T
	startMem        runtime.MemStats
	startGoroutines int
	stopCh          chan struct{}
	enabled         bool
}

func NewResourceMonitor(t *testing.T) *ResourceMonitor {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return &ResourceMonitor{
		t:               t,
		startMem:        mem,
		startGoroutines: runtime.NumGoroutine(),
		stopCh:          make(chan struct{}),
		enabled:         true,
	}
}

func (rm *ResourceMonitor) Start() {
	if !rm.enabled {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rm.logSnapshot()
			case <-rm.stopCh:
				return
			}
		}
	}()
}

func (rm *ResourceMonitor) Disable() {
	rm.enabled = false
}

func (rm *ResourceMonitor) Stop() {
	close(rm.stopCh)
	rm.logFinal()
}

func (rm *ResourceMonitor) logSnapshot() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	rm.t.Logf("[Resource Monitor] Goroutines: %d, Alloc: %.2f MB, HeapAlloc: %.2f MB, Sys: %.2f MB",
		runtime.NumGoroutine(),
		float64(mem.Alloc)/1024/1024,
		float64(mem.HeapAlloc)/1024/1024,
		float64(mem.Sys)/1024/1024,
	)
}

func (rm *ResourceMonitor) logFinal() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	allocDiff := int64(mem.Alloc) - int64(rm.startMem.Alloc)
	goroutineDiff := runtime.NumGoroutine() - rm.startGoroutines

	rm.t.Logf("[Resource Monitor Final] Start/Goroutines: %d, End/Goroutines: %d, Diff: %d",
		rm.startGoroutines, runtime.NumGoroutine(), goroutineDiff)
	rm.t.Logf("[Resource Monitor Final] Start/Alloc: %.2f MB, End/Alloc: %.2f MB, Diff: %.2f MB",
		float64(rm.startMem.Alloc)/1024/1024,
		float64(mem.Alloc)/1024/1024,
		float64(allocDiff)/1024/1024,
	)
}

func EnsureEngineCleanup(t *testing.T, eng Stoppable) {
	t.Cleanup(func() {
		if eng != nil {
			eng.Stop()
		}
		runtime.GC()
	})
}

func CheckNetNSLimit(t *testing.T) bool {
	if runtime.GOOS != "linux" {
		return true
	}

	count, err := getNetNSCount()
	if err != nil {
		t.Logf("Failed to check netns count: %v", err)
		return true
	}

	if count >= maxNetNSCount {
		t.Skipf("Skipping test: netns count %d exceeds limit %d", count, maxNetNSCount)
		return false
	}

	return true
}

func CleanupStaleNetNS(t *testing.T) {
	if runtime.GOOS != "linux" {
		return
	}

	stale, err := getStaleNetNS()
	if err != nil {
		t.Logf("Failed to get stale netns: %v", err)
		return
	}

	for _, ns := range stale {
		if err := deleteNetNS(ns); err != nil {
			t.Logf("Failed to delete stale netns %s: %v", ns, err)
		} else {
			t.Logf("Cleaned up stale netns: %s", ns)
		}
	}
}

func getNetNSCount() (int, error) {
	cmd := exec.Command("bash", "-c", "lsns -t net 2>/dev/null | wc -l")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(output)))
}

func getStaleNetNS() ([]string, error) {
	var result []string

	cmd := exec.Command("ip", "netns", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		nsName := parts[0]

		ageStr := parts[len(parts)-2]
		ageUnit := parts[len(parts)-1]

		if isStale(ageStr, ageUnit) {
			result = append(result, nsName)
		}
	}

	return result, nil
}

func isStale(ageStr, ageUnit string) bool {
	age, err := strconv.Atoi(ageStr)
	if err != nil {
		return false
	}

	switch ageUnit {
	case "min":
		return age >= 10
	case "hour":
		return true
	case "day":
		return true
	default:
		return false
	}
}

func deleteNetNS(name string) error {
	cmd := exec.Command("ip", "netns", "del", name)
	return cmd.Run()
}

func CaptureGoroutineDump(t *testing.T) {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("=== Goroutine Dump ===\n")

	cmd := exec.Command(os.Args[0], "-test.v", "-test.run", t.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		buf.WriteString(fmt.Sprintf("Error: %v\n", err))
	}
	buf.Write(output)

	t.Log(buf.String())
}
