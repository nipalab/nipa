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

	request, notify, err := f.saveRequest(ctx, lock.ID, claim.UserID, message, now)
	if err != nil {
		return nil, err
	}
	if notify {
		f.emitReleaseRequested(ctx, projectID, lock, request, claim.UserID)
	}
	return request, nil
}

// saveRequest creates the request or updates the existing one; notify says
// whether the holder is due a notification. A concurrent first ask that lost
// the insert race falls back to updating the winner's row.
func (f *FileLock) saveRequest(ctx context.Context, lockID, requester snow.ID, message string, now time.Time) (*domain.FileLockRequest, bool, error) {
	request, err := f.repo.GetRequest(ctx, lockID, requester)
	if domain.IsErrorNotFound(err) {
		created, createErr := f.repo.CreateRequest(ctx, domain.FileLockRequest{
			ID:          f.snowNode.Generate(),
			LockID:      lockID,
			RequesterID: requester,
			Message:     message,
			CreatedAt:   now,
		})
		if createErr == nil {
			return created, true, nil
		}
		if !domain.IsErrorConflict(createErr) {
			return nil, false, createErr
		}
		request, err = f.repo.GetRequest(ctx, lockID, requester)
	}
	if err != nil {
		return nil, false, err
	}
	createdAt := request.CreatedAt
	notify := now.Sub(createdAt) >= LockRequestCooldown
	if notify {
		createdAt = now
	}
	if err := f.repo.UpdateRequest(ctx, request.ID, message, createdAt); err != nil {
		return nil, false, err
	}
	request.Message = message
	request.CreatedAt = createdAt
	return request, notify, nil
}

// emitReleaseRequested notifies the holder. When that fails, the request's
// timestamp is reset so a repeat ask is not held back by the cooldown.
func (f *FileLock) emitReleaseRequested(ctx context.Context, projectID snow.ID, lock *domain.FileLock, request *domain.FileLockRequest, actor snow.ID) {
	if f.hooks == nil {
		return
	}
	err := f.hooks.EmitLockReleaseRequested(ctx, projectID, lock, request, actor)
	if err == nil {
		return
	}
	slog.Warn("emitting lock release request failed", "project", projectID, "lock", lock.ID, "error", err)
	if resetErr := f.repo.UpdateRequest(ctx, request.ID, request.Message, time.Unix(0, 0).UTC()); resetErr != nil {
		slog.Warn("resetting lock release request cooldown failed", "project", projectID, "lock", lock.ID, "error", resetErr)
	}
}
