package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"

	cosecbor "github.com/hat47x/eki-fabric/internal/adapters/reference/cosedecoder/cbor"
	payloadcbor "github.com/hat47x/eki-fabric/internal/adapters/reference/decisiondecoder/cbor"
	"github.com/hat47x/eki-fabric/internal/control/dispatch"
	"github.com/hat47x/eki-fabric/internal/control/signingkeys"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/core/leaseissue"
	"github.com/hat47x/eki-fabric/internal/core/placementissue"
	"github.com/hat47x/eki-fabric/internal/core/placementverify"
	"github.com/hat47x/eki-fabric/internal/core/workorderissue"
)

// DispatchConfig wires the Control Plane's placement loop. All of it is
// optional: a process without it accepts Tasks but does not move them.
type DispatchConfig struct {
	// SigningKeysFile is the Control Plane's own signing keys for the PLC-1,
	// LSE-1, and WOR-1 roles (internal/control/signingkeys).
	SigningKeysFile string `json:"signing_keys_file"`
	// TrustDomain names this deployment. It must match the signing keys and
	// the Workers' trust anchors.
	TrustDomain string `json:"trust_domain"`
	// PlacementPrincipal, LeasePrincipal, and WorkOrderPrincipal name the
	// three Authority instances this process signs as.
	PlacementPrincipal string `json:"placement_principal"`
	LeasePrincipal     string `json:"lease_principal"`
	WorkOrderPrincipal string `json:"work_order_principal"`
	// KeyPolicyRevision is the key-management policy revision the signing
	// keys are registered under.
	KeyPolicyRevision string `json:"key_policy_revision"`
	// Interval is how often the loop runs.
	Interval time.Duration `json:"interval"`
	// LeaseLifetime is the Lease lifetime the loop asks for.
	LeaseLifetime time.Duration `json:"lease_lifetime"`
	// PlacementValidity caps the Placement Approval window.
	PlacementValidity time.Duration `json:"placement_validity"`
	// Limit bounds how many Tasks one tick may advance.
	Limit int `json:"limit,omitempty"`

	// ExecutorApproval is the EXE-1 approval the issued WorkOrders grant.
	// Required: the MVP has nothing to derive it from yet (#242).
	ExecutorApproval *ExecutorApprovalConfig `json:"executor_approval"`
	// Limits and Sandbox are what the issued WorkOrders grant.
	Limits  *decisionpayload.ResourceLimits `json:"limits"`
	Sandbox *decisionpayload.SandboxPolicy  `json:"sandbox"`
}

// ExecutorApprovalConfig is the JSON form of workorderissue.ExecutorApproval.
type ExecutorApprovalConfig struct {
	DecisionID     string `json:"decision_id"`
	PayloadDigest  string `json:"payload_digest"`
	ExecutorDigest string `json:"executor_digest"`
}

func (c *ExecutorApprovalConfig) digest(raw string) contract.Digest {
	d, err := contract.ParseSHA256(raw)
	if err != nil {
		return contract.SHA256([]byte(raw))
	}
	return d
}

func (c *DispatchConfig) executorApproval() workorderissue.ExecutorApproval {
	if c.ExecutorApproval == nil {
		return workorderissue.ExecutorApproval{}
	}
	return workorderissue.ExecutorApproval{
		DecisionID:     c.ExecutorApproval.DecisionID,
		PayloadDigest:  c.ExecutorApproval.digest(c.ExecutorApproval.PayloadDigest),
		ExecutorDigest: c.ExecutorApproval.digest(c.ExecutorApproval.ExecutorDigest),
	}
}

func (c *DispatchConfig) limits() decisionpayload.ResourceLimits {
	if c.Limits == nil {
		return decisionpayload.ResourceLimits{}
	}
	return *c.Limits
}

func (c *DispatchConfig) sandbox() decisionpayload.SandboxPolicy {
	if c.Sandbox == nil {
		return decisionpayload.SandboxPolicy{}
	}
	return *c.Sandbox
}

// defaults fills the intervals the loop needs to run.
func (c *DispatchConfig) defaults() {
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.LeaseLifetime <= 0 {
		c.LeaseLifetime = 5 * time.Minute
	}
	if c.PlacementValidity <= 0 {
		c.PlacementValidity = 10 * time.Minute
	}
	if c.Limit <= 0 {
		c.Limit = 8
	}
}

func (c *DispatchConfig) validate() error {
	if c.SigningKeysFile == "" {
		return errors.New("signing_keys_file is required")
	}
	if c.TrustDomain == "" {
		return errors.New("trust_domain is required")
	}
	if c.PlacementPrincipal == "" || c.LeasePrincipal == "" || c.WorkOrderPrincipal == "" {
		return errors.New("placement_principal, lease_principal and work_order_principal are required")
	}
	if c.KeyPolicyRevision == "" {
		return errors.New("key_policy_revision is required")
	}
	return nil
}

