package contribution

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	sentinelProvider   = "provider-sentinel-3c9e41"
	sentinelObserver   = "observer-sentinel-77b2"
	sentinelProvenance = "provenance-sentinel-5d20 lease meter"
)

func sentinelRecord(t *testing.T) Record {
	t.Helper()
	in := testInput(t)
	in.ProviderID = sentinelProvider
	in.Observations = []Observation{controlObservation(sentinelObserver, 120000)}
	in.Provenance = sentinelProvenance
	return mustRecord(t, in)
}

// TestRestrictiveAuditViewOmitsIdentityAndProvenance checks that the default view
// contains none of the sentinel strings, in either its JSON or its Go text form.
func TestRestrictiveAuditViewOmitsIdentityAndProvenance(t *testing.T) {
	rec := sentinelRecord(t)
	view, err := NewAuditView(rec, false)
	if err != nil {
		t.Fatalf("NewAuditView() error = %v", err)
	}

	if view.Full {
		t.Errorf("default view reports Full = true")
	}
	if view.ProviderID != "" || len(view.Observers) != 0 || view.Provenance != "" {
		t.Errorf("restrictive view exposes identity or provenance: %+v", view)
	}
	if view.ObserverCount != 1 || view.Assurance != AssuranceControlObserved {
		t.Errorf("restrictive view lost audit facts: count=%d assurance=%q", view.ObserverCount, view.Assurance)
	}
	if view.ResourceClass != "gpu-class-a" || view.Quantity.Value != 120000 {
		t.Errorf("restrictive view lost the measured quantity: %+v", view)
	}

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := fmt.Sprintf("%+v %#v", view, view)
	for _, sentinel := range []string{sentinelProvider, sentinelObserver, sentinelProvenance} {
		if strings.Contains(string(raw), sentinel) {
			t.Errorf("restrictive JSON contains %q", sentinel)
		}
		if strings.Contains(text, sentinel) {
			t.Errorf("restrictive text form contains %q", sentinel)
		}
	}
}

func TestFullAuditViewIncludesIdentityAndProvenance(t *testing.T) {
	rec := sentinelRecord(t)
	view, err := NewAuditView(rec, true)
	if err != nil {
		t.Fatalf("NewAuditView() error = %v", err)
	}
	if !view.Full {
		t.Errorf("full view reports Full = false")
	}
	if view.ProviderID != sentinelProvider {
		t.Errorf("ProviderID = %q", view.ProviderID)
	}
	if len(view.Observers) != 1 || view.Observers[0].ID != sentinelObserver {
		t.Errorf("Observers = %+v", view.Observers)
	}
	if view.Provenance != sentinelProvenance {
		t.Errorf("Provenance = %q", view.Provenance)
	}
}

func TestAuditViewDigestsMatchTheRecord(t *testing.T) {
	rec := mustRecord(t, testInput(t))
	view, err := NewAuditView(rec, false)
	if err != nil {
		t.Fatalf("NewAuditView() error = %v", err)
	}
	if view.RecordDigest != mustDigest(t, rec).String() {
		t.Errorf("RecordDigest = %q", view.RecordDigest)
	}
	if view.TaskDigest != rec.TaskDigest().String() || view.ResultDigest != rec.ResultDigest().String() {
		t.Errorf("work digests do not match the record")
	}
}
