//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	CPUMax    string `json:"cpu_max,omitempty"`
	MemoryMax string `json:"memory_max,omitempty"`
	PidsMax   string `json:"pids_max,omitempty"`
	IOMax     string `json:"io_max,omitempty"`
}

type FileStatus struct {
	Path     string `json:"path"`
	Present  bool   `json:"present"`
	Writable bool   `json:"writable"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	Error    string `json:"error,omitempty"`
}

type CPUObservation struct {
	WallMicros        int64   `json:"wall_micros"`
	UsageDeltaMicros  uint64  `json:"usage_delta_micros"`
	ApproxOneCPUShare float64 `json:"approx_one_cpu_share"`
}

type Report struct {
	Target           string                `json:"target"`
	Policy           Policy                `json:"policy"`
	Files            map[string]FileStatus `json:"files"`
	AttachedPID      int                   `json:"attached_pid,omitempty"`
	CPU              *CPUObservation       `json:"cpu,omitempty"`
	StopMethod       string                `json:"stop_method,omitempty"`
	QuiescenceMicros int64                 `json:"quiescence_micros,omitempty"`
	WorkloadExit     string                `json:"workload_exit,omitempty"`
	Error            string                `json:"error,omitempty"`
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func applyOne(target, name, value string) FileStatus {
	p := filepath.Join(target, name)
	st := FileStatus{Path: p}
	if _, err := os.Stat(p); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Present = true

	if v, err := readTrimmed(p); err == nil {
		st.Before = v
	}
	st.Writable = writable(p)

	if value == "" {
		return st
	}
	if !st.Writable {
		st.Error = "control file is not writable"
		return st
	}
	if err := os.WriteFile(p, []byte(value), 0); err != nil {
		st.Error = err.Error()
		return st
	}
	if v, err := readTrimmed(p); err == nil {
		st.After = v
	}
	return st
}

func applyPolicy(target string, p Policy) map[string]FileStatus {
	return map[string]FileStatus{
		"cpu.max":      applyOne(target, "cpu.max", p.CPUMax),
		"memory.max":   applyOne(target, "memory.max", p.MemoryMax),
		"pids.max":     applyOne(target, "pids.max", p.PidsMax),
		"io.max":       applyOne(target, "io.max", p.IOMax),
		"cgroup.procs": applyOne(target, "cgroup.procs", ""),
		"cgroup.kill":  applyOne(target, "cgroup.kill", ""),
	}
}

func parseCPUUsage(r io.Reader) (uint64, error) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 2 && f[0] == "usage_usec" {
			return strconv.ParseUint(f[1], 10, 64)
		}
	}
	if err := s.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("usage_usec not found")
}

func cpuUsage(target string) (uint64, error) {
	f, err := os.Open(filepath.Join(target, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return parseCPUUsage(f)
}

func attachPID(target string, pid int) error {
	return os.WriteFile(filepath.Join(target, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0)
}

func killCgroup(target string) error {
	p := filepath.Join(target, "cgroup.kill")
	if !writable(p) {
		return os.ErrPermission
	}
	return os.WriteFile(p, []byte("1"), 0)
}

func cpuWorker(duration time.Duration, gateFD uintptr) error {
	gate := os.NewFile(gateFD, "gate")
	if gate == nil {
		return errors.New("gate fd missing")
	}
	defer gate.Close()

	var b [1]byte
	if _, err := gate.Read(b[:]); err != nil {
		return err
	}

	deadline := time.Now().Add(duration)
	var x uint64 = 1
	for time.Now().Before(deadline) {
		x = x*1664525 + 1013904223
	}
	if x == 0 {
		fmt.Fprint(io.Discard, x)
	}
	return nil
}

func run(target string, policy Policy, workloadDuration, stopAfter time.Duration) Report {
	rep := Report{Target: target, Policy: policy}
	if target == "" {
		rep.Error = "target cgroup is required"
		return rep
	}

	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		if err != nil {
			rep.Error = err.Error()
		} else {
			rep.Error = "target is not a directory"
		}
		return rep
	}

	rep.Files = applyPolicy(target, policy)
	requested := map[string]string{
		"cpu.max":    policy.CPUMax,
		"memory.max": policy.MemoryMax,
		"pids.max":   policy.PidsMax,
		"io.max":     policy.IOMax,
	}
	for name, value := range requested {
		if value == "" {
			continue
		}
		st := rep.Files[name]
		if st.Error != "" || st.After == "" {
			rep.Error = name + " was requested but could not be applied"
			return rep
		}
	}
	if !rep.Files["cgroup.procs"].Writable {
		rep.Error = "cgroup.procs is not writable; target is not delegated to this process"
		return rep
	}

	gateR, gateW, err := os.Pipe()
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	defer gateR.Close()
	defer gateW.Close()

	cmd := exec.Command(os.Args[0], "--internal-cpu-worker", workloadDuration.String())
	cmd.ExtraFiles = []*os.File{gateR}
	if err := cmd.Start(); err != nil {
		rep.Error = err.Error()
		return rep
	}
	rep.AttachedPID = cmd.Process.Pid

	if err := attachPID(target, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		rep.Error = "attach pid: " + err.Error()
		return rep
	}

	before, _ := cpuUsage(target)
	start := time.Now()
	_, _ = gateW.Write([]byte{1})
	_ = gateW.Close()

	if stopAfter > 0 && stopAfter < workloadDuration {
		time.Sleep(stopAfter)
		qStart := time.Now()
		if err := killCgroup(target); err == nil {
			rep.StopMethod = "cgroup.kill"
		} else {
			rep.StopMethod = "process.kill-fallback"
			_ = cmd.Process.Kill()
		}
		err = cmd.Wait()
		rep.QuiescenceMicros = time.Since(qStart).Microseconds()
		rep.WorkloadExit = fmt.Sprint(err)
	} else {
		err = cmd.Wait()
		rep.WorkloadExit = fmt.Sprint(err)
	}

	elapsed := time.Since(start)
	after, _ := cpuUsage(target)
	if after >= before && elapsed > 0 {
		delta := after - before
		rep.CPU = &CPUObservation{
			WallMicros:        elapsed.Microseconds(),
			UsageDeltaMicros:  delta,
			ApproxOneCPUShare: float64(delta) / float64(elapsed.Microseconds()),
		}
	}
	return rep
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "--internal-cpu-worker" {
		d, err := time.ParseDuration(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		if err := cpuWorker(d, 3); err != nil {
			os.Exit(3)
		}
		return
	}

	target := flag.String("target", "", "existing delegated leaf cgroup directory")
	cpuMax := flag.String("cpu-max", "", "value for cpu.max, e.g. '20000 100000'")
	memoryMax := flag.String("memory-max", "", "value for memory.max")
	pidsMax := flag.String("pids-max", "", "value for pids.max")
	ioMax := flag.String("io-max", "", "value for io.max")
	duration := flag.Duration("duration", 3*time.Second, "CPU workload wall duration")
	stopAfter := flag.Duration("stop-after", 0, "simulate local opt-out by killing workload after this delay")
	flag.Parse()

	rep := run(*target, Policy{
		CPUMax:    *cpuMax,
		MemoryMax: *memoryMax,
		PidsMax:   *pidsMax,
		IOMax:     *ioMax,
	}, *duration, *stopAfter)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	if rep.Error != "" {
		os.Exit(1)
	}
}
