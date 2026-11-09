package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=file_lock_mock_test.go -package=usecase
type fileLockRepository interface {
	Create(ctx context.Context, lock domain.FileLock) (*domain.FileLock, error)
	Get(ctx context.Context, projectID snow.ID, path string, branchID *snow.ID, now time.Time) (*domain.FileLock, error)
	ListProject(ctx context.Context, projectID snow.ID, now time.Time) ([]*domain.FileLock, error)
	Delete(ctx context.Context, id snow.ID) error
	DeleteExpired(ctx context.Context, projectID snow.ID, now time.Time) error
	SetExpiry(ctx context.Context, id, holder snow.ID, expiresAt time.Time) error
	DeleteByMergeRequest(ctx context.Context, projectID, mergeRequestID snow.ID) error
	DeleteByBranch(ctx context.Context, projectID, branchID snow.ID) error
}

// fileLockGate is the subset of the lock usecase used by the push, branch and
// merge-request flows.
type fileLockGate interface {
	EnsureLocks(ctx context.Context, projectID snow.ID, branch *domain.Branch, required, checked []string, holder snow.ID) error
	ReleaseLanded(ctx context.Context, projectID snow.ID, branch *domain.Branch, paths []string, holder snow.ID) error
	EnsureMergeRequestLocks(ctx context.Context, projectID snow.ID, mergeRequestID snow.ID, target *domain.Branch, paths []string, holder, author snow.ID) error
	ReleaseForMergeRequest(ctx context.Context, projectID snow.ID, mergeRequestID snow.ID) error
	ReleaseBranch(ctx context.Context, projectID, branchID snow.ID) error
}

// lockSerializer takes a project-wide exclusive lock for the current
// transaction, so lock grants stay serialized across server processes.
type lockSerializer interface {
	LockProject(ctx context.Context, projectID snow.ID) error
}

type FileLock struct {
	tx         transactor
	serializer lockSerializer
	repo       fileLockRepository
	branchRepo branchRepository
	perm       permissionUsecase
	snowNode   snow.Node
	audit      AuditRecorder
	now        func() time.Time

	// acquireMu serializes overlap check + insert. The unique indexes only
	// catch identical (project, branch, path) rows, so distinct-but-overlapping
	// prefixes need an application-level gate. nipad runs a single node over
	// SQLite, so a process mutex is sufficient.
	acquireMu sync.Mutex
}

func NewFileLock(repo fileLockRepository, branchRepo branchRepository, perm permissionUsecase, snowNode snow.Node) *FileLock {
	return &FileLock{
		repo:       repo,
		branchRepo: branchRepo,
		perm:       perm,
		snowNode:   snowNode,
		now:        time.Now,
	}
}

const (
	MinLockTTL = time.Minute
	MaxLockTTL = 7 * 24 * time.Hour
)

type acquireOptions struct {
	ttl    time.Duration
	hasTTL bool
}

// AcquireOption tunes a lock grant.
type AcquireOption func(*acquireOptions)

// LockTTL makes the lock expire ttl after it is granted; a re-lock by the same
// holder renews it.
func LockTTL(ttl time.Duration) AcquireOption {
	return func(o *acquireOptions) {
		o.ttl = ttl
		o.hasTTL = true
	}
}

// LockTTLSeconds is LockTTL for wire values, out-of-range input stays invalid.
func LockTTLSeconds(seconds int64) AcquireOption {
	if seconds > int64(MaxLockTTL/time.Second) {
		return LockTTL(MaxLockTTL + time.Second)
	}
	if seconds < int64(MinLockTTL/time.Second) {
		return LockTTL(MinLockTTL - time.Second)
	}
	return LockTTL(time.Duration(seconds) * time.Second)
}

// WithClock replaces the clock used for lock expiry.
func (f *FileLock) WithClock(now func() time.Time) *FileLock {
	f.now = now
	return f
}

// WithAudit records locks released by someone other than their holder.
func (f *FileLock) WithAudit(rec AuditRecorder) *FileLock {
	f.audit = rec
	return f
}

// WithSerializer makes Acquire run its overlap check and insert inside one
// transaction guarded by a database-level project lock, which keeps grants
// exclusive when several server processes overlap (rolling upgrades).
func (f *FileLock) WithSerializer(tx transactor, serializer lockSerializer) *FileLock {
	f.tx = tx
	f.serializer = serializer
	return f
}

