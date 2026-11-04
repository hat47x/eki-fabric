package clientapi

import (
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// publicArtifact is one entry of `artifacts` in the result body
// (spec/external/README.md, #493).
type publicArtifact struct {
	Sha256    publicDigest `json:"sha256"`
	SizeBytes int64        `json:"size_bytes"`
	Available bool         `json:"available"`
	// ArtifactID is present only when the body can be fetched: handing out an
	// identifier that answers 410 or 404 would read as "try and see".
	ArtifactID string `json:"artifact_id,omitempty"`
	// MediaType is the Worker's declaration, not a promise about the bytes.
	MediaType string `json:"media_type,omitempty"`
	// DataClass is the Worker's declaration (ADR-0034 §9). Omitted when it said
	// nothing: a served "unclassified" would look like a classification.
	DataClass string `json:"data_class,omitempty"`
}

// buildArtifacts projects what the store knows onto the result body. An artifact
// whose metadata is missing or expired is still listed, with available false: the
// digest and size are the record of what the Task produced, and dropping the line
// would read as "nothing was produced".
func buildArtifacts(digest contract.Digest, meta ArtifactMeta, found bool, now time.Time) []publicArtifact {
	if digest == (contract.Digest{}) {
		return []publicArtifact{}
	}
	out := publicArtifact{
		Sha256: publicDigest{Algorithm: "sha256", Value: digest.Hex()},
	}
	switch {
	case !found:
		return []publicArtifact{out}
	case !now.UTC().Before(meta.ExpiresAt):
		out.SizeBytes = meta.Size
		return []publicArtifact{out}
	default:
		out.SizeBytes = meta.Size
		out.Available = true
		out.ArtifactID = digest.Hex()
		out.MediaType = meta.MediaType
		out.DataClass = meta.DataClass
		return []publicArtifact{out}
	}
}
