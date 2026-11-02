package trustbundle

import (
	"errors"
	"testing"
	"time"
)

func TestTheFirstStartTakesTheConfiguredVersion(t *testing.T) {
	config := validBundle()
	got, err := Reconcile(config, nil)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.Version != config.Version {
		t.Fatalf("version = %d, want %d", got.Version, config.Version)
	}
}

func TestAStaleConfigurationIsIgnored(t *testing.T) {
	stored := validBundle()
	stored.Version = 3
	stored.NotAfter = stored.NotAfter.Add(48 * time.Hour)
	got, err := Reconcile(validBundle(), []Signed{{Bundle: stored}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("version = %d, want the stored 3", got.Version)
	}
}

func TestAConfigurationAheadOfTheStoreStopsTheStart(t *testing.T) {
	stored := validBundle()
	config := validBundle()
	config.Version = 2
	config.NotAfter = config.NotAfter.Add(24 * time.Hour)
	if _, err := Reconcile(config, []Signed{{Bundle: stored}}); !errors.Is(err, ErrConfigAhead) {
		t.Fatalf("error = %v, want %v", err, ErrConfigAhead)
	}
}

func TestTheSameVersionWithAnotherContentStopsTheStart(t *testing.T) {
	config := validBundle()
	config.Issuers = []Issuer{issuer(7, base, base.Add(365*24*time.Hour))}
	if _, err := Reconcile(config, []Signed{{Bundle: validBundle()}}); !errors.Is(err, ErrConfigDiverged) {
		t.Fatalf("error = %v, want %v", err, ErrConfigDiverged)
	}
}

func TestTheSameVersionAndContentProceeds(t *testing.T) {
	config := validBundle()
	got, err := Reconcile(config, []Signed{{Bundle: validBundle()}})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.Digest() != config.Digest() {
		t.Fatal("the reconciled bundle differs from the configured one")
	}
}
