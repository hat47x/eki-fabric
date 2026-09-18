package safezip

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type Limits struct {
	MaxFiles            int
	MaxTotalBytes       uint64
	MaxFileBytes        uint64
	MaxDepth            int
	MaxCompressionRatio float64
}

type Entry struct {
	Name              string
	CompressedBytes   uint64
	UncompressedBytes uint64
	Directory         bool
}

type Report struct {
	Entries        []Entry
	FileCount      int
	TotalDeclared  uint64
	TotalExtracted uint64
}

var drivePrefix = regexp.MustCompile(`^[A-Za-z]:`)

func validateName(name string, maxDepth int) (string, error) {
	if name == "" {
		return "", errors.New("empty archive entry name")
	}
	if strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("NUL is not allowed in archive path: %q", name)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("backslash is not allowed in archive path: %q", name)
	}
	if strings.HasPrefix(name, "/") || drivePrefix.MatchString(name) {
		return "", fmt.Errorf("absolute or drive-qualified archive path: %q", name)
	}
	clean := path.Clean(name)
	trimmed := strings.TrimSuffix(name, "/")
	if clean != trimmed {
		return "", fmt.Errorf("archive path is not canonical: %q", name)
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive path escapes destination: %q", name)
	}
	parts := strings.Split(strings.TrimSuffix(clean, "/"), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("invalid archive path segment in %q", name)
		}
	}
	if maxDepth > 0 && len(parts) > maxDepth {
		return "", fmt.Errorf("archive path depth %d exceeds limit %d: %q", len(parts), maxDepth, name)
	}
	return clean, nil
}

func Inspect(r *zip.Reader, limits Limits) (Report, error) {
	var rep Report
	if limits.MaxFiles > 0 && len(r.File) > limits.MaxFiles {
		return rep, fmt.Errorf("file count %d exceeds limit %d", len(r.File), limits.MaxFiles)
	}
	for _, f := range r.File {
		clean, err := validateName(f.Name, limits.MaxDepth)
		if err != nil {
			return rep, err
		}
		_ = clean
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return rep, fmt.Errorf("symbolic link entry is not allowed: %q", f.Name)
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return rep, fmt.Errorf("special file entry is not allowed: %q (%v)", f.Name, mode)
		}
		if limits.MaxFileBytes > 0 && f.UncompressedSize64 > limits.MaxFileBytes {
			return rep, fmt.Errorf("entry %q declared size %d exceeds per-file limit %d", f.Name, f.UncompressedSize64, limits.MaxFileBytes)
		}
		if ^uint64(0)-rep.TotalDeclared < f.UncompressedSize64 {
			return rep, errors.New("declared uncompressed size overflow")
		}
		rep.TotalDeclared += f.UncompressedSize64
		if limits.MaxTotalBytes > 0 && rep.TotalDeclared > limits.MaxTotalBytes {
			return rep, fmt.Errorf("declared total size %d exceeds limit %d", rep.TotalDeclared, limits.MaxTotalBytes)
		}
		if limits.MaxCompressionRatio > 0 && f.UncompressedSize64 > 0 {
			if f.CompressedSize64 == 0 {
				return rep, fmt.Errorf("entry %q has non-zero output with zero compressed size", f.Name)
			}
			ratio := float64(f.UncompressedSize64) / float64(f.CompressedSize64)
			if ratio > limits.MaxCompressionRatio {
				return rep, fmt.Errorf("entry %q compression ratio %.2f exceeds limit %.2f", f.Name, ratio, limits.MaxCompressionRatio)
			}
		}
		rep.Entries = append(rep.Entries, Entry{
			Name: f.Name, CompressedBytes: f.CompressedSize64, UncompressedBytes: f.UncompressedSize64, Directory: mode.IsDir(),
		})
	}
	rep.FileCount = len(r.File)
	return rep, nil
}

func validateExtractionLimits(limits Limits) error {
	if limits.MaxFiles <= 0 {
		return errors.New("MaxFiles must be a positive finite limit")
	}
	if limits.MaxTotalBytes == 0 {
		return errors.New("MaxTotalBytes must be a positive finite limit")
	}
	if limits.MaxFileBytes == 0 {
		return errors.New("MaxFileBytes must be a positive finite limit")
	}
	if limits.MaxDepth <= 0 {
		return errors.New("MaxDepth must be a positive finite limit")
	}
	return nil
}

func Extract(r *zip.Reader, dest string, limits Limits) (Report, error) {
	if err := validateExtractionLimits(limits); err != nil {
		return Report{}, err
	}
	rep, err := Inspect(r, limits)
	if err != nil {
		return Report{}, err
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		return Report{}, fmt.Errorf("create private destination: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dest)
		}
	}()

	base, err := filepath.Abs(dest)
	if err != nil {
		return Report{}, err
	}
	var total uint64
	for _, f := range r.File {
		clean, err := validateName(f.Name, limits.MaxDepth)
		if err != nil {
			return Report{}, err
		}
		out := filepath.Join(base, filepath.FromSlash(clean))
		if out != base && !strings.HasPrefix(out, base+string(os.PathSeparator)) {
			return Report{}, fmt.Errorf("entry escaped destination after join: %q", f.Name)
		}
		if f.Mode().IsDir() {
			if err := os.MkdirAll(out, 0700); err != nil {
				return Report{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			return Report{}, err
		}
		rc, err := f.Open()
		if err != nil {
			return Report{}, err
		}
		of, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			rc.Close()
			return Report{}, err
		}
		perRemaining := limits.MaxFileBytes
		if perRemaining == 0 {
			perRemaining = ^uint64(0)
		}
		totalRemaining := limits.MaxTotalBytes
		if totalRemaining == 0 {
			totalRemaining = ^uint64(0)
		} else if total <= totalRemaining {
			totalRemaining -= total
		} else {
			totalRemaining = 0
		}
		allowed := perRemaining
		if totalRemaining < allowed {
			allowed = totalRemaining
		}
		var n int64
		if allowed == ^uint64(0) || allowed >= uint64(1<<63-1) {
			n, err = io.Copy(of, rc)
		} else {
			lr := io.LimitReader(rc, int64(allowed)+1)
			n, err = io.Copy(of, lr)
			if err == nil && uint64(n) > allowed {
				err = errors.New("expanded data exceeded configured byte limit")
			}
		}
		cerr := rc.Close()
		oerr := of.Close()
		if err != nil {
			return Report{}, err
		}
		if cerr != nil {
			return Report{}, cerr
		}
		if oerr != nil {
			return Report{}, oerr
		}
		total += uint64(n)
	}
	rep.TotalExtracted = total
	success = true
	return rep, nil
}
