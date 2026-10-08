package federation

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewRegistryRequiresPositiveMaxAge(t *testing.T) {
	for _, age := range []time.Duration{0, -time.Second} {
		if _, err := NewRegistry(age); !errors.Is(err, ErrInvalidMaxBundleAge) {
			t.Fatalf("NewRegistry(%s) error = %v, want ErrInvalidMaxBundleAge", age, err)
		}
	}
	if _, err := NewRegistry(time.Nanosecond); err != nil {
		t.Fatalf("NewRegistry(1ns): %v", err)
	}
}

func TestFederateRequiresAuthoritySignature(t *testing.T) {
	w := newTestWorld(t)
	bundle := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b11)...)
	next := mustBundle(t, w.alpha.domain, 2, testNow, w.bundleKeys(0b11)...)

	cases := map[string]struct {
		signature []byte
		signer    party
	}{
		"nil signature":                 {nil, w.alpha},
		"signed by another authority":   {authoritySign(t, w.beta, bundle), w.beta},
		"signature over another bundle": {authoritySign(t, w.alpha, next), w.alpha},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reg := mustRegistry(t)
			err := reg.Federate(w.alphaLink(t), bundle, tc.signature)
			if !errors.Is(err, ErrBundleSignature) {
				t.Fatalf("Federate error = %v, want ErrBundleSignature", err)
			}
			if _, err := reg.Binding(w.alpha.domain); !errors.Is(err, ErrNotFederated) {
				t.Fatalf("a rejected Federate left a binding behind: %v", err)
			}
		})
	}

	reg := mustRegistry(t)
	if err := reg.Federate(w.alphaLink(t), bundle, authoritySign(t, w.alpha, bundle)); err != nil {
		t.Fatalf("Federate with a valid signature: %v", err)
	}
}

func TestFederateRejectsDomainMismatchAndInvalidLink(t *testing.T) {
	w := newTestWorld(t)
	alphaBundle := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b11)...)
	reg := mustRegistry(t)

	if err := reg.Federate(w.betaLink(t), alphaBundle, authoritySign(t, w.alpha, alphaBundle)); !errors.Is(err, ErrDomainMismatch) {
		t.Fatalf("Federate with a bundle of another domain: error = %v, want ErrDomainMismatch", err)
	}
	if err := reg.Federate(Link{}, alphaBundle, authoritySign(t, w.alpha, alphaBundle)); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("Federate with a zero link: error = %v, want ErrInvalidLink", err)
	}
}

func TestFederateOncePerDomain(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	again := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b01)...)
	err := reg.Federate(w.alphaLink(t), again, authoritySign(t, w.alpha, again))
	if !errors.Is(err, ErrAlreadyFederated) {
		t.Fatalf("second Federate error = %v, want ErrAlreadyFederated", err)
	}
}

func TestAuthorityKeyCannotBePinnedForTwoDomains(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	// beta reuses alpha's authority key. Each domain must have its own root.
	link := mustLink(t, w.gamma.domain, w.alpha.authPub, PurposeTaskSubmission)
	bundle := mustBundle(t, w.gamma.domain, 1, testNow, BundleKey{ID: "k1", PublicKey: w.keyPubs[0]})
	err := reg.Federate(link, bundle, authoritySign(t, w.alpha, bundle))
	if !errors.Is(err, ErrAuthorityKeyReused) {
		t.Fatalf("Federate with a reused authority key: error = %v, want ErrAuthorityKeyReused", err)
	}
}

