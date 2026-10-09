package fixture

import "github.com/hat47x/eki-fabric/internal/core/resourceprovider"

// NewSlurmLike returns a fixture that behaves like a Slurm batch job. Its defaults
// come from the upstream Slurm documentation, which the README lists with the URL.
// The pages were fetched and checked on 2026-10-09.
//
//   - Batch jobs are requeued by default. The page says the cluster-wide JobRequeue
//     parameter "default value is 1". Verified: https://slurm.schedmd.com/slurm.conf.html
//   - One job can opt out with sbatch --no-requeue, which the page describes as
//     "never be requeued under any circumstances". Verified:
//     https://slurm.schedmd.com/sbatch.html. The fixture models that as a
//     per-submission RetryDisabled setting.
//
// Not modelled:
//   - The ForceRequeueOnFail PrologFlags setting. The sbatch page says that setting
//     can override --no-requeue, so a per-submission disable is not an absolute
//     guarantee.
//   - A requeue after preemption. The slurm.conf page says PreemptMode=REQUEUE
//     preempts "by requeuing them (if possible) or canceling them", and that the job
//     must have --requeue or JobRequeue set to 1 to be requeued. The fixture reports
//     a preemption as Preempted and ends the lease, so it does not follow that
//     requeue path.
//   - The PreemptMode default. The slurm.conf page states that OFF "Is the default
//     value and disables job preemption and gang scheduling" (checked 2026-10-09).
//     So a cluster that keeps the default does not preempt at all. The fixture's
//     preemption events describe a cluster that configures a preemption mode.
//   - The automatic requeue count. The slurm.conf page gives MaxBatchRequeue "The
//     default value is 5." (checked 2026-10-09). It limits how often a batch job is
//     automatically requeued before it is marked JobHeldAdmin. Whether one of the
//     fixture's requeues corresponds to one of Slurm's automatic requeues is not
//     verified. The fixture therefore leaves RetryLimit nil, which means no count
//     limit is modelled. The value 5 is recorded in the README, not in the fixture.
func NewSlurmLike() (*Memory, error) {
	return New(Config{
		Declaration: resourceprovider.Declaration{
			Provider:        "fixture-slurm-like",
			Retry:           resourceprovider.RetryDeclaration{OnByDefault: true, DisableablePerSubmission: true},
			DeclaredManaged: true,
			Credential: resourceprovider.CredentialScope{
				Providers: []resourceprovider.ProviderID{"fixture-slurm-like"},
				Partition: "eki-batch",
			},
		},
	})
}
