package trustanchor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

// WithVersion returns the document with one accepted version appended and
// bundle_revision set to it. It refuses a version that does not follow the
// newest one, so the file's history stays a chain (ADR-0036 §2.1, #478).
func (doc File) WithVersion(signed trustbundle.Signed) (File, error) {
	if len(doc.TrustBundleVersions) > 0 {
		current, err := enrollwire.DecodeBundle(doc.TrustBundleVersions[len(doc.TrustBundleVersions)-1])
		if err != nil {
			return File{}, err
		}
		if signed.Bundle.Version <= current.Bundle.Version {
			if signed.Bundle.Version == current.Bundle.Version && signed.Bundle.Digest() == current.Bundle.Digest() {
				return doc, nil
			}
			return File{}, fmt.Errorf("%w: version %d does not follow %d", ErrInvalid, signed.Bundle.Version, current.Bundle.Version)
		}
		if err := signed.Bundle.Follows(current.Bundle); err != nil {
			return File{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	} else if signed.Bundle.Version != 1 {
		return File{}, fmt.Errorf("%w: the first stored version must be 1, got %d", ErrInvalid, signed.Bundle.Version)
	}
	doc.TrustBundleVersions = append(append([]enrollwire.Bundle(nil), doc.TrustBundleVersions...), enrollwire.EncodeBundle(signed))
	doc.BundleRevision = signed.Bundle.Version
	return doc, nil
}

// WriteFile replaces path with the document atomically: a crash during the write
// leaves the previous file intact rather than a truncated one.
func WriteFile(path string, doc File) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".trust-anchor-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := temp.Name()
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// ReadFile reads the document without building a verifier boundary, so a caller
// that only appends a version does not need working keys yet (ADR-0036, #478).
// The size limit and the strict decoder match Load: a file that Load refuses is
// not silently accepted here.
func ReadFile(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(raw) > maxFileBytes {
		return File{}, fmt.Errorf("%w: file is too large", ErrInvalid)
	}
	var doc File
	if err := json.Unmarshal(raw, &doc); err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := checkVersions(doc); err != nil {
		return File{}, err
	}
	return doc, nil
}
