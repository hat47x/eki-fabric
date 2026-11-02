package clientapi

import (
	"net/http"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/measurement"
)

func TestAnAuthFailureIsCounted(t *testing.T) {
	c := measurement.New()
	h := (&Handler{}).WithCounters(c)
	h.countRejection(nil, http.StatusUnauthorized)
	if got := c.Reasons(measurement.AdmissionRejected)["auth"]; got != 1 {
		t.Fatalf("auth = %d, want 1", got)
	}
}

func TestADuplicateIsCountedAsADuplicate(t *testing.T) {
	c := measurement.New()
	h := (&Handler{}).WithCounters(c)
	h.countRejection(nil, http.StatusConflict)
	if got := c.Reasons(measurement.AdmissionRejected)["duplicate"]; got != 1 {
		t.Fatalf("duplicate = %d, want 1", got)
	}
}

func TestALimitIsCountedAgainstTheRequester(t *testing.T) {
	c := measurement.New()
	h := (&Handler{}).WithCounters(c)
	h.countRejection(nil, http.StatusTooManyRequests)
	if got := c.Reasons(measurement.AdmissionRejected)["limit"]; got != 1 {
		t.Fatalf("limit = %d, want 1", got)
	}
}

func TestAnInternalFailureIsNotCounted(t *testing.T) {
	c := measurement.New()
	h := (&Handler{}).WithCounters(c)
	h.countRejection(nil, http.StatusInternalServerError)
	if got := c.Total(measurement.AdmissionRejected); got != 0 {
		t.Fatalf("count = %d, want 0: an internal failure is EKI's fault, not a refusal", got)
	}
}

func TestAHandlerWithoutCountersIsHarmless(t *testing.T) {
	h := &Handler{}
	h.countRejection(nil, http.StatusUnauthorized)
}
