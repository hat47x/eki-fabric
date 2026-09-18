//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileCapability struct {
	Path      string `json:"path"`
	Present   bool   `json:"present"`
	Readable  bool   `json:"readable"`
	Writable  bool   `json:"writable"`
	Value     string `json:"value,omitempty"`
	ReadError string `json:"read_error,omitempty"`
}

type Report struct {
	CgroupRoot  string                    `json:"cgroup_root"`
	CurrentPath string                    `json:"current_path"`
	Controllers []string                  `json:"controllers,omitempty"`
	Files       map[string]FileCapability `json:"files"`
}

func currentCgroupPath(procCgroup string) (string, error) {
	f, err := os.Open(procCgroup)
	if err != nil {
		return "", err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		// Unified cgroup v2 entry: 0::<path>
		if strings.HasPrefix(line, "0::") {
			p := strings.TrimPrefix(line, "0::")
			if p == "" {
				p = "/"
			}
			return p, nil
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", errors.New("cgroup v2 unified entry not found")
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func probeFile(path string) FileCapability {
	c := FileCapability{Path: path}
	if _, err := os.Stat(path); err != nil {
		return c
	}
	c.Present = true

	if v, err := readTrimmed(path); err == nil {
		c.Readable = true
		c.Value = v
	} else {
		c.ReadError = err.Error()
	}

	// Opening with write-only does not change the file. It is enough to detect
	// common read-only/delegation failures without mutating the cgroup state.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		c.Writable = true
		_ = f.Close()
	}
	return c
}

func buildReport(root, procCgroup string) (Report, error) {
	current, err := currentCgroupPath(procCgroup)
	if err != nil {
		return Report{}, err
	}

	rel := strings.TrimPrefix(filepath.Clean(current), string(filepath.Separator))
	cg := root
	if rel != "." && rel != "" {
		cg = filepath.Join(root, rel)
	}

	r := Report{
		CgroupRoot:  root,
		CurrentPath: current,
		Files:       map[string]FileCapability{},
	}

	if v, err := readTrimmed(filepath.Join(root, "cgroup.controllers")); err == nil && v != "" {
		r.Controllers = strings.Fields(v)
	}

	names := []string{
		"cpu.max",
		"cpu.weight",
		"memory.high",
		"memory.max",
		"io.max",
		"pids.max",
		"cgroup.procs",
		"cgroup.kill",
	}
	for _, name := range names {
		r.Files[name] = probeFile(filepath.Join(cg, name))
	}
	return r, nil
}

func main() {
	root := "/sys/fs/cgroup"
	procCgroup := "/proc/self/cgroup"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if len(os.Args) > 2 {
		procCgroup = os.Args[2]
	}

	r, err := buildReport(root, procCgroup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe failed:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "encode failed:", err)
		os.Exit(1)
	}
}
