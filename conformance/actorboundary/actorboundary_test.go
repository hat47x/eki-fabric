// Package actorboundary is a tripwire for ADR-0033 §6: each decision boundary names
// an authenticated principal, and if one of those principals disappears or changes
// shape, the mapping in the ADR no longer describes the code.
//
// The test does not exercise behaviour. Its value is that it stops compiling when a
// boundary moves: the compiler is the cheapest detector for "the document and the
// code drifted apart", and it runs in every verify.
package actorboundary

import (
	"testing"

	"github.com/hat47x/eki-fabric/internal/agent/assignment"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// TestTheBoundariesStillHaveThePrincipalsADR0033Names pins one type per boundary.
// A boundary that gains or loses a principal type has to be re-read against
// ADR-0033 §6 rather than discovered in production.
func TestTheBoundariesStillHaveThePrincipalsADR0033Names(t *testing.T) {
	// 依頼元 (Client): the bearer token's principal, established by the Client API.
	var _ clientapi.Identity

	// 端末の利用者 (Worker): the enrolled identity the Worker Plane carries.
	var _ workerenrollment.WorkerPrincipal
	var _ workerplane.Principal

	// 実行する者 (Agent): the claim path's principal and the assignment path's.
	var _ claimplane.Principal
	var _ assignment.Config

	// 管理・運用 (Operator / Administrator): the certificate's identity and role.
	var _ operatorcert.Identity

	// 端末の資源判断 (Local Governor): the plan the gate produces for a start.
	var _ startgate.Plan
}
