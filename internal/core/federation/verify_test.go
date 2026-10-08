package federation

import (
	"errors"
	"testing"
	"time"
)

func TestVerifyBindsEachIdentityToItsClaimedDomain(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	const subject = "worker-1"

	cases := []struct {
		name    string
		id      Identity
		purpose Purpose
		want    Reason // empty means the identity must be accepted
	}{
		{
			name:    "alpha identity signed by its own key",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
		},
		{
			name:    "beta identity under the key that beta also binds (same key, own namespace)",
			id:      makeIdentity(t, w.beta.domain, "k1", w.keyPrivs[0], w.beta.domain, subject),
			purpose: PurposeExecutorParticipation,
		},
		{
			name:    "signature made for alpha, claimed as beta, with a key bound in both",
			id:      makeIdentity(t, w.beta.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonSignatureInvalid,
		},
		{
			name:    "key bound only in alpha cannot vouch for an identity claiming beta",
			id:      makeIdentity(t, w.beta.domain, "k2", w.keyPrivs[1], w.beta.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonUnknownKey,
		},
		{
			name:    "alpha-only key with an alpha signature, claimed as beta",
			id:      makeIdentity(t, w.beta.domain, "k2", w.keyPrivs[1], w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonUnknownKey,
		},
		{
			name:    "key ID names k1 but the signature comes from k2",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[1], w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonSignatureInvalid,
		},
		{
			name:    "key ID not in the bundle",
			id:      makeIdentity(t, w.alpha.domain, "k9", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonUnknownKey,
		},
		{
			name:    "outsider key claims alpha by its own ID",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.outsiderPriv, w.alpha.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonSignatureInvalid,
		},
		{
			name:    "identity of an unfederated domain",
			id:      makeIdentity(t, w.gamma.domain, "k1", w.outsiderPriv, w.gamma.domain, subject),
			purpose: PurposeExecutorParticipation,
			want:    ReasonNotFederated,
		},
		{
			name: "subject changed after signing",
			id: func() Identity {
				id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject)
				id.Subject = "worker-2"
				return id
			}(),
			purpose: PurposeExecutorParticipation,
			want:    ReasonSignatureInvalid,
		},
		{
			name:    "alpha identity asked for a purpose its link does not allow",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: PurposeDataClassAccess,
			want:    ReasonPurposeNotAllowed,
		},
		{
			name:    "beta identity asked to submit tasks (beta is executor-only)",
			id:      makeIdentity(t, w.beta.domain, "k1", w.keyPrivs[0], w.beta.domain, subject),
			purpose: PurposeTaskSubmission,
			want:    ReasonPurposeNotAllowed,
		},
		{
			name:    "wildcard purpose",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: Purpose("*"),
			want:    ReasonUnknownPurpose,
		},
		{
			name:    "empty purpose",
			id:      makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, subject),
			purpose: Purpose(""),
			want:    ReasonUnknownPurpose,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verified, err := reg.Verify(tc.id, tc.purpose, testNow)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Verify rejected a valid identity: %v", err)
				}
				if verified.Domain != tc.id.Domain || verified.KeyID != tc.id.KeyID || verified.Purpose != tc.purpose {
					t.Fatalf("Verified = %+v does not match the identity", verified)
				}
				return
			}
			if err == nil {
				t.Fatalf("Verify accepted an identity that must be rejected with %s", tc.want)
			}
			expectReason(t, err, tc.want)
		})
	}
}

func TestVerifyReportsTheBindingThatAcceptedTheIdentity(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	bundle, err := reg.Binding(w.alpha.domain)
	if err != nil {
		t.Fatalf("Binding: %v", err)
	}
	id := makeIdentity(t, w.alpha.domain, "k2", w.keyPrivs[1], w.alpha.domain, "worker-7")
	verified, err := reg.Verify(id, PurposeTaskSubmission, testNow)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Subject != "worker-7" || verified.BundleSequence != bundle.Sequence() || verified.BundleDigest != bundle.Digest() {
		t.Fatalf("Verified = %+v, want the bound sequence %d and digest %s", verified, bundle.Sequence(), bundle.Digest())
	}
}

