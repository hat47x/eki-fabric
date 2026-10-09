package resourceprovider

import "fmt"

// CredentialScope is the reach of the credential that an adapter uses to call a
// provider. An adapter credential is among the strongest authorities in EKI, so
// its reach must be limited (research section 5.8, RQ-051).
type CredentialScope struct {
	// Providers lists the providers the credential can reach. It must name
	// exactly one provider.
	Providers []ProviderID
	// Partition is the namespace or partition inside the provider. It is required.
	Partition string
	// ClusterWide is true when the credential reaches the whole provider. It must
	// be false.
	ClusterWide bool
}

// ValidateCredentialScope checks rule (e) for the adapter that owns the credential.
//
// Rule (e): a provider adapter credential is scoped to one provider. It names
// exactly that provider, is bound to one partition, and is not cluster-wide.
func ValidateCredentialScope(scope CredentialScope, adapter ProviderID) error {
	if adapter == "" {
		return fmt.Errorf("%w: empty adapter provider id", ErrInvalidDeclaration)
	}
	if scope.ClusterWide {
		return fmt.Errorf("%w: credential is cluster-wide", ErrCredentialScope)
	}
	if len(scope.Providers) != 1 {
		return fmt.Errorf("%w: credential names %d providers, want 1", ErrCredentialScope, len(scope.Providers))
	}
	if scope.Providers[0] != adapter {
		return fmt.Errorf("%w: credential names provider %q, adapter is %q", ErrCredentialScope, scope.Providers[0], adapter)
	}
	if scope.Partition == "" {
		return fmt.Errorf("%w: credential has no partition", ErrCredentialScope)
	}
	return nil
}
