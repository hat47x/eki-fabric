package recovery

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type EffectMode string

const (
	EffectNone          EffectMode = "none"
	EffectIdempotent    EffectMode = "idempotent-external"
	EffectNonIdempotent EffectMode = "non-idempotent-external"
)

type Contract struct {
	TaskID        string
	EffectMode    EffectMode
	OperationID   string
	RequestDigest string
	MaxAttempts   int
}

type Attempt struct {
	AttemptNo        int
	EffectDispatched bool
	Confirmed        bool
}

type RecoveryState string

const (
	StateRetryable RecoveryState = "retryable"
	StateComplete  RecoveryState = "complete"
	StateUnknown   RecoveryState = "unknown"
	StateExhausted RecoveryState = "retry-budget-exhausted"
)

type Decision struct {
	State       RecoveryState
	OperationID string
	Reason      string
}

func ValidateContract(c Contract) error {
	if c.TaskID == "" || c.MaxAttempts <= 0 {
		return errors.New("task id and positive max attempts are required")
	}
	switch c.EffectMode {
	case EffectNone:
		if c.OperationID != "" || c.RequestDigest != "" {
			return errors.New("pure compute task must not declare external effect fields")
		}
	case EffectIdempotent, EffectNonIdempotent:
		if c.OperationID == "" || c.RequestDigest == "" {
			return errors.New("external effect requires stable operation id and request digest")
		}
	default:
		return errors.New("unknown effect mode")
	}
	return nil
}

func RecoverAfterDisconnect(c Contract, a Attempt) (Decision, error) {
	if err := ValidateContract(c); err != nil {
		return Decision{}, err
	}
	if a.AttemptNo <= 0 {
		return Decision{}, errors.New("attempt number must be positive")
	}
	if a.Confirmed {
		return Decision{State: StateComplete, OperationID: c.OperationID, Reason: "authoritative effect receipt already confirmed"}, nil
	}
	if a.AttemptNo >= c.MaxAttempts {
		return Decision{State: StateExhausted, OperationID: c.OperationID, Reason: "retry budget exhausted"}, nil
	}
	if !a.EffectDispatched || c.EffectMode == EffectNone {
		return Decision{State: StateRetryable, OperationID: c.OperationID, Reason: "no external side effect may have occurred"}, nil
	}
	if c.EffectMode == EffectIdempotent {
		return Decision{State: StateRetryable, OperationID: c.OperationID, Reason: "retry is allowed only with the same idempotency identity"}, nil
	}
	return Decision{State: StateUnknown, OperationID: c.OperationID, Reason: "non-idempotent external effect may already have occurred"}, nil
}

func RetryContract(original Contract, operationID, requestDigest string) error {
	if err := ValidateContract(original); err != nil {
		return err
	}
	if original.EffectMode == EffectNone {
		return nil
	}
	if operationID != original.OperationID {
		return errors.New("retry must reuse the original operation id")
	}
	if requestDigest != original.RequestDigest {
		return errors.New(bretry operation id cannot be reused with different intent")
	}
	return nil
}

type KeyPair struct {
	Public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func NewKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: pub, private: priv}, nil
}

type Receipt struct {
	OperationID   string    `json:"operation_id"`
	RequestDigest string    `json:"request_digest"`
	EffectID      string    `json:"effect_id"`
	AppliedAt     time.Time `json:"applied_at"`
}

type SignedReceipt struct {
	Receipt   Receipt `json:"receipt"`
	Signature []byte  `json:"signature"`
}

func receiptBytes(r Receipt) ([]byte, error) { return json.Marshal(r) }

func signReceipt(key KeyPair, r Receipt) (SignedReceipt, error) {
	b, err := receiptBytes(r)
	if err != nil {
		return SignedReceipt{}, err
	}
	return SignedReceipt{Receipt: r, Signature: ed25519.Sign(key.private, b)}, nil
}

func VerifyReceipt(pub ed25519.PublicKey, signed SignedReceipt) bool {
	b, err := receiptBytes(signed.Receipt)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, b, signed.Signature)
}

func DigestRequest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type EffectService struct {
	key          KeyPair
	records      map[string]Receipt
	applications int
}

func NewEffectService(key KeyPair) *EffectService {
	return &EffectService{key: key, records: map[string]Receipt{}}
}

func (s *EffectService) ApplyIdempotent(now time.Time, operationID, requestDigest string) (SignedReceipt, error) {
	if operationID == "" || requestDigest == "" {
		return SignedReceipt{}, errors.New("operation id and request digest are required")
	}
	if existing, ok := s.records[operationID]; ok {
		if existing.RequestDigest != requestDigest {
			return SignedReceipt{}, errors.New("same operation id was reused with different intent")
		}
		return signReceipt(s.key, existing)
	}
	s.applications++
	r := Receipt{
		OperationID:   operationID,
		RequestDigest: requestDigest,
		EffectID:      fmt.Sprintf("effect-%d", s.applications),
		AppliedAt:     now,
	}
	s.records[operationID] = r
	return signReceipt(s.key, r)
}

func (s *EffectService) Applications() int { return s.applications }

type ReceiptGuard struct {
	effectPub ed25519.PublicKey
}

func NewReceiptGuard(effectPub ed25519.PublicKey) (*ReceiptGuard, error) {
	if len(effectPub) != ed25519.PublicKeySize {
		return nil, errors.New("effect authority trust root is required")
	}
	return &ReceiptGuard{effectPub: append(ed25519.PublicKey(nil), effectPub...)}, nil
}

func (g *ReceiptGuard) Confirm(c Contract, signed SignedReceipt) error {
	if err := ValidateContract(c); err != nil {
		return err
	}
	if c.EffectMode == EffectNone {
		return errors.New("pure compute task does not accept external effect receipt")
	}
	if !VerifyReceipt(g.effectPub, signed) {
		return errors.New("receipt is not signed by the pinned external effect authority")
	}
	r := signed.Receipt
	if r.OperationID != c.OperationID || r.RequestDigest != c.RequestDigest {
		return errors.New,"receipt is not bound to this task intent")
	}
	return nil
}

type CancellationResult string

const (
	CancellationSafe          CancellationResult = "cancelled-before-effect"
	CancellationUnknown       CancellationResult = "cancelled-but-effect-outcome-unknown"
	CancellationAfterComplete CancellationResult = "already-complete"
)

func Cancel(c Contract, a Attempt) CancellationResult {
	if a.Confirmed {
		return CancellationAfterComplete
	}
	if a.EffectDispatched && c.EffectMode == EffectNonIdempotent {
		return CancellationUnknown
	}
	return CancellationSafe
}
