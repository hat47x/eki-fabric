package trustanchor

import (
	"errors"
	"testing"
)

func TestAStoreIsRebuiltFromTheHistory(t *testing.T) {
	doc, err := File{}.WithVersion(signedVersion(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	doc, err = doc.WithVersion(signedVersion(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	store, err := StoreFromFile(doc)
	if err != nil {
		t.Fatalf("StoreFromFile() error = %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Bundle.Version != 2 {
		t.Fatalf("version = %d, want 2", current.Bundle.Version)
	}
	if len(store.Chain()) != 2 {
		t.Fatalf("chain = %d, want 2", len(store.Chain()))
	}
}

func TestADocumentWithoutVersionsCannotBuildAStore(t *testing.T) {
	if _, err := StoreFromFile(File{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}

func TestAHistoryThatSkipsAVersionIsRefused(t *testing.T) {
	doc, err := File{}.WithVersion(signedVersion(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	doc.TrustBundleVersions = append(doc.TrustBundleVersions, wireOf(t, signedVersion(t, 3)))
	doc.BundleRevision = 3
	if _, err := StoreFromFile(doc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}
