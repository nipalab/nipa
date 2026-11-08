//go:build !windows

package localrepo

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockSharedFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_SH)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func tryLockExclusiveFile(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
