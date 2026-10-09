package federation

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

// snapshotRegistry returns the registry of testWorld after three steps. Alpha is
// federated with k1 and k2 at sequence 1, then moves to k1 alone at sequence 2.
// Beta is revoked five minutes before testNow.
func (w testWorld) snapshotRegistry(t testing.TB) *Registry {
	t.Helper()
	reg := w.federatedRegistry(t)
	next := mustBundle(t, w.alpha.domain, 2, testNow.Add(-30*time.Minute), w.bundleKeys(0b01)...)
	if err := reg.InstallBundle(next, authoritySign(t, w.alpha, next)); err != nil {
		t.Fatalf("InstallBundle(alpha, 2): %v", err)
	}
	if err := reg.Revoke(w.beta.domain, testNow.Add(-5*time.Minute)); err != nil {
		t.Fatalf("Revoke(beta): %v", err)
	}
	return reg
}

func mustSnapshot(t testing.TB, reg *Registry) Snapshot {
	t.Helper()
	snap, err := reg.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snap
}

func mustRestore(t testing.TB, reg *Registry) *Registry {
	t.Helper()
	restored, err := RestoreRegistry(testMaxAge, mustSnapshot(t, reg))
	if err != nil {
		t.Fatalf("RestoreRegistry: %v", err)
	}
	return restored
}

// sameBinding fails the test unless got and want describe the same binding.
func sameBinding(t testing.TB, got, want Binding) {
	t.Helper()
	switch {
	case got.Domain() != want.Domain():
		t.Fatalf("domain = %s, want %s", got.Domain(), want.Domain())
	case got.Sequence() != want.Sequence() || got.Digest() != want.Digest():
		t.Fatalf("%s: sequence %d digest %s, want sequence %d digest %s",
			want.Domain(), got.Sequence(), got.Digest(), want.Sequence(), want.Digest())
	case !got.ValidatedAt().Equal(want.ValidatedAt()):
		t.Fatalf("%s: validated at %s, want %s", want.Domain(), got.ValidatedAt(), want.ValidatedAt())
	case !slices.Equal(got.Purposes(), want.Purposes()):
		t.Fatalf("%s: purposes %v, want %v", want.Domain(), got.Purposes(), want.Purposes())
	case got.Revoked() != want.Revoked() || !got.RevokedAt().Equal(want.RevokedAt()):
		t.Fatalf("%s: revoked %t at %s, want %t at %s",
			want.Domain(), got.Revoked(), got.RevokedAt(), want.Revoked(), want.RevokedAt())
	case !reflect.DeepEqual(got.Bundle().Keys(), want.Bundle().Keys()):
		t.Fatalf("%s: keys differ", want.Domain())
	}
}

// verdict is the outcome of one Verify call: either the accepted result, or the
// reason of the rejection.
type verdict struct {
	verified Verified
	reason   Reason
	accepted bool
}

func verdictOf(reg *Registry, id Identity, purpose Purpose, now time.Time) verdict {
	v, err := reg.Verify(id, purpose, now)
	if err == nil {
		return verdict{verified: v, accepted: true}
	}
	reason, _ := ReasonOf(err)
	return verdict{reason: reason}
}

