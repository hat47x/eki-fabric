package resultaccept

import (
	"slices"
	"strings"
)

// evidence is a verification with its canonical key, used to collapse repeats.
type evidence struct {
	Verification
	key string
}

func (e evidence) dissents() bool {
	return e.Check == CheckDiffers || e.Check == CheckValidatorRejected
}

// Decide evaluates claim against verifications under policy for an output of the
// given class.
//
// Decide returns an error that wraps ErrRejectedInput when the input cannot be
// evaluated safely, and then no Decision. Otherwise it returns a Decision whose
// Outcome is accepted, held or rejected. Zero verifications is a held decision,
// not an error.
func Decide(claim ResultClaim, verifications []Verification, output OutputClass, policy *Policy) (Decision, error) {
	pol, digest, err := checkPolicy(policy)
	if err != nil {
		return Decision{}, err
	}
	if err := checkOutput(output, pol.Level); err != nil {
		return Decision{}, err
	}
	required := requiredDimensions(pol)
	if err := checkClaim(claim, required); err != nil {
		return Decision{}, err
	}
	if len(verifications) > maxVerifications {
		return Decision{}, rejectf("%d verifications exceed the limit of %d", len(verifications), maxVerifications)
	}

	list := make([]evidence, 0, len(verifications))
	for _, v := range verifications {
		if err := checkVerification(claim, required, v); err != nil {
			return Decision{}, err
		}
		list = append(list, evidence{Verification: v, key: verificationKey(v)})
	}
	unique, err := collapse(list)
	if err != nil {
		return Decision{}, err
	}

	authorities := make(map[string]bool, len(pol.RejectAuthorities))
	for _, id := range pol.RejectAuthorities {
		authorities[id] = true
	}

	var (
		eligible          []evidence
		excluded          []Exclusion
		dissent           []Observation
		authorityRejected bool
	)
	for _, e := range unique {
		if reason := exclusionReason(claim, pol, e); reason != "" {
			excluded = append(excluded, Exclusion{VerifierID: e.VerifierID, Reason: reason})
		} else {
			eligible = append(eligible, e)
		}
		// Dissent blocks acceptance whoever raises it. Only independent verifications
		// can justify a result, but a dissent can never be ignored.
		if e.dissents() {
			dissent = append(dissent, Observation{VerifierID: e.VerifierID, Check: e.Check, ObservedDigest: e.ObservedDigest})
		}
		if e.Check == CheckValidatorRejected && authorities[e.VerifierID] {
			authorityRejected = true
		}
	}

	need := requiredCheck(pol.Level)
	var voices []Voice
	for _, group := range independentVoices(eligible, pol.DistinctDimensions) {
		if slices.ContainsFunc(group, func(e evidence) bool { return e.Check == need }) {
			voices = append(voices, newVoice(group, pol.DistinctDimensions))
		}
	}

	dec := Decision{
		Level:         LevelRA0,
		PolicyDigest:  digest,
		Voices:        voices,
		Verifiers:     eligibleIDs(eligible),
		Dissent:       dissent,
		Excluded:      excluded,
		NotGuaranteed: notGuaranteed(output),
	}
	switch {
	case authorityRejected:
		dec.Outcome, dec.Reason = OutcomeRejected, ReasonAuthorityRejected
	case len(dissent) > 0:
		dec.Outcome, dec.Reason = OutcomeHeld, ReasonVerificationsDisagree
	case len(eligible) == 0:
		dec.Outcome, dec.Reason = OutcomeHeld, ReasonNoIndependentVerification
	case len(voices) < pol.Threshold:
		dec.Outcome, dec.Reason = OutcomeHeld, ReasonQuorumNotMet
	default:
		dec.Outcome, dec.Reason = OutcomeAccepted, ReasonQuorumMet
		dec.Level = pol.Level
	}
	return dec, nil
}

