package service

import (
	"context"
	"errors"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// artifactResolver answers an input reference for admission (#763).
//
// A reference is accepted only when the body it names is actually stored: a Task
// that pointed at nothing would reach a Worker with an input it can never fetch,
// and the failure would appear as an execution error rather than a refusal at
// admission. The name is the digest — the store keeps nothing else — so the shape
// and the existence are the whole check (ADR-0029 §5.4).
type artifactResolver struct {
	store *artifactfile.Store
}

func (r artifactResolver) ResolveArtifact(ctx context.Context, artifactID string) (contract.Digest, error) {
	digest, err := (admission.DigestIDResolver{}).ResolveArtifact(ctx, artifactID)
	if err != nil {
		return contract.Digest{}, err
	}
	// Open is the existence check: it returns ErrNotFound when no body is kept.
	reader, _, oerr := r.store.Open(digest.Hex())
	switch {
	case errors.Is(oerr, artifactfile.ErrNotFound):
		return contract.Digest{}, admission.ErrUnresolvableArtifact
	case oerr != nil:
		return contract.Digest{}, oerr
	}
	_ = reader.Close()
	return digest, nil
}
