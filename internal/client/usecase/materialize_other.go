//go:build !windows

package usecase

import "os"

// replaceFile renames src over dst. A read-only dst stays read-only: the
// editor plugin that locked it owns that state.
func replaceFile(src, dst string) error {
	if isReadOnlyFile(dst) {
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if err := os.Chmod(src, info.Mode().Perm()&^0o222); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}
