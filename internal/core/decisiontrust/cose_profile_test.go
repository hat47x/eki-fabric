package decisiontrust

import (
	"errors"
	"strings"
	"testing"
)

func TestAlgorithmFromCOSEIdentifier(t *testing.T) {
	tests := []struct {
		id   int64
		want Algorithm
	}{
		{-8, AlgorithmEdDSA},
		{-7, AlgorithmES256},
	}
	for _, tt := range tests {
		got, ok := AlgorithmFromCOSEIdentifier(tt.id)
		if !ok || got != tt.want {
			t.Fatalf("AlgorithmFromCOSEIdentifier(%d) = %q/%v, want %q/true", tt.id, got, ok, tt.want)
		}
		roundTrip, ok := got.COSEIdentifier()
		if !ok || roundTrip != tt.id {
			t.Fatalf("COSEIdentifier(%q) = %d/%v, want %d/true", got, roundTrip, ok, tt.id)
		}
	}
	if _, ok := AlgorithmFromCOSEIdentifier(0); ok {
		t.Fatal("unknown COSE algorithm identifier unexpectedly accepted")
	}
}

func TestProtectedHeaderProfileAcceptsBaselineProfiles(t *testing.T) {
	for _, algorithmID := range []int64{-8, -7} {
		gotAlg, gotKey, err := (ProtectedHeaderProfile{
			AlgorithmIdentifier: algorithmID,
			CriticalLabels:      []int64{COSEHeaderLabelEKIProfile},
			KeyID:               []byte("kid-local-1"),
			Profile:             SignedDecisionProfileV1,
		}).Validate()
		if err != nil {
			t.Fatalf("Validate(%d) error = %v", algorithmID, err)
		}
		wantAlg, _ := AlgorithmFromCOSEIdentifier(algorithmID)
		if gotAlg != wantAlg {
			t.Fatalf("algorithm = %q, want %q", gotAlg, wantAlg)
		}
		if gotKey != KeyID("kid-local-1") {
			t.Fatalf("key ID = %q, want kid-local-1", gotKey)
		}
	}
}

func TestProtectedHeaderProfileRejectsUnknownAlgorithm(t *testing.T) {
	_, _, err := (ProtectedHeaderProfile{
		AlgorithmIdentifier: -999,
		CriticalLabels:      []int64{COSEHeaderLabelEKIProfile},
		KeyID:               []byte("kid-local-1"),
		Profile:             SignedDecisionProfileV1,
	}).Validate()
	if !errors.Is(err, ErrInvalidCOSEProfile) {
		t.Fatalf("Validate() error = %v, want ErrInvalidCOSEProfile", err)
	}
}

func TestProtectedHeaderProfileRequiresExactCriticalProfileLabel(t *testing.T) {
	tests := [][]int64{
		nil,
		{},
		{COSEHeaderLabelAlgorithm},
		{COSEHeaderLabelEKIProfile, COSEHeaderLabelAlgorithm},
		{COSEHeaderLabelEKIProfile, COSEHeaderLabelEKIProfile},
	}
	for _, critical := range tests {
		_, _, err := (ProtectedHeaderProfile{
			AlgorithmIdentifier: -8,
			CriticalLabels:      critical,
			KeyID:               []byte("kid-local-1"),
			Profile:             SignedDecisionProfileV1,
		}).Validate()
		if !errors.Is(err, ErrInvalidCOSEProfile) {
			t.Fatalf("critical=%v error = %v, want ErrInvalidCOSEProfile", critical, err)
		}
	}
}

func TestProtectedHeaderProfileRequiresExactProfile(t *testing.T) {
	_, _, err := (ProtectedHeaderProfile{
		AlgorithmIdentifier: -8,
		CriticalLabels:      []int64{COSEHeaderLabelEKIProfile},
		KeyID:               []byte("kid-local-1"),
		Profile:             "eki.signed-decision/v2",
	}).Validate()
	if !errors.Is(err, ErrInvalidCOSEProfile) {
		t.Fatalf("Validate() error = %v, want ErrInvalidCOSEProfile", err)
	}
}

func TestProtectedHeaderProfileUsesKeyRegistryIDRules(t *testing.T) {
	tests := [][]byte{
		nil,
		{},
		[]byte(strings.Repeat("k", MaxKeyIDBytes+1)),
		{0xff},
	}
	for _, kid := range tests {
		_, _, err := (ProtectedHeaderProfile{
			AlgorithmIdentifier: -8,
			CriticalLabels:      []int64{COSEHeaderLabelEKIProfile},
			KeyID:               kid,
			Profile:             SignedDecisionProfileV1,
		}).Validate()
		if !errors.Is(err, ErrInvalidCOSEProfile) {
			t.Fatalf("kid length=%d error = %v, want ErrInvalidCOSEProfile", len(kid), err)
		}
	}
}

func TestCOSEProfileConstantsStayInsideBaselineLimits(t *testing.T) {
	if COSESign1Tag != 18 {
		t.Fatalf("COSESign1Tag = %d, want 18", COSESign1Tag)
	}
	if COSEHeaderLabelEKIProfile >= -65536 {
		t.Fatalf("EKI profile label %d is not in COSE private-use integer range", COSEHeaderLabelEKIProfile)
	}
	if MaxCOSESign1Bytes != 256*1024 {
		t.Fatalf("MaxCOSESign1Bytes = %d, want 256 KiB", MaxCOSESign1Bytes)
	}
	if MaxProtectedHeaderBytes != 4*1024 {
		t.Fatalf("MaxProtectedHeaderBytes = %d, want 4 KiB", MaxProtectedHeaderBytes)
	}
	if MaxKeyIDBytes != 128 {
		t.Fatalf("MaxKeyIDBytes = %d, want 128", MaxKeyIDBytes)
	}
}
