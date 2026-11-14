package usecase

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	// LockRequestCooldown is how long a repeated request stays silent for the
	// holder: the message is updated but nobody is notified again.
	LockRequestCooldown = 24 * time.Hour
	// MaxLockRequestMessage caps the optional message, in characters.
	MaxLockRequestMessage = 500
)

type fileLockHooks interface {
	EmitLockReleaseRequested(ctx context.Context, projectID snow.ID, lock *domain.FileLock, request *domain.FileLockRequest, actor snow.ID) error
}

// WithHooks enables the release-request webhook and email.
func (f *FileLock) WithHooks(hooks fileLockHooks) *FileLock {
	f.hooks = hooks
	return f
}

// RequestRelease asks the holder of a lock to release it. Anyone who can read
// the path may ask, except the holder; asking again updates the message and
// only notifies the holder once per LockRequestCooldown.
func (f *FileLock) RequestRelease(ctx context.Context, projectID, lockID snow.ID, message string) (*domain.FileLockRequest, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !f.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > MaxLockRequestMessage {
		return nil, domain.NewErrorUser("message is too long")
	}
	now := f.now()
	locks, err := f.repo.ListProject(ctx, projectID, now)
	if err != nil {
		return nil, err
	}
	var lock *domain.FileLock
	for _, candidate := range locks {
		if candidate.ID == lockID {
			lock = candidate
			break
		}
	}
	if lock == nil || !f.perm.HasPathAccess(ctx, projectID, lock.Path, domain.PermissionRead) {
		return nil, domain.NewErrorNotFound("lock not found")
	}
	if lock.HeldBy == claim.UserID {
		return nil, domain.NewErrorUser("you hold this lock")
	}

	notify := true
	request, err := f.repo.GetRequest(ctx, lock.ID, claim.UserID)
	switch {
	case domain.IsErrorNotFound(err):
		request, err = f.repo.CreateRequest(ctx, domain.FileLockRequest{
			ID:          f.snowNode.Generate(),
			LockID:      lock.ID,
			RequesterID: claim.UserID,
			Message:     message,
			CreatedAt:   now,
		})
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		createdAt := request.CreatedAt
		notify = now.Sub(createdAt) >= LockRequestCooldown
		if notify {
			createdAt = now
		}
		if err := f.repo.UpdateRequest(ctx, request.ID, message, createdAt); err != nil {
			return nil, err
		}
		request.Message = message
		request.CreatedAt = createdAt
	}
	if notify {
		f.emitReleaseRequested(ctx, projectID, lock, request, claim.UserID)
	}
	return request, nil
}

func (f *FileLock) emitReleaseRequested(ctx context.Context, projectID snow.ID, lock *domain.FileLock, request *domain.FileLockRequest, actor snow.ID) {
	if f.hooks == nil {
		return
	}
	if err := f.hooks.EmitLockReleaseRequested(ctx, projectID, lock, request, actor); err != nil {
		slog.Warn("emitting lock release request failed", "project", projectID, "lock", lock.ID, "error", err)
	}
}