// Acquire takes an exclusive lock on a binary path or path prefix. Locks on
// the default branch are project-global; other branches are lock-scoped.
func (f *FileLock) Acquire(ctx context.Context, projectID snow.ID, branchName, path string, opts ...AcquireOption) (*domain.FileLock, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	var options acquireOptions
	for _, opt := range opts {
		opt(&options)
	}
	if options.hasTTL && (options.ttl < MinLockTTL || options.ttl > MaxLockTTL) {
		return nil, domain.NewErrorUser(fmt.Sprintf("lock ttl must be between %s and %s", MinLockTTL, MaxLockTTL))
	}
	if !f.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	norm, err := normalizeLockPath(path)
	if err != nil {
		return nil, err
	}
	if !f.perm.HasPathAccess(ctx, projectID, norm, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	scope, err := f.resolveScope(ctx, projectID, branchName)
	if err != nil {
		return nil, err
	}

	f.acquireMu.Lock()
	defer f.acquireMu.Unlock()

	var granted *domain.FileLock
	err = f.serialized(ctx, projectID, func(ctx context.Context) error {
		now := f.now()
		if err := f.repo.DeleteExpired(ctx, projectID, now); err != nil {
			return err
		}
		var err error
		granted, err = f.grant(ctx, projectID, norm, *scope, claim.UserID, now, options)
		return err
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}

// serialized runs fn so that lock grants of one project cannot interleave
// across server processes: inside a transaction holding the project's advisory
// lock (joining the caller's transaction when there is one). Without a
// serializer only the in-process mutex applies.
func (f *FileLock) serialized(ctx context.Context, projectID snow.ID, fn func(ctx context.Context) error) error {
	if f.tx == nil || f.serializer == nil {
		return fn(ctx)
	}
	return f.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := f.serializer.LockProject(txCtx, projectID); err != nil {
			return err
		}
		return fn(txCtx)
	})
}

func (f *FileLock) grant(ctx context.Context, projectID snow.ID, norm string, scope lockScope, holder snow.ID, now time.Time, options acquireOptions) (*domain.FileLock, error) {
	locks, err := f.repo.ListProject(ctx, projectID, now)
	if err != nil {
		return nil, err
	}
	for _, lock := range locks {
		if !sameLockScope(lock.BranchID, scope.branchID) || !lockPathsOverlap(lock.Path, norm) {
			continue
		}
		// The same holder still needs the wider lock when their existing lock
		// only overlaps without covering the request (file held, dir wanted).
		if lock.HeldBy == holder {
			if domain.PrefixCovers(lock.Path, norm) {
				return f.renew(ctx, lock, norm, now, options)
			}
			continue
		}
		return nil, lockConflictError(lock, norm)
	}
	var expiresAt *time.Time
	if options.hasTTL {
		at := now.Add(options.ttl)
		expiresAt = &at
	}
	created, err := f.repo.Create(ctx, domain.FileLock{
		ID:        f.snowNode.Generate(),
		ProjectID: projectID,
		BranchID:  scope.branchID,
		Path:      norm,
		HeldBy:    holder,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		if existing, getErr := f.repo.Get(ctx, projectID, norm, scope.branchID, now); getErr == nil {
			if existing.HeldBy != holder {
				return nil, lockConflictError(existing, norm)
			}
			return f.renew(ctx, existing, norm, now, options)
		}
		return nil, err
	}
	created.Branch = scope.branchName
	return created, nil
}

// renew moves the expiry of the holder's covering lock when the request carries
// a TTL. Permanent directory locks and merge-request locks are left alone.
func (f *FileLock) renew(ctx context.Context, lock *domain.FileLock, norm string, now time.Time, options acquireOptions) (*domain.FileLock, error) {
	if !options.hasTTL || lock.MergeRequestID != nil || (lock.ExpiresAt == nil && lock.Path != norm) {
		return lock, nil
	}
	expiresAt := now.Add(options.ttl)
	if err := f.repo.SetExpiry(ctx, lock.ID, lock.HeldBy, expiresAt); err != nil {
		return nil, err
	}
	renewed := *lock
	renewed.ExpiresAt = &expiresAt
	return &renewed, nil
}

// Release removes a lock. The holder and project admins may release it.
func (f *FileLock) Release(ctx context.Context, projectID snow.ID, branchName, path string) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if !f.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return domain.NewErrorNoPermission()
	}
	norm, err := normalizeLockPath(path)
	if err != nil {
		return err
	}
	scope, err := f.resolveScope(ctx, projectID, branchName)
	if err != nil {
		return err
	}
	lock, err := f.repo.Get(ctx, projectID, norm, scope.branchID, f.now())
	if domain.IsErrorNotFound(err) {
		return domain.NewErrorNotFound(fmt.Sprintf("no lock on %q in %s", norm, scope.describe()))
	}
	if err != nil {
		return err
	}
	if lock.HeldBy == claim.UserID {
		return f.repo.Delete(ctx, lock.ID)
	}
	if !f.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return auditedWrite(ctx, f.audit, func(ctx context.Context) error {
		return f.repo.Delete(ctx, lock.ID)
	}, func() *domain.AuditEntry {
		metadata := map[string]any{
			"path":      lock.Path,
			"holder_id": lock.HeldBy,
		}
		if scope.branchID != nil {
			metadata["branch"] = scope.branchName
		}
		return projectAudit(domain.AuditLockForceReleased, projectID, domain.AuditTargetLock, lock.ID.Base36(), metadata)
	})
}

