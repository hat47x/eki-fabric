package fixture

import "github.com/hat47x/eki-fabric/internal/core/resourceprovider"

// NewKubernetesLike returns a fixture that behaves like a Kubernetes Job. Its
// defaults come from the upstream Job documentation, which the README lists with
// the URL. The page was fetched and checked on 2026-10-09.
//
//   - Automatic retry is on by default. The page says ".spec.backoffLimit is set by
//     default to 6". Verified: https://kubernetes.io/docs/concepts/workloads/controllers/job/
//   - backoffLimit is a field of the Job spec, so it is set per Job and can be set
//     per submission. Verified on the same page. The fixture models that as a
//     per-submission RetryDisabled setting.
//   - The fixture models the limit of 6 retries.
//
// Not modelled:
//   - Container restarts under restartPolicy OnFailure. The page says these also
//     count toward backoffLimit. The fixture models pod-level retries only.
//   - The podFailurePolicy rule that ignores DisruptionTarget pod failures, such as
//     preemption. The page says that by default each pod failure counts toward
//     backoffLimit, and that a podFailurePolicy rule is needed to exclude disruptions.
//   - The replacement Pod that the Job controller starts after a disruption. The
//     fixture reports a preemption as Preempted and ends the lease, which is EKI's
//     reclaim view under rule (b). This is a simplification. Against a real cluster
//     with default settings, the provider would retry inside the same Job, so this
//     path must be checked there.
//
// Unverified: that backoffLimit 0 turns retry off. The page defines backoffLimit as
// the number of retries, which implies that, but it does not state the value 0.
func NewKubernetesLike() (*Memory, error) {
	return New(Config{
		Declaration: resourceprovider.Declaration{
			Provider:        "fixture-kubernetes-like",
			Retry:           resourceprovider.RetryDeclaration{OnByDefault: true, DisableablePerSubmission: true},
			DeclaredManaged: true,
			Credential: resourceprovider.CredentialScope{
				Providers: []resourceprovider.ProviderID{"fixture-kubernetes-like"},
				Partition: "eki-workers",
			},
		},
		RetryLimit: 6,
	})
}
