package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) IssueEnrollmentCredential(ctx context.Context, credential workerregistry.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := credential.Validate(); err != nil {
		return err
	}
	if credential.State != workerregistry.CredentialIssued {
		return fmt.Errorf("%w: a new credential is issued", workerregistry.ErrInvalidCredential)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	hash := credential.SecretSHA256.Hex()
	if _, exists := s.credentials[credential.ID]; exists {
		return fmt.Errorf("%w: enrollment credential %s", statestore.ErrAlreadyExists, credential.ID)
	}
	if _, exists := s.credentialByHash[hash]; exists {
		return fmt.Errorf("%w: enrollment secret digest", statestore.ErrAlreadyExists)
	}
	s.credentials[credential.ID] = credential
	s.credentialByHash[hash] = credential.ID
	return nil
}

func (s *Store) LoadEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID) (workerregistry.Credential, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Credential{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.credentials[id]
	if !ok {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s", statestore.ErrNotFound, id)
	}
	return c, nil
}

func (s *Store) RevokeEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID, actor string, at time.Time) (workerregistry.Credential, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.credentials[id]
	if !ok {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s", statestore.ErrNotFound, id)
	}
	next, err := c.Revoke(actor, at)
	if err != nil {
		return workerregistry.Credential{}, err
	}
	s.credentials[id] = next
	return next, nil
}

func (s *Store) ConsumeEnrollmentCredential(ctx context.Context, req statestore.ConsumeEnrollment) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.credentialByHash[req.SecretSHA256.Hex()]
	if !ok {
		return workerregistry.Worker{}, workerregistry.NewRefusal(workerregistry.ReasonUnknown)
	}
	credential := s.credentials[id]
	if err := credential.CheckUsable(req.SecretSHA256, req.TrustDomain, req.At); err != nil {
		return workerregistry.Worker{}, err
	}
	if _, exists := s.workers[req.Identity]; exists {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker identity %s", statestore.ErrAlreadyExists, req.Identity)
	}
	spent, err := credential.Consume(req.Identity, req.At)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	worker, err := workerregistry.NewWorker(req.Identity, credential, spent.ConsumedAt)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	s.credentials[id] = spent
	s.workers[worker.Identity] = worker
	return worker, nil
}

func (s *Store) LoadWorker(ctx context.Context, identity ids.WorkerIdentity) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	w, ok := s.workers[identity]
	if !ok {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s", statestore.ErrNotFound, identity)
	}
	return w, nil
}

func (s *Store) TransitionWorker(ctx context.Context, identity ids.WorkerIdentity, to workerregistry.State, actor string, at time.Time) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.workers[identity]
	if !ok {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s", statestore.ErrNotFound, identity)
	}
	next, err := w.Transition(to, actor, at)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	s.workers[identity] = next
	return next, nil
}

func (s *Store) RecordWorkerCertificate(ctx context.Context, identity ids.WorkerIdentity, cert workerregistry.Certificate, at time.Time) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.workers[identity]
	if !ok {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s", statestore.ErrNotFound, identity)
	}
	next, err := w.WithCertificate(cert, at)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	s.workers[identity] = next
	return next, nil
}

var _ statestore.WorkerRegistryStore = (*Store)(nil)
