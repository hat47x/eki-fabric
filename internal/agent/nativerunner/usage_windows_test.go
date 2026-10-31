//go:build windows

package nativerunner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunnerRejectsCPUOverLimitWindows(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.CPUMillis = 1
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "for /L %i in (1,1,2000000) do @rem"},
	}
	_, err := runner.Run(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("CPU over-limit was not rejected: %v", err)
	}
}