func (f *FileLock) List(ctx context.Context, projectID snow.ID) ([]*domain.FileLock, error) {
	if !f.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	locks, err := f.repo.ListProject(ctx, projectID, f.now())
	if err != nil {
		return nil, err
	}
	filter, err := f.perm.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	visible := make([]*domain.FileLock, 0, len(locks))
	for _, lock := range locks {
		if filter.Allow(lock.Path) {
			visible = append(visible, lock)
		}
	}
	return visible, nil
}

// EnsureLocks is the mandatory gate for pushes and fast-forwards. Required
// paths (tracked binaries being modified or removed) must be covered by a lock
// held by the caller. Checked paths (newly added binaries) are only blocked
// when someone else's lock covers them, so adding a new asset needs no lock
// unless a directory or pre-emptive lock guards it.
func (f *FileLock) EnsureLocks(ctx context.Context, projectID snow.ID, branch *domain.Branch, required, checked []string, holder snow.ID) error {
	if len(required) == 0 && len(checked) == 0 {
		return nil
	}
	locks, err := f.repo.ListProject(ctx, projectID, f.now())
	if err != nil {
		return err
	}
	scopeID := branchScopeID(branch)
	for _, path := range required {
		lock := findCoveringLock(locks, scopeID, path)
		if lock == nil {
			return domain.NewErrorConflict(fmt.Sprintf("binary file %q requires a lock; run `nipa lock %s` first", path, path))
		}
		if lock.HeldBy != holder {
			return lockConflictError(lock, path)
		}
	}
	for _, path := range checked {
		lock := findCoveringLock(locks, scopeID, path)
		if lock != nil && lock.HeldBy != holder {
			return lockConflictError(lock, path)
		}
	}
	return nil
}

