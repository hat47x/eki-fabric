// Package workerenrollment is the Worker registry's operations: issuing and
// revoking enrollment credentials, enrolling a Worker with one, changing a
// Worker's state, and looking a Worker up (ADR-0027 sections 2.2 and 2.4).
//
// It checks who may do what and draws the random values. The durable rules
// live in workerregistry and in the State Store adapters. This stage has no
// CA, no HTTP endpoint and no mTLS of its own. Stage 2 (Issue #222) adds
// Enroll and Renew, which call a CertificateIssuer that the CA adapter
// implements; the HTTP endpoints and the mTLS authenticator are in
// internal/control/workerplane.
package workerenrollment

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var (
	ErrInvalidConfig  = errors.New("invalid worker enrollment configuration")
	ErrInvalidRequest = errors.New("invalid worker enrollment request")
	// ErrNotAdmin is returned when the caller is not an administrator.
	ErrNotAdmin = errors.New("administrator role is required")
	// ErrNotSelf is returned when a Worker tries to act on another identity
	// (ADR-0027 section 2.4 item 19).
	ErrNotSelf = errors.New("a worker may act only on its own identity")
	// ErrWorkerNotUsable is returned by Authorize for a Worker that is unknown,
	// in another trust domain, disabled or revoked. The caller sees one error
	// for all of them.
	ErrWorkerNotUsable = errors.New("worker is not usable")
)

// ErrNoIssuer is returned by Enroll and Renew when no CertificateIssuer is
// configured.
var ErrNoIssuer = errors.New("no certificate issuer is configured")

const maxIdentityAttempts = 4

// CertificateRequest is what the CA is asked to sign. Identity and TrustDomain
// come from the registry. PublicKey is the only value that comes from the
// Worker (from its CSR).
type CertificateRequest struct {
	Identity    ids.WorkerIdentity
	TrustDomain string
	PublicKey   crypto.PublicKey
	Now         time.Time
}

// IssuedCertificate is a signed client certificate.
type IssuedCertificate struct {
	// DER is the certificate.
	DER []byte
	// Serial is the serial number as hex text.
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
}

// CertificateIssuer signs Worker client certificates. The CA adapter
// (internal/adapters/reference/workerca) implements it; the CA key never
// enters this package.
type CertificateIssuer interface {
	IssueCertificate(CertificateRequest) (IssuedCertificate, error)
}

// Admin is the capability to act as an administrator. Only the administrator
// authenticator constructs it, after it has verified an Admin credential
// (RoleAdmin in clientapi). A Worker certificate or a Client token never yields
// one, so neither can issue credentials or change another Worker (SEC-010).
type Admin struct {
	id string
}

// NewAdmin returns the capability for the authenticated administrator id.
func NewAdmin(id string) (Admin, error) {
	if id == "" || len(id) > workerregistry.MaxActorBytes-len(adminPrefix) {
		return Admin{}, fmt.Errorf("%w: administrator id must be 1..%d bytes", ErrInvalidRequest, workerregistry.MaxActorBytes-len(adminPrefix))
	}
	return Admin{id: id}, nil
}

const adminPrefix = "admin:"

func (a Admin) actor() (string, error) {
	if a.id == "" {
		return "", ErrNotAdmin
	}
	return adminPrefix + a.id, nil
}

// WorkerPrincipal is the authenticated Worker, as the transport established
// it. It is what a self-revocation is checked against.
type WorkerPrincipal struct {
	Identity    ids.WorkerIdentity
	TrustDomain string
}

type Clock interface {
	Now() time.Time
}

type Config struct {
	Store statestore.WorkerRegistryStore
	Clock Clock
	// Issuer signs certificates for Enroll and Renew. It may be nil when the
	// service is used only to manage credentials and Worker state.
	Issuer CertificateIssuer
}

type Service struct {
	store  statestore.WorkerRegistryStore
	clock  Clock
	issuer CertificateIssuer
}

func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: Store is required", ErrInvalidConfig)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: Clock is required", ErrInvalidConfig)
	}
	return &Service{store: cfg.Store, clock: cfg.Clock, issuer: cfg.Issuer}, nil
}

// IssueRequest asks for one enrollment credential.
type IssueRequest struct {
	TrustDomain string
	// TTL is the lifetime. Zero means DefaultCredentialTTL. More than
	// MaxCredentialTTL is refused.
	TTL time.Duration
}

