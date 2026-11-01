//go:build windows

package nativerunner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerStopsProcessWhenCPULimitIsExceeded(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 1
	spec.Limits.WallTimeMillis = 10000
	spec.Deadline = now.Add(10 * time.Second)
	// The watch samples the CPU time of the process it started. A command that
	// only waits (cmd /c ping) spends its CPU in the child, so the root process
	// never crosses a 1ms ceiling and the test depended on when a sample
	// happened to land. The helper burns CPU in the process the watch samples.
	runner := helper("burn")
	started := time.Now()
	_, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "CPU limit") {
		t.Fatalf("CPU over-limit was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("process was not stopped promptly: %s", elapsed)
	}
}

func TestRunnerStopsProcessWhenTempDiskLimitIsExceeded(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 0
	spec.Limits.TemporaryDiskBytes = 1
	spec.Limits.WallTimeMillis = 10000
	spec.Deadline = now.Add(10 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "echo hello> %TEMP%\\eki-big.txt & ping -n 10 127.0.0.1 >nul"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	started := time.Now()
	_, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "temporary disk limit") {
		t.Fatalf("temp-disk over-limit was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("process was not stopped promptly: %s", elapsed)
	}
}

// TestRunnerStopsAChildThatSpendsTheCPU pins the tree-wide observation of
// #351: the helper idles in the root process while its child burns CPU. A
// watch that only samples the root never crosses the ceiling and the run
// lasts its full wall time; a watch that sums the tree stops it promptly.
func TestRunnerStopsAChildThatSpendsTheCPU(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 500
	spec.Limits.WallTimeMillis = 20000
	spec.Deadline = now.Add(20 * time.Second)
	runner := helper("childburn")
	started := time.Now()
	_, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "CPU limit") {
		t.Fatalf("CPU over-limit spent by a child was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("child was not stopped promptly: %s", elapsed)
	}
}
