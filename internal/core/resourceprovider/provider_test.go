package resourceprovider

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

func validDeclaration() Declaration {
	return Declaration{
		Provider: "cluster-a",
		Credential: CredentialScope{
			Providers: []ProviderID{"cluster-a"},
			Partition: "eki-workers",
		},
	}
}

func TestValidateDeclaration(t *testing.T) {
	if err := ValidateDeclaration(validDeclaration()); err != nil {
		t.Fatalf("ValidateDeclaration(valid) = %v, want nil", err)
	}

	empty := validDeclaration()
	empty.Provider = ""
	if err := ValidateDeclaration(empty); !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("empty provider error = %v, want ErrInvalidDeclaration", err)
	}

	wide := validDeclaration()
	wide.Credential.ClusterWide = true
	if err := ValidateDeclaration(wide); !errors.Is(err, ErrCredentialScope) {
		t.Fatalf("cluster-wide credential error = %v, want ErrCredentialScope", err)
	}
}

func TestValidateAllocationRequest(t *testing.T) {
	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}

	valid := AllocationRequest{LeaseID: lease, Slots: 2, Retry: RetryProviderDefault}
	if err := ValidateAllocationRequest(valid); err != nil {
		t.Fatalf("valid request = %v, want nil", err)
	}
	disabled := valid
	disabled.Retry = RetryDisabled
	if err := ValidateAllocationRequest(disabled); err != nil {
		t.Fatalf("disabled-retry request = %v, want nil", err)
	}

	cases := []struct {
		name string
		req  AllocationRequest
	}{
		{name: "malformed lease id", req: AllocationRequest{LeaseID: "not-a-lease", Slots: 1, Retry: RetryProviderDefault}},
		{name: "zero slots", req: AllocationRequest{LeaseID: lease, Slots: 0, Retry: RetryProviderDefault}},
		{name: "too many slots", req: AllocationRequest{LeaseID: lease, Slots: MaxSlotsPerAllocation + 1, Retry: RetryProviderDefault}},
		{name: "unknown retry setting", req: AllocationRequest{LeaseID: lease, Slots: 1, Retry: "maybe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateAllocationRequest(tc.req); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ValidateAllocationRequest() = %v, want ErrInvalidRequest", err)
			}
		})
	}
}
