package workercert

import (
	"net/url"
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
