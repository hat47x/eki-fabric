package statestore

import (
	"context"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

// ConsumeEnrollment asks the store to spend one enrollment credential and
// create the Worker it entitles, in one transaction.
type ConsumeEnrollment struct {
	// SecretSHA256 is the digest of the presented secret.
	SecretSHA256 contract.Digest
	// TrustDomain is the domain the Worker asks to join. It must equal the
	// credential's.
	TrustDomain string
	// Identity is the new Worker's identity. The enrollment service draws it
	// from the random source; no Worker or caller chooses it. The store fails
	// with ErrAlreadyExists if the identity was ever issued, including to a
	// Worker that has since been revoked, and then leaves the credential unspent.
	Identity ids.WorkerIdentity
	// At is the time of the request.
	At time.Time
}

// WorkerRegistryStore keeps the Worker registry and the enrollment credentials
// (ADR-0027 sections 2.2 to 2.4).
//
// Callers outside the enrollment service must not hold this interface: it
// accepts an identity and a time. The service is where identities are
// generated and where the caller's role is checked.
//
// Rules every adapter enforces atomically:
//
//   - A credential is spent by at most one ConsumeEnrollmentCredential, and
//     the spend creates exactly one Worker. Concurrent attempts yield one
//     Worker; the others get a *workerregistry.Refusal.
//   - A refused credential is refused with the same error text whatever the
//     reason. The reason is available through workerregistry.RefusalReason.
//   - Worker identities are never reused and Workers are never deleted.
//   - Revoked is terminal for a Worker. A credential that is consumed or
//     revoked does not change again.
//   - Update times never move backwards.
type WorkerRegistryStore interface {
	// IssueEnrollmentCredential stores a new issued credential. It fails with
	// ErrAlreadyExists if the ID or the secret digest is already stored.
	IssueEnrollmentCredential(ctx context.Context, credential workerregistry.Credential) error
	// LoadEnrollmentCredential returns the credential or ErrNotFound.
	LoadEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID) (workerregistry.Credential, error)
	// RevokeEnrollmentCredential revokes an issued credential and returns it.
	// Revoking a revoked credential returns it unchanged. A consumed one fails
	// with workerregistry.ErrCredentialConsumed. actor is "admin:<id>".
	RevokeEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID, actor string, at time.Time) (workerregistry.Credential, error)
	// ConsumeEnrollmentCredential spends the credential and returns the new
	// registered Worker.
	ConsumeEnrollmentCredential(ctx context.Context, req ConsumeEnrollment) (workerregistry.Worker, error)

	// LoadWorker returns the Worker or ErrNotFound.
	LoadWorker(ctx context.Context, identity ids.WorkerIdentity) (workerregistry.Worker, error)
	// ListWorkers returns every registered Worker in identity order. It backs
	// dispatch, which must see the population it plans over without knowing
	// identities in advance. Revoked Workers are included so a caller that
	// must not act on stale knowledge can see them; Usable() is the caller's
	// filter.
	ListWorkers(ctx context.Context) ([]workerregistry.Worker, error)
	// TransitionWorker applies Worker.Transition and returns the result. A
	// change out of revoked fails with workerregistry.ErrWorkerRevoked.
	TransitionWorker(ctx context.Context, identity ids.WorkerIdentity, to workerregistry.State, actor string, at time.Time) (workerregistry.Worker, error)
	// RecordWorkerCertificate stores the Worker's current certificate
	// bookkeeping (used by the CA, stage 2). A revoked Worker fails with
	// workerregistry.ErrWorkerRevoked.
	RecordWorkerCertificate(ctx context.Context, identity ids.WorkerIdentity, cert workerregistry.Certificate, at time.Time) (workerregistry.Worker, error)
}
