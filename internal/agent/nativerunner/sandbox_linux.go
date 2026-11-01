//go:build linux

package nativerunner

import (
	"fmt"
	"os/exec"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
)

func supportedSandboxProfile(profile string) bool {
	return profile == SandboxProfile || profile == SandboxProfileLinuxBwrap
}

func wrapSandboxCommand(spec executor.Spec, executable string, args []string, workDir string) (string, []string, error) {
	if spec.Sandbox.Profile != SandboxProfileLinuxBwrap {
		return executable, args, nil
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "", nil, fmt.Errorf("linux-bwrap profile requires bwrap: %w", err)
	}
	return bwrap, bwrapArguments(executable, args, workDir), nil
}

func applySandbox(*exec.Cmd, executor.Spec) (func(), error) {
	return func() {}, nil
}
