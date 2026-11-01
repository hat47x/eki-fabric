package nativerunner

// bwrapArguments builds a bubblewrap invocation that gives the child a
// read-only root, a private /tmp, no network, and its own PID namespace. The
// per-run working directory is bound read-write so temporary-disk accounting
// keeps working.
func bwrapArguments(executable string, args []string, workDir string) []string {
	out := []string{
		"--die-with-parent",
		"--unshare-net",
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	if workDir != "" {
		out = append(out, "--bind", workDir, workDir, "--chdir", workDir)
	}
	out = append(out, "--", executable)
	return append(out, args...)
}
