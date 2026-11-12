package usecase

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	minPasswordLength = 8
	maxPasswordBytes  = 72
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=user_mock_test.go -package=usecase
type userAdminRepository interface {
	userRepository
	List(ctx context.Context) ([]*domain.User, error)
	Create(ctx context.Context, user domain.User) (*domain.User, error)
	UpdateProfile(ctx context.Context, id snow.ID, name, photoUrl string, notifyEmail *bool) error
	UpdateEmailAndRevokeTokens(ctx context.Context, id snow.ID, email string) error
	UpdatePasswordAndRevokeTokens(ctx context.Context, id snow.ID, passwordHash string) error
	UpdateAdminFlags(ctx context.Context, id snow.ID, isAdmin, isSuperAdmin bool) error
	DeactivateAndRevokeTokens(ctx context.Context, id snow.ID) error
}

type User struct {
	snowNode snow.Node
	repo     userAdminRepository
	hasher   passwordHasher
	audit    AuditRecorder
	limiter  *ratelimit.Limiter
}

// WithLimiter throttles password changes per user.
func (u *User) WithLimiter(limiter *ratelimit.Limiter) *User {
	u.limiter = limiter
	return u
}

// WithAudit records account administration.
func (u *User) WithAudit(rec AuditRecorder) *User {
	u.audit = rec
	return u
}

func NewUser(snowNode snow.Node, repo userAdminRepository, hasher passwordHasher) *User {
	return &User{
		snowNode: snowNode,
		repo:     repo,
		hasher:   hasher,
	}
}

func (u *User) Get(ctx context.Context, id snow.ID) (*domain.User, error) {
	return u.repo.GetByID(ctx, id)
}

func (u *User) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return u.repo.GetByEmail(ctx, normalizeEmail(email))
}

func (u *User) List(ctx context.Context) ([]*domain.User, error) {
	if err := requireGlobalAdmin(ctx); err != nil {
		return nil, err
	}
	return u.repo.List(ctx)
}

func (u *User) Create(ctx context.Context, name, email, password string) (*domain.User, error) {
	if err := requireGlobalAdmin(ctx); err != nil {
		return nil, err
	}
	return u.CreateAccount(ctx, name, email, password)
}

// CreateAccount validates the identity, hashes the password and creates a
// verified account. It performs no permission check: the admin create path and
// the SaaS onboarding flows share it, the latter checking its own rules.
func (u *User) CreateAccount(ctx context.Context, name, email, password string) (*domain.User, error) {
	name, email, err := ValidateUserIdentity(name, email)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	if _, err := u.repo.GetByEmail(ctx, email); err == nil {
		return nil, domain.NewErrorConflict("email already in use")
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	hash, err := u.hasher.Hash(password)
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	var created *domain.User
	err = auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		var err error
		created, err = u.repo.Create(ctx, domain.User{
			ID:            u.snowNode.Generate(),
			Name:          name,
			Email:         email,
			Password:      hash,
			EmailVerified: true,
		})
		return err
	}, func() *domain.AuditEntry {
		entry := UserCreatedAudit(created, "admin")
		return &entry
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UserCreatedAudit describes a new account; source says which flow created it.
func UserCreatedAudit(user *domain.User, source string) domain.AuditEntry {
	return domain.AuditEntry{
		Action:     domain.AuditUserCreated,
		TargetType: domain.AuditTargetUser,
		TargetID:   user.ID.Base36(),
		Metadata: map[string]any{
			"name":   user.Name,
			"email":  user.Email,
			"source": source,
		},
	}
}

func (u *User) UpdateProfile(ctx context.Context, userID snow.ID, name, photoUrl string, notifyEmail *bool) (*domain.User, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID != userID {
		if err := u.requireCanManage(ctx, userID); err != nil {
			return nil, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("name is required")
	}
	if err := u.repo.UpdateProfile(ctx, userID, name, strings.TrimSpace(photoUrl), notifyEmail); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) UpdateEmail(ctx context.Context, userID snow.ID, email string) (*domain.User, error) {
	if err := u.requireCanManage(ctx, userID); err != nil {
		return nil, err
	}
	email = normalizeEmail(email)
	if !isEmail(email) {
		return nil, domain.NewErrorUser("invalid email")
	}
	if existing, err := u.repo.GetByEmail(ctx, email); err == nil && existing.ID != userID {
		return nil, domain.NewErrorConflict("email already in use")
	} else if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	oldEmail := ""
	if u.audit != nil {
		current, err := u.repo.GetByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		oldEmail = current.Email
	}
	if err := auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		return u.repo.UpdateEmailAndRevokeTokens(ctx, userID, email)
	}, func() *domain.AuditEntry {
		if oldEmail == email {
			return nil
		}
		return userAudit(domain.AuditUserEmailChanged, userID, map[string]any{"old_email": oldEmail, "new_email": email})
	}); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) ChangePassword(ctx context.Context, userID snow.ID, oldPassword, newPassword string) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || claim.UserID != userID {
		return domain.NewErrorNoPermission()
	}
	if err := allowLimit(ctx, u.limiter, ratelimit.ScopeAuthID, "user:"+userID.Base36()); err != nil {
		return err
	}
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.hasher.Compare(user.Password, oldPassword) {
		return domain.NewErrorUser("current password is incorrect")
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := u.hasher.Hash(newPassword)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	return auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		return u.repo.UpdatePasswordAndRevokeTokens(ctx, userID, hash)
	}, func() *domain.AuditEntry {
		return userAudit(domain.AuditUserPasswordChanged, userID, nil)
	})
}

