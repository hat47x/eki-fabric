package teideleg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BundleDir packs a directory into a deterministic tar.gz so that the same
// tree always yields the same digest. Paths, ownership and timestamps are
// normalized; a digest that changed because a file was edited yesterday would
// make cache and identity indistinguishable.
//
// The bundle is the input artifact TEI sends. It is not a container image and
// not a call to build anything: EKI does not unpack it at this revision.
func BundleDir(root string) ([]byte, string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	type entry struct {
		rel  string
		info fs.FileInfo
	}
	var entries []entry
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle: %s is a symbolic link; refusing to make an ambiguous input", rel)
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), info: info})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	if len(entries) == 0 {
		return nil, "", fmt.Errorf("bundle: %s is empty", root)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.ModTime = time.Time{}
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := int64(0o644)
		if e.info.IsDir() {
			mode = 0o755
		} else if e.info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     e.rel,
			Mode:     mode,
			Size:     0,
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg,
			Uname:    "",
			Gname:    "",
		}
		if e.info.IsDir() {
			hdr.Typeflag = tar.TypeDir
			hdr.Name = strings.TrimSuffix(e.rel, "/") + "/"
		} else {
			hdr.Size = e.info.Size()
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", fmt.Errorf("bundle: write header %s: %w", e.rel, err)
		}
		if e.info.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(abs, filepath.FromSlash(e.rel)))
		if err != nil {
			return nil, "", err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return nil, "", copyErr
		}
		if closeErr != nil {
			return nil, "", closeErr
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

// SHA256Hex returns the SHA-256 hex of bytes.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
