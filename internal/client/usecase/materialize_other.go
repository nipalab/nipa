//go:build !windows

package usecase

func replaceIn(fsys replaceFS, src, dst string) error {
	return fsys.Rename(src, dst)
}
