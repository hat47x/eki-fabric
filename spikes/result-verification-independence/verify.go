package verify

import (
	"fmt"
	"sort"
)

// Subject describes identity attributes that have already been verified by a
// trusted EKI boundary. A worker must not be able to populate these fields by
// self-reporting arbitrary strings in a ResultClaim.
type Subject struct {
	WorkerID    string
	PrincipalID string
	TrustDomain string
	Managed     bool
}

type Claim struct {
	Subject Subject
	Digest  string
}

type Policy struct {
	MinMatchingClaims       int
	MinDistinctPrincipals   int
	MinDistinctTrustDomains int
	RequireManagedAnchor    bool
}

type Candidate struct {
	Digest               string
	Claims               int
	DistinctPrincipals   int
	DistinctTrustDomains int
	HasManagedAnchor     bool
}

type Decision struct {
	Accepted   bool
	Digest     string
	Reason     string
	Candidates []Candidate
}

func naiveQuorum(claims []Claim, minMatching int) Decision {
	if minMatching <= 0 {
		return Decision{Reason: "minMatching must be positive"}
	}
	counts := map[string]int{}
	for _, c := range claims {
		counts[c.Digest]++
	}
	for d, n := range counts {
		if n >= minMatching {
			return Decision{Accepted: true, Digest: d, Reason: "worker-count quorum reached"}
		}
	}
	return Decision{Reason: "worker-count quorum not reached"}
}

func VerifyExact(claims []Claim, p Policy) Decision {
	if p.MinMatchingClaims <= 0 {
		return Decision{Reason: "MinMatchingClaims must be positive"}
	}
	if p.MinDistinctPrincipals <= 0 {
		return Decision{Reason: "MinDistinctPrincipals must be positive"}
	}
	if p.MinDistinctTrustDomains <= 0 {
		return Decision{Reason: "MinDistinctTrustDomains must be positive"}
	}

	byDigest := map[string][]Claim{}
	for _, c := range claims {
		if c.Digest == "" || c.Subject.WorkerID == "" || c.Subject.PrincipalID == "" || c.Subject.TrustDomain == "" {
			return Decision{Reason: "claim is missing verified identity or digest"}
		}
		byDigest[c.Digest] = append(byDigest[c.Digest], c)
	}

	digests := make([]string, 0, len(byDigest))
	for d := range byDigest {
		digests = append(digests, d)
	}
	sort.Strings(digests)

	var passing []Candidate
	all := make([]Candidate, 0, len(digests))
	for _, d := range digests {
		group := byDigest[d]
		principals := map[string]struct{}{}
		domains := map[string]struct{}{}
		managed := false
		workers := map[string]struct{}{}
		duplicateWorker := false
		for _, c := range group {
			if _, ok := workers[c.Subject.WorkerID]; ok {
				duplicateWorker = true
			}
			workers[c.Subject.WorkerID] = struct{}{}
			principals[c.Subject.PrincipalID] = struct{}{}
			domains[c.Subject.TrustDomain] = struct{}{}
			managed = managed || c.Subject.Managed
		}
		candidate := Candidate{
			Digest:               d,
			Claims:               len(group),
			DistinctPrincipals:   len(principals),
			DistinctTrustDomains: len(domains),
			HasManagedAnchor:     managed,
		}
		all = append(all, candidate)

		if duplicateWorker {
			continue
		}
		if candidate.Claims < p.MinMatchingClaims {
			continue
		}
		if candidate.DistinctPrincipals < p.MinDistinctPrincipals {
			continue
		}
		if candidate.DistinctTrustDomains < p.MinDistinctTrustDomains {
			continue
		}
		if p.RequireManagedAnchor && !candidate.HasManagedAnchor {
			continue
		}
		passing = append(passing, candidate)
	}

	if len(passing) == 0 {
		return Decision{Reason: "no digest satisfies the independence policy", Candidates: all}
	}
	if len(passing) > 1 {
		return Decision{Reason: fmt.Sprintf("ambiguous: %d conflicting digests satisfy the policy", len(passing)), Candidates: all}
	}
	return Decision{Accepted: true, Digest: passing[0].Digest, Reason: "exact result and independence policy satisfied", Candidates: all}
}
