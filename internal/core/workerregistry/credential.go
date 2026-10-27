package workerregistry

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

const (
	// SecretPrefix starts every enrollment secret. It is fixed so that a secret
	// scanner can find a secret that leaked into a log or a repository
	// (ADR-0027 section 2.2 item 5).
	SecretPrefix = "eki_enroll_v1_"
	// SecretRandomBytes is the entropy of a secret: 256 bits.
	SecretRandomBytes = 32

	// DefaultCredentialTTL is the lifetime when the administrator names none.
	DefaultCredentialTTL = 24 * time.Hour
	// MaxCredentialTTL is the longest lifetime the registry accepts.
	MaxCredentialTTL = 7 * 24 * time.Hour
)

var (
	ErrInvalidCredential = errors.New("invalid enrollment credential")
	// ErrCredentialConsumed is returned when an administrator tries to revoke a
	// credential that already created a Worker. Revoking the Worker is the way
	// to stop what the credential started.
	ErrCredentialConsumed = errors.New("enrollment credential is already consumed")
	// ErrCredentialRefused is the one error class a caller outside the Control
	// Plane may observe for an unusable credential. It matches every Refusal.
	ErrCredentialRefused = errors.New("enrollment credential refused")
)

// Secret is the value an administrator hands to a Worker. It appears once, in
// the result of issuing, and is never stored: the registry keeps only its
// SHA-256.
//
// Printing, logging and JSON-encoding a Secret show a placeholder. Reveal is
// the only way to read it, so a stray log line cannot leak it.
type Secret struct {
	value string
}

// NewSecret draws a new secret from the operating system's random source.
func NewSecret() (Secret, error) {
	var raw [SecretRandomBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Secret{}, fmt.Errorf("generate enrollment secret: %w", err)
	}
	return Secret{value: SecretPrefix + base64.RawURLEncoding.EncodeToString(raw[:])}, nil
}

// Reveal returns the secret text.
func (s Secret) Reveal() string { return s.value }

// Hash returns the digest that the registry stores.
func (s Secret) Hash() contract.Digest { return contract.SHA256([]byte(s.value)) }

func (s Secret) String() string               { return "[redacted enrollment secret]" }
func (s Secret) GoString() string             { return s.String() }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(s.String()) }
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// HashPresentedSecret returns the digest of a secret a Worker presented. It
// reports a Refusal for text that cannot be a secret, so a malformed value
// never reaches storage and never costs a lookup.
func HashPresentedSecret(presented string) (contract.Digest, error) {
	rest, ok := strings.CutPrefix(presented, SecretPrefix)
	if !ok || len(rest) != base64.RawURLEncoding.EncodedLen(SecretRandomBytes) {
		return contract.Digest{}, &Refusal{reason: ReasonMalformed}
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(rest); err != nil {
		return contract.Digest{}, &Refusal{reason: ReasonMalformed}
	}
	return contract.SHA256([]byte(presented)), nil
}

// CredentialState is the stored state of an enrollment credential. Expiry is
// not stored: it follows from the clock (see Credential.Status).
type CredentialState string

const (
	CredentialIssued   CredentialState = "issued"
	CredentialConsumed CredentialState = "consumed"
	CredentialRevoked  CredentialState = "revoked"
)

// CredentialStatus is the state as a caller sees it at one moment: the stored
// state, or expired when an issued credential is past its deadline.
type CredentialStatus string

const (
	StatusIssued   CredentialStatus = "issued"
	StatusConsumed CredentialStatus = "consumed"
	StatusRevoked  CredentialStatus = "revoked"
	StatusExpired  CredentialStatus = "expired"
)

// Reason says why a credential was refused. It is for the audit log only. The
// error a caller sees is the same for every reason, so a caller cannot use the
// answer to learn whether a guessed secret ever existed, was used, or belongs
// to another trust domain.
type Reason string

const (
	ReasonMalformed         Reason = "malformed"
	ReasonUnknown           Reason = "unknown"
	ReasonWrongTrustDomain  Reason = "wrong_trust_domain"
	ReasonExpired           Reason = "expired"
	ReasonConsumed          Reason = "consumed"
	ReasonRevoked           Reason = "revoked"
	ReasonInconsistentStore Reason = "inconsistent_store"
)

// Refusal is returned when a credential cannot be consumed. Its text and its
// errors.Is behavior do not depend on the reason; use RefusalReason for the
// audit log.
type Refusal struct {
	reason Reason
}

// NewRefusal returns a Refusal for reason.
func NewRefusal(reason Reason) *Refusal { return &Refusal{reason: reason} }

func (r *Refusal) Error() string { return ErrCredentialRefused.Error() }

func (r *Refusal) Is(target error) bool { return target == ErrCredentialRefused }

// RefusalReason returns the audit reason of a Refusal anywhere in err's chain.
// Never send it to the caller that presented the secret.
func RefusalReason(err error) (Reason, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r.reason, true
	}
	return "", false
}

