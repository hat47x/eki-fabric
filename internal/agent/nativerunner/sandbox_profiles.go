package nativerunner

// SandboxProfileLinuxBwrap runs the fixed command through bubblewrap with a
// read-only root, a private /tmp, no network, and a separate PID namespace.
const SandboxProfileLinuxBwrap = "linux-bwrap"

// SandboxProfileJobObject confines the child in a Windows Job Object.
const SandboxProfileJobObject = "native-job-object"

// profileDeniesNetwork reports whether the profile removes network access
// entirely. A numeric network budget is satisfied by such a profile because no
// bytes can leave or enter the process.
func profileDeniesNetwork(profile string) bool {
	return profile == SandboxProfileLinuxBwrap
}
