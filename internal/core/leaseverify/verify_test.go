package leaseverify

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

// 署名付きbytesを使う検証は conformance/statestoretest が担う。
// ここでは、設定の不備と呼出し側の入力不備が、ゼロ値のVerifiedと
// 安定した失敗理由で拒否されることだけを確かめる。

func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(zero config) error = %v; want ErrInvalidConfig", err)
	}
}

func TestNilVerifierReturnsZeroVerified(t *testing.T) {
	var v *Verifier
	got, err := v.Verify(Request{Raw: []byte{0x01}, Now: time.Unix(1, 0)})
	reason, ok := decisiontrust.FailureReason(err)
	if !ok || reason != decisiontrust.ReasonSchemaUnsupported {
		t.Fatalf("Verify error = %v; want %s", err, decisiontrust.ReasonSchemaUnsupported)
	}
	if !reflect.DeepEqual(got, Verified{}) || !got.IsZero() || !got.VerifiedAt().IsZero() {
		t.Fatalf("failure returned non-zero Verified: %#v", got)
	}
}

func TestZeroVerifiedIsNotAGrant(t *testing.T) {
	var v Verified
	if !v.IsZero() {
		t.Fatal("zero Verified must report IsZero")
	}
	if v.KeyID() != "" || v.TrustDomain() != "" || v.TrustBundleRevision() != 0 || !v.VerifiedAt().IsZero() {
		t.Fatalf("zero Verified exposes values: %#v", v)
	}
}
