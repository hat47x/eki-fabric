package federation

import (
	"fmt"
	"testing"
	"time"
)

// The enumeration test walks a small state space of bindings, sequences,
// revocations, freshness, and identity claims. For every combination it checks
// the registry against an independent oracle that encodes the rule: a
// verification succeeds only when the claimed domain is federated, the purpose is
// allowed, the domain is not revoked at the verification time, the bound bundle is
// fresh and not from the future, the claimed key is in that bundle, and the
// signature was made by that key for that same domain.

type freshness int

const (
	freshOK freshness = iota
	freshStale
	freshFuture
)

type revocation int

const (
	revNone revocation = iota
	revPast
	revFuture
)

// Indexes into the enumerated domains.
const (
	enumAlpha = iota
	enumBeta
	enumGamma
)

// enumState is one point of the state space. Key masks use bit 0 for "k1" and
// bit 1 for "k2". alphaV2 == 0 means alpha never received a second bundle.
type enumState struct {
	alphaV1, alphaV2, betaV1 uint8
	alphaFresh, betaFresh    freshness
	alphaRev, betaRev        revocation
}

func (s enumState) String() string {
	return fmt.Sprintf("alpha{v1=%02b v2=%02b fresh=%d rev=%d} beta{v1=%02b fresh=%d rev=%d}",
		s.alphaV1, s.alphaV2, s.alphaFresh, s.alphaRev, s.betaV1, s.betaFresh, s.betaRev)
}

// validatedAtFor returns the validated-at time that realizes a freshness option at now.
func validatedAtFor(f freshness, now time.Time) time.Time {
	switch f {
	case freshStale:
		return now.Add(-(testMaxAge + time.Hour))
	case freshFuture:
		return now.Add(time.Hour)
	default:
		return now.Add(-time.Hour)
	}
}

// revocationEffectiveAt returns the effective time that realizes a revocation option.
func revocationEffectiveAt(r revocation, now time.Time) time.Time {
	if r == revFuture {
		return now.Add(time.Hour)
	}
	return now.Add(-time.Hour)
}

// buildEnumRegistry applies a state to a fresh registry. Operations run in the
// order that a real operator would use: federate, install the later sequence,
// then revoke.
func buildEnumRegistry(t testing.TB, w testWorld, st enumState) *Registry {
	t.Helper()
	reg := mustRegistry(t)

	currentAt := validatedAtFor(st.alphaFresh, testNow)
	firstAt := currentAt
	if st.alphaV2 != 0 {
		firstAt = validatedAtFor(freshOK, testNow)
	}
	alpha1 := mustBundle(t, w.alpha.domain, 1, firstAt, w.bundleKeys(st.alphaV1)...)
	if err := reg.Federate(w.alphaLink(t), alpha1, authoritySign(t, w.alpha, alpha1)); err != nil {
		t.Fatalf("%s: Federate(alpha): %v", st, err)
	}
	if st.alphaV2 != 0 {
		alpha2 := mustBundle(t, w.alpha.domain, 2, currentAt, w.bundleKeys(st.alphaV2)...)
		if err := reg.InstallBundle(alpha2, authoritySign(t, w.alpha, alpha2)); err != nil {
			t.Fatalf("%s: InstallBundle(alpha 2): %v", st, err)
		}
	}

	beta1 := mustBundle(t, w.beta.domain, 1, validatedAtFor(st.betaFresh, testNow), w.bundleKeys(st.betaV1)...)
	if err := reg.Federate(w.betaLink(t), beta1, authoritySign(t, w.beta, beta1)); err != nil {
		t.Fatalf("%s: Federate(beta): %v", st, err)
	}

	if st.alphaRev != revNone {
		if err := reg.Revoke(w.alpha.domain, revocationEffectiveAt(st.alphaRev, testNow)); err != nil {
			t.Fatalf("%s: Revoke(alpha): %v", st, err)
		}
	}
	if st.betaRev != revNone {
		if err := reg.Revoke(w.beta.domain, revocationEffectiveAt(st.betaRev, testNow)); err != nil {
			t.Fatalf("%s: Revoke(beta): %v", st, err)
		}
	}
	return reg
}

func containsPurpose(list []Purpose, p Purpose) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}

