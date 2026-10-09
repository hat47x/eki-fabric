package resourceprovider

import (
	"errors"
	"testing"
)

// TestManagedForPlacementIgnoresDeclaration covers rule (d).
func TestManagedForPlacementIgnoresDeclaration(t *testing.T) {
	declaredManaged := validDeclaration()
	declaredManaged.DeclaredManaged = true

	empty, err := NewAdministratorPolicy()
	if err != nil {
		t.Fatal(err)
	}
	named, err := NewAdministratorPolicy("cluster-a")
	if err != nil {
		t.Fatal(err)
	}
	otherOnly, err := NewAdministratorPolicy("cluster-b")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		decl   Declaration
		policy AdministratorPolicy
		want   bool
	}{
		{name: "self-declared managed with zero policy is not managed", decl: declaredManaged, policy: AdministratorPolicy{}, want: false},
		{name: "self-declared managed with empty policy is not managed", decl: declaredManaged, policy: empty, want: false},
		{name: "self-declared managed and named by administrator is managed", decl: declaredManaged, policy: named, want: true},
		{name: "administrator naming the provider is enough without the declaration", decl: validDeclaration(), policy: named, want: true},
		{name: "administrator naming another provider does not manage this one", decl: declaredManaged, policy: otherOnly, want: false},
		{name: "empty provider is never managed", decl: Declaration{}, policy: named, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ManagedForPlacement(tc.decl, tc.policy); got != tc.want {
				t.Fatalf("ManagedForPlacement() = %v, want %v", got, tc.want)
			}
		})
	}

	if _, err := NewAdministratorPolicy("cluster-a", ""); !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("policy with empty provider error = %v, want ErrInvalidDeclaration", err)
	}
}
