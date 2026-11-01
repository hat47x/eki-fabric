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

func TestRunWithJobObjectCPULimit(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Sandbox.Profile = SandboxProfileJobObject
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 5000
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "echo hello"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	if _, err := runner.Run(context.Background(), spec); err != nil {
		t.Fatalf("job-object CPU limit run: %v", err)
	}
}

func TestJobObjectCPULimitStopsBusyProcess(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Sandbox.Profile = SandboxProfileJobObject
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 10
	spec.Limits.WallTimeMillis = 10000
	spec.Deadline = now.Add(10 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "for /L %i in (1,1,200000000) do @rem"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	started := time.Now()
	result, err := runner.Run(context.Background(), spec)
	if err == nil && result.ExitCode == 0 {
		t.Fatal("busy process under a CPU limit exited successfully")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("process was not stopped promptly: %s", elapsed)
	}
}

func TestRunWithJobObjectAggregateLimits(t *testing.T) {
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Sandbox.Profile = SandboxProfileJobObject
	spec.Limits.MemoryBytes = 256 << 20
	spec.Limits.CPUMillis = 5000
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)
	runner := Runner{
		Command: "cmd.exe",
		Args:    []string{"/c", "echo hello"},
		Env:     []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")},
	}
	if _, err := runner.Run(context.Background(), spec); err != nil {
		t.Fatalf("job-object aggregate limits run: %v", err)
	}
}
