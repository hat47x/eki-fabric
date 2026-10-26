// Package workerregistry defines the durable Worker registry and the
// enrollment credentials that create its entries (ADR-0027 sections 2.2 to
// 2.4).
//
// The registry answers one question for the Control Plane: which Worker
// identities exist, in which trust domain, and may each still act. It does not
// say that a Worker's claims are true. Authentication only establishes whose
// claim it is (RQ-019, RQ-020, SEC-003).
//
// This package holds the records and their transition rules. It has no storage
// and no clock. The State Store adapters call these rules inside their own
// transaction, so the memory and SQLite adapters cannot drift apart.
package workerregistry

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/federation"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var (
	ErrInvalidWorker = errors.New("invalid worker record")
	// ErrInvalidTransition is returned for a state change the registry never
	// allows, for example any change out of revoked.
	ErrInvalidTransition = errors.New("invalid worker state transition")
	// ErrWorkerRevoked is returned when a revoked Worker is asked to change.
	// Revoked is terminal: an identity that was revoked is not enabled again
	// and is never issued to another Worker.
	ErrWorkerRevoked = errors.New("worker is revoked")
)

// State is the lifecycle state of a registered Worker.
type State string

const (
	// StateRegistered is the only state in which a Worker may act.
	StateRegistered State = "registered"
	// StateDisabled stops the Worker. An administrator can enable it again.
	StateDisabled State = "disabled"
	// StateRevoked is terminal.
	StateRevoked State = "revoked"
)

func (s State) valid() bool {
	return s == StateRegistered || s == StateDisabled || s == StateRevoked
}

const (
	// MaxActorBytes bounds the recorded name of whoever changed a state.
	MaxActorBytes = 256
	// MaxCertificateSerialBytes bounds the recorded certificate serial.
	MaxCertificateSerialBytes = 128
)

// Certificate is the bookkeeping the CA stage (stage 2) keeps for the Worker's
// current client certificate. It is empty until a certificate is issued.
type Certificate struct {
	// Serial is the serial number of the current certificate, as the CA wrote
	// it. Hex text.
	Serial string
	// NotAfter is when the current certificate stops being valid.
	NotAfter time.Time
}

// Worker is one entry of the registry.
type Worker struct {
	// Identity is issued by the registry. It is the SAN component of the
	// Worker's certificate (ADR-0027 section 2.3).
	Identity    ids.WorkerIdentity
	TrustDomain string
	State       State
	// Revision starts at 1 and grows by one on every change.
	Revision uint64
	// CredentialID is the enrollment credential that created this Worker. One
	// credential creates at most one Worker.
	CredentialID ids.EnrollmentCredentialID
	// StateChangedBy names who made the latest state change: an administrator,
	// "admin:<id>", or "worker:<identity>" for a Worker revoking its own identity. Empty until the first
	// change.
	StateChangedBy string
	// Certificate is the zero value until stage 2 records one.
	Certificate Certificate
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Usable reports whether the Worker may act. Everything but registered fails
// closed.
func (w Worker) Usable() bool {
	return w.State == StateRegistered
}

func (w Worker) Validate() error {
	if _, err := ids.ParseWorkerIdentity(string(w.Identity)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWorker, err)
	}
	if err := ValidateTrustDomain(w.TrustDomain); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWorker, err)
	}
	if !w.State.valid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidWorker, w.State)
	}
	if w.Revision < 1 {
		return fmt.Errorf("%w: revision must be at least 1", ErrInvalidWorker)
	}
	if _, err := ids.ParseEnrollmentCredentialID(string(w.CredentialID)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWorker, err)
	}
	if len(w.StateChangedBy) > MaxActorBytes {
		return fmt.Errorf("%w: state changer is longer than %d bytes", ErrInvalidWorker, MaxActorBytes)
	}
	if w.Revision == 1 && (w.State != StateRegistered || w.StateChangedBy != "" || w.Certificate.Serial != "") {
		return fmt.Errorf("%w: a new worker is registered and unchanged", ErrInvalidWorker)
	}
	if (w.Certificate.Serial == "") != w.Certificate.NotAfter.IsZero() {
		return fmt.Errorf("%w: certificate serial and not-after are set together", ErrInvalidWorker)
	}
	if len(w.Certificate.Serial) > MaxCertificateSerialBytes {
		return fmt.Errorf("%w: certificate serial is longer than %d bytes", ErrInvalidWorker, MaxCertificateSerialBytes)
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: created and updated times are required", ErrInvalidWorker)
	}
	if w.UpdatedAt.Before(w.CreatedAt) {
		return fmt.Errorf("%w: updated time precedes created time", ErrInvalidWorker)
	}
	return nil
}