// requiredCheck is the agreeing check that the requested level needs. RA-2 needs
// re-derivation of the same digest, and RA-1 needs an application validator's
// acceptance.
func requiredCheck(level AssuranceLevel) Check {
	if level == LevelRA2 {
		return CheckReproduced
	}
	return CheckValidatorAccepted
}

// exclusionReason returns why a verification cannot justify the claim, or "" when
// it can. A verifier that is the producer, or shares the producer's principal or
// executor, is not independent. So is one that shares any distinct dimension with
// the producer.
func exclusionReason(claim ResultClaim, pol Policy, e evidence) string {
	switch {
	case e.VerifierID == claim.WorkerID:
		return "verifier is the producing worker"
	case e.Dimensions[DimensionPrincipal] == claim.Dimensions[DimensionPrincipal]:
		return "same principal as the producing worker"
	case e.Dimensions[DimensionExecutorDigest] == claim.Dimensions[DimensionExecutorDigest]:
		return "same executor digest as the producing worker"
	}
	for _, d := range pol.DistinctDimensions {
		if e.Dimensions[d] == claim.Dimensions[d] {
			return "shares " + d.String() + " with the producing worker"
		}
	}
	return ""
}

// collapse sorts the evidence by verifier identity and removes byte-identical
// repeats. A verifier identity that appears with different content is refused.
func collapse(list []evidence) ([]evidence, error) {
	slices.SortFunc(list, func(a, b evidence) int {
		if c := strings.Compare(a.VerifierID, b.VerifierID); c != 0 {
			return c
		}
		return strings.Compare(a.key, b.key)
	})
	out := make([]evidence, 0, len(list))
	for i, e := range list {
		if i > 0 && list[i-1].VerifierID == e.VerifierID {
			if list[i-1].key == e.key {
				continue
			}
			return nil, rejectf("verifier %q has conflicting verifications", e.VerifierID)
		}
		out = append(out, e)
	}
	return out, nil
}

// independentVoices groups the eligible evidence into voices. Two verifications
// are joined when they share a value on any distinct dimension, and the joins are
// closed transitively. Groups are ordered by their first member, and members keep
// the verifier-identity order from collapse, so the result depends only on the
// set of verifications.
func independentVoices(list []evidence, distinct []Dimension) [][]evidence {
	parent := make([]int, len(list))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, d := range distinct {
		first := make(map[string]int, len(list))
		for i, e := range list {
			value := e.Dimensions[d]
			j, seen := first[value]
			if !seen {
				first[value] = i
				continue
			}
			if ri, rj := find(i), find(j); ri != rj {
				parent[ri] = rj
			}
		}
	}

	groupOf := make(map[int]int, len(list))
	var groups [][]evidence
	for i, e := range list {
		root := find(i)
		g, ok := groupOf[root]
		if !ok {
			g = len(groups)
			groupOf[root] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], e)
	}
	return groups
}

func newVoice(group []evidence, distinct []Dimension) Voice {
	var v Voice
	seen := make(map[Check]bool, len(group))
	for _, e := range group {
		v.VerifierIDs = append(v.VerifierIDs, e.VerifierID)
		seen[e.Check] = true
	}
	for _, d := range distinct {
		values := make([]string, 0, len(group))
		for _, e := range group {
			values = append(values, e.Dimensions[d])
		}
		slices.Sort(values)
		v.Dimensions = append(v.Dimensions, DimensionValues{Dimension: d, Values: slices.Compact(values)})
	}
	for c := range seen {
		v.Checks = append(v.Checks, c)
	}
	slices.Sort(v.Checks)
	return v
}

func eligibleIDs(list []evidence) []string {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e.VerifierID)
	}
	return ids
}

func notGuaranteed(output OutputClass) []string {
	out := slices.Clone(baseNotGuaranteed)
	if output == OutputSemanticallyOpen {
		return append(out, NotGuaranteedSemanticCorrectness)
	}
	return append(out, NotGuaranteedMechanicalCheckLimit)
}