func TestVerifyMalformedInputFailsClosed(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	good := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")

	cases := map[string]struct {
		id  Identity
		now time.Time
	}{
		"zero identity":          {Identity{}, testNow},
		"zero verification time": {good, time.Time{}},
		"nil signature":          {withSignature(good, nil), testNow},
		"short signature":        {withSignature(good, good.Signature[:63]), testNow},
		"long signature":         {withSignature(good, append(append([]byte(nil), good.Signature...), 0)), testNow},
		"empty key ID":           {withKeyID(good, ""), testNow},
		"key ID with a space":    {withKeyID(good, "k 1"), testNow},
		"empty subject":          {withSubject(good, ""), testNow},
		"subject with a space":   {withSubject(good, "worker 1"), testNow},
		"subject with non-ASCII": {withSubject(good, "worker\xc3\xa91"), testNow},
		"zero trust domain":      {withDomain(good, TrustDomain{}), testNow},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := reg.Verify(tc.id, PurposeExecutorParticipation, tc.now)
			expectReason(t, err, ReasonMalformedInput)
		})
	}
}

func withSignature(id Identity, sig []byte) Identity {
	id.Signature = sig
	return id
}

func withKeyID(id Identity, keyID string) Identity {
	id.KeyID = keyID
	return id
}

func withSubject(id Identity, subject string) Identity {
	id.Subject = subject
	return id
}

func withDomain(id Identity, domain TrustDomain) Identity {
	id.Domain = domain
	return id
}

func TestVerifyDoesNotCacheAcrossBindingChanges(t *testing.T) {
	w := newTestWorld(t)
	reg := mustRegistry(t)
	v1 := mustBundle(t, w.alpha.domain, 1, testNow.Add(-time.Hour), w.bundleKeys(0b11)...)
	if err := reg.Federate(w.alphaLink(t), v1, authoritySign(t, w.alpha, v1)); err != nil {
		t.Fatalf("Federate: %v", err)
	}
	id := makeIdentity(t, w.alpha.domain, "k2", w.keyPrivs[1], w.alpha.domain, "worker-2")
	if _, err := reg.Verify(id, PurposeExecutorParticipation, testNow); err != nil {
		t.Fatalf("Verify under bundle v1: %v", err)
	}

	// Substitution: sequence 2 removes k2. The same identity must now fail.
	v2 := mustBundle(t, w.alpha.domain, 2, testNow, w.bundleKeys(0b01)...)
	if err := reg.InstallBundle(v2, authoritySign(t, w.alpha, v2)); err != nil {
		t.Fatalf("InstallBundle(v2): %v", err)
	}
	_, err := reg.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, err, ReasonUnknownKey)

	// The old bundle cannot be put back to revive the key.
	if err := reg.InstallBundle(v1, authoritySign(t, w.alpha, v1)); !errors.Is(err, ErrRollback) {
		t.Fatalf("re-installing v1 error = %v, want ErrRollback", err)
	}
	_, err = reg.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, err, ReasonUnknownKey)
}

func TestForeignTrustMaterialCannotBeMappedIntoAnotherNamespace(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)

	// Gamma's authority signs a bundle that carries the outsider key, and the
	// bundle is offered as beta's next sequence. It must not be accepted.
	swapped := mustBundle(t, w.beta.domain, 2, testNow, BundleKey{ID: "k3", PublicKey: w.outsiderPub})
	err := reg.InstallBundle(swapped, authoritySign(t, w.gamma, swapped))
	if !errors.Is(err, ErrBundleSignature) {
		t.Fatalf("gamma-signed bundle for beta: error = %v, want ErrBundleSignature", err)
	}

	// Gamma's key cannot be used to claim beta, even though gamma exists.
	id := makeIdentity(t, w.beta.domain, "k3", w.outsiderPriv, w.beta.domain, "worker-3")
	_, verifyErr := reg.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, verifyErr, ReasonUnknownKey)
}

func TestFederationDoesNotImplyTransitiveTrust(t *testing.T) {
	w := newTestWorld(t)
	// alpha federates beta. Beta's bundle is the only one in the registry. Gamma's
	// identities are not accepted, even if beta is federated with gamma elsewhere.
	reg := mustRegistry(t)
	beta := mustBundle(t, w.beta.domain, 1, testNow.Add(-time.Hour), w.bundleKeys(0b01)...)
	if err := reg.Federate(w.betaLink(t), beta, authoritySign(t, w.beta, beta)); err != nil {
		t.Fatalf("Federate(beta): %v", err)
	}
	id := makeIdentity(t, w.gamma.domain, "k1", w.keyPrivs[0], w.gamma.domain, "worker-9")
	_, err := reg.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, err, ReasonNotFederated)
}

