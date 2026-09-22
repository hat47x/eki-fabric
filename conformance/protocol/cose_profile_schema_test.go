package protocol

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

func coseProfileCDDL(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "decisions", "v1", "cose-profile.cddl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read COSE profile CDDL: %v", err)
	}
	return string(raw)
}

func TestCOSEProfileSchemaUsesTaggedSign1(t *testing.T) {
	schema := coseProfileCDDL(t)
	want := "#6." + strconv.FormatUint(decisiontrust.COSESign1Tag, 10) + "(["
	if !strings.Contains(schema, want) {
		t.Fatalf("COSE profile must require tagged Sign1 %q", want)
	}
	if strings.Contains(schema, "COSE_Sign1_Untagged") {
		t.Fatal("EKI baseline must not admit untagged COSE_Sign1")
	}
}

func TestCOSEProfileSchemaClosesOuterShape(t *testing.T) {
	schema := coseProfileCDDL(t)
	for _, want := range []string{
		"unprotected: {}",
		"payload: bstr",
		"signature: bstr .size 64",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("COSE profile missing %q", want)
		}
	}
	if strings.Contains(schema, "payload: bstr / nil") {
		t.Fatal("EKI baseline must not allow detached payload")
	}
}

func TestCOSEProfileSchemaMatchesHeaderConstants(t *testing.T) {
	schema := coseProfileCDDL(t)
	checks := []string{
		strconv.FormatInt(decisiontrust.COSEHeaderLabelAlgorithm, 10) + ": -8 / -7",
		strconv.FormatInt(decisiontrust.COSEHeaderLabelCritical, 10) + ": [" +
			strconv.FormatInt(decisiontrust.COSEHeaderLabelEKIProfile, 10) + "]",
		strconv.FormatInt(decisiontrust.COSEHeaderLabelKeyID, 10) + ": bstr .size (1.." +
			strconv.Itoa(decisiontrust.MaxKeyIDBytes) + ")",
		strconv.FormatInt(decisiontrust.COSEHeaderLabelEKIProfile, 10) + ": \"" +
			decisiontrust.SignedDecisionProfileV1 + "\"",
	}
	for _, want := range checks {
		if !strings.Contains(schema, want) {
			t.Fatalf("COSE profile missing %q", want)
		}
	}
}

func TestCOSEProfilePrivateLabelStaysInPrivateUseRange(t *testing.T) {
	if decisiontrust.COSEHeaderLabelEKIProfile >= -65536 {
		t.Fatalf(
			"EKI profile label %d must remain below -65536",
			decisiontrust.COSEHeaderLabelEKIProfile,
		)
	}
}

func TestCOSEProfileAlgorithmsMatchDecisionTrust(t *testing.T) {
	for _, algorithm := range []decisiontrust.Algorithm{
		decisiontrust.AlgorithmEdDSA,
		decisiontrust.AlgorithmES256,
	} {
		id, ok := algorithm.COSEIdentifier()
		if !ok {
			t.Fatalf("algorithm %q has no COSE identifier", algorithm)
		}
		roundTrip, ok := decisiontrust.AlgorithmFromCOSEIdentifier(id)
		if !ok || roundTrip != algorithm {
			t.Fatalf("COSE algorithm %d round trip = %q/%v, want %q/true", id, roundTrip, ok, algorithm)
		}
	}
}
