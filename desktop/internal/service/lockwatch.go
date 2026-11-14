package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// LockWatchStore keeps the lock paths each clone waits on.
type LockWatchStore interface {
	LockWatches(root string) ([]string, error)
	SetLockWatch(root, path string, watch bool) error
}

// LockWatch is one lock the user waits on. Branch is empty for mainline.
type LockWatch struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

const lockWatchSep = "\x00"

func (w LockWatch) key() string { return w.Branch + lockWatchSep + w.Path }

func parseLockWatch(key string) LockWatch {
	branch, path, _ := strings.Cut(key, lockWatchSep)
	return LockWatch{Branch: branch, Path: path}
}

// LockWatches lists the locks the user asked to be told about once free.
func (s *Service) LockWatches(_ context.Context, root string) ([]LockWatch, error) {
	out := []LockWatch{}
	if s.Watches == nil {
		return out, nil
	}
	keys, err := s.Watches.LockWatches(root)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		out = append(out, parseLockWatch(k))
	}
	return out, nil
}

// WatchLock asks for a notification when the lock on path in the branch
// scope (empty = mainline) is released.
func (s *Service) WatchLock(_ context.Context, root, branch, path string) error {
	return s.setLockWatch(root, branch, path, true)
}

// UnwatchLock drops the watch on path in the branch scope.
func (s *Service) UnwatchLock(_ context.Context, root, branch, path string) error {
	return s.setLockWatch(root, branch, path, false)
}

func (s *Service) setLockWatch(root, branch, path string, watch bool) error {
	if s.Watches == nil {
		return errors.New("lock watching is not available")
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return errors.New("choose a file or folder to watch")
	}
	return s.Watches.SetLockWatch(root, LockWatch{Branch: branch, Path: path}.key(), watch)
}

type watchedNotice struct {
	Notification
	watch LockWatch
}

// watchedLockNotifications turns a released lock that covers watched paths of
// the same branch scope into "lock it now" notifications. The caller drops a
// watch once its notification was delivered.
func (s *Service) watchedLockNotifications(name, root string, e TeamEvent) []watchedNotice {
	if e.Kind != "lock_released" || s.Watches == nil {
		return nil
	}
	watched, err := s.LockWatches(context.Background(), root)
	if err != nil {
		return nil
	}
	released := strings.Trim(e.Path, "/")
	var out []watchedNotice
	for _, w := range watched {
		if w.Branch != e.Branch || (w.Path != released && !strings.HasPrefix(w.Path, released+"/")) {
			continue
		}
		out = append(out, watchedNotice{
			Notification: Notification{
				ID:     "lockwatch:" + root + ":" + w.key(),
				Title:  name + ": " + w.Path + " is free",
				Body:   fmt.Sprintf("%s released it. Click to lock it now.", e.Actor),
				Target: Navigation{Root: root, Tab: "locks", Path: w.Path, Action: "lock"},
			},
			watch: w,
		})
	}
	return out
}
