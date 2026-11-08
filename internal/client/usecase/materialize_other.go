//go:build !windows

package usecase

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
