//go:build windows

package usecase

import "os"

const windowsOwnerWrite os.FileMode = 0o200

// replaceIn renames src over dst. Windows refuses to replace a file with
// FILE_ATTRIBUTE_READONLY, so the attribute is cleared for the rename and the
// source's final mode is applied afterwards.
func replaceIn(fsys replaceFS, src, dst string) error {
	if !isReadOnlyIn(fsys, dst) {
		return fsys.Rename(src, dst)
	}
	dstInfo, err := fsys.Stat(dst)
	if err != nil {
		return err
	}
	srcInfo, err := fsys.Stat(src)
	if err != nil {
		return err
	}
	if err := fsys.Chmod(dst, dstInfo.Mode().Perm()|windowsOwnerWrite); err != nil {
		return err
	}
	if err := fsys.Rename(src, dst); err != nil {
		_ = fsys.Chmod(dst, dstInfo.Mode().Perm())
		return err
	}
	return fsys.Chmod(dst, srcInfo.Mode().Perm())
}
