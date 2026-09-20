package contribution

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

type Assurance string

const (
	AssuranceSelfReported    Assurance = "self-reported"
	AssuranceControlObserved Assurance = "control-observed"
	AssuranceAttested        Assurance = "attested-metering"
)

func assuranceRank(a Assurance) int {
	switch a {
	case AssuranceSelfReported:
		return 1
	case AssuranceControlObserved:
		return 2
	case AssuranceAttested:
		return 3
	default:
		return 0
	}
}

type Quantity struct {
	Unit  string `json:"unit"`
	Value uint64 `json:"value"`
}

type WorkContext struct {
	ProviderID    string `json:"provider_id"`
	WorkerID      string `json:"worker_id"`
	TaskID        string `json:"task_id"`
	AttemptID     string `json:"attempt_id"`
	ResourceClass string `json:"resource_class"`
}

type Measurement struct {
	Context    WorkContext `json:"context"`
	Metric     string      `json:"metric"`
	Quantity   Quantity    `json:"quantity"`
	Assurance  Assurance   `json:"assurance"`
	ObserverID string      `json:"observer_id"`
	ObservedAt time.Time   `json:"observed_at"`
}

type ResultEvidence struct {
	TaskID                string `json:"task_id"`
	AttemptID             string `json:"attempt_id"`
	Accepted              bool   `json:"accepted"`
	VerificationAuthority string `json:"verification_authority"`
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

type Signed[T any] struct {
	Payload   T      `json:"payload"`
	Signature []byte `json:"signature"`
}

func canonical(v any) ([]byte, error) { return json.Marshal(v) }
func sign[T any](k KeyPair, p T) (Signed[T], error) {
	b, err := canonical(p)
	if err != nil {
		return Signed[T]{}, err
	}
	return Signed[T]{Payload: p, Signature: ed25519.Sign(k.private, b)}, nil
}
func Verify[T any](pub ed25519.PublicKey, s Signed[T]) bool {
	b, err := canonical(s.Payload)
	return err == nil && ed25519.Verify(pub, b, s.Signature)
}
func digest(v any) (string, error) {
	b, err := canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type MeasurementTrust struct {
	ObserverID   string
	Public       ed25519.PublicKey
	MaxAssurance Assurance
}

type Policy struct {
	Revision              uint64
	MinAssurance          Assurance
	AllowedMetrics        map[string]bool
	AllowedUnits          map[string]bool
	RequireAcceptedResult bool
	RecordTTL             time.Duration
}

type ContributionRecord struct {
	RecordID              string    `json:"record_id"`
	ProviderID            string    `json:"provider_id"`
	WorkerID              string    `json:"worker_id"`
	TaskID                string    `json:"task_id"`
	AttemptID             string    `json:"attempt_id"`
	ResourceClass         string    `json:"resource_class"`
	Metric                string    `json:"metric"`
	Quantity              Quantity  `json:"quantity"`
	MeasurementAssurance  Assurance `json:"measurement_assurance"`
	ObserverID            string    `json:"observer_id"`
	MeasurementDigest     string    `json:"measurement_digest"`
	ResultAccepted        bool      `json:"result_accepted"`
	VerificationAuthority string    `json:"verification_authority,omitempty"`
	PolicyRevision        uint64    `json:"policy_revision"`
	IssuedAt              time.Time `json:"issued_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type Authority struct {
	key    KeyPair
	policy Policy
	trust  map[string]MeasurementTrust
	issued map[string]Signed[ContributionRecord]
}

func NewAuthority(key KeyPair, policy Policy, trusts []MeasurementTrust) (*Authority, error) {
	if policy.Revision == 0 || assuranceRank(policy.MinAssurance) == 0 || policy.RecordTTL <= 0 {
		return nil, errors.New("policy revision, minimum assurance, and record TTL are required")
	}
	t := map[string]MeasurementTrust{}
	for _, x := range trusts {
		if x.ObserverID == "" || len(x.Public) != ed25519.PublicKeySize || assuranceRank(x.MaxAssurance) == 0 {
			return nil, errors.New("complete measurement trust binding is required")
		}
		if _, ok := t[x.ObserverID]; ok {
			return nil, fmt.Errorf("duplicate observer %q", x.ObserverID)
		}
		t[x.ObserverID] = x
	}
	return &Authority{key: key, policy: policy, trust: t, issued: map[string]Signed[ContributionRecord]{}}, nil
}

func validateContext(c WorkContext) error {
	if c.ProviderID == "" || c.WorkerID == "" || c.TaskID == "" || c.AttemptID == "" || c.ResourceClass == "" {
		return errors.New("complete work context is required")
	}
	return nil
}
func validateMeasurement(m Measurement) error {
	if err := validateContext(m.Context); err != nil {
		return err
	}
	if m.Metric == "" || m.Quantity.Unit == "" || m.Quantity.Value == 0 || m.ObserverID == "" || m.ObservedAt.IsZero() || assuranceRank(m.Assurance) == 0 {
		return errors.New("complete measurement fields are required")
	}
	return nil
}

func recordKey(m Measurement) string {
	c := m.Context
	return c.ProviderID + "\x00" + c.WorkerID + "\x00" + c.TaskID + "\x00" + c.AttemptID + "\x00" + m.Metric
}

func (a *Authority) Issue(now time.Time, sm Signed[Measurement], result ResultEvidence) (Signed[ContributionRecord], error) {
	m := sm.Payload
	if err := validateMeasurement(m); err != nil {
		return Signed[ContributionRecord]{}, err
	}
	trust, ok := a.trust[m.ObserverID]
	if !ok {
		return Signed[ContributionRecord]{}, errors.New("measurement observer is not trusted")
	}
	if !Verify(trust.Public, sm) {
		return Signed[ContributionRecord]{}, errors.New("measurement signature verification failed")
	}
	if assuranceRank(m.Assurance) > assuranceRank(trust.MaxAssurance) {
		return Signed[ContributionRecord]{}, errors.New("observer cannot assert requested measurement assurance")
	}
	if assuranceRank(m.Assurance) < assuranceRank(a.policy.MinAssurance) {
		return Signed[ContributionRecord]{}, errors.New("measurement assurance is below policy minimum")
	}
	if !a.policy.AllowedMetrics[m.Metric] || !a.policy.AllowedUnits[m.Quantity.Unit] {
		return Signed[ContributionRecord]{}, errors.New("metric or unit is not allowed by contribution policy")
	}
	if m.ObservedAt.After(now) {
		return Signed[ContributionRecord]{}, errors.New("measurement timestamp is in the future")
	}
	if result.TaskID != m.Context.TaskID || result.AttemptID != m.Context.AttemptID {
		return Signed[ContributionRecord]{}, errors.New("result evidence is not bound to measured attempt")
	}
	if a.policy.RequireAcceptedResult && !result.Accepted {
		return Signed[ContributionRecord]{}, errors.New("policy requires accepted result before contribution record")
	}
	k := recordKey(m)
	if existing, ok := a.issued[k]; ok {
		if existing.Payload.MeasurementDigest == mustDigest(m) {
			return existing, nil
		}
		return Signed[ContributionRecord]{}, errors.New("conflicting measurement already recorded for attempt and metric")
	}
	md, err := digest(m)
	if err != nil {
		return Signed[ContributionRecord]{}, err
	}
	ridSum := sha256.Sum256([]byte(k + "\x00" + md))
	r := ContributionRecord{RecordID: "ctr:" + hex.EncodeToString(ridSum[:]), ProviderID: m.Context.ProviderID, WorkerID: m.Context.WorkerID, TaskID: m.Context.TaskID, AttemptID: m.Context.AttemptID, ResourceClass: m.Context.ResourceClass, Metric: m.Metric, Quantity: m.Quantity, MeasurementAssurance: m.Assurance, ObserverID: m.ObserverID, MeasurementDigest: md, ResultAccepted: result.Accepted, VerificationAuthority: result.VerificationAuthority, PolicyRevision: a.policy.Revision, IssuedAt: now, ExpiresAt: now.Add(a.policy.RecordTTL)}
	signed, err := sign(a.key, r)
	if err != nil {
		return Signed[ContributionRecord]{}, err
	}
	a.issued[k] = signed
	return signed, nil
}

func mustDigest(v any) string { d, _ := digest(v); return d }

func SignMeasurement(k KeyPair, m Measurement) (Signed[Measurement], error) {
	if err := validateMeasurement(m); err != nil {
		return Signed[Measurement]{}, err
	}
	return sign(k, m)
}

func VerifyRecord(pub ed25519.PublicKey, now time.Time, s Signed[ContributionRecord]) error {
	if !Verify(pub, s) {
		return errors.New("contribution record signature invalid")
	}
	if !s.Payload.ExpiresAt.After(now) {
		return errors.New("contribution record expired")
	}
	return nil
}
