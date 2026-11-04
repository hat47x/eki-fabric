package leaserecall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/leasestate"
)

// File is a lease registry that keeps the open leases in one JSON file (#555).
//
// It exists because leases are not in the state store yet, and the recall loop
// needs a list that survives a restart to be worth running: a reclaim that forgets
// what it reclaimed would either do nothing or reclaim the same lease twice. When
// the lease lands in the state store, that store implements OpenLeases and Reclaim
// and this file goes away.
type File struct {
	mu   sync.Mutex
	path string
	// leases is the current content, keyed by ID.
	leases map[ids.LeaseID]leasestate.LeaseRecord
}

// ErrCorruptLeaseFile means the file exists but is not a lease registry. It is
// returned rather than treated as empty: an empty registry would make the sweep
// silently do nothing for as long as the file stays broken.
var ErrCorruptLeaseFile = errors.New("leaserecall: corrupt lease file")

type leaseFile struct {
	Schema string                   `json:"schema"`
	Leases []leasestate.LeaseRecord `json:"leases"`
}

// OpenFile reads the registry at path. A missing file is an empty registry, which
// is what a fresh deployment has.
func OpenFile(path string) (*File, error) {
	if path == "" {
		return nil, fmt.Errorf("leaserecall: a path is required")
	}
	f := &File{path: path, leases: map[ids.LeaseID]leasestate.LeaseRecord{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leaserecall: read: %w", err)
	}
	var stored leaseFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptLeaseFile, err)
	}
	for _, lease := range stored.Leases {
		f.leases[lease.ID] = lease
	}
	return f, nil
}

// Open records a lease and writes the file. Recording the same lease twice keeps
// the later value: a renewal is the same lease with a later expiry.
func (f *File) Open(lease leasestate.LeaseRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leases[lease.ID] = lease
	return f.save()
}

// OpenLeases returns the leases in the file.
func (f *File) OpenLeases(context.Context) ([]leasestate.LeaseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]leasestate.LeaseRecord, 0, len(f.leases))
	for _, lease := range f.leases {
		out = append(out, lease)
	}
	return out, nil
}

// Reclaim removes a lease and writes the file. Reclaiming one that is not open is
// an error: the sweep stops on it, and "already reclaimed" is information the
// operator should see.
func (f *File) Reclaim(_ context.Context, lease leasestate.LeaseRecord, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.leases[lease.ID]; !ok {
		return fmt.Errorf("leaserecall: lease %s is not open", lease.ID)
	}
	delete(f.leases, lease.ID)
	return f.save()
}

// save writes the whole registry through a temporary file and a rename, so a crash
// leaves either the old file or the new one and never a half-written list.
func (f *File) save() error {
	out := leaseFile{Schema: "eki.lease-registry/v1", Leases: make([]leasestate.LeaseRecord, 0, len(f.leases))}
	for _, lease := range f.leases {
		out.Leases = append(out.Leases, lease)
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("leaserecall: encode: %w", err)
	}
	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".lease-registry-*.json")
	if err != nil {
		return fmt.Errorf("leaserecall: create: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("leaserecall: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("leaserecall: close: %w", err)
	}
	if err := os.Rename(name, f.path); err != nil {
		os.Remove(name)
		return fmt.Errorf("leaserecall: rename: %w", err)
	}
	return nil
}
