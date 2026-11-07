//go:build windows

package usecase

import "os"

// replaceFile renames src over dst. Windows refuses to replace a file with
// FILE_ATTRIBUTE_READONLY, so the attribute is cleared for the rename and set
// again on the new file: the editor plugin that locked it owns that state.
func replaceFile(src, dst string) error {
	if !isReadOnlyFile(dst) {
		return os.Rename(src, dst)
	}
	if err := os.Chmod(dst, 0o666); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Chmod(dst, 0o444)
		return err
	}
	return os.Chmod(dst, 0o444)
}
