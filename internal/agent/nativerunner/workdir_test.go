package nativerunner

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestRunnerRunsInPerRunWorkingDirectory(t *testing.T) {
	const name = "eki-relative-cwd-test.txt"
	_ = os.Remove(name)
	defer os.Remove(name)

	now := time.Now().UTC()
	spec := validSpec(now)
	spec.Limits.MemoryBytes = 0
	spec.Limits.CPUMillis = 0
	spec.Limits.TemporaryDiskBytes = 1 << 20
	spec.Limits.WallTimeMillis = 5000
	spec.Deadline = now.Add(5 * time.Second)

	command, args, env := "/bin/sh", []string{"-c", "echo hello > " + name}, []string{"PATH=/usr/bin:/bin"}
	if runtime.GOOS == "windows" {
		command, args = "cmd.exe", []string{"/c", "echo hello> " + name}
		env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH")}
	}
	runner := Runner{Command: command, Args: args, Env: env}
	if _, err := runner.Run(context.Background(), spec); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(name); err == nil {
		t.Fatal("a relative write landed in the caller working directory")
	}
}
