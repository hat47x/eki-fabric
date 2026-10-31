package signingkeys

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

const testTrustDomain = "example.org"

var notBefore = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func seedKey(seed byte) (ed25519.PrivateKey, string, string) {
	seedBytes := make([]byte, ed25519.SeedSize)
	seedBytes[0] = seed
	private := ed25519.NewKeyFromSeed(seedBytes)
	return private,
		base64.StdEncoding.EncodeToString(seedBytes),
		base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
}

func validFile(t *testing.T, mutate func(*File)) File {
	t.Helper()
	_, priv, pub := seedKey(11)
	_, privL, pubL := seedKey(12)
	_, privW, pubW := seedKey(13)
	key := func(id, role, principal, priv, pub string) Key {
		return Key{
			KeyID: id, Role: role, Principal: principal, Algorithm: "EdDSA",
			PrivateKey: priv, PublicKey: pub,
			SigningNotBefore: notBefore, SigningNotAfter: notBefore.Add(24 * time.Hour),
			KeyPolicy: "key-policy-1",
		}
	}
	doc := File{TrustDomain: testTrustDomain, Keys: []Key{
		key("kid-plc", string(authority.RolePlacementAuthorization), "control-plane/plc-1", priv, pub),
		key("kid-lse", string(authority.RoleLease), "control-plane/lse-1", privL, pubL),
		key("kid-wor", string(authority.RoleWorkOrder), "control-plane/wor-1", privW, pubW),
	}}
	if mutate != nil {
		mutate(&doc)
	}
	return doc
}

func writeDoc(t *testing.T, doc File, name string) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRegistersTheThreeAuthorityRolesAsPrimary(t *testing.T) {
	path := writeDoc(t, validFile(t, nil), "keys.json")
	keys, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if keys.Registry == nil || keys.Signer == nil {
		t.Fatal("Load() left the registry or signer unset")
	}
	now := notBefore.Add(time.Hour)
	for _, role := range []authority.Role{
		authority.RolePlacementAuthorization, authority.RoleLease, authority.RoleWorkOrder,
	} {
		key, err := keys.Registry.ResolveSigningKey(decisiontrust.SigningRequest{
			TrustDomain: testTrustDomain, AuthorityRole: role,
			AuthorityPrincipal: "control-plane/" + roleShort(role), DecisionKind: kindOf(role),
			Algorithm: decisiontrust.AlgorithmEdDSA,
		}, now)
		if err != nil {
			t.Fatalf("ResolveSigningKey(%s) error = %v", role, err)
		}
		if key.AuthorityRole() != role {
			t.Fatalf("resolved key role = %s, want %s", key.AuthorityRole(), role)
		}
	}
}

func TestLoadRejectsUnusableFiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*File)
	}{
		{"no domain", func(d *File) { d.TrustDomain = "" }},
		{"no keys", func(d *File) { d.Keys = nil }},
		{"wrong algorithm", func(d *File) { d.Keys[0].Algorithm = "ES256" }},
		{"seed mismatch", func(d *File) {
			_, _, pub := seedKey(99)
			d.Keys[0].PublicKey = pub
		}},
		{"missing lease role", func(d *File) { d.Keys = d.Keys[:1] }},
		{"bad base64 seed", func(d *File) { d.Keys[0].PrivateKey = "!!!" }},
		{"empty key id", func(d *File) { d.Keys[0].KeyID = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeDoc(t, validFile(t, tt.mutate), "keys.json")
			if _, err := Load(path); err == nil {
				t.Fatal("Load() error = nil, want ErrInvalid")
			}
		})
	}
}

func TestLoadRejectsMissingAndTruncatedFiles(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load(\"\") error = nil, want ErrInvalid")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("Load(absent) error = nil, want ErrInvalid")
	}
	path := filepath.Join(t.TempDir(), "truncated.json")
	if err := os.WriteFile(path, []byte(`{"trust_domain": "example.org"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load(truncated) error = nil, want ErrInvalid")
	}
	full := writeDoc(t, validFile(t, nil), "full.json")
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte(" trailing")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load(trailing data) error = nil, want ErrInvalid")
	}
}

func roleShort(role authority.Role) string {
	switch role {
	case authority.RolePlacementAuthorization:
		return "plc-1"
	case authority.RoleLease:
		return "lse-1"
	default:
		return "wor-1"
	}
}

func kindOf(role authority.Role) authority.DecisionKind {
	switch role {
	case authority.RolePlacementAuthorization:
		return authority.DecisionPlacementApproval
	case authority.RoleLease:
		return authority.DecisionLease
	default:
		return authority.DecisionWorkOrder
	}
}
