#!/usr/bin/env bash
# Run the Linux-only Native Runner tests, including the bubblewrap sandbox.
#
# Build the test binary where Go is available, then run it on a Linux host or
# WSL that has bwrap installed:
#
#   GOOS=linux GOARCH=amd64 go test -c -o runner-linux.test ./internal/agent/nativerunner
#   tools/linux-sandbox-verify/verify.sh ./runner-linux.test
set -euo pipefail

binary="${1:-}"
if [[ -z "$binary" ]]; then
	echo "usage: $0 <linux test binary>" >&2
	exit 2
fi
if [[ ! -x "$binary" ]]; then
	echo "test binary is not executable: $binary" >&2
	exit 2
fi
if ! command -v bwrap >/dev/null 2>&1; then
	echo "bwrap is required for TestRunWithBwrap*" >&2
	exit 2
fi

exec "$binary" -test.v -test.timeout 120s -test.run 'TestRunWithBwrap|TestRunnerRejectsCPUOverLimit|TestRunnerRunsInPerRunWorkingDirectory'
