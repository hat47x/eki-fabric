package clientapi

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestADeclaredDataClassIsServed(t *testing.T) {
	digest := contract.SHA256([]byte("out"))
	got := buildArtifacts(digest, ArtifactMeta{Size: 3, DataClass: "internal", ExpiresAt: time.Now().Add(time.Hour)}, true, time.Now())
	if len(got) != 1 || got[0].DataClass != "internal" {
		t.Fatalf("artifacts = %+v", got)
	}
}

func TestAnUndeclaredDataClassIsNotServed(t *testing.T) {
	digest := contract.SHA256([]byte("out"))
	got := buildArtifacts(digest, ArtifactMeta{Size: 3, ExpiresAt: time.Now().Add(time.Hour)}, true, time.Now())
	if len(got) != 1 {
		t.Fatalf("artifacts = %+v", got)
	}
	if got[0].DataClass != "" {
		t.Fatalf("data_class = %q, want empty: an undeclared class must not look classified", got[0].DataClass)
	}
}
