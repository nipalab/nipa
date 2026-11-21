package localrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/nipalab/nipa/internal/client/domain"
)

const LockFile = "lock"

// Each acquisition opens its own handle, so locks taken by one process
// conflict with each other exactly like locks held by different processes.
func (l *LocalRepo) openLock() (*os.File, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	dir, err := openRealDir(l.target, ConfigDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	rel := filepath.Join(ConfigDir, LockFile)
	found, err := dir.Lstat(LockFile)
	switch {
	case err == nil:
		if kind := domain.LinkKind(found.Mode()); kind != "" {
			return nil, linkedMetadataError(rel, kind)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	f, err := dir.OpenFile(LockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if found != nil {
		if opened, err := f.Stat(); err != nil || !os.SameFile(opened, found) {
			_ = f.Close()
			if err == nil {
				err = linkedMetadataError(rel, "file that changed while it was opened")
			}
			return nil, err
		}
	}
	return f, nil
}

// LockShared blocks until the repository lock is held in shared mode.
func (l *LocalRepo) LockShared() (func(), error) {
	f, err := l.openLock()
	if err != nil {
		return nil, err
	}
	if err := lockSharedFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return releaser(f), nil
}

// TryLockExclusive takes the repository lock in exclusive mode without
// waiting; acquired is false while any other holder exists.
func (l *LocalRepo) TryLockExclusive() (release func(), acquired bool, err error) {
	f, err := l.openLock()
	if err != nil {
		return nil, false, err
	}
	ok, err := tryLockExclusiveFile(f)
	if err != nil || !ok {
		_ = f.Close()
		return nil, false, err
	}
	return releaser(f), true, nil
}

func releaser(f *os.File) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockFile(f)
			_ = f.Close()
		})
	}
}
