// Package signedchain builds real PLC-1, LSE-1 and WOR-1 signed material for
// tests that cross a process boundary.
//
// It is test support, not a product package. It takes the same reference
// implementations the vertical slice uses (the COSE codec, the decision
// payload codec, the Authority key registry, the software signer, leaseissue
// and workorderissue) and runs them against a durable State Store: a Task and
// an Attempt are bound to a Worker, a Placement Approval is signed and turned
// into a Lease reservation, the Lease is signed and committed EMITTED, and a
// WorkOrder is signed and committed EMITTED. What is left in the store is what
// the Control Plane would hold, so a Control Plane process that only reads the
// store delivers exactly these bytes.
//
// Keys are derived from a seed, so a test that wants a forger uses a different
// seed under the same key IDs and principals.
package signedchain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/core/workorderissue"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const (
	// TrustDomain is the Trust Domain of the Authority keys.
	TrustDomain = "example.org"

	PlacementPrincipal = "control-plane/plc-1"
	LeasePrincipal     = "control-plane/lse-1"
	WorkOrderPrincipal = "control-plane/wor-1"
	KeyPolicy          = "key-policy-1"

	PlacementKeyID = "kid-plc-1"
	LeaseKeyID     = "kid-lse-1"
	WorkOrderKeyID = "kid-wor-1"

	// BundleRevision is the Trust Bundle revision the helpers sign under.
	BundleRevision = 3
)

// State is the durable State Store the chain is built on.
type State interface {
	statestore.TaskStore
	statestore.AttemptStore
	leaseissue.State
	leaseissue.EmissionState
	workorderissue.IssueState
}

// ExecutorApproval is the EXE-1 reference. It is synthetic: nothing signs it.
var ExecutorApproval = workorderissue.ExecutorApproval{
	DecisionID:     "executor-approval-1",
	PayloadDigest:  contract.SHA256([]byte("executor approval payload")),
	ExecutorDigest: contract.SHA256([]byte("registered executor")),
}

// Limits and Sandbox are what the WorkOrders grant.
var (
	Limits = decisionpayload.ResourceLimits{
		CPUMillis: 1000, MemoryBytes: 1 << 28, TemporaryDiskBytes: 1 << 20,
		WallTimeMillis: 300_000, OutputBytesMaximum: 1 << 20, NetworkBytesMax: 1 << 20,
	}
	Sandbox = decisionpayload.SandboxPolicy{Profile: "sandbox/container/v1"}
)

// Authorities is one set of Authority keys with everything that signs and
// verifies with them.
type Authorities struct {
	Registry  *decisiontrust.Registry
	Signer    *decisiontrust.ReferenceSoftwareSigner
	Codec     *cosecbor.Codec
	Payload   *payloadcbor.Decoder
	Boundary  *decisiontrust.VerifierBoundary
	Placement *placementverify.Verifier

	enc fxcbor.EncMode
	// KeyTime is when the keys were registered and activated.
	KeyTime time.Time
	// PublicKeys are the public halves, in the order PLC-1, LSE-1, WOR-1.
	PublicKeys []PublicKey
}

// PublicKey is what a verifier needs to trust one Authority key.
type PublicKey struct {
	ID        string
	Role      authority.Role
	Principal string
	Key       ed25519.PublicKey
	NotBefore time.Time
	NotAfter  time.Time
}

// New builds the keys. keyTime is when they were registered; they sign from
// keyTime to keyTime+25h. A different seed gives different keys under the same
// IDs, which is what a forger would have.
func New(t testing.TB, keyTime time.Time, seed byte) *Authorities {
	t.Helper()
	codec, err := cosecbor.New()
	must(t, err)
	payload, err := payloadcbor.New()
	must(t, err)
	enc, err := fxcbor.CoreDetEncOptions().EncMode()
	must(t, err)
	registry := decisiontrust.NewRegistry(keyTime)
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	must(t, err)
	a := &Authorities{Registry: registry, Signer: signer, Codec: codec, Payload: payload, enc: enc, KeyTime: keyTime}
	for i, k := range []struct {
		id        string
		role      authority.Role
		principal string
	}{
		{PlacementKeyID, authority.RolePlacementAuthorization, PlacementPrincipal},
		{LeaseKeyID, authority.RoleLease, LeasePrincipal},
		{WorkOrderKeyID, authority.RoleWorkOrder, WorkOrderPrincipal},
	} {
		keySeed := make([]byte, ed25519.SeedSize)
		keySeed[0], keySeed[1] = seed, byte(i)
		private := ed25519.NewKeyFromSeed(keySeed)
		notAfter := keyTime.Add(25 * time.Hour)
		record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
			ID: decisiontrust.KeyID(k.id), AuthorityRole: k.role, AuthorityPrincipal: k.principal,
			TrustDomain: TrustDomain, Algorithm: decisiontrust.AlgorithmEdDSA,
			KeyType: decisiontrust.KeyTypeOKP, Curve: decisiontrust.CurveEd25519, PublicKey: private.Public(),
			SigningNotBefore: keyTime, SigningNotAfter: notAfter, PolicyRevision: KeyPolicy,
		}, keyTime)
		must(t, err)
		for _, step := range []func() error{
			func() error { return registry.Register(record, keyTime) },
			func() error { return registry.Activate(record.ID(), keyTime) },
			func() error { return registry.SetPrimary(record.ID(), keyTime) },
			func() error { return signer.RegisterPrivateKey(record.ID(), private) },
		} {
			must(t, step())
		}
		a.PublicKeys = append(a.PublicKeys, PublicKey{
			ID: k.id, Role: k.role, Principal: k.principal,
			Key: private.Public().(ed25519.PublicKey), NotBefore: keyTime, NotAfter: notAfter,
		})
	}
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew: 5 * time.Second, MaxTrustBundleStaleness: time.Hour,
		AllowedAlgorithms: []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionPlacementApproval: 10 * time.Minute,
			authority.DecisionLease:             10 * time.Minute,
			authority.DecisionWorkOrder:         10 * time.Minute,
		},
	})
	must(t, err)
	a.Boundary, err = decisiontrust.NewVerifierBoundary(registry, policy)
	must(t, err)
	a.Placement, err = placementverify.New(placementverify.Config{
		Codec: codec, Payload: payload, Boundary: a.Boundary, TrustDomain: TrustDomain,
	})
	must(t, err)
	return a
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type bundle struct{ at time.Time }

