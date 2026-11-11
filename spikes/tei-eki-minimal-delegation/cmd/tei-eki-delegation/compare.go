package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	teideleg "github.com/hat47x/eki-fabric/spikes/tei-eki-minimal-delegation"
)

// runCompare assembles the local-vs-EKI cost report from records the caller
// already produced. It measures nothing itself: the numbers come from
// `local` and `live` runs, so the report can be regenerated without rerunning
// anything, and cannot quietly become a fresh unsourced claim.
func runCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	var locals, traces repeatedFlag
	fs.Var(&locals, "local", "local-run JSON written by `local` (repeatable)")
	fs.Var(&traces, "trace", "trace JSON written by `live` (repeatable)")
	operation := fs.String("operation", "tei.validate", "the TEI operation the numbers belong to")
	out := fs.String("out", "", "write the comparison JSON here (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	localRuns := make([]teideleg.LocalRun, 0, len(locals))
	for _, path := range locals {
		var run teideleg.LocalRun
		if err := readJSONFile(path, &run); err != nil {
			return fmt.Errorf("read local run %s: %w", path, err)
		}
		localRuns = append(localRuns, run)
	}
	records := make([]teideleg.Trace, 0, len(traces))
	for _, path := range traces {
		var trace teideleg.Trace
		if err := readJSONFile(path, &trace); err != nil {
			return fmt.Errorf("read trace %s: %w", path, err)
		}
		records = append(records, trace)
	}
	if len(localRuns) == 0 && len(records) == 0 {
		return errors.New("compare: at least one -local or -trace record is required")
	}

	comparison := teideleg.Compare(*operation, localRuns, records, time.Now())
	if err := writeJSON(*out, comparison); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, comparison.MedianLabel())
	return nil
}

func readJSONFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
