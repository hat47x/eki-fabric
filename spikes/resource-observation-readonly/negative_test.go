package resourceobservationreadonly_test

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/spikes/resource-observation-readonly"
)

func TestObservationRejectsStaleSchema(t *testing.T) {
	now := time.Now()
	obs := resourceobservationreadonly.Observation{
		ProviderID: "P1",
		Schema:     "wrong-schema",
		ObservedAt: now,
		Staleness:  time.Minute,
	}
	if err := obs.Validate(); err == nil {
		t.Fatal("expected validation error for wrong schema, got nil")
	}
}

func TestObservationRejectsZeroTime(t *testing.T) {
	obs := resourceobservationreadonly.Observation{
		ProviderID: "P1",
		Schema:     resourceobservationreadonly.Schema,
		ObservedAt: time.Time{},
		Staleness:  time.Minute,
	}
	if err := obs.Validate(); err == nil {
		t.Fatal("expected validation error for zero observed at, got nil")
	}
}
