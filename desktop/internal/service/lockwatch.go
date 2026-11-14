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

// LockWatches lists the paths the user asked to be told about once free.
func (s *Service) LockWatches(_ context.Context, root string) ([]string, error) {
	if s.Watches == nil {
		return []string{}, nil
	}
	return s.Watches.LockWatches(root)
}

// WatchLock asks for a notification when path is free to lock.
func (s *Service) WatchLock(_ context.Context, root, path string) error {
	return s.setLockWatch(root, path, true)
}

// UnwatchLock drops the watch on path.
func (s *Service) UnwatchLock(_ context.Context, root, path string) error {
	return s.setLockWatch(root, path, false)
}

func (s *Service) setLockWatch(root, path string, watch bool) error {
	if s.Watches == nil {
		return errors.New("lock watching is not available")
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return errors.New("choose a file or folder to watch")
	}
	return s.Watches.SetLockWatch(root, path, watch)
}

// watchedLockNotifications turns a released lock that covers watched paths
// into "lock it now" notifications and drops those watches.
func (s *Service) watchedLockNotifications(name, root string, e TeamEvent) []Notification {
	if e.Kind != "lock_released" || s.Watches == nil {
		return nil
	}
	watched, err := s.Watches.LockWatches(root)
	if err != nil {
		return nil
	}
	released := strings.Trim(e.Path, "/")
	var out []Notification
	for _, p := range watched {
		if p != released && !strings.HasPrefix(p, released+"/") {
			continue
		}
		if s.Watches.SetLockWatch(root, p, false) != nil {
			continue
		}
		out = append(out, Notification{
			ID:     "lockwatch:" + root + ":" + p,
			Title:  name + ": " + p + " is free",
			Body:   fmt.Sprintf("%s released it. Click to lock it now.", e.Actor),
			Target: Navigation{Root: root, Tab: "locks", Path: p, Action: "lock"},
		})
	}
	return out
}