// Credential is one enrollment credential (ADR-0027 section 2.2 items 4, 5).
type Credential struct {
	ID ids.EnrollmentCredentialID
	// SecretSHA256 is the only trace of the secret that is stored.
	SecretSHA256 contract.Digest
	// TrustDomain is the domain the credential can register a Worker into.
	TrustDomain string
	// IssuedBy names the administrator, as "admin:<id>".
	IssuedBy  string
	IssuedAt  time.Time
	ExpiresAt time.Time

	State CredentialState
	// ConsumedWorker and ConsumedAt are set when State is consumed.
	ConsumedWorker ids.WorkerIdentity
	ConsumedAt     time.Time
	// RevokedBy and RevokedAt are set when State is revoked.
	RevokedBy string
	RevokedAt time.Time
}

// NewCredential returns an issued credential. It does not choose the
// lifetime: ttl is checked against MaxCredentialTTL and zero means the default.
func NewCredential(id ids.EnrollmentCredentialID, secret Secret, trustDomain, issuedBy string, ttl time.Duration, now time.Time) (Credential, error) {
	if ttl == 0 {
		ttl = DefaultCredentialTTL
	}
	c := Credential{
		ID:           id,
		SecretSHA256: secret.Hash(),
		TrustDomain:  trustDomain,
		IssuedBy:     issuedBy,
		IssuedAt:     now.UTC(),
		ExpiresAt:    now.UTC().Add(ttl),
		State:        CredentialIssued,
	}
	if ttl < 0 || ttl > MaxCredentialTTL {
		return Credential{}, fmt.Errorf("%w: lifetime %v is outside (0, %v]", ErrInvalidCredential, ttl, MaxCredentialTTL)
	}
	if err := c.Validate(); err != nil {
		return Credential{}, err
	}
	return c, nil
}

func (c Credential) Validate() error {
	if _, err := ids.ParseEnrollmentCredentialID(string(c.ID)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCredential, err)
	}
	if c.SecretSHA256.IsZero() {
		return fmt.Errorf("%w: secret digest is required", ErrInvalidCredential)
	}
	if err := ValidateTrustDomain(c.TrustDomain); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCredential, err)
	}
	if c.IssuedBy == "" || len(c.IssuedBy) > MaxActorBytes {
		return fmt.Errorf("%w: issuer must be 1..%d bytes", ErrInvalidCredential, MaxActorBytes)
	}
	if c.IssuedAt.IsZero() || c.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: issue and expiry times are required", ErrInvalidCredential)
	}
	if lifetime := c.ExpiresAt.Sub(c.IssuedAt); lifetime <= 0 || lifetime > MaxCredentialTTL {
		return fmt.Errorf("%w: lifetime %v is outside (0, %v]", ErrInvalidCredential, lifetime, MaxCredentialTTL)
	}

	consumed := c.State == CredentialConsumed
	revoked := c.State == CredentialRevoked
	switch c.State {
	case CredentialIssued, CredentialConsumed, CredentialRevoked:
	default:
		return fmt.Errorf("%w: unknown state %q", ErrInvalidCredential, c.State)
	}
	if consumed != (c.ConsumedWorker != "") || consumed != !c.ConsumedAt.IsZero() {
		return fmt.Errorf("%w: consumed state and consumer fields disagree", ErrInvalidCredential)
	}
	if consumed {
		if _, err := ids.ParseWorkerIdentity(string(c.ConsumedWorker)); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCredential, err)
		}
		if c.ConsumedAt.Before(c.IssuedAt) {
			return fmt.Errorf("%w: consumed before it was issued", ErrInvalidCredential)
		}
	}
	if revoked != (c.RevokedBy != "") || revoked != !c.RevokedAt.IsZero() {
		return fmt.Errorf("%w: revoked state and revoker fields disagree", ErrInvalidCredential)
	}
	if revoked && (len(c.RevokedBy) > MaxActorBytes || c.RevokedAt.Before(c.IssuedAt)) {
		return fmt.Errorf("%w: revoker fields are out of range", ErrInvalidCredential)
	}
	return nil
}

