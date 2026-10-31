//go:build windows

package nativerunner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerStopsProcessWhenMemoryLimitIsExceeded(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.MemoryBytes = 1
	spec.Limits.CPUMillis = 0
	spec.Limits.WallTimeMillis = 10000
	spec.Deadline = now.Add(10 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "ping -n 10 127.0.0.1 >nul"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	started := time.Now()
	_, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "memory limit") {
		t.Fatalf("memory over-limit was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("process was not stopped promptly: %s", elapsed)
	}
}
