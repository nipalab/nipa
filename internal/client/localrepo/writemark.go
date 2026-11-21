package localrepo

import (
	"crypto/rand"
	"encoding/hex"
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
	writes, err := ensureRealDir(l.target, filepath.Join(ConfigDir, WritesDir))
	if err != nil {
		return nil, false, err
	}
	// The root stays open until end runs; every error path closes it.
	keep := false
	defer func() {
		if !keep {
			_ = writes.Close()
		}
	}()
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

	marker, name, err := createMarker(writes)
	if err != nil {
		return nil, false, err
	}
	if ok, err := tryLockExclusiveFile(marker); err != nil || !ok {
		_ = marker.Close()
		_ = writes.Remove(name)
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
			_ = writes.Remove(name)
			_ = writes.Close()
		})
	}
	keep = true
	return end, interrupted, nil
}

// createMarker creates a new marker with a random name inside the verified
// writes folder.
func createMarker(writes *os.Root) (*os.File, string, error) {
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := "w-" + hex.EncodeToString(b[:])
		f, err := writes.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, name, err
	}
	return nil, "", errors.New("could not create a working-copy write marker")
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
	writes, err := ensureRealDir(l.target, filepath.Join(ConfigDir, WritesDir))
	if err != nil {
		return err
	}
	defer func() { _ = writes.Close() }()
	f, err := writes.OpenFile(interruptedMarker, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
