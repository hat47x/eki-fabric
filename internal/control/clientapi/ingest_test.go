package clientapi

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// validateIngest is the whole check the declaration route will perform. It is
// tested here so the route can be exposed without changing what it does
// (ADR-0029 §5.12).
func TestValidateIngestAcceptsAUsableDeclaration(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	intent, got, err := validateIngest([]byte(`{"sha256":"` + digest.Hex() + `","size_bytes":5,"data_class":"public"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Hex() != digest.Hex() || intent.SizeBytes != 5 {
		t.Fatalf("validateIngest() = %+v, %s", intent, got.Hex())
	}
}

// Everything this deployment cannot honour is refused here, before a body is sent.
func TestValidateIngestRefusesWhatItCannotHonour(t *testing.T) {
	digest := contract.SHA256([]byte("input")).Hex()
	for _, raw := range []string{
		`{"sha256":"not-hex","size_bytes":5}`,
		`{"sha256":"` + digest + `","size_bytes":0}`,
		`{"sha256":"` + digest + `","size_bytes":1048576000}`,
		`{"sha256":"` + digest + `","size_bytes":5,"data_class":"unknown"}`,
		`{"sha256":"` + digest + `","size_bytes":5,"extra":1}`,
		`not json`,
	} {
		if _, _, err := validateIngest([]byte(raw)); err == nil {
			t.Errorf("validateIngest(%s) accepted a declaration it cannot honour", raw)
		}
	}
}
