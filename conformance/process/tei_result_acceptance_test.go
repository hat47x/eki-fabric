package process

// WP-5: the result is accepted, and the Client API answers it.
//
// The acceptance is injected by the test, not by the deployment. EKI's
// resultacceptance service is wired into claimaccept (PR #822), but with no
// independent verifiers the default policy holds the Attempt in verifying
// forever: the default is RA2 with threshold 2 over the principal and operator
// dimensions, and defaultProducers returns the Worker identity for both, so one
// claim cannot reach a quorum (#242). The test therefore calls Accept itself
// with two independent verifications, exactly as the existing
// TestAgentExecutesAndSubmitsResultClaim does.
//
// What this proves: given the verifications, EKI records an AcceptedResult and
// the public result route answers 200 with the accepted digest. What it does
// not prove: that an independent verifier exists, or that the deployment would
// accept the same claim without the test's input. Do not read it as a claim of
// independent verification.

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
)

func TestTEIValidationResultIsAcceptedAndReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixed command is a POSIX wrapper; see #312")
	}
	teiEnvironment(t)
	run := runTEIValidation(t)

	// D3: an AcceptedResult exists, created from the Agent's claim and two
	// independent verifications supplied by the test.
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
		t.Fatalf("result outcome = %s, want accepted: %+v", decision.Outcome(), decision.Rejected)
	}
	if got := decision.Accepted.OutputDigest().Hex(); got != run.expected {
		t.Fatalf("the AcceptedResult recorded %s, TEI computes %s", got, run.expected)
	}
	t.Logf("D3: AcceptedResult recorded %s for attempt %s", decision.Accepted.OutputDigest().Hex(), attemptID)

	// D4: the public route answers 200, and the digest it names is the one TEI
	// computed locally.
	status, body := run.resultBody(t)
	if status != 200 {
		t.Fatalf("GET result = %d: %s", status, body)
	}
	var result struct {
		Schema    string `json:"schema"`
		TaskID    string `json:"task_id"`
		Artifacts []struct {
			SHA256 struct {
				Value string `json:"value"`
			} `json:"sha256"`
			SizeBytes  int64  `json:"size_bytes"`
			Available  bool   `json:"available"`
			ArtifactID string `json:"artifact_id"`
		} `json:"artifacts"`
		OutputDigest *struct {
			Value string `json:"value"`
		} `json:"output_digest"`
	}
	must(t, json.Unmarshal(body, &result))
	if result.Schema != "eki.task-result/v1" {
		t.Errorf("result schema = %q", result.Schema)
	}
	if result.TaskID != run.taskID.String() {
		t.Errorf("result task = %q, want %q", result.TaskID, run.taskID)
	}
	if result.OutputDigest == nil || result.OutputDigest.Value != run.expected {
		t.Errorf("output_digest = %+v, want %s", result.OutputDigest, run.expected)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(result.Artifacts))
	}
	artifact := result.Artifacts[0]
	if artifact.SHA256.Value != run.expected {
		t.Errorf("artifact digest = %s, want %s", artifact.SHA256.Value, run.expected)
	}
	if !artifact.Available || artifact.ArtifactID == "" {
		t.Fatalf("the accepted artifact is not fetchable: %+v", artifact)
	}

	// The body TEI would use must re-hash to the name EKI gave it.
	code, raw := fetchClientArtifact(t, run, artifact.ArtifactID)
	if code != 200 {
		t.Fatalf("artifact fetch = %d: %s", code, raw)
	}
	if digestOf(raw) != artifact.SHA256.Value {
		t.Fatalf("the fetched body hashes to %s, the result names %s", digestOf(raw), artifact.SHA256.Value)
	}
	if size := int64(len(raw)); size != artifact.SizeBytes {
		t.Errorf("fetched %d bytes, the result declares %d", size, artifact.SizeBytes)
	}
	t.Logf("D4: GET result = 200, output_digest = %s, body re-hashes to its name", run.expected)

	task, err := store.LoadTask(context.Background(), run.taskID)
	must(t, err)
	if task.Phase != taskstate.TaskPhaseTerminal || task.TerminalOutcome != taskstate.TerminalOutcomeCompleted {
		t.Fatalf("task phase = %s outcome = %s, want terminal/completed", task.Phase, task.TerminalOutcome)
	}

	// The wrapper's own report proves the operation ran and exited 0. The
	// digest alone cannot: a failed run also produces output bytes, and those
	// bytes would match the expectation.
	reportRaw, err := os.ReadFile(run.report)
	must(t, err)
	if !strings.Contains(string(reportRaw), "exit=0") {
		t.Fatalf("the TEI operation failed inside the wrapper: %s", strings.TrimSpace(string(reportRaw)))
	}
}

func fetchClientArtifact(t *testing.T, run teiRun, artifactID string) (int, []byte) {
	t.Helper()
	return run.get(t, "/v1/artifacts/"+artifactID)
}