func TestSnapshotRestoreKeepsBindingsAndVerdicts(t *testing.T) {
	w := newTestWorld(t)
	reg := w.snapshotRegistry(t)
	restored := mustRestore(t, reg)

	for _, d := range []TrustDomain{w.alpha.domain, w.beta.domain} {
		want, err := reg.Binding(d)
		if err != nil {
			t.Fatalf("Binding(%s): %v", d, err)
		}
		got, err := restored.Binding(d)
		if err != nil {
			t.Fatalf("restored Binding(%s): %v", d, err)
		}
		sameBinding(t, got, want)
	}

	k1, k2 := w.keyIDs[0], w.keyIDs[1]
	cases := map[string]struct {
		id      Identity
		purpose Purpose
		now     time.Time
		reason  Reason // empty when the identity is accepted
	}{
		"accepted with the key of the current bundle": {
			makeIdentity(t, w.alpha.domain, k1, w.keyPrivs[0], w.alpha.domain, "worker-1"),
			PurposeTaskSubmission, testNow, "",
		},
		"key removed by sequence 2": {
			makeIdentity(t, w.alpha.domain, k2, w.keyPrivs[1], w.alpha.domain, "worker-2"),
			PurposeTaskSubmission, testNow, ReasonUnknownKey,
		},
		"purpose not allowed by the link": {
			makeIdentity(t, w.alpha.domain, k1, w.keyPrivs[0], w.alpha.domain, "worker-3"),
			PurposeDataClassAccess, testNow, ReasonPurposeNotAllowed,
		},
		"revoked domain at a later time": {
			makeIdentity(t, w.beta.domain, k1, w.keyPrivs[0], w.beta.domain, "worker-4"),
			PurposeExecutorParticipation, testNow, ReasonDomainRevoked,
		},
		"revoked domain before the revocation takes effect": {
			makeIdentity(t, w.beta.domain, k1, w.keyPrivs[0], w.beta.domain, "worker-5"),
			PurposeExecutorParticipation, testNow.Add(-10 * time.Minute), "",
		},
		"domain that was never federated": {
			makeIdentity(t, w.gamma.domain, k1, w.keyPrivs[0], w.gamma.domain, "worker-6"),
			PurposeTaskSubmission, testNow, ReasonNotFederated,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			want := verdictOf(reg, tc.id, tc.purpose, tc.now)
			got := verdictOf(restored, tc.id, tc.purpose, tc.now)
			if got != want {
				t.Fatalf("restored verdict = %+v, original = %+v", got, want)
			}
			if want.reason != tc.reason || want.accepted != (tc.reason == "") {
				t.Fatalf("verdict = %+v, want reason %q", want, tc.reason)
			}
		})
	}
}

func TestSnapshotIsDeterministicAndDoesNotAliasTheRegistry(t *testing.T) {
	w := newTestWorld(t)
	reg := w.snapshotRegistry(t)
	first := mustSnapshot(t, reg)
	second := mustSnapshot(t, reg)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two snapshots of the same state differ")
	}
	if len(first.Entries) != 2 || first.Entries[0].Domain != w.alpha.domain.String() || first.Entries[1].Domain != w.beta.domain.String() {
		t.Fatalf("entries are not sorted by domain: %+v", first.Entries)
	}

	// Changing a snapshot must not reach the registry.
	first.Entries[0].AuthorityKey[0] ^= 0xff
	first.Entries[0].Purposes[0] = PurposeComputeGrantUse
	first.Entries[0].Bundle.Keys[0].PublicKey[0] ^= 0xff
	first.Entries[0].Bundle.AuthoritySignature[0] ^= 0xff
	if !reflect.DeepEqual(mustSnapshot(t, reg), second) {
		t.Fatal("changing a snapshot changed the registry")
	}

	// A restored registry snapshots to the same value.
	restored, err := RestoreRegistry(testMaxAge, second)
	if err != nil {
		t.Fatalf("RestoreRegistry: %v", err)
	}
	if got := mustSnapshot(t, restored); !reflect.DeepEqual(got, second) {
		t.Fatal("restored registry does not snapshot to the snapshot it was restored from")
	}
}

