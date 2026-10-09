package fixture

import "github.com/hat47x/eki-fabric/internal/core/resourceprovider"

// DefaultKubernetesBackoffLimit is the default of .spec.backoffLimit. The Job page
// says "The `.spec.backoffLimit` is set by default to 6, unless the backoff limit per
// index ... is specified." Verified: https://kubernetes.io/docs/concepts/workloads/controllers/job/
// (checked 2026-10-09, see the README in the parent directory).
const DefaultKubernetesBackoffLimit = 6

// NewKubernetesLike returns a fixture that behaves like a Kubernetes Job with the
// default backoffLimit. Its defaults come from the upstream Job documentation, which
// the README lists with the URL. The page was fetched and checked on 2026-10-09.
//
//   - Automatic retry is on by default. The page says ".spec.backoffLimit is set by
//     default to 6". Verified: https://kubernetes.io/docs/concepts/workloads/controllers/job/
//   - backoffLimit is a field of the Job spec, so it is set per Job and can be set
//     per submission. Verified on the same page. The fixture models that as a
//     per-submission RetryDisabled setting.
//   - The fixture models the default limit of 6 retries.
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
func NewKubernetesLike() (*Memory, error) {
	return NewKubernetesLikeWithBackoffLimit(DefaultKubernetesBackoffLimit)
}

// NewKubernetesLikeWithBackoffLimit returns the Kubernetes-like fixture with the given
// backoffLimit. A limit of 0 is expressible here. The fixture then gives no automatic
// retry: the first failure is final.
//
// Unverified: that a real Job with backoffLimit 0 gives no retry. The Job page says
// "If either of the calculations reaches the .spec.backoffLimit, the Job is considered
// failed." It does not state the value 0 (checked 2026-10-09). The fixture's model of 0
// is therefore an assumption, and the real behaviour must be checked on a cluster.
//
// A limit of 0 with OnByDefault true is outside the shared contract. The contract
// check "declared-default-retry-matches-observed" expects a requeue whenever the
// default is on, so RunContract does not apply to a fixture with limit 0. Its behaviour
// is checked in suite_test.go instead.
func NewKubernetesLikeWithBackoffLimit(backoffLimit int) (*Memory, error) {
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
		RetryLimit: intPtr(backoffLimit),
	})
}
