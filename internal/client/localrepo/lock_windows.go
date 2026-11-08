//go:build windows

package localrepo

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockSharedFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), 0, 0, 1, 0, &windows.Overlapped{})
}

func tryLockExclusiveFile(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
