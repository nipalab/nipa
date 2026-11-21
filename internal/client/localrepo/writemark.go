package localrepo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// WritesDir holds one marker file per working-copy write in progress.
const WritesDir = "writes"

const interruptedMarker = "interrupted"

func (l *LocalRepo) writesDir() string {
	return filepath.Join(l.target, ConfigDir, WritesDir)
}

// BeginWorkingWrite marks a write to the working copy as in progress. The
// marker is a file this process keeps locked until end is called, so a marker
// nobody holds was left by a process that died mid-write, together with
// whatever temp files it had open. interrupted reports that such a marker was
// found (and cleared) while no other write is running: the caller may then
// sweep the working copy for leftovers. While another write is running the
// stale markers stay, and a later write reports them.
func (l *LocalRepo) BeginWorkingWrite() (end func(), interrupted bool, err error) {
	if l.db == nil {
		return nil, false, errors.New("local repo not initialized")
	}
	dir := l.writesDir()
	writes, err := ensureRealDir(l.target, filepath.Join(ConfigDir, WritesDir))
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = writes.Close() }()
	entries, err := fs.ReadDir(writes.FS(), ".")
	if err != nil {
		return nil, false, err
	}
	var stale []string
	live := false
	for _, entry := range entries {
		switch markerState(writes, entry.Name()) {
		case markerStale:
			stale = append(stale, entry.Name())
		case markerLive:
			live = true
		}
	}
	if !live {
		for _, name := range stale {
			_ = writes.Remove(name)
		}
		interrupted = len(stale) > 0
	}

	marker, err := os.CreateTemp(dir, "w-*")
	if err != nil {
		return nil, false, err
	}
	if ok, err := tryLockExclusiveFile(marker); err != nil || !ok {
		_ = marker.Close()
		_ = os.Remove(marker.Name())
		if err == nil {
			err = errors.New("could not lock the working-copy write marker")
		}
		return nil, false, err
	}
	var once sync.Once
	end = func() {
		once.Do(func() {
			_ = unlockFile(marker)
			_ = marker.Close()
			_ = os.Remove(marker.Name())
		})
	}
	return end, interrupted, nil
}

type markerLock int

const (
	markerGone markerLock = iota
	markerStale
	markerLive
)

func markerState(dir *os.Root, name string) markerLock {
	f, err := dir.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return markerGone
	}
	defer func() { _ = f.Close() }()
	ok, err := tryLockExclusiveFile(f)
	if err != nil {
		return markerGone
	}
	if !ok {
		return markerLive
	}
	_ = unlockFile(f)
	return markerStale
}

// MarkInterruptedWrite leaves a marker no process holds, so the next write
// reports an interrupted one again; a sweep that had to leave files behind
// uses it to be repeated.
func (l *LocalRepo) MarkInterruptedWrite() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	if err := os.MkdirAll(l.writesDir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.writesDir(), interruptedMarker), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
