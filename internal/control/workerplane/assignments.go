package workerplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hat47x/eki-fabric/internal/spi/statestore"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// This file is the assignment route of the process-level TEST PROFILE: the
// authenticated Worker asks for the signed Lease and WorkOrder of the Attempts
// bound to it. It is not the Worker Protocol (#198).

// ErrInvalidAssignmentConfig is returned for an incomplete AssignmentService.
var ErrInvalidAssignmentConfig = errors.New("invalid assignment service configuration")

// AssignmentService serves the EMITTED Lease and WorkOrder bytes of one Worker
// from the State Store. It re-signs nothing and changes nothing: the same call
// returns the same bytes until the Attempt moves on.
type AssignmentService struct {
	store statestore.AssignmentStore
	limit int
}

var _ workerplane.AssignmentSource = (*AssignmentService)(nil)

// NewAssignmentService builds the service over a State Store.
func NewAssignmentService(store statestore.AssignmentStore) (*AssignmentService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: state store is required", ErrInvalidAssignmentConfig)
	}
	return &AssignmentService{store: store, limit: statestore.MaxAssignmentsPerList}, nil
}

// ListAssignments returns the assignments of the principal's Worker only. A
// record whose recipient is not that Worker is a store invariant violation and
// fails the call instead of being passed on.
func (s *AssignmentService) ListAssignments(ctx context.Context, principal workerplane.Principal) ([]workerplane.Assignment, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("%w: service is not initialized", ErrInvalidAssignmentConfig)
	}
	if principal.WorkerIdentity == "" {
		return nil, fmt.Errorf("%w: Worker identity is required", ErrInvalidAssignmentConfig)
	}
	list, err := s.store.ListEmittedAssignments(ctx, principal.WorkerIdentity, s.limit)
	if err != nil {
		return nil, err
	}
	out := make([]workerplane.Assignment, 0, len(list))
	for _, a := range list {
		if a.WorkOrder.WorkerIdentity() != principal.WorkerIdentity {
			return nil, errors.New("assignment store returned another Worker's record")
		}
		out = append(out, workerplane.Assignment{
			AttemptID: a.AttemptID.String(), LeaseID: a.LeaseID.String(),
			LeaseEnvelope: a.Lease.SignedEnvelope(), WorkOrderEnvelope: a.WorkOrder.SignedEnvelope(),
		})
	}
	return out, nil
}

// serveAssignments handles an authenticated assignments request. The principal
// comes from the certificate; the body is only checked to be the empty object.
func (h *mtlsHandler) serveAssignments(w http.ResponseWriter, r *http.Request, principal workerplane.Principal) {
	if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != testprofile.ContentType {
		reply(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reply(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		reply(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := testprofile.DecodeAssignmentsRequest(body); err != nil {
		reply(w, http.StatusBadRequest, "invalid_request")
		return
	}
	list, err := h.assignments.ListAssignments(r.Context(), principal)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reply(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		h.log.Error("worker assignment listing failed", "worker", principal.WorkerIdentity, "error", err.Error())
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	out, err := testprofile.EncodeAssignments(list)
	if err != nil {
		reply(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", testprofile.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}
