//go:build windows

package nativerunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunKillsProcessTreeWindows(t *testing.T) {
	if _, err := exec.LookPath("taskkill"); err != nil {
		t.Skip("taskkill is unavailable")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = helper("tree", marker).Run(ctx, spec(100))
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("process tree was not stopped promptly: %s", elapsed)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child process survived taskkill /T")
	}
}
