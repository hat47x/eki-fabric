package service

import (
	"context"
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
	// tasks answers whether the Task was withdrawn; nil means no answer is
	// available and the body is served as before.
	tasks taskWithdrawal
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
	withdrawn, werr := a.withdrawnFor(context.Background(), meta)
	return clientapi.ArtifactMeta{
		Owner: meta.Owner, MediaType: meta.MediaType, DataClass: meta.DataClass, Size: meta.Size, ExpiresAt: meta.ExpiresAt,
		Withdrawn: withdrawn,
	}, werr
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
	// dataClass is what this deployment declares about the bodies it keeps
	// (ADR-0034 §10.2). Empty means nothing is declared and nothing is shown.
	dataClass string
}

func (m metaSink) PutMeta(digestHex, owner, taskID string, size int64, mediaType string, expiresAt time.Time) error {
	return m.store.PutMeta(digestHex, artifactfile.Meta{
		Owner: owner, MediaType: mediaType, Size: size, ExpiresAt: expiresAt, TaskID: taskID,
		DataClass: m.dataClass,
	})
}

// artifactRetention is how long a kept body may be fetched. It matches the
// Worker's spool default (ADR-0037 §2): a body the Worker still holds must not be
// expired here, or the two sides disagree about whether it can be sent.
const artifactRetention = 24 * time.Hour

// artifactSink adapts the artifact file store to the client API's upload route
// (#245). It is separate from artifactSource because the write path needs the
// store”s measuring Put, while the read path needs the metadata view.
type artifactSink struct {
	store *artifactfile.Store
	// dataClass is what this deployment declares about the bodies it keeps
	// (ADR-0034 §10.2). Empty means nothing is declared and nothing is shown.
	dataClass string
}

func (s artifactSink) Put(r io.Reader, claimedHex string, maxBytes int64) (string, int64, error) {
	digest, size, err := s.store.Put(r, claimedHex, maxBytes)
	if errors.Is(err, artifactfile.ErrDigestMismatch) {
		return "", 0, clientapi.ErrDigestMismatch
	}
	return digest, size, err
}

func (s artifactSink) PutMeta(digestHex string, record clientapi.UploadRecord) error {
	dataClass := s.dataClass
	if dataClass == "" {
		// The Client declared a class for this body; a deployment that
		// declares none for Worker-produced bodies still records what the
		// Client asked for (ADR-0034 §9). It is a request, not a judgement.
		dataClass = record.DataClass
	}
	return s.store.PutMeta(digestHex, artifactfile.Meta{
		Owner: record.Owner, MediaType: record.MediaType, Size: record.Size,
		ExpiresAt: record.ExpiresAt, DataClass: dataClass,
	})
}