func TestRestoreRefusesTamperedKeysAndSignatures(t *testing.T) {
	w := newTestWorld(t)
	original := w.snapshotRegistry(t)
	identityPoint := make([]byte, ed25519.PublicKeySize)
	identityPoint[0] = 1
	nonCanonical := bytes.Repeat([]byte{0xff}, ed25519.PublicKeySize)

	// Entry 0 is alpha, the first domain in sorted order. It is the entry the cases change.
	cases := map[string]struct {
		mutate func(*Snapshot)
		want   error
	}{
		"pinned authority key replaced": {
			func(s *Snapshot) { s.Entries[0].AuthorityKey = slices.Clone(w.outsiderPub) },
			ErrBundleSignature,
		},
		"pinned authority key too short": {
			func(s *Snapshot) { s.Entries[0].AuthorityKey = slices.Clone(w.alpha.authPub[:31]) },
			ErrInvalidLink,
		},
		"pinned authority key is the identity point": {
			func(s *Snapshot) { s.Entries[0].AuthorityKey = slices.Clone(identityPoint) },
			ErrInvalidLink,
		},
		"bundle key public key replaced": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys[0].PublicKey = slices.Clone(w.outsiderPub) },
			ErrBundleSignature,
		},
		"bundle key ID renamed": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys[0].ID = "k9" },
			ErrBundleSignature,
		},
		"bundle key added": {
			func(s *Snapshot) {
				s.Entries[0].Bundle.Keys = append(s.Entries[0].Bundle.Keys, BundleKey{ID: "k9", PublicKey: slices.Clone(w.outsiderPub)})
			},
			ErrBundleSignature,
		},
		"bundle sequence changed": {
			func(s *Snapshot) { s.Entries[0].Bundle.Sequence = 3; s.Entries[0].HighestSequence = 3 },
			ErrBundleSignature,
		},
		"bundle key is the identity point": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys[0].PublicKey = slices.Clone(identityPoint) },
			ErrInvalidBundle,
		},
		"bundle key is not canonically encoded": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys[0].PublicKey = slices.Clone(nonCanonical) },
			ErrInvalidBundle,
		},
		"authority signature bit flipped": {
			func(s *Snapshot) { s.Entries[0].Bundle.AuthoritySignature[0] ^= 1 },
			ErrBundleSignature,
		},
		"authority signature truncated": {
			func(s *Snapshot) { s.Entries[0].Bundle.AuthoritySignature = s.Entries[0].Bundle.AuthoritySignature[:63] },
			ErrBundleSignature,
		},
		"authority signature missing": {
			func(s *Snapshot) { s.Entries[0].Bundle.AuthoritySignature = nil },
			ErrBundleSignature,
		},
		"bundle names another domain": {
			func(s *Snapshot) { s.Entries[0].Bundle.Domain = w.beta.domain.String() },
			ErrDomainMismatch,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snap := mustSnapshot(t, original)
			if snap.Entries[0].Domain != w.alpha.domain.String() {
				t.Fatalf("entry 0 is %s, want alpha", snap.Entries[0].Domain)
			}
			tc.mutate(&snap)
			_, err := RestoreRegistry(testMaxAge, snap)
			if !errors.Is(err, tc.want) {
				t.Fatalf("RestoreRegistry error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRestoreRefusesSequenceRollback(t *testing.T) {
	w := newTestWorld(t)
	original := w.snapshotRegistry(t)
	alpha := w.alpha.domain.String()

	t.Run("highest accepted sequence is above the installed one", func(t *testing.T) {
		snap := mustSnapshot(t, original)
		snap.Entries[0].HighestSequence = 3
		_, err := RestoreRegistry(testMaxAge, snap)
		if !errors.Is(err, ErrRollback) {
			t.Fatalf("RestoreRegistry error = %v, want ErrRollback", err)
		}
	})

	t.Run("an older bundle with a valid signature replaces the installed one", func(t *testing.T) {
		snap := mustSnapshot(t, original)
		older := mustBundle(t, w.alpha.domain, 1, testNow.Add(-time.Hour), w.bundleKeys(0b11)...)
		e := &snap.Entries[0]
		if e.Domain != alpha {
			t.Fatalf("entry 0 is %s, want alpha", e.Domain)
		}
		e.Bundle = BundleRecord{
			Domain:             alpha,
			Sequence:           older.Sequence(),
			ValidatedAt:        older.ValidatedAt(),
			Keys:               older.Keys(),
			AuthoritySignature: authoritySign(t, w.alpha, older),
		}
		_, err := RestoreRegistry(testMaxAge, snap)
		if !errors.Is(err, ErrRollback) {
			t.Fatalf("RestoreRegistry error = %v, want ErrRollback", err)
		}
	})

	t.Run("installed sequence is above the highest accepted one", func(t *testing.T) {
		snap := mustSnapshot(t, original)
		snap.Entries[0].HighestSequence = 1
		_, err := RestoreRegistry(testMaxAge, snap)
		if err == nil || errors.Is(err, ErrRollback) {
			t.Fatalf("RestoreRegistry error = %v, want a refusal that is not ErrRollback", err)
		}
	})

	t.Run("sequence zero", func(t *testing.T) {
		snap := mustSnapshot(t, original)
		snap.Entries[0].Bundle.Sequence = 0
		snap.Entries[0].HighestSequence = 0
		_, err := RestoreRegistry(testMaxAge, snap)
		if !errors.Is(err, ErrInvalidBundle) {
			t.Fatalf("RestoreRegistry error = %v, want ErrInvalidBundle", err)
		}
	})
}

func TestRestoredRevocationIsTerminal(t *testing.T) {
	w := newTestWorld(t)
	restored := mustRestore(t, w.snapshotRegistry(t))

	next := mustBundle(t, w.beta.domain, 2, testNow, w.bundleKeys(0b01)...)
	if err := restored.Federate(w.betaLink(t), next, authoritySign(t, w.beta, next)); !errors.Is(err, ErrAlreadyFederated) {
		t.Fatalf("Federate(revoked beta) error = %v, want ErrAlreadyFederated", err)
	}
	if err := restored.InstallBundle(next, authoritySign(t, w.beta, next)); !errors.Is(err, ErrDomainRevoked) {
		t.Fatalf("InstallBundle(revoked beta) error = %v, want ErrDomainRevoked", err)
	}
	if err := restored.Revoke(w.beta.domain, testNow); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatalf("Revoke(revoked beta) error = %v, want ErrAlreadyRevoked", err)
	}

	b, err := restored.Binding(w.beta.domain)
	if err != nil {
		t.Fatalf("Binding(beta): %v", err)
	}
	if !b.Revoked() || !b.RevokedAt().Equal(testNow.Add(-5*time.Minute)) {
		t.Fatalf("beta revoked = %t at %s, want revoked at %s", b.Revoked(), b.RevokedAt(), testNow.Add(-5*time.Minute))
	}

	id := makeIdentity(t, w.beta.domain, w.keyIDs[0], w.keyPrivs[0], w.beta.domain, "worker")
	_, err = restored.Verify(id, PurposeExecutorParticipation, testNow)
	expectReason(t, err, ReasonDomainRevoked)
}

func TestRestoredRegistryRefusesTheSameBadInputs(t *testing.T) {
	w := newTestWorld(t)
	original := w.snapshotRegistry(t)
	restored := mustRestore(t, original)

	gamma := mustBundle(t, w.gamma.domain, 1, testNow.Add(-time.Hour), w.bundleKeys(0b01)...)
	alphaRollback := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b11)...)
	alphaEquivocation := mustBundle(t, w.alpha.domain, 2, testNow, w.bundleKeys(0b10)...)
	alphaForged := mustBundle(t, w.alpha.domain, 3, testNow, w.bundleKeys(0b01)...)
	betaNext := mustBundle(t, w.beta.domain, 2, testNow, w.bundleKeys(0b01)...)

	cases := map[string]struct {
		run  func(*Registry) error
		want error
	}{
		"federate a domain that pins an authority key already in use": {
			func(r *Registry) error {
				link := mustLink(t, w.gamma.domain, w.alpha.authPub, PurposeTaskSubmission)
				return r.Federate(link, gamma, authoritySign(t, w.alpha, gamma))
			},
			ErrAuthorityKeyReused,
		},
		"federate a federated domain again": {
			func(r *Registry) error {
				return r.Federate(w.alphaLink(t), alphaRollback, authoritySign(t, w.alpha, alphaRollback))
			},
			ErrAlreadyFederated,
		},
		"install a bundle that is a rollback": {
			func(r *Registry) error {
				return r.InstallBundle(alphaRollback, authoritySign(t, w.alpha, alphaRollback))
			},
			ErrRollback,
		},
		"install an equivocating bundle": {
			func(r *Registry) error {
				return r.InstallBundle(alphaEquivocation, authoritySign(t, w.alpha, alphaEquivocation))
			},
			ErrEquivocation,
		},
		"install a bundle with a forged signature": {
			func(r *Registry) error {
				forger := party{domain: w.alpha.domain, authPub: w.outsiderPub, authPriv: w.outsiderPriv}
				return r.InstallBundle(alphaForged, authoritySign(t, forger, alphaForged))
			},
			ErrBundleSignature,
		},
		"install a bundle for a revoked domain": {
			func(r *Registry) error {
				return r.InstallBundle(betaNext, authoritySign(t, w.beta, betaNext))
			},
			ErrDomainRevoked,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wantErr := tc.run(original)
			gotErr := tc.run(restored)
			if !errors.Is(wantErr, tc.want) {
				t.Fatalf("original error = %v, want %v", wantErr, tc.want)
			}
			if !errors.Is(gotErr, tc.want) {
				t.Fatalf("restored error = %v, want %v", gotErr, tc.want)
			}
		})
	}
}

