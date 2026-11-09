// Package workorder holds test fixtures that build signed WorkOrders.
//
// It is for tests only: it carries a fixed Ed25519 seed and a signer, so no
// production package may import it. The three packages that check a WorkOrder
// (workorderverify, workorderaccept and agent/startgate) share it so that the
// key registration, the COSE envelope and the signature are assembled in one
// place. Because the tests of workorderverify import this package, it must not
// import workorderverify, workorderaccept or anything that imports them;
// callers build the verifier from the pieces returned here.
package workorder

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// Fixed identities shared by the fixtures.
const (
	TrustDomain       = "example.org"
	Worker            = "worker-a"
	Principal         = "control-plane/wor-1"
	PolicyRevision    = "policy-42"
	KeyPolicyRevision = "key-policy-1"
	// LeaseDecisionID is the Lease decision the default WorkOrder refers to.
	LeaseDecisionID = "lease-decision-001"
)

// Fixed times and identifiers shared by the fixtures.
var (
	IssuedAt = time.UnixMilli(1_780_000_000_000).UTC()
	Expires  = IssuedAt.Add(2 * time.Minute)
	// Now is inside the default WorkOrder's validity window.
	Now   = IssuedAt.Add(10 * time.Second)
	KeyID = decisiontrust.KeyID("wor-key-1")

	Task    = ids.TaskID("tsk_00000000000000000000000000000001")
	Attempt = ids.AttemptID("atm_00000000000000000000000000000001")
	TaskIR  = contract.SHA256([]byte("taskir"))
)

// Digests of the decisions and the executable the default WorkOrder refers to.
// Evidence handed to workorderaccept must carry the same values.
var (
	PlacementDigest  = contract.SHA256([]byte("placement"))
	LeaseDigest      = contract.SHA256([]byte("lease"))
	ExecutorDigest   = contract.SHA256([]byte("executor"))
	ExecutableDigest = contract.SHA256([]byte("executable"))
)

// Signing is a trust registry with one active WorkOrder key, and the pieces
// needed to sign with it and to verify what it signed.
type Signing struct {
	t        testing.TB
	Codec    *cosecbor.Codec
	Payload  *payloadcbor.Decoder
	Enc      fxcbor.EncMode
	Registry *decisiontrust.Registry
	Signer   *decisiontrust.ReferenceSoftwareSigner
	Boundary *decisiontrust.VerifierBoundary
}

