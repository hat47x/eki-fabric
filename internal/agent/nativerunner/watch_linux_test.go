//go:build linux

package nativerunner

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunnerStopsAChildThatSpendsTheCPU pins the tree-wide observation of
// #351 on Linux: the helper idles in the root process while its child burns
// CPU. The watch sums the process group, so the child's usage crosses the
// ceiling and the run stops promptly.
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
