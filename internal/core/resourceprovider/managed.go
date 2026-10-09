package resourceprovider

import "fmt"

// AdministratorPolicy names the providers that an administrator has declared to
// be managed. It is a placement input. Only the trusted policy path may build one,
// for example from a Policy revision of the Policy authority. A zero value names
// no provider.
type AdministratorPolicy struct {
	managed map[ProviderID]struct{}
}

// NewAdministratorPolicy returns a policy that names the given providers. An empty
// provider identifier is refused.
func NewAdministratorPolicy(managed ...ProviderID) (AdministratorPolicy, error) {
	p := AdministratorPolicy{managed: make(map[ProviderID]struct{}, len(managed))}
	for _, id := range managed {
		if id == "" {
			return AdministratorPolicy{}, fmt.Errorf("%w: empty provider id in policy", ErrInvalidDeclaration)
		}
		p.managed[id] = struct{}{}
	}
	return p, nil
}

// Names reports whether the policy names the provider.
func (p AdministratorPolicy) Names(id ProviderID) bool {
	_, ok := p.managed[id]
	return ok
}

// ManagedForPlacement reports whether placement may treat the workers of the
// provider as managed. This input can raise the Assurance of those workers, for
// example the managed-device binding on Axis A of requirements/ASSURANCE_MODEL.md.
//
// Rule (d): only the administrator policy can make the result true. decl.DeclaredManaged
// is the provider's own claim and never enters the result, so a provider cannot
// raise its own assurance by declaring itself managed. Requirement RQ-047 covers
// this.
//
// No production code calls this function yet. Its callers are the tests of this
// package and the shared conformance suite. Open question 6 in
// conformance/providercontract/README.md records that DeclaredManaged has no reader
// and that the consumer is undecided.
func ManagedForPlacement(decl Declaration, policy AdministratorPolicy) bool {
	return decl.Provider != "" && policy.Names(decl.Provider)
}
