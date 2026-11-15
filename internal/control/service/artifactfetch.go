package service

import (
	"context"
	"errors"
	"io"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// OpenArtifact serves one input body to a Worker that holds an Attempt
// declaring that digest (ADR-0029 §5.10). The authorization is not decided
// here: workerplane has already checked that the caller's current assignments
// name the digest, so this adapter only answers "is there a body under this
// name".
//
// It is the same store the Client API's retrieval route reads, which is what
// keeps the input a Client uploaded and the input a Worker fetches the same
// bytes (#245).
func (a artifactSource) OpenArtifact(ctx context.Context, digest contract.Digest) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	reader, size, err := a.store.Open(digest.Hex())
	switch {
	case errors.Is(err, artifactfile.ErrNotFound):
		// "Not kept here" and "removed" are the same answer to a Worker: it
		// cannot start either way (ADR-0029 §5.10).
		return nil, 0, workerplane.ErrArtifactNotFound
	case err != nil:
		return nil, 0, err
	}
	return reader, size, nil
}