// enumOracle is the specification the registry is checked against. It returns
// the reason that a verification must fail with, or true when it must succeed.
func enumOracle(s enumState, claimed, keyIdx, signer, signedFor int, purpose Purpose, now time.Time) (Reason, bool) {
	if claimed == enumGamma {
		return ReasonNotFederated, false
	}

	var (
		allowed []Purpose
		mask    uint8
		fresh   freshness
		rev     revocation
	)
	if claimed == enumAlpha {
		allowed = []Purpose{PurposeTaskSubmission, PurposeExecutorParticipation}
		mask, fresh, rev = s.alphaV1, s.alphaFresh, s.alphaRev
		if s.alphaV2 != 0 {
			mask = s.alphaV2
		}
	} else {
		allowed = []Purpose{PurposeExecutorParticipation}
		mask, fresh, rev = s.betaV1, s.betaFresh, s.betaRev
	}

	if !containsPurpose(allowed, purpose) {
		return ReasonPurposeNotAllowed, false
	}
	if rev != revNone && !now.Before(revocationEffectiveAt(rev, now)) {
		return ReasonDomainRevoked, false
	}
	validatedAt := validatedAtFor(fresh, now)
	if validatedAt.After(now) {
		return ReasonTrustBundleInvalid, false
	}
	if now.Sub(validatedAt) > testMaxAge {
		return ReasonTrustBundleStale, false
	}
	if mask&(1<<keyIdx) == 0 {
		return ReasonUnknownKey, false
	}
	if signedFor != claimed || signer != keyIdx {
		return ReasonSignatureInvalid, false
	}
	return "", true
}

func TestVerifyEnumeratedBindingStates(t *testing.T) {
	w := newTestWorld(t)
	const subject = "worker-1"
	domains := [3]TrustDomain{w.alpha.domain, w.beta.domain, w.gamma.domain}

	// sigs[signer][signedFor] is what key signer signs for an identity of signedFor.
	var sigs [2][2][]byte
	for signer := 0; signer < 2; signer++ {
		for signedFor := enumAlpha; signedFor <= enumBeta; signedFor++ {
			sigs[signer][signedFor] = identitySign(t, w.keyPrivs[signer], domains[signedFor], subject)
		}
	}

	outcomes := map[Reason]int{}
	accepted, states, checks := 0, 0, 0
	for _, alphaV1 := range []uint8{0b01, 0b10, 0b11} {
		for _, alphaV2 := range []uint8{0, 0b01, 0b10} {
			for _, betaV1 := range []uint8{0b01, 0b10, 0b11} {
				for _, alphaFresh := range []freshness{freshOK, freshStale, freshFuture} {
					for _, betaFresh := range []freshness{freshOK, freshStale, freshFuture} {
						for _, alphaRev := range []revocation{revNone, revPast, revFuture} {
							for _, betaRev := range []revocation{revNone, revPast, revFuture} {
								st := enumState{
									alphaV1: alphaV1, alphaV2: alphaV2, betaV1: betaV1,
									alphaFresh: alphaFresh, betaFresh: betaFresh,
									alphaRev: alphaRev, betaRev: betaRev,
								}
								states++
								reg := buildEnumRegistry(t, w, st)

								for claimed := enumAlpha; claimed <= enumGamma; claimed++ {
									for keyIdx := 0; keyIdx < 2; keyIdx++ {
										for signer := 0; signer < 2; signer++ {
											for signedFor := enumAlpha; signedFor <= enumBeta; signedFor++ {
												for _, purpose := range []Purpose{PurposeTaskSubmission, PurposeExecutorParticipation} {
													id := Identity{
														Domain:    domains[claimed],
														Subject:   subject,
														KeyID:     w.keyIDs[keyIdx],
														Signature: sigs[signer][signedFor],
													}
													_, err := reg.Verify(id, purpose, testNow)
													checks++

													wantReason, wantOK := enumOracle(st, claimed, keyIdx, signer, signedFor, purpose, testNow)
													if wantOK {
														if err != nil {
															t.Fatalf("%s: claimed=%d key=%d signer=%d signedFor=%d purpose=%s: want accept, got %v",
																st, claimed, keyIdx, signer, signedFor, purpose, err)
														}
														accepted++
														outcomes["ACCEPT"]++
														continue
													}
													got, ok := ReasonOf(err)
													if !ok || got != wantReason {
														t.Fatalf("%s: claimed=%d key=%d signer=%d signedFor=%d purpose=%s: want %s, got %v",
															st, claimed, keyIdx, signer, signedFor, purpose, wantReason, err)
													}
													outcomes[got]++
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	for _, want := range []Reason{
		ReasonNotFederated, ReasonPurposeNotAllowed, ReasonDomainRevoked,
		ReasonTrustBundleInvalid, ReasonTrustBundleStale, ReasonUnknownKey, ReasonSignatureInvalid,
	} {
		if outcomes[want] == 0 {
			t.Errorf("the state space never produced %s; the enumeration does not cover it", want)
		}
	}
	if accepted == 0 {
		t.Error("the state space never produced an accepted identity")
	}
	t.Logf("enumerated %d states and %d verifications; %d accepted; outcomes %v", states, checks, accepted, outcomes)
}