func TestFreshnessBoundary(t *testing.T) {
	w := newTestWorld(t)
	validatedAt := testNow.Add(-testMaxAge)
	reg := mustRegistry(t)
	bundle := mustBundle(t, w.alpha.domain, 1, validatedAt, w.bundleKeys(0b01)...)
	if err := reg.Federate(w.alphaLink(t), bundle, authoritySign(t, w.alpha, bundle)); err != nil {
		t.Fatalf("Federate: %v", err)
	}
	id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")

	cases := map[string]struct {
		now  time.Time
		want Reason
	}{
		"validated at the verification time":        {validatedAt, ""},
		"age exactly the maximum":                   {validatedAt.Add(testMaxAge), ""},
		"age one nanosecond over the maximum":       {validatedAt.Add(testMaxAge + time.Nanosecond), ReasonTrustBundleStale},
		"verified one nanosecond before validation": {validatedAt.Add(-time.Nanosecond), ReasonTrustBundleInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := reg.Verify(id, PurposeExecutorParticipation, tc.now)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Verify at %s: %v", tc.now, err)
				}
				return
			}
			expectReason(t, err, tc.want)
		})
	}
}

func TestStaleBundleFailsClosedAndIsRefreshedByRevalidation(t *testing.T) {
	w := newTestWorld(t)
	reg := mustRegistry(t)
	old := testNow.Add(-testMaxAge - time.Hour)
	bundle := mustBundle(t, w.alpha.domain, 1, old, w.bundleKeys(0b01)...)
	if err := reg.Federate(w.alphaLink(t), bundle, authoritySign(t, w.alpha, bundle)); err != nil {
		t.Fatalf("Federate: %v", err)
	}
	id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")
	_, err := reg.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, err, ReasonTrustBundleStale)

	// Re-validating the same signed content moves validated-at forward.
	again := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b01)...)
	if err := reg.InstallBundle(again, authoritySign(t, w.alpha, again)); err != nil {
		t.Fatalf("re-validation of the bound bundle: %v", err)
	}
	if _, err := reg.Verify(id, PurposeExecutorParticipation, testNow); err != nil {
		t.Fatalf("Verify after re-validation: %v", err)
	}
}

func TestRevocationTakesEffectAtTheEffectiveTime(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	effective := testNow.Add(time.Hour)
	if err := reg.Revoke(w.beta.domain, effective); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	id := makeIdentity(t, w.beta.domain, "k1", w.keyPrivs[0], w.beta.domain, "worker-1")

	cases := map[string]struct {
		now  time.Time
		want Reason
	}{
		"before the effective time":     {effective.Add(-time.Nanosecond), ""},
		"exactly at the effective time": {effective, ReasonDomainRevoked},
		"well after the effective time": {effective.Add(48 * time.Hour), ReasonDomainRevoked},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := reg.Verify(id, PurposeExecutorParticipation, tc.now)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Verify before the effective time: %v", err)
				}
				return
			}
			expectReason(t, err, tc.want)
		})
	}

	alphaID := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")
	if _, err := reg.Verify(alphaID, PurposeTaskSubmission, testNow); err != nil {
		t.Fatalf("revoking beta affected alpha: %v", err)
	}
}

func TestRevokedDomainIsRejectedRegardlessOfFreshness(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	if err := reg.Revoke(w.alpha.domain, testNow.Add(-time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	id := makeIdentity(t, w.alpha.domain, "k1", w.keyPrivs[0], w.alpha.domain, "worker-1")
	// Far in the future, the bundle is also stale. The reason must still be the revocation.
	_, err := reg.Verify(id, PurposeExecutorParticipation, testNow.Add(10*testMaxAge))
	expectReason(t, err, ReasonDomainRevoked)
}

func TestRejectionWrapsItsSentinel(t *testing.T) {
	w := newTestWorld(t)
	reg := w.federatedRegistry(t)
	id := makeIdentity(t, w.gamma.domain, "k1", w.outsiderPriv, w.gamma.domain, "worker-1")
	_, err := reg.Verify(id, PurposeExecutorParticipation, testNow)
	if !errors.Is(err, ErrNotFederated) {
		t.Fatalf("rejection %v does not wrap ErrNotFederated", err)
	}
}
