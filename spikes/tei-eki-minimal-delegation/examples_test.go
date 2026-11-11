package teideleg

import (
	"encoding/json"
	"os"
	"testing"
)

// The examples are the shape a follow-up implementer should copy. They are not
// evidence: examples_test.go checks only that every outcome is present and that
// each receipt keeps the invariants the rest of this package relies on.

type receiptExamples struct {
	Schema   string    `json:"schema"`
	Receipts []Receipt `json:"receipts"`
}

func loadReceiptExamples(t *testing.T) receiptExamples {
	t.Helper()
	raw, err := os.ReadFile("examples/receipts.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples receiptExamples
	if err := json.Unmarshal(raw, &examples); err != nil {
		t.Fatal(err)
	}
	return examples
}

func TestExamplesCoverEveryOutcome(t *testing.T) {
	examples := loadReceiptExamples(t)
	if examples.Schema != "tei.eki-delegation-receipt-examples/v0" {
		t.Errorf("schema = %q", examples.Schema)
	}
	seen := map[Outcome]bool{}
	for _, r := range examples.Receipts {
		seen[r.Outcome] = true
	}
	for _, want := range []Outcome{OutcomeAccepted, OutcomeRejected, OutcomeUndetermined} {
		if !seen[want] {
			t.Errorf("no example for outcome %q", want)
		}
	}
}

func TestExampleReceiptsKeepTheInvariants(t *testing.T) {
	for i, r := range loadReceiptExamples(t).Receipts {
		if r.Schema != ReceiptSchema {
			t.Errorf("receipts[%d].schema = %q", i, r.Schema)
		}
		if len(r.Basis) == 0 {
			t.Errorf("receipts[%d] has no basis", i)
		}
		if len(r.NotChecked) == 0 {
			t.Errorf("receipts[%d] has no not_checked; a receipt must say what it could not check", i)
		}
		switch r.Outcome {
		case OutcomeAccepted:
			if r.Admission.Status != 202 {
				t.Errorf("accepted receipt admitted with %d", r.Admission.Status)
			}
			if r.Result.Status != 200 || r.Result.OutputDigest == "" {
				t.Errorf("accepted receipt has result %+v", r.Result)
			}
			if r.Local == nil || r.Local.OutputSHA256 == "" {
				t.Errorf("accepted receipt has no local expectation")
			}
		case OutcomeRejected:
			if r.Admission.Status == 202 && r.Result.Status == 200 {
				t.Errorf("rejected receipt looks like a success: %+v", r)
			}
		case OutcomeUndetermined:
			if r.Result.Status == 200 {
				t.Errorf("undetermined receipt carries a result: %+v", r.Result)
			}
		default:
			t.Errorf("receipts[%d].outcome = %q", i, r.Outcome)
		}
	}
}
