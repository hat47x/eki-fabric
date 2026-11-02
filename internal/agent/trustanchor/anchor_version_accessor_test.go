package trustanchor

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func TestAnAnchorWithoutVersionsReportsNone(t *testing.T) {
	var anchor *Anchor
	if _, ok := anchor.BundleVersion(); ok {
		t.Fatal("a nil anchor reported a version")
	}
	if _, ok := (&Anchor{}).BundleVersion(); ok {
		t.Fatal("an anchor without a version reported one")
	}
}

func TestAnAnchorReportsTheNewestVersionItHolds(t *testing.T) {
	newest := trustbundle.Signed{Bundle: trustbundle.Bundle{Version: 3}}
	anchor := &Anchor{version: &newest}
	got, ok := anchor.BundleVersion()
	if !ok {
		t.Fatal("the anchor did not report its version")
	}
	if got.Bundle.Version != 3 {
		t.Fatalf("version = %d, want 3", got.Bundle.Version)
	}
}
