package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// FilePicker asks the user for a file, starting in dir; an empty path means
// cancelled.
type FilePicker func(dir string) (string, error)

// Opener opens a file with the system's default application.
type Opener func(path string) error

type FileLock struct {
	ID           string `json:"id"`
	Path         string `json:"path"` // file or directory prefix
	Branch       string `json:"branch"`
	Global       bool   `json:"global"` // mainline lock of the default branch
	HeldBy       string `json:"heldBy"`
	HeldByName   string `json:"heldByName"`
	Mine         bool   `json:"mine"`
	MergeRequest int64  `json:"mergeRequest"` // 0 when not held for a merge request
	AcquiredAt   string `json:"acquiredAt"`   // RFC 3339, empty when unknown
	InScope      bool   `json:"inScope"`      // applies to the clone's branch
}

type FileLockState struct {
	Path       string `json:"path"`
	State      string `json:"state"` // mine, theirs, needed, free
	LockPath   string `json:"lockPath"`
	HeldByName string `json:"heldByName"`
	Tracked    bool   `json:"tracked"`
}

// LockStatus is the clone's lock picture: every visible lock, and the lock
// state of each changed binary in the branch's scope.
type LockStatus struct {
	UserID        string          `json:"userId"`
	Branch        string          `json:"branch"`
	DefaultBranch bool            `json:"defaultBranch"`
	CanReleaseAny bool            `json:"canReleaseAny"`
	Locks         []FileLock      `json:"locks"`
	Files         []FileLockState `json:"files"`
}

type LockFailure struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type LockResult struct {
	Locked []string      `json:"locked"`
	Failed []LockFailure `json:"failed"`
}

// LockStatus lists the project's locks and the lock state of changed binaries.
func (s *Service) LockStatus(ctx context.Context, root string) (*LockStatus, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	res, err := client.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	out := &LockStatus{
		UserID: res.GetUserId(), Branch: res.GetBranch(), DefaultBranch: res.GetDefaultBranch(), CanReleaseAny: res.GetCanReleaseAny(),
		Locks: make([]FileLock, 0, len(res.GetLocks())), Files: make([]FileLockState, 0, len(res.GetFiles())),
	}
	for _, l := range res.GetLocks() {
		out.Locks = append(out.Locks, toFileLock(l, res))
	}
	for _, f := range res.GetFiles() {
		out.Files = append(out.Files, FileLockState{Path: f.GetPath(), State: f.GetState(), LockPath: f.GetLockPath(), HeldByName: f.GetHeldByName(), Tracked: f.GetTracked()})
	}
	return out, nil
}

func toFileLock(l *pb.FileLockDetail, res *daemonpb.LockStatusResponse) FileLock {
	lock := FileLock{
		ID: l.GetId(), Path: l.GetPath(), Branch: l.GetBranch(), Global: l.GetGlobal(), HeldBy: l.GetHeldBy(), HeldByName: l.GetHeldByName(),
		Mine: l.GetHeldBy() != "" && l.GetHeldBy() == res.GetUserId(), MergeRequest: l.GetMergeRequestNumber(),
		InScope: res.GetDefaultBranch() && l.GetGlobal() || !res.GetDefaultBranch() && !l.GetGlobal() && l.GetBranch() == res.GetBranch(),
	}
	if l.GetAcquiredAt() != nil {
		lock.AcquiredAt = l.GetAcquiredAt().AsTime().UTC().Format(time.RFC3339)
	}
	return lock
}

// Lock takes a lock on each path in the branch's scope (mainline for the
// default branch). Paths that cannot be locked are reported, not fatal.
func (s *Service) Lock(ctx context.Context, root, branch string, paths []string) (*LockResult, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &LockResult{Locked: []string{}, Failed: []LockFailure{}}
	for _, p := range paths {
		_, err := client.ProxyLockFile(ctx, &daemonpb.ProxyLockFileRequest{Root: root, Request: &pb.LockFileRequest{Path: p, Branch: branch}})
		if err != nil {
			if isConnectionError(err) {
				s.dropIfDisconnected(err)
				out.Failed = append(out.Failed, LockFailure{Path: p, Message: cleanError(err).Error()})
				return out, nil
			}
			out.Failed = append(out.Failed, LockFailure{Path: p, Message: cleanError(err).Error()})
			continue
		}
		out.Locked = append(out.Locked, p)
	}
	return out, nil
}

// Unlock releases a lock. An empty branch addresses the mainline scope.
func (s *Service) Unlock(ctx context.Context, root, branch, path string) error {
	client, err := s.client(ctx, root)
	if err != nil {
		return err
	}
	_, err = client.ProxyUnlockFile(ctx, &daemonpb.ProxyUnlockFileRequest{Root: root, Request: &pb.UnlockFileRequest{Path: path, Branch: branch}})
	if err != nil {
		s.dropIfDisconnected(err)
		return cleanError(err)
	}
	return nil
}

// PickFile asks for a file inside the clone and returns its clone-relative
// path, or "" when cancelled.
func (s *Service) PickFile(_ context.Context, root string) (string, error) {
	if s.PickFileFunc == nil {
		return "", errors.New("file dialogs are not available")
	}
	picked, err := s.PickFileFunc(root)
	if err != nil || picked == "" {
		return "", err
	}
	rel, ok := insideRoot(root, picked)
	if !ok {
		return "", errors.New("choose a file inside this clone")
	}
	return rel, nil
}

// OpenFile opens a clone file with the program set for its extension, or the
// system's default application.
func (s *Service) OpenFile(ctx context.Context, root, path string) error {
	return s.OpenFileAt(ctx, root, path, 0)
}

// OpenFileAt opens a clone file at a line: the program set for its extension
// gets the line through {line} in its command; the system's default
// application only opens the file.
func (s *Service) OpenFileAt(_ context.Context, root, path string, line int) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	if _, ok := insideRoot(root, full); !ok {
		return errors.New("the file is outside this clone")
	}
	if tool := toolFor(s.prefs().Tools, full); tool != "" {
		return s.launch(tool, full, line)
	}
	if s.OpenFunc == nil {
		return errors.New("opening files is not available")
	}
	return s.OpenFunc(full)
}

func insideRoot(root, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".nipa" || strings.HasPrefix(rel, ".nipa/") {
		return "", false
	}
	return rel, true
}
