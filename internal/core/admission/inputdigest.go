package admission

import (
	"context"
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrUnresolvableArtifact means an input named by a Task cannot be turned into a
// digest. A Task whose inputs cannot be resolved is not admitted: the Task would
// otherwise be placed with a hole where its input should be (ADR-0029 §5.3).
var ErrUnresolvableArtifact = errors.New("admission: input artifact cannot be resolved")

// ArtifactResolver turns an input reference into the digest of its bytes.
//
// The reference is an opaque identifier, not a digest: admission accepts any
// value matching the identifier pattern, and only some deployments issue digest
// hex (ADR-0029 §5.4). A resolver is what knows the difference.
type ArtifactResolver interface {
	ResolveArtifact(ctx context.Context, artifactID string) (contract.Digest, error)
}

// DigestIDResolver resolves references that are already the digest in hex.
//
// It is the resolver for a deployment that issues digest hex as the identifier,
// which is what the Client API's artifact listing does. It is deliberately not a
// guess at other schemes: a reference it does not recognise is unresolvable rather
// than "probably a digest".
type DigestIDResolver struct{}

// ResolveArtifact parses the reference as a SHA-256 digest.
func (DigestIDResolver) ResolveArtifact(_ context.Context, artifactID string) (contract.Digest, error) {
	digest, err := contract.ParseSHA256(artifactID)
	if err != nil {
		return contract.Digest{}, fmt.Errorf("%w: %q is not a digest: %v", ErrUnresolvableArtifact, artifactID, err)
	}
	return digest, nil
}

// InputDigests resolves the inputs of a Task in declaration order.
//
// A reference may repeat (the same artifact under two names); the digests keep
// that repetition, because they describe the inputs the Task declared, and
// dropping one would change what the record says the Task reads.
func InputDigests(ctx context.Context, inputs []ArtifactReference, resolver ArtifactResolver) ([]contract.Digest, error) {
	if resolver == nil {
		return nil, fmt.Errorf("%w: no resolver is configured", ErrUnresolvableArtifact)
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	digests := make([]contract.Digest, 0, len(inputs))
	for i, input := range inputs {
		digest, err := resolver.ResolveArtifact(ctx, input.ArtifactID)
		if err != nil {
			return nil, fmt.Errorf("%w: inputs[%d] %q: %v", ErrUnresolvableArtifact, i, input.ArtifactID, err)
		}
		if digest.IsZero() {
			return nil, fmt.Errorf("%w: inputs[%d] %q resolved to nothing", ErrUnresolvableArtifact, i, input.ArtifactID)
		}
		digests = append(digests, digest)
	}
	return digests, nil
}
