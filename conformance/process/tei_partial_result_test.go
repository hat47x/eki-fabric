package process

// WP-11: a result whose body is no longer fetchable.
//
// The Task is accepted normally (WP-5), and then the body's retention is
// brought forward, which is what ADR-0037's retirement does: the record of
// what was produced survives, the bytes do not. The public result then names
// the digest with available=false, and TEI must not call that an accepted
// result: it has nothing to re-hash.
//
// The test does not retire the body through a public route, because this
// revision has none. It writes the metadata the store reads, which is what a
// real retirement does to it. That is a test-side action on a throwaway
// deployment, not a Client capability.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	teideleg "github.com/hat47x/eki-fabric/spikes/tei-eki-minimal-delegation"
)

// resultView is the part of eki.task-result/v1 the test reads. It is decoded
// into a fresh value for every answer: decoding into a reused struct would
// keep fields the new answer omits (artifact_id is omitted when it is not
// fetchable), and then the test would be reading a previous answer.
type resultView struct {
	Schema       string `json:"schema"`
	OutputDigest *struct {
		Value string `json:"value"`
	} `json:"output_digest"`
	Artifacts []struct {
		SHA256 struct {
			Value string `json:"value"`
		} `json:"sha256"`
		SizeBytes  int64  `json:"size_bytes"`
		Available  bool   `json:"available"`
		ArtifactID string `json:"artifact_id"`
	} `json:"artifacts"`
}

func readResult(t *testing.T, run teiRun) resultView {
	t.Helper()
	status, body := run.resultBody(t)
	if status != 200 {
		t.Fatalf("GET result = %d: %s", status, body)
	}
	var view resultView
	must(t, json.Unmarshal(body, &view))
	return view
}

func TestTEIResultWhoseBodyWasRetiredIsNotAccepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX wrapper; see #312")
	}
	teiEnvironment(t)
	run := runTEIValidation(t)
	acceptTEIResult(t, run)

	// Before retirement: the body is fetchable and named by the digest.
	before := readResult(t, run)
	if len(before.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(before.Artifacts))
	}
	artifact := before.Artifacts[0]
	if !artifact.Available || artifact.ArtifactID != artifact.SHA256.Value {
		t.Fatalf("the result is not fetchable before retirement: %+v", artifact)
	}

	// Retirement: the metadata says the body's retention has passed, and the
	// body is removed. A real retirement does both; leaving the bytes would
	// only hide whether the route consults the record.
	metaPath := filepath.Join(run.env.artifactsDir, artifact.SHA256.Value+".meta.json")
	raw, err := os.ReadFile(metaPath)
	must(t, err)
	var meta map[string]any
	must(t, json.Unmarshal(raw, &meta))
	meta["expires_at"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	next, err := json.Marshal(meta)
	must(t, err)
	must(t, os.WriteFile(metaPath, next, 0o600))
	must(t, os.Remove(filepath.Join(run.env.artifactsDir, artifact.SHA256.Value+".body")))

	// After retirement: the record survives, the body is not offered.
	after := readResult(t, run)
	if after.OutputDigest == nil || after.OutputDigest.Value != run.expected {
		t.Errorf("output_digest = %+v, want %s", after.OutputDigest, run.expected)
	}
	if len(after.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(after.Artifacts))
	}
	retired := after.Artifacts[0]
	if retired.Available || retired.ArtifactID != "" {
		t.Fatalf("the retired body is still offered: %+v", retired)
	}
	if retired.SHA256.Value != run.expected || retired.SizeBytes != artifact.SizeBytes {
		t.Errorf("the record of what was produced did not survive: %+v", retired)
	}

	// Fetching it is refused, and the answer does not disclose which case it is.
	if code, body := run.get(t, "/v1/artifacts/"+retired.SHA256.Value); code != 410 && code != 404 {
		t.Fatalf("fetching the retired body = %d: %s", code, body)
	}

	// TEI's rule: a result whose bodies cannot be fetched is not accepted.
	receipt := evaluateThroughTheAPI(t, run)
	if receipt.Outcome == teideleg.OutcomeAccepted {
		t.Fatalf("TEI accepted a result whose body is gone: %+v", receipt)
	}
	t.Logf("after retirement: outcome=%s available=%d fetched=%d",
		receipt.Outcome, receipt.Result.AvailableCount, receipt.Result.FetchedCount)
	for _, line := range receipt.NotChecked {
		t.Logf("not checked: %s", line)
	}
}

// acceptTEIResult records the AcceptedResult for the run, as WP-5 does. It is
// factored out so WP-11 does not silently depend on a different acceptance.
func acceptTEIResult(t *testing.T, run teiRun) {
	t.Helper()
	store := openState(t, run.env.controlDB)
	defer store.Close()
	policy := resultaccept.Policy{
		Level: resultaccept.LevelRA2, Threshold: 2,
		DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
	}
	acceptance, err := resultacceptance.New(resultacceptance.Config{
		Attempts: store, Tasks: store, Claims: store, Results: store, Rejections: store,
		Policies:  fixedPolicy{policy: resultacceptance.ResultPolicy{Policy: policy, Output: resultaccept.OutputMechanical}},
		Producers: fixedProducers{executorDigest: run.claim.ExecutorDigest().Hex()},
		Clock:     systemClock{},
	})
	must(t, err)
	attemptID := run.claim.AttemptID()
	verification := func(id, principal, operator string) resultaccept.Verification {
		return resultaccept.Verification{
			VerifierID: id, TaskID: run.claim.TaskID().String(), AttemptID: attemptID.String(),
			Check: resultaccept.CheckReproduced, ObservedDigest: run.expected,
			Dimensions: map[resultaccept.Dimension]string{
				resultaccept.DimensionPrincipal: principal, resultaccept.DimensionOperator: operator,
				resultaccept.DimensionExecutorDigest: contract.SHA256([]byte("verifier-" + id)).Hex(),
			},
		}
	}
	decision, err := acceptance.Accept(context.Background(), resultacceptance.Request{
		AttemptID: attemptID,
		Verifications: []resultaccept.Verification{
			verification("tei-verifier-1", "p-1", "o-1"),
			verification("tei-verifier-2", "p-2", "o-2"),
		},
	})
	must(t, err)
	if decision.Outcome() != resultaccept.OutcomeAccepted {
		t.Fatalf("result outcome = %s, want accepted", decision.Outcome())
	}
}

// evaluateThroughTheAPI is the TEI side: read the Task and decide, using the
// spike's own rules. A test that decided the outcome itself would be a second
// implementation of the rule under test.
func evaluateThroughTheAPI(t *testing.T, run teiRun) teideleg.Receipt {
	t.Helper()
	receipt, err := run.teiReceipt(t)
	must(t, err)
	return receipt
}
