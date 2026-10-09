package fixture

import "github.com/hat47x/eki-fabric/internal/core/resourceprovider"

// NewRetryNotDisableable returns a deliberately misconfigured fixture. Its automatic
// retry is on by default and cannot be disabled per submission. Rule (a) must refuse
// to place a retry-sensitive task on it.
//
// The fixture is honest about its configuration, so it still passes the shared
// contract. Its problem shows only in placement, which is what the rule is for.
func NewRetryNotDisableable() (*Memory, error) {
	return New(Config{
		Declaration: resourceprovider.Declaration{
			Provider:        "fixture-retry-locked",
			Retry:           resourceprovider.RetryDeclaration{OnByDefault: true, DisableablePerSubmission: false},
			DeclaredManaged: true,
			Credential: resourceprovider.CredentialScope{
				Providers: []resourceprovider.ProviderID{"fixture-retry-locked"},
				Partition: "eki-workers",
			},
		},
		RetryLimit: 6,
	})
}
