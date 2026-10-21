package sqlite

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueProcessHelper(t *testing.T) {
	path := os.Getenv("EKI_CLAIM_QUEUE_HELPER_DATABASE")
	if path == "" {
		t.Skip("subprocess helper")
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	value := entry(t, 1)
	value.Attempts, value.LastError, value.NextAt = 3, "offline", baseTime.Add(time.Minute)
	if err := s.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	fmt.Println("saved")
	// Bypass Close and test cleanup, after Save's durable commit.
	os.Exit(0)
}

func TestQueueSurvivesProcessExitWithoutClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-queue.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQueueProcessHelper$")
	cmd.Env = append(os.Environ(), "EKI_CLAIM_QUEUE_HELPER_DATABASE="+path)
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "saved\n" {
		t.Fatalf("child process = %q, %v", output, err)
	}
	s := openStore(t, path)
	want := entry(t, 1)
	want.Attempts, want.LastError, want.NextAt = 3, "offline", baseTime.Add(time.Minute)
	got, found, err := s.Load(context.Background(), want.Claim.AttemptID())
	if err != nil || !found {
		t.Fatalf("restarted queue Load = %v, %v", found, err)
	}
	assertEntry(t, got, want)
}
