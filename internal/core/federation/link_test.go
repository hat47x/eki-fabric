package federation

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestNewLinkRejectsBlanketAndUnknownScope(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	authority, _ := testKeyPair(t, 0xA1)

	cases := map[string]struct {
		domain    TrustDomain
		authority ed25519.PublicKey
		purposes  []Purpose
	}{
		"no purposes (blanket trust)": {domain, authority, nil},
		"empty purpose string":        {domain, authority, []Purpose{""}},
		"wildcard purpose":            {domain, authority, []Purpose{"*"}},
		"unknown purpose":             {domain, authority, []Purpose{"control-plane-admin"}},
		"unknown next to a valid one": {domain, authority, []Purpose{PurposeTaskSubmission, "all"}},
		"duplicated purpose":          {domain, authority, []Purpose{PurposeTaskSubmission, PurposeTaskSubmission}},
		"zero trust domain":           {TrustDomain{}, authority, []Purpose{PurposeTaskSubmission}},
		"nil authority key":           {domain, nil, []Purpose{PurposeTaskSubmission}},
		"short authority key":         {domain, authority[:31], []Purpose{PurposeTaskSubmission}},
		"long authority key":          {domain, append(ed25519.PublicKey(nil), append(authority, 0)...), []Purpose{PurposeTaskSubmission}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			link, err := NewLink(tc.domain, tc.authority, tc.purposes...)
			if err == nil {
				t.Fatalf("NewLink succeeded with %+v; want an error", link.Purposes())
			}
			if !errors.Is(err, ErrInvalidLink) {
				t.Fatalf("error %v does not wrap ErrInvalidLink", err)
			}
		})
	}
}

func TestLinkAllowsOnlyItsPurposes(t *testing.T) {
	w := newTestWorld(t)
	link := w.alphaLink(t)

	allowed := map[Purpose]bool{
		PurposeTaskSubmission:        true,
		PurposeExecutorParticipation: true,
		PurposeDataClassAccess:       false,
		PurposeComputeGrantUse:       false,
		Purpose("*"):                 false,
		Purpose(""):                  false,
		Purpose("control-plane"):     false,
	}
	for p, want := range allowed {
		if got := link.Allows(p); got != want {
			t.Errorf("Allows(%q) = %v, want %v", string(p), got, want)
		}
	}
}

func TestZeroLinkAllowsNothing(t *testing.T) {
	var link Link
	for _, p := range []Purpose{PurposeTaskSubmission, PurposeExecutorParticipation, PurposeDataClassAccess, PurposeComputeGrantUse} {
		if link.Allows(p) {
			t.Fatalf("zero Link allows %q", string(p))
		}
	}
	if link.Domain().Valid() {
		t.Fatal("zero Link reports a valid domain")
	}
}

func TestPurposeVocabularyIsClosed(t *testing.T) {
	for _, p := range []Purpose{PurposeTaskSubmission, PurposeExecutorParticipation, PurposeDataClassAccess, PurposeComputeGrantUse} {
		if !p.Valid() {
			t.Errorf("%q is in the vocabulary but Valid() is false", string(p))
		}
	}
	for _, p := range []Purpose{"", "*", "any", "all", "federation-admin", "control-plane", "verify", "Task-Submission"} {
		if p.Valid() {
			t.Errorf("%q is outside the vocabulary but Valid() is true", string(p))
		}
	}
}

func TestLinkCopiesItsInputs(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	authority, _ := testKeyPair(t, 0xA1)
	purposes := []Purpose{PurposeExecutorParticipation, PurposeTaskSubmission}
	link, err := NewLink(domain, authority, purposes...)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	authority[0] ^= 0xff
	purposes[0] = PurposeComputeGrantUse

	if !link.Allows(PurposeTaskSubmission) || !link.Allows(PurposeExecutorParticipation) {
		t.Fatal("link changed after the caller modified its input slices")
	}
	if link.Allows(PurposeComputeGrantUse) {
		t.Fatal("link picked up a purpose written into the caller's slice")
	}
	got := link.Purposes()
	got[0] = PurposeComputeGrantUse
	if link.Allows(PurposeComputeGrantUse) {
		t.Fatal("Purposes() returned a slice that aliases the link")
	}
	key := link.AuthorityKey()
	key[0] ^= 0xff
	if link.AuthorityKey()[0] == key[0] {
		t.Fatal("AuthorityKey() returned a slice that aliases the link")
	}
}