func TestRestoreRefusesReusedAuthorityKey(t *testing.T) {
	w := newTestWorld(t)
	snap := mustSnapshot(t, w.snapshotRegistry(t))

	// Give beta the authority key of alpha, and sign beta's bundle with it. The
	// signature is valid, so only the rule against reusing a pinned key can refuse.
	beta := &snap.Entries[1]
	if beta.Domain != w.beta.domain.String() {
		t.Fatalf("entry 1 is %s, want beta", beta.Domain)
	}
	bundle := mustBundle(t, w.beta.domain, beta.Bundle.Sequence, beta.Bundle.ValidatedAt, beta.Bundle.Keys...)
	beta.AuthorityKey = slices.Clone(w.alpha.authPub)
	beta.Bundle.AuthoritySignature = authoritySign(t, w.alpha, bundle)

	_, err := RestoreRegistry(testMaxAge, snap)
	if !errors.Is(err, ErrAuthorityKeyReused) {
		t.Fatalf("RestoreRegistry error = %v, want ErrAuthorityKeyReused", err)
	}
}

func TestRestoreRefusesMalformedSnapshots(t *testing.T) {
	w := newTestWorld(t)
	original := w.snapshotRegistry(t)

	cases := map[string]struct {
		mutate func(*Snapshot)
		want   error // nil means that any refusal is acceptable
	}{
		"domains out of order": {
			func(s *Snapshot) { s.Entries[0], s.Entries[1] = s.Entries[1], s.Entries[0] },
			nil,
		},
		"domain listed twice": {
			func(s *Snapshot) { s.Entries = append(s.Entries, s.Entries[0]) },
			nil,
		},
		"domain name is not canonical": {
			func(s *Snapshot) { s.Entries[0].Domain = "Alpha.example"; s.Entries[0].Bundle.Domain = "Alpha.example" },
			ErrInvalidTrustDomain,
		},
		"revoked domain without a revocation time": {
			func(s *Snapshot) { s.Entries[1].RevokedAt = time.Time{} },
			nil,
		},
		"live domain with a revocation time": {
			func(s *Snapshot) { s.Entries[0].RevokedAt = testNow },
			nil,
		},
		"link without purposes": {
			func(s *Snapshot) { s.Entries[0].Purposes = nil },
			ErrInvalidLink,
		},
		"installed bundle without a validated-at time": {
			func(s *Snapshot) { s.Entries[0].Bundle.ValidatedAt = time.Time{} },
			ErrInvalidBundle,
		},
		"installed bundle without keys": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys = nil },
			ErrInvalidBundle,
		},
		"installed bundle with a repeated key ID": {
			func(s *Snapshot) { s.Entries[0].Bundle.Keys = append(s.Entries[0].Bundle.Keys, s.Entries[0].Bundle.Keys[0]) },
			ErrInvalidBundle,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snap := mustSnapshot(t, original)
			tc.mutate(&snap)
			_, err := RestoreRegistry(testMaxAge, snap)
			if err == nil {
				t.Fatal("RestoreRegistry accepted a malformed snapshot")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("RestoreRegistry error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSnapshotAndRestoreEdgeCases(t *testing.T) {
	var nilReg *Registry
	if _, err := nilReg.Snapshot(); !errors.Is(err, ErrNilRegistry) {
		t.Fatalf("nil Registry Snapshot error = %v, want ErrNilRegistry", err)
	}
	var zero Registry
	if _, err := zero.Snapshot(); !errors.Is(err, ErrNilRegistry) {
		t.Fatalf("zero Registry Snapshot error = %v, want ErrNilRegistry", err)
	}

	empty, err := RestoreRegistry(testMaxAge, Snapshot{})
	if err != nil {
		t.Fatalf("RestoreRegistry(empty): %v", err)
	}
	if got := mustSnapshot(t, empty); len(got.Entries) != 0 {
		t.Fatalf("empty restore has %d entries", len(got.Entries))
	}
	if _, err := RestoreRegistry(0, Snapshot{}); !errors.Is(err, ErrInvalidMaxBundleAge) {
		t.Fatalf("RestoreRegistry with zero max age error = %v, want ErrInvalidMaxBundleAge", err)
	}
}
