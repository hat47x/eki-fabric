//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentCgroupPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cgroup")
	if err := os.WriteFile(p, []byte("0::/user.slice/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := currentCgroupPath(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/user.slice/demo" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildReportFromFixture(t *testing.T) {
	root := t.TempDir()
	cg := filepath.Join(root, "demo")
	if err := os.MkdirAll(cg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory pids\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"cpu.max":      "50000 100000\n",
		"memory.max":   "1073741824\n",
		"pids.max":     "64\n",
		"cgroup.procs": "\n",
	} {
		if err := os.WriteFile(filepath.Join(cg, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(root, "proc-cgroup")
	if err := os.WriteFile(proc, []byte("0::/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := buildReport(root, proc)
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentPath != "/demo" {
		t.Fatalf("current path = %q", r.CurrentPath)
	}
	if len(r.Controllers) != 3 {
		t.Fatalf("controllers = %#v", r.Controllers)
	}
	if got := r.Files["cpu.max"].Value; got != "50000 100000" {
		t.Fatalf("cpu.max = %q", got)
	}
	if !r.Files["cpu.max"].Writable {
		t.Fatalf("fixture cpu.max should be writable")
	}
}
