//go:build !windows

package nativerunner

import (
	"os/exec"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
)

// SandboxProfileJobObject is Windows-only; other platforms reject it.
const SandboxProfileJobObject = "native-job-object"

func supportedSandboxProfile(profile string) bool {
	return profile == SandboxProfile
}

func applySandbox(*exec.Cmd, executor.Spec) (func(), error) {
	return func() {}, nil
}
