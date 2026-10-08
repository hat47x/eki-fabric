package federation

import (
	"errors"
	"fmt"
	"testing"
)

func TestReasonCodesAreStableAndDistinct(t *testing.T) {
	want := map[Reason]string{
		ReasonMalformedInput:     "MALFORMED_INPUT",
		ReasonUnknownPurpose:     "UNKNOWN_PURPOSE",
		ReasonNotFederated:       "DOMAIN_NOT_FEDERATED",
		ReasonPurposeNotAllowed:  "PURPOSE_NOT_ALLOWED",
		ReasonDomainRevoked:      "DOMAIN_REVOKED",
		ReasonTrustBundleInvalid: "TRUST_BUNDLE_INVALID",
		ReasonTrustBundleStale:   "TRUST_BUNDLE_STALE",
		ReasonUnknownKey:         "UNKNOWN_KEY",
		ReasonSignatureInvalid:   "SIGNATURE_INVALID",
	}
	seen := make(map[string]bool, len(want))
	for reason, code := range want {
		if string(reason) != code {
			t.Errorf("reason %q has code %q, want %q", string(reason), string(reason), code)
		}
		if seen[code] {
			t.Errorf("code %q is used twice", code)
		}
		seen[code] = true
	}
}

func TestReasonOfReadsTheFirstRejectionInTheChain(t *testing.T) {
	if reason, ok := ReasonOf(nil); ok || reason != "" {
		t.Fatalf("ReasonOf(nil) = %q, %v; want empty and false", reason, ok)
	}
	if reason, ok := ReasonOf(errors.New("plain error")); ok || reason != "" {
		t.Fatalf("ReasonOf(plain error) = %q, %v; want empty and false", reason, ok)
	}
	var typedNil *Rejection
	if reason, ok := ReasonOf(typedNil); ok || reason != "" {
		t.Fatalf("ReasonOf(typed nil) = %q, %v; want empty and false", reason, ok)
	}

	wrapped := fmt.Errorf("outer context: %w", reject(ReasonUnknownKey, ErrUnknownKey))
	reason, ok := ReasonOf(wrapped)
	if !ok || reason != ReasonUnknownKey {
		t.Fatalf("ReasonOf(wrapped) = %q, %v; want %q and true", reason, ok, ReasonUnknownKey)
	}
	if !errors.Is(wrapped, ErrUnknownKey) {
		t.Fatal("wrapped rejection does not unwrap to its sentinel")
	}
}

func TestRejectionStringAndNilReceiver(t *testing.T) {
	rejection := &Rejection{Reason: ReasonSignatureInvalid, cause: ErrSignatureInvalid}
	if got := rejection.Error(); got != "SIGNATURE_INVALID: "+ErrSignatureInvalid.Error() {
		t.Fatalf("Error() = %q", got)
	}
	bare := &Rejection{Reason: ReasonMalformedInput}
	if got := bare.Error(); got != "MALFORMED_INPUT" {
		t.Fatalf("Error() without cause = %q", got)
	}
	var nilRejection *Rejection
	if nilRejection.Error() == "" || nilRejection.Unwrap() != nil {
		t.Fatal("nil Rejection must not panic and must not unwrap to anything")
	}
}