func (b bundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return decisiontrust.TrustBundleMetadata{Revision: BundleRevision, ValidatedAt: b.at}, nil
}

// Placed is one Attempt taken through the signed chain: the Control Plane's
// store holds the EMITTED Lease and WorkOrder, and these are the same bytes.
type Placed struct {
	TaskID          ids.TaskID
	AttemptID       ids.AttemptID
	LeaseID         ids.LeaseID
	Worker          string
	FenceEpoch      uint64
	SignedPlacement []byte
	SignedLease     []byte
	SignedWorkOrder []byte
	LeaseExpiresAt  time.Time
}

// Place creates the n-th Task and its first Attempt, binds the Attempt to
// worker, and runs PLC-1, LSE-1 and WOR-1 in the store as if the Control Plane
// clock read at. A Lease signed with at in the past is expired for a Worker
// that reads the real clock, which is how a test makes an expired Lease.
//
// policy is the active Policy the store already holds.
func (a *Authorities) Place(t testing.TB, state State, policy policystate.State, n int, worker string, at time.Time) Placed {
	t.Helper()
	ctx := context.Background()
	clk, bun := clock{now: at}, bundle{at: at}

	task, err := taskstate.NewTask(ids.TaskID(fmt.Sprintf("tsk_%032x", n)), contract.SHA256([]byte(fmt.Sprintf("taskir-%d", n))), at.Add(-time.Minute))
	must(t, err)
	task.Phase = taskstate.TaskPhaseReady
	must(t, state.CreateTask(ctx, task))
	attempt, err := attemptstate.New(ids.AttemptID(fmt.Sprintf("atm_%032x", n)), task.ID, task.TaskIRDigest, 1, task.CreatedAt)
	must(t, err)
	must(t, state.CreateAttempt(ctx, attempt))
	placing := attempt
	placing.Phase, placing.WorkerIdentity = attemptstate.AttemptPhasePlacing, worker
	placing.Revision++
	placing.UpdatedAt = at
	must(t, state.CompareAndSwapAttempt(ctx, attempt.ID, attempt.Revision, placing))

	signedPlacement := a.signPlacement(t, placementSpec{
		decisionID: "placement-" + attempt.ID.String(), task: task.ID, taskIR: task.TaskIRDigest, attempt: attempt.ID,
		worker: worker, policy: policy.Revision, issuedAt: at.Add(-time.Second), expiresAt: at.Add(5 * time.Minute),
	})
	svc, err := leaseissue.New(leaseissue.Config{Verifier: a.Placement, State: state, TrustBundle: bun, Clock: clk, MaxLifetime: 5 * time.Minute})
	must(t, err)
	reserved, err := svc.Reserve(ctx, leaseissue.Request{SignedPlacement: signedPlacement, RequestedLifetime: 3 * time.Minute})
	must(t, err)

	emitter, err := leaseissue.NewEmitter(leaseissue.EmitterConfig{
		State: state, Signer: a.Signer, Keys: a.Registry, Verifier: a.Boundary,
		Payload: a.Payload, Envelope: a.Codec, TrustBundle: bun, Clock: clk,
		TrustDomain: TrustDomain, AuthorityPrincipal: LeasePrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: KeyPolicy,
	})
	must(t, err)
	emitted, err := emitter.Emit(ctx, leaseissue.EmitRequest{LeaseID: reserved.Lease.ID})
	must(t, err)
	if emitted.Record.Status() != leaseemission.StatusEmitted {
		t.Fatalf("Lease emission = %v", emitted.Record.Status())
	}

	issuer, err := workorderissue.New(workorderissue.Config{
		State: state, Signer: a.Signer, Keys: a.Registry, Verifier: a.Boundary,
		Payload: a.Payload, Envelope: a.Codec, TrustBundle: bun, Clock: clk,
		Nonce: rand.Reader, TrustDomain: TrustDomain, AuthorityPrincipal: WorkOrderPrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: KeyPolicy, MaxLifetime: 5 * time.Minute,
	})
	must(t, err)
	order, err := issuer.Issue(ctx, workorderissue.IssueRequest{
		LeaseID: reserved.Lease.ID, WorkerIdentity: worker,
		ExecutorApproval: ExecutorApproval, Limits: Limits, Sandbox: Sandbox,
	})
	must(t, err)
	return Placed{
		TaskID: task.ID, AttemptID: attempt.ID, LeaseID: reserved.Lease.ID, Worker: worker,
		FenceEpoch: reserved.Lease.FenceEpoch, SignedPlacement: signedPlacement,
		SignedLease: emitted.SignedLease, SignedWorkOrder: order.SignedWorkOrder,
		LeaseExpiresAt: reserved.Lease.ExpiresAt,
	}
}

