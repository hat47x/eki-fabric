package decisiontrust

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

var (
	ErrKeyNotFound       = errors.New("signed-decision key not found")
	ErrKeyAlreadyExists  = errors.New("signed-decision key already exists")
	ErrKeyIDReused       = errors.New("signed-decision key ID reused for different key")
	ErrInvalidTransition = errors.New("invalid signed-decision key state transition")
	ErrNoSigningKey      = errors.New("no active primary signing key")
	ErrKeyNotVerifiable  = errors.New("signed-decision key is not valid for verification")
)

type signingScope struct {
	TrustDomain        string
	AuthorityRole      authority.Role
	AuthorityPrincipal string
	Algorithm          Algorithm
}

func (s signingScope) validate() error {
	if !s.AuthorityRole.Valid() {
		return fmt.Errorf("%w: invalid authority role %q", ErrInvalidKeyRecord, s.AuthorityRole)
	}
	if err := validateText("trust domain", s.TrustDomain, 256); err != nil {
		return err
	}
	if err := validateText("authority principal", s.AuthorityPrincipal, 256); err != nil {
		return err
	}
	switch s.Algorithm {
	case AlgorithmEd25519, AlgorithmES256:
		return nil
	default:
		return fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidKeyRecord, s.Algorithm)
	}
}

type Registry struct {
	mu        sync.RWMutex
	keys      map[KeyID]KeyRecord
	primary   map[signingScope]KeyID
	revision  uint64
	updatedAt time.Time
}

func NewRegistry(now time.Time) *Registry {
	return &Registry{
		keys:      make(map[KeyID]KeyRecord),
		primary:   make(map[signingScope]KeyID),
		revision:  1,
		updatedAt: now.UTC(),
	}
}

func (r *Registry) Revision() (uint64, time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision, r.updatedAt
}

func (r *Registry) Register(record KeyRecord, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.keys[record.id]; ok {
		if existing.sameRegistration(record) {
			return fmt.Errorf("%w: key %q", ErrKeyAlreadyExists, record.id)
		}
		return fmt.Errorf("%w: key %q", ErrKeyIDReused, record.id)
	}
	r.keys[record.id] = cloneRecord(record)
	r.bump(now)
	return nil
}

func (r *Registry) Activate(id KeyID, now time.Time) error {
	return r.transition(id, KeyStateActive, now, KeyStatePending)
}

func (r *Registry) SetVerifyOnly(id KeyID, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.keys[id]
	if !ok {
		return fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	if record.state != KeyStateActive {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, record.state, KeyStateVerifyOnly)
	}
	record.state = KeyStateVerifyOnly
	record.revision++
	record.stateChangedAt = now.UTC()
	r.keys[id] = record
	if r.primary[record.scope()] == id {
		delete(r.primary, record.scope())
	}
	r.bump(now)
	return nil
}

func (r *Registry) Revoke(id KeyID, now time.Time) error {
	return r.transitionTerminal(id, KeyStateRevoked, now, KeyStatePending, KeyStateActive, KeyStateVerifyOnly)
}

func (r *Registry) MarkCompromised(id KeyID, now time.Time) error {
	return r.transitionTerminal(id, KeyStateCompromised, now, KeyStatePending, KeyStateActive, KeyStateVerifyOnly, KeyStateRevoked)
}

func (r *Registry) SetPrimary(id KeyID, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.keys[id]
	if !ok {
		return fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	if record.state != KeyStateActive {
		return fmt.Errorf("%w: only ACTIVE key may be primary, got %s", ErrInvalidTransition, record.state)
	}
	now = now.UTC()
	if now.Before(record.signingNotBefore) || !now.Before(record.signingNotAfter) {
		return fmt.Errorf("%w: primary key must be inside signing validity window", ErrInvalidTransition)
	}
	r.primary[record.scope()] = id
	r.bump(now)
	return nil
}

func (r *Registry) resolveForSigning(scope signingScope, now time.Time) (KeyRecord, error) {
	if err := scope.validate(); err != nil {
		return KeyRecord{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.primary[scope]
	if !ok {
		return KeyRecord{}, ErrNoSigningKey
	}
	record, ok := r.keys[id]
	if !ok || record.state != KeyStateActive {
		return KeyRecord{}, ErrNoSigningKey
	}
	now = now.UTC()
	if now.Before(record.signingNotBefore) || !now.Before(record.signingNotAfter) {
		return KeyRecord{}, ErrNoSigningKey
	}
	return cloneRecord(record), nil
}

func (r *Registry) resolveForVerification(id KeyID, issuedAt time.Time) (KeyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	record, ok := r.keys[id]
	if !ok {
		return KeyRecord{}, fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	switch record.state {
	case KeyStateActive, KeyStateVerifyOnly:
	default:
		return KeyRecord{}, fmt.Errorf("%w: key %q is %s", ErrKeyNotVerifiable, id, record.state)
	}
	issuedAt = issuedAt.UTC()
	if issuedAt.Before(record.signingNotBefore) || !issuedAt.Before(record.signingNotAfter) {
		return KeyRecord{}, fmt.Errorf("%w: issued time outside signing validity", ErrKeyNotVerifiable)
	}
	return cloneRecord(record), nil
}

func (r *Registry) Get(id KeyID) (KeyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.keys[id]
	if !ok {
		return KeyRecord{}, fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	return cloneRecord(record), nil
}

func (r *Registry) transition(id KeyID, target KeyState, now time.Time, allowed ...KeyState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.keys[id]
	if !ok {
		return fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	if !containsState(allowed, record.state) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, record.state, target)
	}
	record.state = target
	record.revision++
	record.stateChangedAt = now.UTC()
	r.keys[id] = record
	r.bump(now)
	return nil
}

func (r *Registry) transitionTerminal(id KeyID, target KeyState, now time.Time, allowed ...KeyState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.keys[id]
	if !ok {
		return fmt.Errorf("%w: key %q", ErrKeyNotFound, id)
	}
	if !containsState(allowed, record.state) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, record.state, target)
	}
	record.state = target
	record.revision++
	record.stateChangedAt = now.UTC()
	r.keys[id] = record
	if r.primary[record.scope()] == id {
		delete(r.primary, record.scope())
	}
	r.bump(now)
	return nil
}

func (r *Registry) bump(now time.Time) {
	r.revision++
	r.updatedAt = now.UTC()
}

func containsState(states []KeyState, value KeyState) bool {
	for _, state := range states {
		if state == value {
			return true
		}
	}
	return false
}

func cloneRecord(record KeyRecord) KeyRecord {
	record.publicKey = clonePublicKey(record.publicKey)
	return record
}
