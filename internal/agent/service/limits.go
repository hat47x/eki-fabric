package service

import (
	"github.com/hat47x/eki-fabric/internal/agent/governor"
	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

// openBudget builds the host's output budget (ADR-0037 §5) from the spool the
// Agent already keeps bodies in. It exists so the construction lives in one place:
// before this, OutputBudget was implemented and tested but never built, so a full
// disk could not refuse anything (#558).
//
// A nil store or a zero limit means the check is off, which is what a deployment
// that keeps no bodies or has no budget gets.
func openBudget(store *spool.Store, limit int64) (*governor.OutputBudget, error) {
	if limit < 0 {
		return nil, governor.ErrOutputBudget
	}
	if store == nil || limit == 0 {
		return governor.NewOutputBudget(nil, 0)
	}
	return governor.NewOutputBudget(store, limit)
}