type placementSpec struct {
	decisionID string
	task       ids.TaskID
	taskIR     contract.Digest
	attempt    ids.AttemptID
	worker     string
	policy     string
	issuedAt   time.Time
	expiresAt  time.Time
}

// signPlacement produces the Placement Approval a PLC-1 would sign.
func (a *Authorities) signPlacement(t testing.TB, s placementSpec) []byte {
	t.Helper()
	digestBytes, err := hex.DecodeString(s.taskIR.Hex())
	must(t, err)
	mb := func(v []byte, err error) []byte {
		t.Helper()
		must(t, err)
		return v
	}
	payload := mb(a.enc.Marshal(map[uint64]any{
		1: uint64(1),
		2: string(authority.DecisionPlacementApproval),
		3: s.decisionID,
		4: map[uint64]any{1: string(authority.RolePlacementAuthorization), 2: PlacementPrincipal},
		5: uint64(s.issuedAt.UnixMilli()),
		6: uint64(s.expiresAt.UnixMilli()),
		7: s.policy,
		8: map[uint64]any{
			1: s.task.String(),
			2: map[uint64]any{1: uint64(1), 2: digestBytes},
			3: s.attempt.String(),
			4: s.worker,
		},
	}))
	protected := mb(a.enc.Marshal(map[int64]any{
		1:      int64(-8),
		2:      []int64{decisiontrust.COSEHeaderLabelEKIProfile},
		4:      []byte(PlacementKeyID),
		-70000: decisiontrust.SignedDecisionProfileV1,
	}))
	sigStructure, err := a.Codec.EncodeSignature1(protected, payload)
	must(t, err)
	signed, err := a.Signer.SignCOSESigStructure(decisiontrust.COSESigningRequest{
		Key: decisiontrust.SigningRequest{
			TrustDomain: TrustDomain, AuthorityRole: authority.RolePlacementAuthorization,
			AuthorityPrincipal: PlacementPrincipal, DecisionKind: authority.DecisionPlacementApproval,
			Algorithm: decisiontrust.AlgorithmEdDSA,
		},
		PolicyRevision: KeyPolicy, SigStructure: sigStructure,
	}, s.issuedAt)
	must(t, err)
	out := []byte{0xd2, 0x84}
	out = append(out, mb(a.enc.Marshal(protected))...)
	out = append(out, 0xa0)
	out = append(out, mb(a.enc.Marshal(payload))...)
	out = append(out, mb(a.enc.Marshal(signed.Signature))...)
	return out
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TrustAnchor is the file a Worker loads (internal/agent/trustanchor) to trust
// these keys: the Lease and WorkOrder keys, the bundle validated at
// validatedAt, and the Policy revision the Worker accepts.
func (a *Authorities) TrustAnchor(validatedAt time.Time, policyRevision string) trustanchor.File {
	doc := trustanchor.File{
		TrustDomain: TrustDomain, PolicyRevision: policyRevision,
		BundleRevision: BundleRevision, ValidatedAt: validatedAt,
		MaxClockSkewSeconds: 5, MaxBundleStalenessSeconds: 3600, MaxDecisionLifetimeSeconds: 600,
	}
	for _, k := range a.PublicKeys {
		doc.Keys = append(doc.Keys, trustanchor.Key{
			KeyID: k.ID, Role: string(k.Role), Principal: k.Principal, Algorithm: "EdDSA",
			PublicKey:        base64.StdEncoding.EncodeToString(k.Key),
			SigningNotBefore: k.NotBefore, SigningNotAfter: k.NotAfter, KeyPolicy: KeyPolicy,
		})
	}
	return doc
}
