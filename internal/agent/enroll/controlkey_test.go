package enroll

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func TestAControlKeyIsReadFromHexOrBase64(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []string{hex.EncodeToString(pub), base64.StdEncoding.EncodeToString(pub), "  " + hex.EncodeToString(pub) + "\n"} {
		got, err := ParseControlKey(form)
		if err != nil {
			t.Fatalf("ParseControlKey(%q) error = %v", form, err)
		}
		if string(got) != string(pub) {
			t.Fatalf("key = %x, want %x", got, pub)
		}
	}
}

func TestAWrongLengthIsRefusedRatherThanPadded(t *testing.T) {
	short := hex.EncodeToString(make([]byte, 16))
	if _, err := ParseControlKey(short); !errors.Is(err, ErrInvalidControlKey) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidControlKey)
	}
}

func TestEmptyOrGarbageIsRefused(t *testing.T) {
	for _, form := range []string{"", "   ", "zzzz", "not a key"} {
		if _, err := ParseControlKey(form); !errors.Is(err, ErrInvalidControlKey) {
			t.Fatalf("ParseControlKey(%q) error = %v, want %v", form, err, ErrInvalidControlKey)
		}
	}
}