// IssuedCredential is what issuing returns. Secret is shown here and nowhere
// else: the registry keeps only its digest.
type IssuedCredential struct {
	ID          ids.EnrollmentCredentialID
	Secret      workerregistry.Secret
	TrustDomain string
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

// IssueEnrollmentCredential creates a credential for admin. Only an
// administrator can; the credential is single use and bound to the trust
// domain.
func (s *Service) IssueEnrollmentCredential(ctx context.Context, admin Admin, req IssueRequest) (IssuedCredential, error) {
	actor, err := admin.actor()
	if err != nil {
		return IssuedCredential{}, err
	}
	if req.TTL < 0 || req.TTL > workerregistry.MaxCredentialTTL {
		return IssuedCredential{}, fmt.Errorf("%w: lifetime %v is outside (0, %v]", ErrInvalidRequest, req.TTL, workerregistry.MaxCredentialTTL)
	}
	id, err := ids.NewEnrollmentCredentialID()
	if err != nil {
		return IssuedCredential{}, err
	}
	secret, err := workerregistry.NewSecret()
	if err != nil {
		return IssuedCredential{}, err
	}
	credential, err := workerregistry.NewCredential(id, secret, req.TrustDomain, actor, req.TTL, s.clock.Now())
	if err != nil {
		return IssuedCredential{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.IssueEnrollmentCredential(ctx, credential); err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{
		ID:          id,
		Secret:      secret,
		TrustDomain: credential.TrustDomain,
		IssuedAt:    credential.IssuedAt,
		ExpiresAt:   credential.ExpiresAt,
	}, nil
}

// RevokeEnrollmentCredential stops an unused credential. Revoking a revoked
// credential succeeds; revoking a consumed one fails with
// workerregistry.ErrCredentialConsumed, and the Worker is revoked instead.
func (s *Service) RevokeEnrollmentCredential(ctx context.Context, admin Admin, id ids.EnrollmentCredentialID) error {
	actor, err := admin.actor()
	if err != nil {
		return err
	}
	_, err = s.store.RevokeEnrollmentCredential(ctx, id, actor, s.clock.Now())
	return err
}

// ConsumeEnrollmentCredential spends the secret a Worker presented and creates
// its registry entry, in one transaction. The Worker gets a new identity that
// the registry draws; nothing the Worker sends can choose it.
//
// An unusable secret returns an error for which errors.Is(err,
// workerregistry.ErrCredentialRefused) holds, with the same text whatever went
// wrong: unknown, malformed, expired, spent, revoked, or for another trust
// domain. Log workerregistry.RefusalReason(err) for the audit trail and show
// the caller nothing more. Of several concurrent calls with one secret, one
// succeeds.
func (s *Service) ConsumeEnrollmentCredential(ctx context.Context, presented, trustDomain string) (workerregistry.Worker, error) {
	hash, err := workerregistry.HashPresentedSecret(presented)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	now := s.clock.Now()
	for attempt := 0; attempt < maxIdentityAttempts; attempt++ {
		identity, err := ids.NewWorkerIdentity()
		if err != nil {
			return workerregistry.Worker{}, err
		}
		worker, err := s.store.ConsumeEnrollmentCredential(ctx, statestore.ConsumeEnrollment{
			SecretSHA256: hash,
			TrustDomain:  trustDomain,
			Identity:     identity,
			At:           now,
		})
		if errors.Is(err, statestore.ErrAlreadyExists) {
			// A 128-bit identity collided. The store left the credential
			// unspent, so draw another.
			continue
		}
		return worker, err
	}
	return workerregistry.Worker{}, fmt.Errorf("could not draw an unused worker identity after %d attempts", maxIdentityAttempts)
}

// SetWorkerState lets an administrator disable, enable or revoke a Worker. A
// revoked Worker cannot be enabled again, and no other Worker or credential
// can do this.
func (s *Service) SetWorkerState(ctx context.Context, admin Admin, identity ids.WorkerIdentity, to workerregistry.State) (workerregistry.Worker, error) {
	actor, err := admin.actor()
	if err != nil {
		return workerregistry.Worker{}, err
	}
	return s.store.TransitionWorker(ctx, identity, to, actor, s.clock.Now())
}

// WorkerSelfRevoke revokes target on behalf of the authenticated Worker. It
// succeeds only when target is the caller's own identity, in the caller's trust
// domain: a Worker can stop itself (RQ-006, RQ-042) and nothing else.
// Revoking an already revoked identity succeeds without change.
func (s *Service) WorkerSelfRevoke(ctx context.Context, caller WorkerPrincipal, target ids.WorkerIdentity) (workerregistry.Worker, error) {
	if caller.Identity == "" || caller.Identity != target {
		return workerregistry.Worker{}, ErrNotSelf
	}
	current, err := s.store.LoadWorker(ctx, target)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	if current.TrustDomain != caller.TrustDomain {
		return workerregistry.Worker{}, ErrNotSelf
	}
	worker, err := s.store.TransitionWorker(ctx, target, workerregistry.StateRevoked, "worker:"+string(target), s.clock.Now())
	if errors.Is(err, workerregistry.ErrWorkerRevoked) {
		return current, nil
	}
	return worker, err
}

// LookupWorker returns the registry entry or statestore.ErrNotFound.
func (s *Service) LookupWorker(ctx context.Context, identity ids.WorkerIdentity) (workerregistry.Worker, error) {
	return s.store.LoadWorker(ctx, identity)
}

// Authorize is the per-request check (ADR-0027 section 2.4 item 16): the
// Worker is in the registry, in the principal's trust domain, and registered.
// It returns ErrWorkerNotUsable for every failing case, so a caller cannot tell
// an unknown identity from a disabled one. A store failure is returned as is
// and must be treated as a refusal too.
func (s *Service) Authorize(ctx context.Context, principal WorkerPrincipal) (workerregistry.Worker, error) {
	w, err := s.store.LoadWorker(ctx, principal.Identity)
	if errors.Is(err, statestore.ErrNotFound) {
		return workerregistry.Worker{}, ErrWorkerNotUsable
	}
	if err != nil {
		return workerregistry.Worker{}, err
	}
	if w.TrustDomain != principal.TrustDomain || !w.Usable() {
		return workerregistry.Worker{}, ErrWorkerNotUsable
	}
	return w, nil
}

// Enrolled is the result of Enroll: the new registry entry and its first
// certificate.
type Enrolled struct {
	Worker      workerregistry.Worker
	Certificate IssuedCertificate
}

// Enroll spends the secret, creates the Worker, and signs a certificate for
// the public key the Worker generated (ADR-0027 section 2.2 items 7 and 8).
// The refusal behavior of ConsumeEnrollmentCredential applies.
//
// The caller validates the CSR before calling, because the secret is spent
// here. If signing fails after the secret is spent, the Worker exists without
// a certificate and the administrator issues a new credential; the secret is
// never reusable.
func (s *Service) Enroll(ctx context.Context, presented, trustDomain string, key crypto.PublicKey) (Enrolled, error) {
	if s.issuer == nil {
		return Enrolled{}, ErrNoIssuer
	}
	worker, err := s.ConsumeEnrollmentCredential(ctx, presented, trustDomain)
	if err != nil {
		return Enrolled{}, err
	}
	cert, worker, err := s.issueAndRecord(ctx, worker, key)
	if err != nil {
		return Enrolled{}, fmt.Errorf("worker %s is registered but has no certificate: %w", worker.Identity, err)
	}
	return Enrolled{Worker: worker, Certificate: cert}, nil
}

// Renew signs a new certificate for a Worker that authenticated with its
// current one (ADR-0027 section 2.4 item 15). The registry is consulted first:
// only a registered Worker renews, so a disabled or revoked Worker cannot. The
// caller has already verified that the current certificate is valid now, which
// is why an expired certificate cannot renew.
func (s *Service) Renew(ctx context.Context, principal WorkerPrincipal, key crypto.PublicKey) (IssuedCertificate, error) {
	if s.issuer == nil {
		return IssuedCertificate{}, ErrNoIssuer
	}
	worker, err := s.Authorize(ctx, principal)
	if err != nil {
		return IssuedCertificate{}, err
	}
	cert, _, err := s.issueAndRecord(ctx, worker, key)
	return cert, err
}

// issueAndRecord signs, then records the serial and expiry. The certificate is
// returned only after the record is stored.
func (s *Service) issueAndRecord(ctx context.Context, worker workerregistry.Worker, key crypto.PublicKey) (IssuedCertificate, workerregistry.Worker, error) {
	now := s.clock.Now()
	cert, err := s.issuer.IssueCertificate(CertificateRequest{
		Identity: worker.Identity, TrustDomain: worker.TrustDomain, PublicKey: key, Now: now,
	})
	if err != nil {
		return IssuedCertificate{}, worker, err
	}
	updated, err := s.store.RecordWorkerCertificate(ctx, worker.Identity, workerregistry.Certificate{
		Serial: cert.Serial, NotAfter: cert.NotAfter,
	}, now)
	if err != nil {
		return IssuedCertificate{}, worker, err
	}
	return cert, updated, nil
}