func TestInstallBundleFollowsRollbackAndEquivocationRules(t *testing.T) {
	w := newTestWorld(t)
	reg := mustRegistry(t)
	v1Validated := testNow.Add(-3 * time.Hour)
	v1 := mustBundle(t, w.alpha.domain, 1, v1Validated, w.bundleKeys(0b01)...)
	if err := reg.Federate(w.alphaLink(t), v1, authoritySign(t, w.alpha, v1)); err != nil {
		t.Fatalf("Federate: %v", err)
	}

	// The same bundle again is idempotent.
	if err := reg.InstallBundle(v1, authoritySign(t, w.alpha, v1)); err != nil {
		t.Fatalf("re-install of the bound bundle: %v", err)
	}

	v2 := mustBundle(t, w.alpha.domain, 2, testNow.Add(-2*time.Hour), w.bundleKeys(0b11)...)
	if err := reg.InstallBundle(v2, authoritySign(t, w.alpha, v2)); err != nil {
		t.Fatalf("install of sequence 2: %v", err)
	}
	binding, err := reg.Binding(w.alpha.domain)
	if err != nil {
		t.Fatalf("Binding: %v", err)
	}
	if binding.Sequence() != 2 || binding.Digest() != v2.Digest() {
		t.Fatalf("binding = seq %d digest %s, want seq 2 digest %s", binding.Sequence(), binding.Digest(), v2.Digest())
	}

	// A lower sequence is a rollback, even when it is validly signed.
	if err := reg.InstallBundle(v1, authoritySign(t, w.alpha, v1)); !errors.Is(err, ErrRollback) {
		t.Fatalf("rollback to sequence 1: error = %v, want ErrRollback", err)
	}

	// The bound sequence with different content is equivocation.
	forked := mustBundle(t, w.alpha.domain, 2, testNow.Add(-2*time.Hour), w.bundleKeys(0b10)...)
	if err := reg.InstallBundle(forked, authoritySign(t, w.alpha, forked)); !errors.Is(err, ErrEquivocation) {
		t.Fatalf("different digest at sequence 2: error = %v, want ErrEquivocation", err)
	}
	binding, _ = reg.Binding(w.alpha.domain)
	if binding.Digest() != v2.Digest() {
		t.Fatal("an equivocating bundle replaced the binding")
	}

	// The same content with a later validated-at time refreshes freshness.
	later := mustBundle(t, w.alpha.domain, 2, testNow.Add(-time.Minute), w.bundleKeys(0b11)...)
	if err := reg.InstallBundle(later, authoritySign(t, w.alpha, later)); err != nil {
		t.Fatalf("re-validation of the bound bundle: %v", err)
	}
	binding, _ = reg.Binding(w.alpha.domain)
	if !binding.ValidatedAt().Equal(testNow.Add(-time.Minute)) {
		t.Fatalf("validated-at = %s, want the later value", binding.ValidatedAt())
	}

	// An earlier validated-at time never moves freshness backward.
	earlier := mustBundle(t, w.alpha.domain, 2, testNow.Add(-5*time.Hour), w.bundleKeys(0b11)...)
	if err := reg.InstallBundle(earlier, authoritySign(t, w.alpha, earlier)); err != nil {
		t.Fatalf("install of an older validation of the bound bundle: %v", err)
	}
	binding, _ = reg.Binding(w.alpha.domain)
	if !binding.ValidatedAt().Equal(testNow.Add(-time.Minute)) {
		t.Fatalf("validated-at moved backward to %s", binding.ValidatedAt())
	}

	// A new sequence must carry a valid authority signature.
	v3 := mustBundle(t, w.alpha.domain, 3, testNow, w.bundleKeys(0b01)...)
	if err := reg.InstallBundle(v3, authoritySign(t, w.beta, v3)); !errors.Is(err, ErrBundleSignature) {
		t.Fatalf("sequence 3 signed by the wrong authority: error = %v, want ErrBundleSignature", err)
	}
	binding, _ = reg.Binding(w.alpha.domain)
	if binding.Sequence() != 2 {
		t.Fatal("a badly signed bundle changed the binding")
	}
	if err := reg.InstallBundle(v3, authoritySign(t, w.alpha, v3)); err != nil {
		t.Fatalf("sequence 3 with a valid signature: %v", err)
	}
	binding, _ = reg.Binding(w.alpha.domain)
	if binding.Sequence() != 3 || binding.Digest() != v3.Digest() {
		t.Fatal("sequence 3 did not replace the binding")
	}
}

func TestInstallBundleRequiresFederation(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	bundle := mustBundle(t, w.gamma.domain, 1, testNow, BundleKey{ID: "k1", PublicKey: w.outsiderPub})
	err := reg.InstallBundle(bundle, authoritySign(t, w.gamma, bundle))
	if !errors.Is(err, ErrNotFederated) {
		t.Fatalf("InstallBundle for an unfederated domain: error = %v, want ErrNotFederated", err)
	}
}

func TestBindingQueriesAreOnePerDomain(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)

	alpha, err := reg.Binding(w.alpha.domain)
	if err != nil {
		t.Fatalf("Binding(alpha): %v", err)
	}
	if alpha.Domain() != w.alpha.domain || alpha.Sequence() != 1 {
		t.Fatalf("alpha binding = %s seq %d", alpha.Domain(), alpha.Sequence())
	}
	if !alpha.Allows(PurposeTaskSubmission) || alpha.Allows(PurposeDataClassAccess) {
		t.Fatal("alpha binding does not report its purpose set")
	}
	if len(alpha.Bundle().Keys()) != 2 {
		t.Fatalf("alpha binding holds %d keys, want 2", len(alpha.Bundle().Keys()))
	}
	if alpha.Revoked() || !alpha.RevokedAt().IsZero() {
		t.Fatal("a live binding reports a revocation")
	}

	beta, err := reg.Binding(w.beta.domain)
	if err != nil {
		t.Fatalf("Binding(beta): %v", err)
	}
	if beta.Digest() == alpha.Digest() {
		t.Fatal("two domains share one bundle digest")
	}

	if _, err := reg.Binding(w.gamma.domain); !errors.Is(err, ErrNotFederated) {
		t.Fatalf("Binding(unfederated) error = %v, want ErrNotFederated", err)
	}
	if _, err := reg.Binding(TrustDomain{}); err == nil {
		t.Fatal("Binding(zero domain) succeeded")
	}
}

