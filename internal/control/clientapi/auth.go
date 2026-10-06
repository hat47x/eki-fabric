package clientapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/admission"
)

// Role separates what a credential may do. A Client token can submit and
// observe its own Tasks; it can never reach administrative operations, and an
// Admin token is not a Client: it cannot submit work as one (RQ-015, RQ-016).
type Role string

const (
	RoleClient Role = "client"
	RoleAdmin  Role = "admin"
)

// Identity is an authenticated caller.
type Identity struct {
	Principal admission.Principal
	Role      Role
}

var (
	ErrNoCredential      = errors.New("no credential presented")
	ErrInvalidCredential = errors.New("credential not recognized")
)

// Authenticator turns a request into an Identity. It must not read the body.
type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// TokenEntry is one static credential. The token itself is never stored, only
// its SHA-256, so a leaked configuration file does not leak a usable token.
type TokenEntry struct {
	Principal   string
	Role        Role
	TokenSHA256 string // lowercase hex
}

type staticToken struct {
	identity Identity
	hash     [sha256.Size]byte
}

// StaticTokens authenticates fixed bearer tokens. It is the M0 mechanism; the
// P0 contract leaves the production scheme (OIDC, mTLS) to a later work unit.
type StaticTokens struct {
	tokens []staticToken
}

var ErrInvalidTokenConfig = errors.New("invalid token configuration")

func NewStaticTokens(entries []TokenEntry) (*StaticTokens, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: at least one token is required", ErrInvalidTokenConfig)
	}
	s := &StaticTokens{}
	seen := map[[sha256.Size]byte]struct{}{}
	for i, e := range entries {
		if e.Principal == "" || len(e.Principal) > 256 {
			return nil, fmt.Errorf("%w: entry %d: principal must be 1..256 bytes", ErrInvalidTokenConfig, i)
		}
		if e.Role != RoleClient && e.Role != RoleAdmin {
			return nil, fmt.Errorf("%w: entry %d: role must be %q or %q", ErrInvalidTokenConfig, i, RoleClient, RoleAdmin)
		}
		raw, err := hex.DecodeString(e.TokenSHA256)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("%w: entry %d: token_sha256 must be 64 hex characters", ErrInvalidTokenConfig, i)
		}
		var h [sha256.Size]byte
		copy(h[:], raw)
		if _, dup := seen[h]; dup {
			return nil, fmt.Errorf("%w: entry %d: duplicate token", ErrInvalidTokenConfig, i)
		}
		seen[h] = struct{}{}
		s.tokens = append(s.tokens, staticToken{
			identity: Identity{Principal: admission.Principal{ID: e.Principal}, Role: e.Role},
			hash:     h,
		})
	}
	return s, nil
}

// HashToken returns the lowercase hex SHA-256 of a token, for configuration.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MinTokenBytes is the shortest token we accept. Generated tokens are longer;
// this only refuses obviously guessable ones.
const MinTokenBytes = 20

func (s *StaticTokens) Authenticate(r *http.Request) (Identity, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return Identity{}, ErrNoCredential
	}
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) < MinTokenBytes || len(token) > 512 {
		return Identity{}, ErrInvalidCredential
	}

	sum := sha256.Sum256([]byte(token))
	// Compare against every entry without stopping at the first match, so the
	// time taken does not reveal which, or how many, entries matched.
	var match *staticToken
	for i := range s.tokens {
		if subtle.ConstantTimeCompare(sum[:], s.tokens[i].hash[:]) == 1 {
			match = &s.tokens[i]
		}
	}
	if match == nil {
		return Identity{}, ErrInvalidCredential
	}
	return match.identity, nil
}
