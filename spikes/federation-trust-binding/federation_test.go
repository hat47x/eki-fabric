package federation

import (
	"testing"
	"time"
)

func mustKey(t *testing.T) KeyPair {
	t.Helper()
	k, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func apply(t *testing.T, store *Store, admin KeyPair, domain string, version uint64, keys ...DomainKey) {
	t.Helper()
	u, err := SignBundleUpdate(admin, BundleUpdate{TrustDomain: domain, Version: version, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(u); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityUsesOnlyExpectedDomainBundle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	admin := mustKey(t)
	bIssuer := mustKey(t)
	cIssuer := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "b.example", 1, DomainKey{KeyID: "b1", PublicKey: bIssuer.Public})
	apply(t, store, admin, "c.example", 1, DomainKey{KeyID: "c1", PublicKey: cIssuer.Public})

	cID, _ := SignIdentity(cIssuer, "c.example", "c1", "worker-7", now.Add(time.Hour))
	if err := store.VerifyIdentity("b.example", cID, now); err == nil {
		t.Fatal("identity from c.example must not be accepted as b.example")
	}
	if err := store.VerifyIdentity("c.example", cID, now); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownDomainIsNotTransitivelyTrusted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	admin := mustKey(t)
	bIssuer := mustKey(t)
	cIssuer := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "b.example", 1, DomainKey{KeyID: "b1", PublicKey: bIssuer.Public})

	cID, _ := SignIdentity(cIssuer, "c.example", "c1", "worker-9", now.Add(time.Hour))
	if err := store.VerifyIdentity("c.example", cID, now); err == nil {
		t.Fatal("c.example must not become trusted merely because a federated peer may trust it")
	}
}

func TestCallerCannotSubstituteBundleWithoutAdminSignature(t *testing.T) {
	admin := mustKey(t)
	attacker := mustKey(t)
	evilIssuer := mustKey(t)
	store := NewStore(admin.Public)

	forged, err := sign(attacker, BundleUpdate{
		TrustDomain: "b.example",
		Version:     1,
		Keys:        []DomainKey{{KeyID: "evil", PublicKey: evilIssuer.Public}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(forged); err == nil {
		t.Fatal("expected pinned admin authority rejection")
	}
}

func TestBundleVersionRollbackIsRejected(t *testing.T) {
	admin := mustKey(t)
	issuer1 := mustKey(t)
	issuer2 := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "b.example", 2, DomainKey{KeyID: "b2", PublicKey: issuer2.Public})

	old, _ := SignBundleUpdate(admin, BundleUpdate{
		TrustDomain: "b.example",
		Version:     1,
		Keys:        []DomainKey{{KeyID: "b1", PublicKey: issuer1.Public}},
	})
	if err := store.Apply(old); err == nil {
		t.Fatal("expected rollback rejection")
	}
}

func TestRotationOverlapAllowsOldAndNewUntilOldRemoved(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	admin := mustKey(t)
	oldKey := mustKey(t)
	newKey := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "b.example", 1, DomainKey{KeyID: "old", PublicKey: oldKey.Public})
	oldID, _ := SignIdentity(oldKey, "b.example", "old", "worker-old", now.Add(time.Hour))
	if err := store.VerifyIdentity("b.example", oldID, now); err != nil {
		t.Fatal(err)
	}

	apply(t, store, admin, "b.example", 2,
		DomainKey{KeyID: "old", PublicKey: oldKey.Public},
		DomainKey{KeyID: "new", PublicKey: newKey.Public},
	)
	newID, _ := SignIdentity(newKey, "b.example", "new", "worker-new", now.Add(time.Hour))
	if err := store.VerifyIdentity("b.example", oldID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyIdentity("b.example", newID, now); err != nil {
		t.Fatal(err)
	}

	apply(t, store, admin, "b.example", 3, DomainKey{KeyID: "new", PublicKey: newKey.Public})
	if err := store.VerifyIdentity("b.example", oldID, now); err == nil {
		t.Fatal("old key must stop validating after removal")
	}
	if err := store.VerifyIdentity("b.example", newID, now); err != nil {
		t.Fatal(err)
	}
}

func TestSameKeyReusedAcrossDomainsStillRequiresDomainBinding(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	admin := mustKey(t)
	shared := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "prod.example", 1, DomainKey{KeyID: "shared", PublicKey: shared.Public})
	apply(t, store, admin, "stage.example", 1, DomainKey{KeyID: "shared", PublicKey: shared.Public})

	stageID, _ := SignIdentity(shared, "stage.example", "shared", "svc", now.Add(time.Hour))
	if err := store.VerifyIdentity("prod.example", stageID, now); err == nil {
		t.Fatal("shared key must not collapse identity namespaces")
	}
}

func TestExpiredIdentityIsRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	admin := mustKey(t)
	issuer := mustKey(t)
	store := NewStore(admin.Public)

	apply(t, store, admin, "b.example", 1, DomainKey{KeyID: "b1", PublicKey: issuer.Public})
	id, _ := SignIdentity(issuer, "b.example", "b1", "worker", now.Add(-time.Second))

	if err := store.VerifyIdentity("b.example", id, now); err == nil {
		t.Fatal("expected expired identity rejection")
	}
}
