package resourceobservationreadonly_test

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/spikes/resource-observation-readonly"
)

func TestSnapshotStoreReadOnly(t *testing.T) {
	now := time.Now()
	store := resourceobservationreadonly.NewSnapshotStore(func() time.Time { return now })
	store.SetReadOnly()

	obs := resourceobservationreadonly.Observation{
		ProviderID: "P1",
		Schema:     resourceobservationreadonly.Schema,
		ObservedAt: now,
		Staleness:  time.Minute,
	}

	err := store.Set(obs)
	if err == nil {
		t.Fatal("expected error setting on read-only store, got nil")
	}

	if !store.IsFresh(obs) {
		t.Error("expected fresh observation")
	}
}

func TestObservationValidate(t *testing.T) {
	now := time.Now()
	obs := resourceobservationreadonly.Observation{
		ProviderID: "P1",
		Schema:     resourceobservationreadonly.Schema,
		ObservedAt: now,
		Staleness:  time.Minute,
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	bad := resourceobservationreadonly.Observation{
		ProviderID: "",
		Schema:     resourceobservationreadonly.Schema,
		ObservedAt: now,
		Staleness:  time.Minute,
	}
	if err := bad.Validate(); err == nil {
		t.Error("expected validation error for empty provider id")
	}
}
