//go:build windows

package usecase

import "os"

const windowsOwnerWrite os.FileMode = 0o200

// replaceFile renames src over dst. Windows refuses to replace a file with
// FILE_ATTRIBUTE_READONLY, so the attribute is cleared for the rename and the
// source's final mode is applied afterwards.
func replaceFile(src, dst string) error {
	if !isReadOnlyFile(dst) {
		return os.Rename(src, dst)
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return err
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.Chmod(dst, dstInfo.Mode().Perm()|windowsOwnerWrite); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Chmod(dst, dstInfo.Mode().Perm())
		return err
	}
	return os.Chmod(dst, srcInfo.Mode().Perm())
}