func (u *User) ResetPassword(ctx context.Context, userID snow.ID, newPassword string) error {
	if err := u.requireCanManage(ctx, userID); err != nil {
		return err
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := u.hasher.Hash(newPassword)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	return auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		return u.repo.UpdatePasswordAndRevokeTokens(ctx, userID, hash)
	}, func() *domain.AuditEntry {
		return userAudit(domain.AuditUserPasswordResetByAdmin, userID, nil)
	})
}

func (u *User) SetAdminFlags(ctx context.Context, userID snow.ID, isAdmin, isSuperAdmin bool) (*domain.User, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID == userID && !isSuperAdmin {
		return nil, domain.NewErrorUser("cannot remove your own super admin flag")
	}
	var old *domain.User
	if u.audit != nil {
		var err error
		if old, err = u.repo.GetByID(ctx, userID); err != nil {
			return nil, err
		}
	}
	if err := auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		return u.repo.UpdateAdminFlags(ctx, userID, isAdmin, isSuperAdmin)
	}, func() *domain.AuditEntry {
		if old.IsAdmin == isAdmin && old.IsSuperAdmin == isSuperAdmin {
			return nil
		}
		return userAudit(domain.AuditUserAdminFlagsChanged, userID, map[string]any{
			"old": map[string]any{"is_admin": old.IsAdmin, "is_superadmin": old.IsSuperAdmin},
			"new": map[string]any{"is_admin": isAdmin, "is_superadmin": isSuperAdmin},
		})
	}); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) Deactivate(ctx context.Context, userID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.UserID == userID {
		return domain.NewErrorUser("cannot deactivate yourself")
	}
	if err := u.requireCanManage(ctx, userID); err != nil {
		return err
	}
	return auditedWrite(ctx, u.audit, func(ctx context.Context) error {
		return u.repo.DeactivateAndRevokeTokens(ctx, userID)
	}, func() *domain.AuditEntry {
		return userAudit(domain.AuditUserDeactivated, userID, nil)
	})
}

func userAudit(action string, userID snow.ID, metadata map[string]any) *domain.AuditEntry {
	return &domain.AuditEntry{
		Action:     action,
		TargetType: domain.AuditTargetUser,
		TargetID:   userID.Base36(),
		Metadata:   metadata,
	}
}

func (u *User) requireCanManage(ctx context.Context, targetID snow.ID) error {
	if err := requireGlobalAdmin(ctx); err != nil {
		return err
	}
	claim, _ := domain.ClaimFromContext(ctx)
	if claim.IsSuperAdmin || claim.UserID == targetID {
		return nil
	}
	target, err := u.repo.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if target.IsAdmin || target.IsSuperAdmin {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func requireGlobalAdmin(ctx context.Context) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if claim.IsSuperAdmin || claim.IsAdmin {
		return nil
	}
	return domain.NewErrorNoPermission()
}

// ValidateUserIdentity trims and checks a name/email pair, returning the
// normalized values.
func ValidateUserIdentity(name, email string) (string, string, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	if name == "" {
		return "", "", domain.NewErrorUser("name is required")
	}
	if !isEmail(email) {
		return "", "", domain.NewErrorUser("invalid email")
	}
	return name, email, nil
}

func isEmail(email string) bool {
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1 && !strings.ContainsAny(email, " \t")
}

// ValidatePassword enforces the password policy. The maximum is in bytes
// because bcrypt refuses longer input.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLength {
		return domain.NewErrorUser("password must be at least 8 characters")
	}
	if len(password) > maxPasswordBytes {
		return domain.NewErrorUser("password must be at most 72 bytes")
	}
	return nil
}