func TestRevocationKeepsHistoryAndIsTerminal(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	effective := testNow.Add(time.Hour)

	if err := reg.Revoke(w.beta.domain, effective); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	binding, err := reg.Binding(w.beta.domain)
	if err != nil {
		t.Fatalf("Binding after revocation: %v", err)
	}
	if !binding.Revoked() || !binding.RevokedAt().Equal(effective) {
		t.Fatalf("revocation not queryable: revoked=%v at=%s", binding.Revoked(), binding.RevokedAt())
	}
	if binding.Sequence() != 1 {
		t.Fatal("revocation discarded the bound bundle")
	}

	if err := reg.Revoke(w.beta.domain, effective); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatalf("second Revoke error = %v, want ErrAlreadyRevoked", err)
	}
	if err := reg.Revoke(w.gamma.domain, effective); !errors.Is(err, ErrNotFederated) {
		t.Fatalf("Revoke(unfederated) error = %v, want ErrNotFederated", err)
	}
	if err := reg.Revoke(w.alpha.domain, time.Time{}); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Revoke with zero time error = %v, want ErrInvalidTime", err)
	}
	if err := reg.Revoke(TrustDomain{}, effective); !errors.Is(err, ErrInvalidTrustDomain) {
		t.Fatalf("Revoke(zero domain) error = %v, want ErrInvalidTrustDomain", err)
	}

	next := mustBundle(t, w.beta.domain, 2, testNow, w.bundleKeys(0b01)...)
	if err := reg.InstallBundle(next, authoritySign(t, w.beta, next)); !errors.Is(err, ErrDomainRevoked) {
		t.Fatalf("InstallBundle for a revoked domain: error = %v, want ErrDomainRevoked", err)
	}
	again := mustBundle(t, w.beta.domain, 1, testNow, w.bundleKeys(0b01)...)
	if err := reg.Federate(w.betaLink(t), again, authoritySign(t, w.beta, again)); !errors.Is(err, ErrAlreadyFederated) {
		t.Fatalf("Federate for a revoked domain: error = %v, want ErrAlreadyFederated", err)
	}
	stillRevoked, _ := reg.Binding(w.beta.domain)
	if !stillRevoked.Revoked() || stillRevoked.Sequence() != 1 {
		t.Fatal("revoked binding was replaced")
	}
}

func TestNilAndZeroRegistriesFailClosed(t *testing.T) {
	w := newTestWorld(t)
	bundle := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b01)...)
	id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")

	var nilReg *Registry
	zeroReg := &Registry{}
	for name, reg := range map[string]*Registry{"nil": nilReg, "zero": zeroReg} {
		t.Run(name, func(t *testing.T) {
			if err := reg.Federate(w.alphaLink(t), bundle, authoritySign(t, w.alpha, bundle)); !errors.Is(err, ErrNilRegistry) {
				t.Fatalf("Federate error = %v, want ErrNilRegistry", err)
			}
			if err := reg.InstallBundle(bundle, authoritySign(t, w.alpha, bundle)); !errors.Is(err, ErrNilRegistry) {
				t.Fatalf("InstallBundle error = %v, want ErrNilRegistry", err)
			}
			if err := reg.Revoke(w.alpha.domain, testNow); !errors.Is(err, ErrNilRegistry) {
				t.Fatalf("Revoke error = %v, want ErrNilRegistry", err)
			}
			if _, err := reg.Binding(w.alpha.domain); !errors.Is(err, ErrNilRegistry) {
				t.Fatalf("Binding error = %v, want ErrNilRegistry", err)
			}
			_, err := reg.Verify(id, PurposeExecutorParticipation, testNow)
			expectReason(t, err, ReasonMalformedInput)
		})
	}
}

func TestRegistryIsSafeForConcurrentUse(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				// Errors are allowed here, because the writer may briefly bind a
				// bundle without k1. The race detector and the state checks do the work.
				_, _ = reg.Verify(id, PurposeTaskSubmission, testNow)
				_, _ = reg.Binding(w.alpha.domain)
			}
		}()
	}
	for seq := uint64(2); seq < 22; seq++ {
		bundle := mustBundle(t, w.alpha.domain, seq, testNow, w.bundleKeys(0b11)...)
		if err := reg.InstallBundle(bundle, authoritySign(t, w.alpha, bundle)); err != nil {
			t.Errorf("InstallBundle(%d): %v", seq, err)
		}
	}
	wg.Wait()
}
