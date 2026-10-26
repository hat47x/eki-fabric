package workercert

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func TestURIRoundTrip(t *testing.T) {
	id, _ := ids.NewWorkerIdentity()
	u, err := URI("example.org", id)
	if err != nil {
		t.Fatal(err)
	}
	if want := "eki://example.org/worker/" + string(id); u.String() != want {
		t.Fatalf("URI = %s, want %s", u, want)
	}
	td, got, err := ParseURI(u)
	if err != nil || td != "example.org" || got != id {
		t.Fatalf("ParseURI = %q %q %v", td, got, err)
	}
}

func TestParseURIRefusesForeignShapes(t *testing.T) {
	id, _ := ids.NewWorkerIdentity()
	for _, raw := range []string{
		"https://example.org/worker/" + string(id),
		"eki://example.org/client/" + string(id),
		"eki://example.org/admin/alice",
		"eki://example.org/worker/" + string(id) + "/extra",
		"eki://example.org:8443/worker/" + string(id),
		"eki://user@example.org/worker/" + string(id),
		"eki://example.org/worker/" + string(id) + "?x=1",
		"eki://example.org/worker/" + string(id) + "#f",
		"eki://example.org/worker/worker-a",
		"eki://Example.org/worker/" + string(id),
		"eki:///worker/" + string(id),
		"eki://example.org/worker/%77rk_00000000000000000000000000000000",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if _, _, err := ParseURI(u); err == nil {
			t.Errorf("ParseURI(%q) succeeded", raw)
		}
	}
}

func TestRenewAtIsOneThirdBeforeExpiry(t *testing.T) {
	nb := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	na := nb.Add(72 * time.Hour)
	if got, want := RenewAt(nb, na), nb.Add(48*time.Hour); !got.Equal(want) {
		t.Fatalf("RenewAt = %v, want %v", got, want)
	}
}

func TestFingerprint(t *testing.T) {
	fp := Fingerprint([]byte("der"))
	if len(fp) != 64 {
		t.Fatalf("fingerprint = %q", fp)
	}
	for _, in := range []string{fp, strings.ToUpper(fp), "sha256:" + fp, " " + fp + "\n", colons(fp)} {
		got, err := ParseFingerprint(in)
		if err != nil || got != fp {
			t.Fatalf("ParseFingerprint(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "abcd", fp[:63], fp + "00", strings.Repeat("g", 64)} {
		if _, err := ParseFingerprint(in); err == nil {
			t.Fatalf("ParseFingerprint(%q) succeeded", in)
		}
	}
}

func colons(fp string) string {
	var parts []string
	for i := 0; i < len(fp); i += 2 {
		parts = append(parts, fp[i:i+2])
	}
	return strings.Join(parts, ":")
}