// ReleaseLanded drops the caller's own non-request locks for paths that just
// landed. Directory locks are kept: they span a whole editing pass.
func (f *FileLock) ReleaseLanded(ctx context.Context, projectID snow.ID, branch *domain.Branch, paths []string, holder snow.ID) error {
	if len(paths) == 0 {
		return nil
	}
	locks, err := f.repo.ListProject(ctx, projectID, f.now())
	if err != nil {
		return err
	}
	scopeID := branchScopeID(branch)
	for _, lock := range locks {
		if !sameLockScope(lock.BranchID, scopeID) || lock.HeldBy != holder || lock.MergeRequestID != nil {
			continue
		}
		for _, path := range paths {
			if lock.Path == path {
				if err := f.repo.Delete(ctx, lock.ID); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// EnsureMergeRequestLocks checks the binary paths of a merge request: paths
// covered by this request's own locks, the author's locks or the caller's locks
// pass, free paths gain request-linked locks and paths locked by anyone else
// abort the operation. author is the request owner so an admin merging on the
// author's behalf accepts the locks acquired when the request was opened.
func (f *FileLock) EnsureMergeRequestLocks(ctx context.Context, projectID snow.ID, mergeRequestID snow.ID, target *domain.Branch, paths []string, holder, author snow.ID) error {
	if len(paths) == 0 {
		return nil
	}

	f.acquireMu.Lock()
	defer f.acquireMu.Unlock()

	return f.serialized(ctx, projectID, func(ctx context.Context) error {
		return f.ensureMergeRequestLocks(ctx, projectID, mergeRequestID, target, paths, holder, author)
	})
}

func (f *FileLock) ensureMergeRequestLocks(ctx context.Context, projectID snow.ID, mergeRequestID snow.ID, target *domain.Branch, paths []string, holder, author snow.ID) error {
	now := f.now()
	if err := f.repo.DeleteExpired(ctx, projectID, now); err != nil {
		return err
	}
	locks, err := f.repo.ListProject(ctx, projectID, now)
	if err != nil {
		return err
	}
	scopeID := branchScopeID(target)
	for _, path := range paths {
		lock := findCoveringLock(locks, scopeID, path)
		if lock != nil {
			if !mergeRequestLockAccepted(lock, mergeRequestID, holder, author) {
				return lockConflictError(lock, path)
			}
			continue
		}
		mrID := mergeRequestID
		created, err := f.repo.Create(ctx, domain.FileLock{
			ID:             f.snowNode.Generate(),
			ProjectID:      projectID,
			BranchID:       scopeID,
			Path:           path,
			HeldBy:         holder,
			MergeRequestID: &mrID,
		})
		if err != nil {
			if existing, getErr := f.repo.Get(ctx, projectID, path, scopeID, now); getErr == nil {
				if !mergeRequestLockAccepted(existing, mergeRequestID, holder, author) {
					return lockConflictError(existing, path)
				}
				continue
			}
			return err
		}
		locks = append(locks, created)
	}
	return nil
}

func mergeRequestLockAccepted(lock *domain.FileLock, mergeRequestID, holder, author snow.ID) bool {
	if lock.MergeRequestID != nil && *lock.MergeRequestID == mergeRequestID {
		return true
	}
	return lock.HeldBy == holder || lock.HeldBy == author
}

func (f *FileLock) ReleaseForMergeRequest(ctx context.Context, projectID snow.ID, mergeRequestID snow.ID) error {
	return f.repo.DeleteByMergeRequest(ctx, projectID, mergeRequestID)
}

func (f *FileLock) ReleaseBranch(ctx context.Context, projectID, branchID snow.ID) error {
	return f.repo.DeleteByBranch(ctx, projectID, branchID)
}

type lockScope struct {
	branchID   *snow.ID
	branchName string
}

func (s *lockScope) describe() string {
	if s.branchID == nil {
		return "mainline"
	}
	return fmt.Sprintf("branch %q", s.branchName)
}

func (f *FileLock) resolveScope(ctx context.Context, projectID snow.ID, branchName string) (*lockScope, error) {
	if strings.TrimSpace(branchName) == "" {
		def, err := f.branchRepo.GetDefaultBranch(ctx, projectID)
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorNotFound("default branch not found")
		}
		if err != nil {
			return nil, err
		}
		return &lockScope{branchName: def.Name}, nil
	}
	branch, err := f.branchRepo.GetBranchByName(ctx, projectID, branchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.IsDefault {
		return &lockScope{branchName: branch.Name}, nil
	}
	return &lockScope{branchID: &branch.ID, branchName: branch.Name}, nil
}

func normalizeLockPath(path string) (string, error) {
	norm, err := domain.NormalizePathPrefix(path)
	if err != nil {
		return "", domain.NewErrorUser(err.Error())
	}
	if norm == "" {
		return "", domain.NewErrorUser("path is required")
	}
	return norm, nil
}

func branchScopeID(branch *domain.Branch) *snow.ID {
	if branch == nil || branch.IsDefault {
		return nil
	}
	id := branch.ID
	return &id
}

func sameLockScope(a, b *snow.ID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func lockPathsOverlap(a, b string) bool {
	return domain.PrefixCovers(a, b) || domain.PrefixCovers(b, a)
}

func findCoveringLock(locks []*domain.FileLock, scopeID *snow.ID, path string) *domain.FileLock {
	for _, lock := range locks {
		if sameLockScope(lock.BranchID, scopeID) && domain.PrefixCovers(lock.Path, path) {
			return lock
		}
	}
	return nil
}

func lockConflictError(lock *domain.FileLock, path string) error {
	holder := lock.HeldByName
	if holder == "" {
		holder = lock.HeldBy.Base36()
	}
	scope := "mainline"
	if lock.BranchID != nil {
		scope = "branch"
		if lock.Branch != "" {
			scope = fmt.Sprintf("branch %q", lock.Branch)
		}
	}
	via := ""
	if lock.MergeRequestNumber != nil {
		via = fmt.Sprintf(" via merge request #%d", *lock.MergeRequestNumber)
	}
	expiry := ""
	if lock.ExpiresAt != nil {
		expiry = fmt.Sprintf(", expires %s", lock.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return domain.NewErrorConflict(fmt.Sprintf(
		"%q is locked by %s on %s%s since %s%s",
		path, holder, scope, via, lock.AcquiredAt.UTC().Format(time.RFC3339), expiry,
	))
}
