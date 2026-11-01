//go:build linux

package nativerunner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
)

func bwrapSpec(t *testing.T) executor.Spec {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap is not installed")
	}
	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Sandbox.Profile = SandboxProfileLinuxBwrap
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 0
	spec.Limits.TemporaryDiskBytes = 0
	spec.Limits.NetworkBytesMax = 0
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)
	return spec
}

func TestRunWithBwrapSandboxSucceeds(t *testing.T) {
	spec := bwrapSpec(t)
	runner := Runner{Command: "/bin/sh", Args: []string{"-c", "echo hello"}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("bwrap run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	if result.OutputDigest.IsZero() {
		t.Fatal("bwrap run produced no output digest")
	}
}

func TestRunWithBwrapSandboxMakesRootReadOnly(t *testing.T) {
	spec := bwrapSpec(t)
	runner := Runner{Command: "/bin/sh", Args: []string{"-c", "echo x > /etc/eki-bwrap-write-test"}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("bwrap run: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("write to /etc succeeded inside the read-only root")
	}
}

func TestRunWithBwrapSandboxHasNoNetworkInterface(t *testing.T) {
	spec := bwrapSpec(t)
	runner := Runner{Command: "/bin/sh", Args: []string{"-c", "if grep -q eth0 /proc/net/dev; then exit 1; fi"}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("bwrap run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("network interface leaked into the sandbox: exit=%d", result.ExitCode)
	}
}

func TestRunWithBwrapSandboxStopsBusyProcessAtCPULimit(t *testing.T) {
	spec := bwrapSpec(t)
	spec.Limits.CPUMillis = 100
	spec.Limits.WallTimeMillis = 10000
	spec.Deadline = time.Now().UTC().Add(10 * time.Second)
	runner := Runner{Command: "/bin/sh", Args: []string{"-c", "while :; do :; done"}}
	started := time.Now()
	_, err := runner.Run(context.Background(), spec)
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("CPU limit was not enforced: err=%v elapsed=%s", err, elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("busy process was not stopped promptly: %s", elapsed)
	}
}

func TestRunWithBwrapSandboxUsesPerRunWorkingDirectory(t *testing.T) {
	spec := bwrapSpec(t)
	spec.Limits.TemporaryDiskBytes = 1 << 20
	runner := Runner{Command: "/bin/sh", Args: []string{"-c", "echo data > out.txt && cat out.txt"}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("bwrap run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("write inside the bound working directory failed: exit=%d", result.ExitCode)
	}
	if result.OutputDigest.IsZero() {
		t.Fatal("bwrap run produced no output digest")
	}
}
