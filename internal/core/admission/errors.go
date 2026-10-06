package admission

import (
	"fmt"

	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// Kind classifies an admission failure independently of any transport. The
// HTTP layer maps it to a status; nothing in core knows about HTTP.
type Kind int

const (
	KindBadRequest      Kind = iota + 1 // malformed or schema-invalid request
	KindForbidden                       // principal may not do this
	KindNotFound                        // absent, or not disclosed to this principal
	KindConflict                        // idempotency or state conflict
	KindUnprocessable                   // well-formed but not admissible under policy
	KindTooManyRequests                 // quota or rate budget exhausted
	KindInternal                        // our fault; details are not shown to the Client
)

// Error is a Client-facing admission failure. Code is the stable machine
// contract; Detail is human text and never part of it.
type Error struct {
	Kind                 Kind
	Code                 string
	Title                string
	Detail               string
	Retryable            bool
	PolicyReasonCategory string
	TaskID               string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Title)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Title, e.Detail)
}

func badRequest(code, detail string) *Error {
	return &Error{Kind: KindBadRequest, Code: code, Title: "The request is malformed or invalid", Detail: detail}
}

func unprocessable(code, category, detail string) *Error {
	return &Error{
		Kind:                 KindUnprocessable,
		Code:                 code,
		Title:                "The request cannot be admitted under the effective policy",
		Detail:               detail,
		PolicyReasonCategory: category,
	}
}

func notFound() *Error {
	// One answer for "does not exist" and "belongs to someone else", so task
	// IDs cannot be probed (RQ-018).
	return &Error{Kind: KindNotFound, Code: "TASK_NOT_FOUND", Title: "The task was not found"}
}

func internalError() *Error {
	return &Error{Kind: KindInternal, Code: "INTERNAL_ERROR", Title: "The service could not complete the request", Retryable: true}
}

// ValidIdempotencyKey reports whether key is an acceptable Idempotency-Key.
func ValidIdempotencyKey(key string) bool { return statestore.ValidIdempotencyKey(key) }

// InvalidIdempotencyKey is the error for a key that fails ValidIdempotencyKey.
func InvalidIdempotencyKey() *Error {
	return badRequest("IDEMPOTENCY_KEY_INVALID", fmt.Sprintf(
		"Idempotency-Key must be %d..%d characters of A-Z a-z 0-9 . _ : -",
		statestore.MinIdempotencyKeyBytes, statestore.MaxIdempotencyKeyBytes))
}
