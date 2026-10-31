package nativerunner

import (
	"io/fs"
	"path/filepath"
)

// dirSize sums the sizes of regular files under dir. It reports false when the
// tree cannot be read, so an unreadable temporary directory never reads as
// within budget.
func dirSize(dir string) (uint64, bool) {
	var total uint64
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		return 0, false
	}
	return total, true
}
