package process

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAControlPlaneAnswersReadinessProbes checks the startup windows of #289
// against the real process: /healthz answers as soon as the listener accepts,
// /readyz follows the listener state, and neither asks for a Bearer token nor
// carries state in the answer.
func TestAControlPlaneAnswersReadinessProbes(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	state := openState(t, e.controlDB)
	policy := seedPolicy(t, state, now)
	seedClaim(t, state, policy, 1, now)
	must(t, state.Close())
	clientAddr := e.writeControlConfig(t)

	control := e.startControl(t)
	defer control.kill()
	base := "http://" + clientAddr
	// The probes answer without authentication on purpose (#289).
	health := base + "/healthz"
	ready := base + "/readyz"

	waitFor(t, "/healthz", func() (bool, string) {
		res, err := http.Get(health)
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("/healthz = %d", res.StatusCode)
		}
		return true, ""
	})

	waitFor(t, "/readyz ready once the Worker listener is up", func() (bool, string) {
		res, err := http.Get(ready)
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("/readyz = %d", res.StatusCode)
		}
		return true, ""
	})

	// The answers carry no state: a fixed short body per probe.
	for _, u := range []string{health, ready} {
		res, err := http.Get(u)
		must(t, err)
		defer res.Body.Close()
		buf := new(strings.Builder)
		body := make([]byte, 512)
		n, _ := res.Body.Read(body)
		buf.Write(body[:n])
		if got := buf.String(); got != "ok\n" {
			t.Fatalf("%s body = %q, want the fixed ok", u, got)
		}
		if res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("%s content type = %q", u, res.Header.Get("Content-Type"))
		}
	}

	// With the Control Plane down, the probes fail to connect at all — the
	// harness waiting for the process sees the difference between a dead
	// process and a live one that is not ready.
	control.kill()
	if _, err := http.Get(health); err == nil {
		t.Fatal("/healthz answered after the process was killed")
	}
}

// TestAReadinessDropsWhenTheWorkerListenerDies checks that /readyz stops
// answering ok when the Worker listener dies, so a harness waiting for the
// window does not submit into a half-configured plane (#289). The Worker
// listener death also closes the Client API (the plane cannot serve claims
// without it), so the observable readiness answer is the failed connection —
// which is the strictest form of "not ready".
func TestAReadinessDropsWhenTheWorkerListenerDies(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	state := openState(t, e.controlDB)
	policy := seedPolicy(t, state, now)
	seedClaim(t, state, policy, 1, now)
	must(t, state.Close())
	clientAddr := e.writeControlConfig(t)

	control := e.startControl(t)
	defer control.kill()
	base := "http://" + clientAddr

	waitFor(t, "/readyz ready", func() (bool, string) {
		res, err := http.Get(base + "/readyz")
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("/readyz = %d", res.StatusCode)
		}
		return true, ""
	})

	// Kill the whole Control Plane: the Worker listener dies with it, and
	// the Client API is closed by the same death. A probe then cannot
	// connect — the harness reads "not serving" instead of a stale ok.
	control.kill()
	if res, err := http.Get(base + "/readyz"); err == nil {
		res.Body.Close()
		t.Fatalf("/readyz answered %d after the process died, want a failed connection", res.StatusCode)
	}
}
