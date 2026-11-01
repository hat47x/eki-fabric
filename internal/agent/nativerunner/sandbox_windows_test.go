//go:build windows

package nativerunner

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRunWithJobObjectSandbox(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Sandbox.Profile = SandboxProfileJobObject
	spec.Limits.MemoryBytes = 64 << 20
	spec.Limits.CPUMillis = 0
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "echo hello"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("job-object run: %v", err)
	}
	if result.OutputDigest.IsZero() {
		t.Fatal("job-object run produced no output digest")
	}
}
