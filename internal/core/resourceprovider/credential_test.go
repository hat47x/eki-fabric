package resourceprovider

import (
	"errors"
	"testing"
)

func TestValidateCredentialScope(t *testing.T) {
	const adapter ProviderID = "cluster-a"
	valid := CredentialScope{Providers: []ProviderID{adapter}, Partition: "eki-workers"}

	cases := []struct {
		name    string
		scope   CredentialScope
		adapter ProviderID
		wantErr bool
	}{
		{name: "one provider and one partition", scope: valid, adapter: adapter},
		{
			name:    "cluster-wide is refused even with one provider",
			scope:   CredentialScope{Providers: []ProviderID{adapter}, Partition: "eki-workers", ClusterWide: true},
			adapter: adapter,
			wantErr: true,
		},
		{
			name:    "two providers are refused",
			scope:   CredentialScope{Providers: []ProviderID{adapter, "cluster-b"}, Partition: "eki-workers"},
			adapter: adapter,
			wantErr: true,
		},
		{
			name:    "no provider is refused",
			scope:   CredentialScope{Partition: "eki-workers"},
			adapter: adapter,
			wantErr: true,
		},
		{
			name:    "a credential for another provider is refused",
			scope:   valid,
			adapter: "cluster-b",
			wantErr: true,
		},
		{
			name:    "missing partition is refused",
			scope:   CredentialScope{Providers: []ProviderID{adapter}},
			adapter: adapter,
			wantErr: true,
		},
		{name: "empty adapter id is refused", scope: valid, adapter: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCredentialScope(tc.scope, tc.adapter)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ValidateCredentialScope() = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateCredentialScope() = %v, want nil", err)
			}
		})
	}

	err := ValidateCredentialScope(CredentialScope{Providers: []ProviderID{"a", "b"}, Partition: "p"}, "a")
	if !errors.Is(err, ErrCredentialScope) {
		t.Fatalf("two-provider error = %v, want ErrCredentialScope", err)
	}
}
