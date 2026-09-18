//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCPUUsage(t *testing.T) {
	got, err := parseCPUUsage(strings.NewReader("usage_usec 12345\nuser_usec 10000\nsystem_usec 2345\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Fatalf("got %d", got)
	}
}

func TestApplyPolicyFixture(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cpu.max", "memory.max", "pids.max", "io.max", "cgroup.procs", "cgroup.kill"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("max\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := applyPolicy(dir, Policy{
		CPUMax:    "20000 100000",
		MemoryMax: "1048576",
		PidsMax:   "32",
	})
	if got["cpu.max"].After != "20000 100000" {
		t.Fatalf("cpu after=%q", got["cpu.max"].After)
	}
	if got["memory.max"].After != "1048576" {
		t.Fatalf("memory after=%q", got["memory.max"].After)
	}
	if got["pids.max"].After != "32" {
		t.Fatalf("pids after=%q", got["pids.max"].After)
	}
}

func TestRunRejectsMissingTarget(t *testing.T) {
	dir := t.TempDir()
	rep := run(filepath.Join(dir, "missing"), Policy{}, 0, 0)
	if rep.Error == "" {
		t.Fatal("expected error")
	}
}