// startDispatch builds the issuing chain and runs the placement loop. Any
// failure is a configuration failure and stops the start-up: a Control Plane
// configured to dispatch must not silently run without it.
func (s *Server) startDispatch(ctx context.Context, cfg *DispatchConfig, store *sqlite.Store) error {
	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	keys, err := signingkeys.Load(cfg.SigningKeysFile)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	// The loop verifies its own signatures before handing them on, against
	// the same key state it signs with and a bundle that never goes stale
	// (the keys are in the same file).
	boundary, err := selfVerifierBoundary(keys, cfg)
	if err != nil {
		return err
	}
	codec, err := cosecbor.New()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	payload, err := payloadcbor.New()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	bundle := selfBundle{at: time.Now().UTC()}
	clk := systemClock{}

	placement, err := placementissue.New(placementissue.Config{
		Signer: keys.Signer, Keys: keys.Registry, Verifier: boundary, Payload: payload, Envelope: codec,
		TrustBundle: bundle, Clock: clk,
		TrustDomain: cfg.TrustDomain, AuthorityPrincipal: cfg.PlacementPrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: cfg.KeyPolicyRevision,
		MaxValidity: cfg.PlacementValidity,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	verifier, err := placementverify.New(placementverify.Config{
		Codec: codec, Payload: payload, Boundary: boundary, TrustDomain: cfg.TrustDomain,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	leaseService, err := leaseissue.New(leaseissue.Config{
		Verifier: verifier, State: store, TrustBundle: bundle, Clock: clk,
		MaxLifetime: cfg.PlacementValidity,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	emitter, err := leaseissue.NewEmitter(leaseissue.EmitterConfig{
		State: store, Signer: keys.Signer, Keys: keys.Registry, Verifier: boundary,
		Payload: payload, Envelope: codec, TrustBundle: bundle, Clock: clk,
		TrustDomain: cfg.TrustDomain, AuthorityPrincipal: cfg.LeasePrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: cfg.KeyPolicyRevision,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	orders, err := workorderissue.New(workorderissue.Config{
		State: store, Signer: keys.Signer, Keys: keys.Registry, Verifier: boundary,
		Payload: payload, Envelope: codec, TrustBundle: bundle, Clock: clk,
		Nonce: rand.Reader, TrustDomain: cfg.TrustDomain, AuthorityPrincipal: cfg.WorkOrderPrincipal,
		Algorithm: decisiontrust.AlgorithmEdDSA, KeyPolicyRevision: cfg.KeyPolicyRevision,
		MaxLifetime: cfg.PlacementValidity,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	loop, err := dispatch.New(dispatch.Config{
		Tasks: store, Attempts: store, Workers: store, Policies: store,
		IssuePlacement: func(ctx context.Context, req placementissue.IssueRequest) ([]byte, error) {
			result, err := placement.Issue(ctx, req)
			if err != nil {
				return nil, err
			}
			return result.SignedPlacement, nil
		},
		Reserve: func(ctx context.Context, signedPlacement []byte, lifetime time.Duration) (dispatch.ReservedLease, error) {
			reserved, err := leaseService.Reserve(ctx, leaseissue.Request{
				SignedPlacement: signedPlacement, RequestedLifetime: lifetime,
			})
			if err != nil {
				return dispatch.ReservedLease{}, err
			}
			return dispatch.ReservedLease{LeaseID: reserved.Lease.ID, FenceEpoch: reserved.Lease.FenceEpoch}, nil
		},
		EmitLease: func(ctx context.Context, leaseID ids.LeaseID) ([]byte, error) {
			emitted, err := emitter.Emit(ctx, leaseissue.EmitRequest{LeaseID: leaseID})
			if err != nil {
				return nil, err
			}
			return emitted.SignedLease, nil
		},
		IssueWorkOrder: func(ctx context.Context, leaseID ids.LeaseID, worker string) ([]byte, error) {
			order, err := orders.Issue(ctx, workorderissue.IssueRequest{
				LeaseID: leaseID, WorkerIdentity: worker,
				ExecutorApproval: cfg.executorApproval(), Limits: cfg.limits(), Sandbox: cfg.sandbox(),
			})
			if err != nil {
				return nil, err
			}
			return order.SignedWorkOrder, nil
		},
		LeaseLifetime: cfg.LeaseLifetime,
		Limit:         cfg.Limit,
		Clock:         clk,
		Log:           s.log,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	s.dispatchDone = make(chan struct{})
	go func() {
		defer close(s.dispatchDone)
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			result, err := loop.Tick(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.log.Error("dispatch tick failed", "error", err.Error())
				continue
			}
			if result.Dispatched != 0 || len(result.Deferred) != 0 {
				s.log.Info("dispatch tick", "considered", result.Considered, "dispatched", result.Dispatched, "deferred", len(result.Deferred))
			}
			for _, d := range result.Deferred {
				s.log.Debug("dispatch deferred", "task", d.TaskID.String(), "reason", d.Reason)
			}
		}
	}()
	return nil
}

// selfVerifierBoundary builds the verifier boundary the loop checks its own
// signatures with: the signing keys themselves and a policy that allows the
// three Authority roles the process signs for.
func selfVerifierBoundary(keys *signingkeys.Keys, cfg *DispatchConfig) (*decisiontrust.VerifierBoundary, error) {
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		// The loop verifies what it just signed, so no clock skew is expected.
		MaxClockSkew:            time.Minute,
		MaxTrustBundleStaleness: 24 * time.Hour,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionPlacementApproval: 24 * time.Hour,
			authority.DecisionLease:             24 * time.Hour,
			authority.DecisionWorkOrder:         24 * time.Hour,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(keys.Registry, policy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return boundary, nil
}

// selfBundle is the Trust Bundle of the process's own keys: revision 1,
// validated when the keys were loaded.
type selfBundle struct{ at time.Time }

func (b selfBundle) CurrentTrustBundle(context.Context) (decisiontrust.TrustBundleMetadata, error) {
	return decisiontrust.TrustBundleMetadata{Revision: 1, ValidatedAt: b.at}, nil
}
