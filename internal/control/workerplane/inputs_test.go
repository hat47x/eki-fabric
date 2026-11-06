package workerplane_test

import (
	"testing"

	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

func currentAssignment(digests ...contract.Digest) spi.Assignment {
	return spi.Assignment{AttemptID: "atm_1", LeaseID: "lse_1", LeaseState: spi.LeaseStateCurrent, InputDigests: digests}
}

// The assignment is the ground: a Worker may read what the Attempt it holds
// declares as input (ADR-0029 §5.10).
func TestTheAssignmentAllowsTheFetch(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	if !server.ArtifactFetchAllowed([]spi.Assignment{currentAssignment(digest)}, digest) {
		t.Fatal("ArtifactFetchAllowed() = false for a digest the current assignment declares")
	}
}

// Anything else is refused: another digest, a revoked Lease, or no assignment.
func TestEverythingElseIsRefused(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	other := contract.SHA256([]byte("other"))
	revoked := currentAssignment(digest)
	revoked.LeaseState = spi.LeaseStateRevoked

	cases := []struct {
		name        string
		assignments []spi.Assignment
		want        bool
	}{
		{"no assignments", nil, false},
		{"digest not declared", []spi.Assignment{currentAssignment(other)}, false},
		{"revoked lease", []spi.Assignment{revoked}, false},
		{"empty digest", []spi.Assignment{currentAssignment(digest)}, false},
	}
	for _, c := range cases {
		wanted := digest
		if c.name == "empty digest" {
			wanted = contract.Digest{}
		}
		if got := server.ArtifactFetchAllowed(c.assignments, wanted); got != c.want {
			t.Errorf("%s: ArtifactFetchAllowed() = %t, want %t", c.name, got, c.want)
		}
	}
}

// One current assignment is enough, even when another is revoked.
func TestACurrentAssignmentWins(t *testing.T) {
	digest := contract.SHA256([]byte("input"))
	revoked := currentAssignment(contract.SHA256([]byte("other")))
	revoked.LeaseState = spi.LeaseStateRevoked
	if !server.ArtifactFetchAllowed([]spi.Assignment{revoked, currentAssignment(digest)}, digest) {
		t.Fatal("ArtifactFetchAllowed() = false although a current assignment declares the digest")
	}
}