// Status is the state at now. An issued credential at or past its deadline is
// expired.
func (c Credential) Status(now time.Time) CredentialStatus {
	switch c.State {
	case CredentialConsumed:
		return StatusConsumed
	case CredentialRevoked:
		return StatusRevoked
	}
	if !now.Before(c.ExpiresAt) {
		return StatusExpired
	}
	return StatusIssued
}

// CheckUsable decides whether presenting the secret with digest presented, for
// trustDomain, at now, may create a Worker. It returns a *Refusal naming the
// internal reason, or nil.
//
// The digest comparison is constant time. The stores find the credential by
// digest, so a mismatch here means the store returned the wrong row, and the
// credential is refused.
func (c Credential) CheckUsable(presented contract.Digest, trustDomain string, now time.Time) error {
	if presented.IsZero() || !equalDigest(c.SecretSHA256, presented) {
		return &Refusal{reason: ReasonInconsistentStore}
	}
	// The terminal states are reported before the domain, so the audit log
	// records the credential's own state. The caller cannot tell them apart.
	switch c.Status(now) {
	case StatusConsumed:
		return &Refusal{reason: ReasonConsumed}
	case StatusRevoked:
		return &Refusal{reason: ReasonRevoked}
	case StatusExpired:
		return &Refusal{reason: ReasonExpired}
	}
	if subtle.ConstantTimeCompare([]byte(c.TrustDomain), []byte(trustDomain)) != 1 {
		return &Refusal{reason: ReasonWrongTrustDomain}
	}
	return nil
}

func equalDigest(a, b contract.Digest) bool {
	return subtle.ConstantTimeCompare([]byte(a.Hex()), []byte(b.Hex())) == 1
}

// Consume returns the credential after it created worker at now. It does not
// check the secret or the deadline: CheckUsable does that, in the same
// transaction.
func (c Credential) Consume(worker ids.WorkerIdentity, now time.Time) (Credential, error) {
	if c.State != CredentialIssued {
		return Credential{}, &Refusal{reason: stateReason(c.State)}
	}
	next := c
	next.State = CredentialConsumed
	next.ConsumedWorker = worker
	next.ConsumedAt = LaterOf(now, c.IssuedAt)
	return next, next.Validate()
}

// Revoke returns the credential after an administrator revoked it. Revoking a
// revoked credential changes nothing. A consumed credential cannot be revoked:
// revoke the Worker instead.
func (c Credential) Revoke(actor string, now time.Time) (Credential, error) {
	switch c.State {
	case CredentialRevoked:
		return c, nil
	case CredentialConsumed:
		return Credential{}, fmt.Errorf("%w: %s", ErrCredentialConsumed, c.ID)
	}
	next := c
	next.State = CredentialRevoked
	next.RevokedBy = actor
	next.RevokedAt = LaterOf(now, c.IssuedAt)
	return next, next.Validate()
}

func stateReason(s CredentialState) Reason {
	if s == CredentialConsumed {
		return ReasonConsumed
	}
	return ReasonRevoked
}
