package operatorcert

import (
	"errors"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAnOperatorNameIsReadWithItsRole(t *testing.T) {
	got, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/operator/alice"),
		mustURL(t, "eki://example.org/role/operator"),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Name != "alice" || got.TrustDomain != "example.org" || got.Role != RoleOperator {
		t.Fatalf("identity = %+v", got)
	}
}

func TestAnAdministratorNameIsReadWithItsRole(t *testing.T) {
	got, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/administrator/bob"),
		mustURL(t, "eki://example.org/role/administrator"),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Role != RoleAdministrator {
		t.Fatalf("role = %q", got.Role)
	}
}

func TestACertificateWithBothRolesIsRefused(t *testing.T) {
	_, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/operator/alice"),
		mustURL(t, "eki://example.org/role/operator"),
		mustURL(t, "eki://example.org/role/administrator"),
	})
	if !errors.Is(err, ErrWrongRoles) {
		t.Fatalf("error = %v, want %v", err, ErrWrongRoles)
	}
}

func TestACertificateWithoutARoleIsRefused(t *testing.T) {
	_, err := Parse([]*url.URL{mustURL(t, "eki://example.org/operator/alice")})
	if !errors.Is(err, ErrWrongRoles) {
		t.Fatalf("error = %v, want %v", err, ErrWrongRoles)
	}
}

func TestANameThatDisagreesWithItsRoleIsRefused(t *testing.T) {
	_, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/operator/alice"),
		mustURL(t, "eki://example.org/role/administrator"),
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidName)
	}
}

func TestTwoIdentityNamesAreRefused(t *testing.T) {
	_, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/operator/alice"),
		mustURL(t, "eki://example.org/administrator/alice"),
		mustURL(t, "eki://example.org/role/operator"),
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidName)
	}
}

func TestShapeViolationsAreRefused(t *testing.T) {
	for _, raw := range []string{
		"https://example.org/operator/alice",
		"eki://example.org:8443/operator/alice",
		"eki://example.org/operator/alice?x=1",
		"eki://example.org/operator",
		"eki://example.org/operator/al%69ce",
		"eki://example.org/worker/alice",
		"eki:///operator/alice",
	} {
		if _, err := Parse([]*url.URL{mustURL(t, raw), mustURL(t, "eki://example.org/role/operator")}); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Parse(%q) error = %v, want %v", raw, err, ErrInvalidName)
		}
	}
}

func TestAnUnknownRoleNameIsRefused(t *testing.T) {
	_, err := Parse([]*url.URL{
		mustURL(t, "eki://example.org/operator/alice"),
		mustURL(t, "eki://example.org/role/root"),
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidName)
	}
}
