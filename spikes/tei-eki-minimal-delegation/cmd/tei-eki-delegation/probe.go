package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	teideleg "github.com/hat47x/eki-fabric/spikes/tei-eki-minimal-delegation"
)

// probeResult is one negative check against a real eki-control. Each check
// states what the contract says before it is run, so a changed answer is a
// finding rather than a test that only passes because it was written around the
// current behaviour.
type probeResult struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	Passed   bool   `json:"passed"`
	Detail   string `json:"detail,omitempty"`
}

type probeReport struct {
	Schema    string        `json:"schema"`
	BaseURL   string        `json:"base_url"`
	RanAt     time.Time     `json:"ran_at"`
	Results   []probeResult `json:"results"`
	PassCount int           `json:"pass_count"`
	FailCount int           `json:"fail_count"`
}

// runProbe exercises the refusal paths the delegation must distinguish. It
// runs against a throwaway eki-control: no production state, no Worker, no
// external effect.
func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	base := fs.String("base", "", "base URL of eki-control's Client API")
	token := fs.String("token", "", "Bearer token")
	projectionPath := fs.String("projection", "", "projection JSON used for the accepted-task probes")
	var inputSpecs repeatedFlag
	fs.Var(&inputSpecs, "input", "input bytes as name=path (repeatable)")
	out := fs.String("out", "", "write the probe report here (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *token == "" || *projectionPath == "" {
		return errors.New("probe: -base, -token and -projection are required")
	}
	raw, err := os.ReadFile(*projectionPath)
	if err != nil {
		return err
	}
	var p teideleg.Projection
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("decode projection: %w", err)
	}
	inputs := map[string][]byte{}
	for _, spec := range inputSpecs {
		name, path, ok := strings.Cut(spec, "=")
		if !ok || name == "" || path == "" {
			return fmt.Errorf("-input %q is not name=path", spec)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read input %q: %w", name, err)
		}
		inputs[name] = body
	}
	inputBytes := func(in teideleg.Input) ([]byte, error) {
		body, ok := inputs[in.Name]
		if !ok {
			return nil, fmt.Errorf("probe: input %q was not supplied with -input", in.Name)
		}
		return body, nil
	}
	client := &teideleg.Client{BaseURL: *base, Token: *token}
	ctx := context.Background()
	report := probeReport{Schema: "tei.eki-delegation-probe/v0", BaseURL: *base, RanAt: time.Now().UTC()}

	add := func(r probeResult) {
		if r.Passed {
			report.PassCount++
		} else {
			report.FailCount++
		}
		report.Results = append(report.Results, r)
	}

	// P1: an input whose body was never uploaded is refused at admission.
	{
		missing := p.Task
		missing.Inputs = []teideleg.ArtifactReference{{Name: "workspace", ArtifactID: strings.Repeat("0", 64)}}
		missing.ClientRequestID = ""
		_, err := client.SubmitTask(ctx, probeKey(p.IdempotencyKey, "p1"), missing)
		// The implementation answers with the resolver's code, not with the
		// policy's ARTIFACT_NOT_AVAILABLE. Both mean "the input cannot be
		// resolved", and neither is declared as an enum in openapi.json.
		add(expectCode("input not uploaded", "422 INPUT_ARTIFACT_UNRESOLVABLE", err, 422, "INPUT_ARTIFACT_UNRESOLVABLE"))
	}

	// P2: the declared expected_digest is not enforced. The bytes are named by
	// their measured digest, whatever the declaration said. TEI must compare
	// the returned artifact_id with what it intended to send; the adapter does,
	// and the Task that names the intended digest is then refused.
	if len(p.Operation.Inputs) > 0 && len(p.Ingests) > 0 {
		in := p.Operation.Inputs[0]
		resp, err := client.CreateIngestIntent(ctx, probeKey(p.IdempotencyKey, "p2"), p.Ingests[0])
		if err == nil {
			wrong := []byte("bytes that are not the declared artifact")
			accepted, werr := client.UploadArtifact(ctx, resp.Upload, wrong)
			got := ""
			if werr == nil {
				got = accepted.Artifact.ArtifactID
			}
			add(probeResult{
				Name:     "declared expected_digest is not enforced",
				Expected: "202, artifact_id equals the declared digest",
				Observed: fmt.Sprintf("202 accepted with artifact_id=%s (declared %s)", got, in.SHA256Hex),
				Passed:   werr == nil && got == in.SHA256Hex,
			})
			_, _, gerr := client.GetArtifact(ctx, in.SHA256Hex)
			add(probeResult{
				Name: "failed upload leaves no body", Expected: "404 ARTIFACT_NOT_FOUND",
				Observed: describe(gerr), Passed: teideleg.IsCode(gerr, "ARTIFACT_NOT_FOUND") || teideleg.StatusOf(gerr) == 404,
			})
		} else {
			add(probeResult{Name: "declared expected_digest is not enforced", Expected: "ingest session", Observed: describe(err), Passed: false})
		}

		// P3: a session is single-use; a second upload is not a resume.
		if body, berr := inputBytes(in); berr == nil {
			resp, err := client.CreateIngestIntent(ctx, probeKey(p.IdempotencyKey, "p3"), p.Ingests[0])
			if err == nil {
				_, firstErr := client.UploadArtifact(ctx, resp.Upload, body)
				add(probeResult{Name: "first upload", Expected: "202", Observed: describe(firstErr), Passed: firstErr == nil})
				_, secondErr := client.UploadArtifact(ctx, resp.Upload, body)
				add(expectStatus("second upload to the same session", "404 (no resume support)", secondErr, 404))
			}
		}
	}

	// P4: the same key with the same body converges; the same key with a
	// different body is refused.
	if len(p.Operation.Inputs) > 0 && len(p.Ingests) > 0 {
		in := p.Operation.Inputs[0]
		body, berr := inputBytes(in)
		if berr != nil {
			return berr
		}
		if resp, err := client.CreateIngestIntent(ctx, probeKey(p.IdempotencyKey, "p4i"), p.Ingests[0]); err == nil {
			if _, uerr := client.UploadArtifact(ctx, resp.Upload, body); uerr == nil {
				key := probeKey(p.IdempotencyKey, "p4")
				first, ferr := client.SubmitTask(ctx, key, p.Task)
				second, serr := client.SubmitTask(ctx, key, p.Task)
				add(probeResult{
					Name: "same key and same body", Expected: "202 replayed=true, same task_id",
					Observed: fmt.Sprintf("first=%s second=%s replayed=%v", describe(ferr), describe(serr), second.Replayed),
					Passed:   ferr == nil && serr == nil && second.Replayed && first.TaskID == second.TaskID,
				})
				changed := p.Task
				changed.Priority = "background"
				_, cerr := client.SubmitTask(ctx, key, changed)
				add(expectCode("same key with a different body", "409 IDEMPOTENCY_KEY_CONFLICT", cerr, 409, "IDEMPOTENCY_KEY_CONFLICT"))
			}
		}
	}

	// P5, P6: a Task with a short deadline is not moved to a terminal state
	// when the deadline passes; cancellation is what ends it.
	noInput := p.Task
	noInput.Inputs = []teideleg.ArtifactReference{}
	noInput.Deadline = time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	noInput.ClientRequestID = ""
	if accepted, err := client.SubmitTask(ctx, probeKey(p.IdempotencyKey, "p5"), noInput); err == nil {
		time.Sleep(4 * time.Second)
		st, serr := client.GetTaskStatus(ctx, accepted.TaskID)
		add(probeResult{
			Name: "deadline passes without dispatch", Expected: "still queued, no terminal_code",
			Observed: fmt.Sprintf("status=%s terminal=%q err=%s", st.Status, st.TerminalCode, describe(serr)),
			Passed:   serr == nil && st.Status == "queued" && st.TerminalCode == "",
		})
		if cerr := client.CancelTask(ctx, probeKey(p.IdempotencyKey, "p5c"), accepted.TaskID); cerr != nil {
			add(probeResult{Name: "cancel", Expected: "202", Observed: describe(cerr), Passed: false})
		} else {
			st, _ := client.GetTaskStatus(ctx, accepted.TaskID)
			add(probeResult{
				Name: "cancel moves the task to terminal", Expected: "cancelled/CANCELLED",
				Observed: fmt.Sprintf("%s/%s", st.Status, st.TerminalCode),
				Passed:   st.Status == "cancelled" && st.TerminalCode == "CANCELLED",
			})
			_, rerr := client.GetTaskResult(ctx, accepted.TaskID)
			add(expectCode("result after cancel", "409 RESULT_NOT_AVAILABLE", rerr, 409, "RESULT_NOT_AVAILABLE"))
		}
	} else {
		add(probeResult{Name: "deadline probe submission", Expected: "202", Observed: describe(err), Passed: false})
	}

	// P7: with no accepted result, the result route says "not ready", not
	// "not found" and not an empty 200.
	future := p.Task
	future.Inputs = []teideleg.ArtifactReference{}
	future.Deadline = time.Now().UTC().Add(60 * time.Second).Format(time.RFC3339)
	future.ClientRequestID = ""
	if accepted, err := client.SubmitTask(ctx, probeKey(p.IdempotencyKey, "p7"), future); err == nil {
		_, rerr := client.GetTaskResult(ctx, accepted.TaskID)
		add(expectCode("result before acceptance", "409 RESULT_NOT_READY", rerr, 409, "RESULT_NOT_READY"))
	} else {
		add(probeResult{Name: "result before acceptance", Expected: "202", Observed: describe(err), Passed: false})
	}

	// P8: a deadline that has already passed is refused at admission. This is
	// a separate check from "the deadline passes later", which does not end
	// the Task (P5).
	past := future
	past.Deadline = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	past.ClientRequestID = ""
	_, derr := client.SubmitTask(ctx, probeKey(p.IdempotencyKey, "p8"), past)
	add(expectCode("deadline already passed", "422 DEADLINE_IN_PAST", derr, 422, "DEADLINE_IN_PAST"))

	// P9: an artifact that does not exist is not distinguishable from one that
	// belongs to someone else.
	_, _, aerr := client.GetArtifact(ctx, strings.Repeat("1", 64))
	add(expectStatus("unknown artifact", "404", aerr, 404))

	if err := writeJSON(*out, report); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "probe: %d passed, %d did not match the contract\n", report.PassCount, report.FailCount)
	// A mismatch is a finding, not a reason to hide the rest of the run: the
	// report is written and the caller records it.
	return nil
}

func expectCode(name, expected string, err error, status int, code string) probeResult {
	r := probeResult{Name: name, Expected: expected, Observed: describe(err)}
	r.Passed = teideleg.StatusOf(err) == status && teideleg.IsCode(err, code)
	return r
}

func expectStatus(name, expected string, err error, status int) probeResult {
	r := probeResult{Name: name, Expected: expected, Observed: describe(err)}
	r.Passed = teideleg.StatusOf(err) == status
	return r
}

func describe(err error) string {
	if err == nil {
		return "ok"
	}
	var e *teideleg.Error
	if errors.As(err, &e) {
		return fmt.Sprintf("%d %s", e.Status, e.Problem.Code)
	}
	return err.Error()
}

func probeKey(base, tag string) string {
	return "tei-probe:" + teideleg.SHA256Hex([]byte(base + "\x00" + tag))[:40]
}
