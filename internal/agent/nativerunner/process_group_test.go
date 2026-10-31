//go:build !windows

package nativerunner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunKillsProcessGroup(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("shell fixture is unavailable")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = helper("tree", marker).Run(ctx, spec(100))
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process tree was not stopped promptly: %s", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child process survived the group cancellation")
	}
}
