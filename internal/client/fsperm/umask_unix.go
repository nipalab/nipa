//go:build !windows

package fsperm

import (
	"os"
	"syscall"
)

// readUmask runs once while the package initializes, before the program
// starts goroutines that create files: the umask can only be read by setting
// it, and the swap must not be observed.
func readUmask() os.FileMode {
	current := syscall.Umask(0)
	syscall.Umask(current)
	return os.FileMode(current) & os.ModePerm
}