// NewSigning registers one Ed25519 key derived from seedByte. Callers pass
// different bytes so that the keys of different test packages stay distinct.
func NewSigning(t testing.TB, seedByte byte) *Signing {
	t.Helper()
	codec, err := cosecbor.New()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatal(err)
	}
	registry := decisiontrust.NewRegistry(IssuedAt.Add(-time.Hour))
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = seedByte
	private := ed25519.NewKeyFromSeed(seed)
	record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
		ID:                 KeyID,
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: Principal,
		TrustDomain:        TrustDomain,
		Algorithm:          decisiontrust.AlgorithmEdDSA,
		KeyType:            decisiontrust.KeyTypeOKP,
		Curve:              decisiontrust.CurveEd25519,
		PublicKey:          private.Public(),
		SigningNotBefore:   IssuedAt.Add(-time.Hour),
		SigningNotAfter:    IssuedAt.Add(24 * time.Hour),
		PolicyRevision:     KeyPolicyRevision,
	}, IssuedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(record, IssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(record.ID(), IssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrimary(record.ID(), IssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.RegisterPrivateKey(record.ID(), private); err != nil {
		t.Fatal(err)
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            5 * time.Second,
		MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionWorkOrder: 5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		t.Fatal(err)
	}
	return &Signing{t: t, Codec: codec, Payload: payload, Enc: enc, Registry: registry, Signer: signer, Boundary: boundary}
}

// Ref builds a DecisionRef for a decision of the given kind.
func Ref(t testing.TB, kind authority.DecisionKind, id string, digest contract.Digest) decisionpayload.DecisionRef {
	t.Helper()
	r, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{Kind: kind, DecisionID: id, PayloadDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// DefaultConfig is a WorkOrder bound to Worker, Task and Attempt, valid at Now.
func DefaultConfig(t testing.TB) decisionpayload.WorkOrderConfig {
	t.Helper()
	return decisionpayload.WorkOrderConfig{
		SchemaVersion:      decisionpayload.WorkOrderSchemaVersion,
		DecisionKind:       authority.DecisionWorkOrder,
		DecisionID:         "wor-decision-001",
		AuthorityRole:      authority.RoleWorkOrder,
		AuthorityPrincipal: Principal,
		IssuedAt:           IssuedAt,
		ExpiresAt:          Expires,
		PolicyRevision:     PolicyRevision,
		WorkOrderID:        "work-order-001",
		TaskID:             Task,
		TaskIRDigest:       TaskIR,
		AttemptID:          Attempt,
		WorkerIdentity:     Worker,
		PlacementRef:       Ref(t, authority.DecisionPlacementApproval, "placement-001", PlacementDigest),
		LeaseRef:           Ref(t, authority.DecisionLease, LeaseDecisionID, LeaseDigest),
		ExecutorApproval:   Ref(t, authority.DecisionExecutorApproval, "executor-001", ExecutorDigest),
		ExecutorDigest:     ExecutableDigest,
		ReadGrants:         []decisionpayload.DecisionRef{Ref(t, authority.DecisionArtifactReadGrant, "read-001", contract.SHA256([]byte("read")))},
		WriteGrants:        []decisionpayload.DecisionRef{Ref(t, authority.DecisionArtifactWriteGrant, "write-001", contract.SHA256([]byte("write")))},
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeMillis: 600_000,
		},
		Sandbox: decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"},
		Nonce:   bytes.Repeat([]byte{0x5a}, 16),
	}
}

// Order builds a WorkOrder from DefaultConfig after mutate has changed it.
func Order(t testing.TB, mutate func(*decisionpayload.WorkOrderConfig)) decisionpayload.WorkOrder {
	t.Helper()
	cfg := DefaultConfig(t)
	if mutate != nil {
		mutate(&cfg)
	}
	order, err := decisionpayload.NewWorkOrder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

// SignPayload wraps already-encoded payload bytes in a COSE_Sign1 envelope
// signed as the WorkOrder authority. The bytes are not checked, so a test can
// sign a payload of another kind.
func (s *Signing) SignPayload(payload []byte) []byte {
	s.t.Helper()
	protected, err := s.Enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte(KeyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	sigStructure, err := s.Codec.EncodeSignature1(protected, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	signed, err := s.Signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain:        TrustDomain,
			AuthorityRole:      authority.RoleWorkOrder,
			AuthorityPrincipal: Principal,
			DecisionKind:       authority.DecisionWorkOrder,
			Algorithm:          decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: KeyPolicyRevision,
		SigStructure:   sigStructure,
	}, IssuedAt)
	if err != nil {
		s.t.Fatal(err)
	}
	protectedBytes, err := s.Enc.Marshal(protected)
	if err != nil {
		s.t.Fatal(err)
	}
	payloadBytes, err := s.Enc.Marshal(payload)
	if err != nil {
		s.t.Fatal(err)
	}
	sig, err := s.Enc.Marshal(signed.Signature)
	if err != nil {
		s.t.Fatal(err)
	}
	out := []byte{0xd2, 0x84}
	out = append(out, protectedBytes...)
	out = append(out, 0xa0)
	out = append(out, payloadBytes...)
	return append(out, sig...)
}

// SignedWorkOrder encodes the WorkOrder from DefaultConfig after mutate and signs it.
func (s *Signing) SignedWorkOrder(mutate func(*decisionpayload.WorkOrderConfig)) []byte {
	s.t.Helper()
	payload, err := s.Payload.EncodeWorkOrder(Order(s.t, mutate))
	if err != nil {
		s.t.Fatal(err)
	}
	return s.SignPayload(payload)
}
