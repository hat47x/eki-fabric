package artifactstore

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutKeepsOnlyABodyThatMatchesItsClaimedDigest(t *testing.T) {
	s := newStore(t)
	body := []byte("accepted output")

	got, size, err := s.Put(bytes.NewReader(body), digestOf(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != digestOf(body) || size != int64(len(body)) {
		t.Fatalf("Put = %s, %d; want %s, %d", got, size, digestOf(body), len(body))
	}

	f, _, err := s.Open(digestOf(body))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	kept, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, body) {
		t.Fatalf("kept %q; want %q", kept, body)
	}
}

func TestPutRefusesABodyThatDisagreesWithItsClaim(t *testing.T) {
	s := newStore(t)
	body := []byte("what actually arrived")

	_, _, err := s.Put(bytes.NewReader(body), digestOf([]byte("what was claimed")), 0)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Put error = %v; want ErrDigestMismatch", err)
	}
	entries, _ := os.ReadDir(s.Root())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("a refused write left %s behind", e.Name())
		}
	}
}

func TestPutRefusesABodyPastTheCapAndKeepsNothing(t *testing.T) {
	s := newStore(t)
	body := make([]byte, 4096)
	rand.Read(body)
	claim := digestOf(body)

	_, _, err := s.Put(bytes.NewReader(body), claim, 1024)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put error = %v; want ErrTooLarge", err)
	}
	if _, _, err := s.Open(claim); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open error = %v; want ErrNotFound", err)
	}
}

func TestABodyOfExactlyTheMaxBytesIsKept(t *testing.T) {
	s := newStore(t)
	body := make([]byte, 1024)
	rand.Read(body)

	if _, size, err := s.Put(bytes.NewReader(body), digestOf(body), 1024); err != nil || size != 1024 {
		t.Fatalf("Put = %d, %v; want 1024, nil", size, err)
	}
}

func TestTheSameBodyWrittenTwiceIsKeptOnce(t *testing.T) {
	s := newStore(t)
	body := []byte("deterministic output")
	digest := digestOf(body)

	if _, _, err := s.Put(bytes.NewReader(body), digest, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put(bytes.NewReader(body), digest, 0); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.Root())
	if len(entries) != 1 {
		t.Fatalf("%d files kept; want 1", len(entries))
	}
}

func TestABodyIsNeverNamedAfterAnythingButItsDigest(t *testing.T) {
	s := newStore(t)
	body := []byte("body")

	got, _, err := s.Put(bytes.NewReader(body), digestOf(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != digestOf(body) {
		t.Fatalf("Put named the body %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), digestOf(body)+".body")); err != nil {
		t.Fatalf("the body is not under its digest: %v", err)
	}
}

func TestOpenReportsTheKeptSizeSoTheCallerCanRecompute(t *testing.T) {
	s := newStore(t)
	body := make([]byte, 2048)
	rand.Read(body)
	if _, _, err := s.Put(bytes.NewReader(body), digestOf(body), 0); err != nil {
		t.Fatal(err)
	}

	f, size, err := s.Open(digestOf(body))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size != int64(len(body)) {
		t.Fatalf("Open size = %d; want %d", size, len(body))
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != digestOf(body) {
		t.Fatalf("re-computed %s; want %s", got, digestOf(body))
	}
}

func TestAMissingBodyIsNotFoundNotAnErrorToHide(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.Open(digestOf([]byte("never written"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open error = %v; want ErrNotFound", err)
	}
}

func TestADigestThatIsNotHexSHA256IsRejectedBeforeAnyIO(t *testing.T) {
	s := newStore(t)
	body := []byte("body")
	for _, claimed := range []string{"", "abc", strings.ToUpper(digestOf(body)) + "0", "gisterminal"} {
		if _, _, err := s.Put(bytes.NewReader(body), claimed, 0); err == nil || errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("Put(%q) error = %v; want a digest shape rejection", claimed, err)
		}
	}
}
