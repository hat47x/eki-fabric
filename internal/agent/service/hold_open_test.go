package service

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWithAStateDirectoryTheHoldKeepsBodiesOnDisk(t *testing.T) {
	hold := openHold(t.TempDir(), 1024, testLog())
	if hold.spool == nil {
		t.Fatal("the hold has no spool")
	}
	hold.Put(attemptX, []byte("body"))
	if hold.LastSpoolError() != nil {
		t.Fatalf("spool error = %v", hold.LastSpoolError())
	}
	if usage, err := hold.spool.Usage(); err != nil || usage.Bodies != 1 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
}

func TestWithoutAStateDirectoryTheHoldStaysInMemory(t *testing.T) {
	hold := openHold("", 1024, testLog())
	if hold.spool != nil {
		t.Fatal("the hold has a spool without a state directory")
	}
}

func TestWithoutAnOutputLimitTheHoldStaysInMemory(t *testing.T) {
	hold := openHold(t.TempDir(), 0, testLog())
	if hold.spool != nil {
		t.Fatal("the hold has a spool without a limit")
	}
}

func TestAnUnusableDirectoryFallsBackToMemory(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/not-a-directory"
	if err := writeFile(file); err != nil {
		t.Fatal(err)
	}
	hold := openHold(file, 1024, testLog())
	if hold.spool != nil {
		t.Fatal("the hold used a file as a spool directory")
	}
}

func writeFile(path string) error {
	return os.WriteFile(path, []byte("x"), 0o600)
}
