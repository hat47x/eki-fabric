// Command tei-eki-delegation composes one side-effect-free TEI operation as an
// EKI request, runs it through EKI's public Client API, and records the result
// as a TEI receipt.
//
// The command has no configuration file and no dependencies outside this
// module. Subcommands:
//
//	offline  compose the projection from a TEI operation (no network)
//	bundle   pack a directory into a deterministic input artifact
//	local    run the same operation locally and record the baseline
//	live     delegate the operation to a running eki-control and write a trace
//	probe    check the refusal paths against a running eki-control
//	compare  assemble the local-vs-EKI cost report from the records above
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	teideleg "github.com/hat47x/eki-fabric/spikes/tei-eki-minimal-delegation"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tei-eki-delegation:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tei-eki-delegation <offline|bundle|local|live> [flags]")
	}
	switch args[0] {
	case "offline":
		return runOffline(args[1:])
	case "bundle":
		return runBundle(args[1:])
	case "local":
		return runLocal(args[1:])
	case "live":
		return runLive(args[1:])
	case "probe":
		return runProbe(args[1:])
	case "compare":
		return runCompare(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// operationFlags are shared by offline and live: live rebuilds the projection
// from the same description so that the trace can show both the composed and
// the sent request.
type operationFlags struct {
	revision    string
	executable  string
	toolchain   string
	executor    string
	command     string
	deadline    time.Duration
	expectedOut string
	inputs      repeatedFlag
	mediaType   string
	dataClass   string
	cpuMillis   int64
	memoryBytes int64
	wallSeconds int64
	outputMax   int64
	networkMax  int64
	priority    string
	revisionOut string
}

func bindOperation(fs *flag.FlagSet) *operationFlags {
	o := &operationFlags{}
	fs.StringVar(&o.revision, "source-revision", "", "TEI source revision the operation is defined at")
	fs.StringVar(&o.executable, "executable", "", "path to the TEI binary; its SHA-256 is recorded (not enforced by EKI)")
	fs.StringVar(&o.toolchain, "toolchain", "", "toolchain the operation expects (recorded, not enforced)")
	fs.StringVar(&o.executor, "executor", "tei.validate", "EKI registered executor id")
	fs.StringVar(&o.command, "command", `["validate","-workspace","reference/workspace","-json"]`, "local command line as a JSON array")
	fs.DurationVar(&o.deadline, "deadline", 30*time.Second, "requested deadline from now (EKI records it but does not act on it)")
	fs.StringVar(&o.expectedOut, "expected-output-sha256", "", "TEI's independent expectation of the output digest")
	fs.Var(&o.inputs, "input", "input artifact as name=path (repeatable)")
	fs.StringVar(&o.mediaType, "input-media-type", "application/gzip", "media type of every input")
	fs.StringVar(&o.dataClass, "data-class", "internal", "requested data class")
	fs.Int64Var(&o.cpuMillis, "cpu-millis", 4000, "requested CPU")
	fs.Int64Var(&o.memoryBytes, "memory-bytes", 1<<30, "requested memory")
	fs.Int64Var(&o.wallSeconds, "wall-seconds", 600, "requested wall time")
	fs.Int64Var(&o.outputMax, "output-bytes-max", 64<<20, "requested output maximum")
	fs.Int64Var(&o.networkMax, "network-bytes-max", 0, "requested network maximum")
	fs.StringVar(&o.priority, "priority", "normal", "requested priority")
	return o
}

func (o *operationFlags) operation() (teideleg.Operation, map[string][]byte, error) {
	op := teideleg.Operation{
		ID:             "tei.validate",
		SourceRevision: o.revision,
		Command:        nil,
		Toolchain:      o.toolchain,
		ExecutorID:     o.executor,
		Resources: teideleg.ResourceRequest{
			CPUMillis:       o.cpuMillis,
			MemoryBytes:     o.memoryBytes,
			WallTimeSeconds: o.wallSeconds,
			OutputBytesMax:  o.outputMax,
			NetworkBytesMax: o.networkMax,
		},
		Deadline:             teideleg.NewDeadline(time.Now(), o.deadline),
		ExpectedOutputSHA256: strings.ToLower(o.expectedOut),
		Priority:             o.priority,
	}
	if err := json.Unmarshal([]byte(o.command), &op.Command); err != nil {
		return op, nil, fmt.Errorf("-command must be a JSON array of strings: %w", err)
	}
	if o.executable != "" {
		raw, err := os.ReadFile(o.executable)
		if err != nil {
			return op, nil, fmt.Errorf("read executable: %w", err)
		}
		op.ExecutableSHA256 = teideleg.SHA256Hex(raw)
	}
	uploads := map[string][]byte{}
	for _, spec := range o.inputs {
		name, path, ok := strings.Cut(spec, "=")
		if !ok || name == "" || path == "" {
			return op, nil, fmt.Errorf("-input %q is not name=path", spec)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return op, nil, fmt.Errorf("read input %q: %w", name, err)
		}
		op.Inputs = append(op.Inputs, teideleg.Input{
			Name: name, SHA256Hex: teideleg.SHA256Hex(raw), SizeBytes: int64(len(raw)),
			MediaType: o.mediaType, DataClass: o.dataClass,
		})
		uploads[name] = raw
	}
	return op, uploads, nil
}

func runOffline(args []string) error {
	fs := flag.NewFlagSet("offline", flag.ContinueOnError)
	o := bindOperation(fs)
	out := fs.String("out", "", "write the projection JSON here (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	op, _, err := o.operation()
	if err != nil {
		return err
	}
	projection, err := teideleg.Project(op)
	if err != nil {
		return err
	}
	return writeJSON(*out, projection)
}

func runBundle(args []string) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to pack")
	out := fs.String("out", "", "write the tar.gz here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *out == "" {
		return errors.New("bundle: -dir and -out are required")
	}
	raw, digest, err := teideleg.BundleDir(*dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil && filepath.Dir(*out) != "." {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	return writeJSON("", map[string]any{
		"schema": "tei.eki-input-bundle/v0", "path": *out,
		"sha256": digest, "size_bytes": len(raw),
	})
}

func runLocal(args []string) error {
	fs := flag.NewFlagSet("local", flag.ContinueOnError)
	tei := fs.String("tei", "", "path to the tei binary")
	workspace := fs.String("workspace", "", "workspace the validate operation reads")
	revision := fs.String("source-revision", "", "TEI source revision")
	executable := fs.String("executable", "", "path to the tei binary whose digest is recorded (defaults to -tei)")
	cacheState := fs.String("cache-state", "warm", "warm or cold: whether the build/module cache is warm")
	out := fs.String("out", "", "write the local-run JSON here (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tei == "" || *workspace == "" {
		return errors.New("local: -tei and -workspace are required")
	}
	exePath := *executable
	if exePath == "" {
		exePath = *tei
	}
	exeRaw, err := os.ReadFile(exePath)
	if err != nil {
		return fmt.Errorf("read executable: %w", err)
	}
	command := []string{"validate", "-workspace", *workspace, "-json"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, *tei, command...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)
	run := teideleg.LocalRun{
		Schema:           teideleg.LocalRunSchema,
		Command:          command,
		ExitCode:         0,
		DurationMillis:   elapsed.Milliseconds(),
		OutputSHA256:     teideleg.SHA256Hex([]byte(stdout.String())),
		OutputBytes:      int64(stdout.Len()),
		CacheState:       *cacheState,
		SourceRevision:   *revision,
		ExecutableSHA256: teideleg.SHA256Hex(exeRaw),
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			run.ExitCode = exit.ExitCode()
		} else {
			return fmt.Errorf("local run: %w", runErr)
		}
	}
	if run.ExitCode != 0 {
		// A failed local run is an observation, not a digest to compare.
		run.OutputSHA256 = ""
		fmt.Fprintln(os.Stderr, "local run stderr:", strings.TrimSpace(stderr.String()))
	}
	return writeJSON(*out, run)
}

func runLive(args []string) error {
	fs := flag.NewFlagSet("live", flag.ContinueOnError)
	base := fs.String("base", "", "base URL of eki-control's Client API")
	token := fs.String("token", "", "Bearer token of the client principal")
	projectionPath := fs.String("projection", "", "projection JSON produced by offline")
	localPath := fs.String("local", "", "local-run JSON used as the expectation")
	wait := fs.Duration("wait", 60*time.Second, "how long to observe before deciding")
	poll := fs.Duration("poll", 1*time.Second, "pause between status reads")
	var inputs repeatedFlag
	fs.Var(&inputs, "input", "input bytes as name=path (repeatable)")
	out := fs.String("out", "", "write the trace JSON here (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *token == "" || *projectionPath == "" {
		return errors.New("live: -base, -token and -projection are required")
	}
	raw, err := os.ReadFile(*projectionPath)
	if err != nil {
		return err
	}
	var projection teideleg.Projection
	if err := json.Unmarshal(raw, &projection); err != nil {
		return fmt.Errorf("decode projection: %w", err)
	}
	uploads := map[string][]byte{}
	for _, spec := range inputs {
		name, path, ok := strings.Cut(spec, "=")
		if !ok || name == "" || path == "" {
			return fmt.Errorf("-input %q is not name=path", spec)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read input %q: %w", name, err)
		}
		expected, err := findInputDigest(projection, name)
		if err != nil {
			return err
		}
		if got := teideleg.SHA256Hex(body); got != expected {
			return fmt.Errorf("input %q has digest %s, but the projection names %s", name, got, expected)
		}
		uploads[name] = body
	}
	for _, in := range projection.Operation.Inputs {
		if _, ok := uploads[in.Name]; !ok {
			return fmt.Errorf("live: input %q was not supplied with -input", in.Name)
		}
	}
	var local *teideleg.LocalRun
	if *localPath != "" {
		lraw, err := os.ReadFile(*localPath)
		if err != nil {
			return err
		}
		local = &teideleg.LocalRun{}
		if err := json.Unmarshal(lraw, local); err != nil {
			return fmt.Errorf("decode local run: %w", err)
		}
	}
	client := &teideleg.Client{BaseURL: *base, Token: *token}
	delegation, err := teideleg.Delegate(context.Background(), client, projection, teideleg.DelegateOptions{
		Uploads:      uploads,
		Deadline:     time.Now().UTC().Add(*wait),
		PollInterval: *poll,
		Local:        local,
	})
	if err != nil {
		return err
	}
	if err := writeJSON(*out, delegation.Trace); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "receipt: %s (%s)\n", delegation.Receipt.Outcome, delegation.Receipt.Reason)
	switch delegation.Receipt.Outcome {
	case teideleg.OutcomeAccepted:
		return nil
	case teideleg.OutcomeUndetermined:
		os.Exit(3)
	}
	os.Exit(1)
	return nil
}

func findInputDigest(p teideleg.Projection, name string) (string, error) {
	for _, candidate := range p.Operation.Inputs {
		if candidate.Name == name {
			return candidate.SHA256Hex, nil
		}
	}
	return "", fmt.Errorf("input %q is not part of the projection", name)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if path == "" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, raw, 0o644)
}
