package startgate

import (
	"fmt"
	"sort"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

// ErrNeedExceedsCeiling means the Local Governor allows less of a resource than the
// WorkOrder needs. It is separate from having no capacity at all: the difference is
// "this machine may never do this" versus "not right now", and an operator can only
// act on the first one (ADR-0037 §11.3).
var ErrNeedExceedsCeiling = fmt.Errorf("startgate: the governor ceiling is below the request")

// RequireWithinCeiling reports the first resource whose request is above the
// Governor's ceiling.
//
// It refuses rather than clamping. Starting with less CPU or memory than the
// WorkOrder asked for would run the Task under limits nobody agreed to, and the
// result would look valid.
func RequireWithinCeiling(decision governor.Decision, need map[governor.Resource]int) error {
	resources := make([]string, 0, len(need))
	for resource := range need {
		resources = append(resources, string(resource))
	}
	sort.Strings(resources)
	for _, name := range resources {
		resource := governor.Resource(name)
		want := need[resource]
		if want <= 0 {
			continue
		}
		if have := decision.Ceilings[resource]; have < want {
			return fmt.Errorf("%w: %s needs %d%%, the ceiling is %d%%", ErrNeedExceedsCeiling, resource, want, have)
		}
	}
	return nil
}
