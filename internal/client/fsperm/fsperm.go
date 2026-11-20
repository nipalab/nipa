// Package fsperm applies the process umask to permissions the client sets
// explicitly. Files created with os.OpenFile or os.MkdirAll get the umask
// from the kernel; a Chmod does not, so content written through a temp file
// and chmodded would otherwise ignore a private umask.
package fsperm

import "os"

var umask = readUmask()

// Mask removes the group and other permission bits the process umask clears.
// Owner bits are kept: they carry the tracked mode (read-only, executable).
func Mask(mode os.FileMode) os.FileMode {
	return mask(mode, umask)
}

func mask(mode, umask os.FileMode) os.FileMode {
	return mode &^ (umask & 0o077)
}
