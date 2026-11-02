package service

import (
	"errors"
	"io"
	"time"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

// artifactSource adapts the artifact file store to what the client API's
// retrieval route needs (#494, #514). It is the only place that knows both
// packages, so neither has to import the other.
type artifactSource struct {
	store *artifactfile.Store
}

func (a artifactSource) LoadMeta(digestHex string) (clientapi.ArtifactMeta, error) {
	meta, err := a.store.LoadMeta(digestHex)
	switch {
	case errors.Is(err, artifactfile.ErrNoMeta), errors.Is(err, artifactfile.ErrNotFound):
		// Both mean "there is nothing to serve under this name". The route
		// answers them the same way on purpose (spec/external/README.md, #494).
		return clientapi.ArtifactMeta{}, clientapi.ErrArtifactNotFound
	case err != nil:
		return clientapi.ArtifactMeta{}, err
	}
	return clientapi.ArtifactMeta{
		Owner: meta.Owner, MediaType: meta.MediaType, Size: meta.Size, ExpiresAt: meta.ExpiresAt,
	}, nil
}

func (a artifactSource) Open(digestHex string) (io.ReadCloser, int64, error) {
	reader, size, err := a.store.Open(digestHex)
	if errors.Is(err, artifactfile.ErrNotFound) {
		return nil, 0, clientapi.ErrArtifactNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	return reader, size, nil
}

// metaSink adapts the store to the receiver's MetaSink, so a body that is kept is
// also described well enough to be served later (#514).
type metaSink struct {
	store *artifactfile.Store
}

func (m metaSink) PutMeta(digestHex, owner string, size int64, mediaType string, expiresAt time.Time) error {
	return m.store.PutMeta(digestHex, artifactfile.Meta{
		Owner: owner, MediaType: mediaType, Size: size, ExpiresAt: expiresAt,
	})
}

// artifactRetention is how long a kept body may be fetched. It matches the
// Worker's spool default (ADR-0037 §2): a body the Worker still holds must not be
// expired here, or the two sides disagree about whether it can be sent.
const artifactRetention = 24 * time.Hour
