package clientapi

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestAnAvailableArtifactCarriesItsIdentifier(t *testing.T) {
	digest := contract.SHA256([]byte("out"))
	got := buildArtifacts(digest, ArtifactMeta{Size: 3, MediaType: "text/plain", ExpiresAt: time.Now().Add(time.Hour)}, true, time.Now())
	if len(got) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(got))
	}
	a := got[0]
	if !a.Available || a.ArtifactID != digest.Hex() || a.SizeBytes != 3 || a.MediaType != "text/plain" {
		t.Fatalf("artifact = %+v", a)
	}
}

func TestAnExpiredArtifactIsListedWithoutAnIdentifier(t *testing.T) {
	digest := contract.SHA256([]byte("out"))
	got := buildArtifacts(digest, ArtifactMeta{Size: 3, ExpiresAt: time.Now().Add(-time.Hour)}, true, time.Now())
	if len(got) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(got))
	}
	if got[0].Available || got[0].ArtifactID != "" {
		t.Fatalf("artifact = %+v, want unavailable without id", got[0])
	}
	if got[0].SizeBytes != 3 {
		t.Fatalf("size = %d, want the recorded size", got[0].SizeBytes)
	}
}

func TestAnArtifactWithNoRecordIsStillListed(t *testing.T) {
	digest := contract.SHA256([]byte("out"))
	got := buildArtifacts(digest, ArtifactMeta{}, false, time.Now())
	if len(got) != 1 || got[0].Available || got[0].ArtifactID != "" {
		t.Fatalf("artifacts = %+v", got)
	}
	if got[0].Sha256.Value != digest.Hex() {
		t.Fatalf("sha256 = %q", got[0].Sha256.Value)
	}
}

func TestAnAbsentDigestProducesAnEmptyList(t *testing.T) {
	got := buildArtifacts(contract.Digest{}, ArtifactMeta{}, false, time.Now())
	if len(got) != 0 {
		t.Fatalf("artifacts = %+v, want none", got)
	}
}
