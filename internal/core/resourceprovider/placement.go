package resourceprovider

import "fmt"

// TaskProfile holds the facts about a task that retry safety depends on. The
// orchestrator fills it in from the Task contract. It is an input to placement,
// not a claim made by a Worker.
type TaskProfile struct {
	// SideEffect is true when the task changes something outside the Worker, such
	// as calling an external API, writing a database, or sending a notification
	// (RQ-046).
	SideEffect bool
	// IdempotencyBasis is true only when there is evidence that running the task
	// again is safe. Without that evidence, a repeat must be treated as a possible
	// duplicate.
	IdempotencyBasis bool
}

// RetrySensitive reports whether an automatic retry that the provider performs on
// its own could repeat the task's effect. A task with a side effect is sensitive,
// and so is a task with no evidence that it is idempotent.
func (t TaskProfile) RetrySensitive() bool {
	return t.SideEffect || !t.IdempotencyBasis
}

// PlaceWithRetryRule decides whether task may be placed on the provider that decl
// describes. It returns the retry setting that the task's submission must carry.
//
// Rule (a): a retry-sensitive task may be placed only on a provider whose
// automatic retry can be disabled per submission. Its submission then carries
// RetryDisabled, so the outcome does not depend on the provider's default. A task
// that is not sensitive keeps the provider's default.
//
// A provider that is off by default but cannot be disabled per submission is still
// refused for a sensitive task. An administrator setting could turn retry on, and
// EKI could neither see nor prevent that at submission time. This choice is listed
// as an open question in conformance/providercontract/README.md.
func PlaceWithRetryRule(task TaskProfile, decl Declaration) (RetrySetting, error) {
	if err := ValidateDeclaration(decl); err != nil {
		return "", err
	}
	if !task.RetrySensitive() {
		return RetryProviderDefault, nil
	}
	if !decl.Retry.DisableablePerSubmission {
		return "", fmt.Errorf("%w: provider %q", ErrRetryNotDisableable, decl.Provider)
	}
	return RetryDisabled, nil
}