// ValidateTrustDomain applies the canonical trust domain rule of the
// federation package, so a name that cannot appear in a certificate URI never
// reaches the registry.
func ValidateTrustDomain(name string) error {
	if _, err := federation.ParseTrustDomain(name); err != nil {
		return err
	}
	return nil
}

// NewWorker returns the registered Worker that consuming credential creates.
// at is the consumption time.
func NewWorker(identity ids.WorkerIdentity, credential Credential, at time.Time) (Worker, error) {
	w := Worker{
		Identity:     identity,
		TrustDomain:  credential.TrustDomain,
		State:        StateRegistered,
		Revision:     1,
		CredentialID: credential.ID,
		CreatedAt:    at.UTC(),
		UpdatedAt:    at.UTC(),
	}
	if err := w.Validate(); err != nil {
		return Worker{}, err
	}
	return w, nil
}

// Transition returns the Worker after moving to state to at time now, recorded
// as made by actor.
//
// The allowed moves are registered to disabled, disabled to registered, and
// either of those to revoked. Revoked never changes; asking to revoke an
// already revoked Worker is reported as ErrWorkerRevoked, and the caller
// decides whether that is an error to surface. Moving to the current state is
// a no-op that returns the Worker unchanged.
//
// The update time never moves backwards: the wall clock can step back (NTP,
// VM sync, a restart on another host), and a record whose update time precedes
// what it already carries would fail Validate.
func (w Worker) Transition(to State, actor string, now time.Time) (Worker, error) {
	if !to.valid() {
		return Worker{}, fmt.Errorf("%w: unknown target state %q", ErrInvalidTransition, to)
	}
	if actor == "" || len(actor) > MaxActorBytes {
		return Worker{}, fmt.Errorf("%w: actor must be 1..%d bytes", ErrInvalidTransition, MaxActorBytes)
	}
	if w.State == StateRevoked {
		return Worker{}, fmt.Errorf("%w: %s", ErrWorkerRevoked, w.Identity)
	}
	if to == w.State {
		return w, nil
	}
	next := w
	next.State = to
	next.StateChangedBy = actor
	next.Revision++
	next.UpdatedAt = LaterOf(now, w.UpdatedAt)
	return next, next.Validate()
}

// WithCertificate returns the Worker with its current certificate bookkeeping
// replaced. A revoked Worker gets no new certificate. Disabled Workers may
// still carry one: stage 2 decides whether to issue, and the registry state is
// what stops the Worker acting.
func (w Worker) WithCertificate(cert Certificate, now time.Time) (Worker, error) {
	if w.State == StateRevoked {
		return Worker{}, fmt.Errorf("%w: %s", ErrWorkerRevoked, w.Identity)
	}
	if cert.Serial == "" || cert.NotAfter.IsZero() {
		return Worker{}, fmt.Errorf("%w: certificate serial and not-after are required", ErrInvalidWorker)
	}
	next := w
	next.Certificate = Certificate{Serial: cert.Serial, NotAfter: cert.NotAfter.UTC()}
	next.Revision++
	next.UpdatedAt = LaterOf(now, w.UpdatedAt)
	return next, next.Validate()
}

// LaterOf returns the later of two times. Records use it so an update time
// never precedes the time the record already carries.
func LaterOf(now, recorded time.Time) time.Time {
	if recorded.After(now) {
		return recorded.UTC()
	}
	return now.UTC()
}
