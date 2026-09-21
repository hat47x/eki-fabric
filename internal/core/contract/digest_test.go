package contract

import (
	"errors"
	"testing"
)

func TestSHA256RoundTrip(t *testing.T) {
	d := SHA256([]byte("eki"))
	if d.Algorithm() != "sha256" {
		t.Fatalf("Algorithm() = %q", d.Algorithm())
	}
	parsed, err := ParseSHA256(d.Hex())
	if err != nil {
		t.Fatalf("ParseSHA256() error = %v", err)
	}
	if parsed != d {
		t.Fatalf("parsed = %v, want %v", parsed, d)
	}
}

func TestParseSHA256RejectsMalformedValue(t *testing.T) {
	_, err := ParseSHA256("not-a-digest")
	if !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("error = %v, want ErrInvalidDigest", err)
	}
}
