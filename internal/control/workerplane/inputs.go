package workerplane

import (
	"github.com/hat47x/eki-fabric/internal/core/contract"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// ArtifactFetchAllowed reports whether the Worker may fetch this digest.
//
// The ground is the assignment, not the Worker's identity: a Worker may read the
// inputs of an Attempt it currently holds, and nothing else (ADR-0029 §5.10). A
// revoked Lease is not an assignment any more, so its inputs stop being fetchable
// the moment the Control Plane withdraws it.
//
// The check is deliberately about the digest and not about a path: a request names
// the content it wants, so what it receives can be verified against the name.
func ArtifactFetchAllowed(assignments []spi.Assignment, digest contract.Digest) bool {
	if digest.IsZero() {
		return false
	}
	wanted := digest.Hex()
	for _, assignment := range assignments {
		if assignment.LeaseState != spi.LeaseStateCurrent {
			continue
		}
		for _, input := range assignment.InputDigests {
			if input.Hex() == wanted {
				return true
			}
		}
	}
	return false
}
