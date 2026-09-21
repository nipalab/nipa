package usecase

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const minPasswordLength = 8

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=user_mock_test.go -package=usecase
type userAdminRepository interface {
	userRepository
	List(ctx context.Context) ([]*domain.User, error)
	Create(ctx context.Context, user domain.User) (*domain.User, error)
	UpdateProfile(ctx context.Context, id snow.ID, name, photoUrl string) error
	UpdateEmail(ctx context.Context, id snow.ID, email string) error
	UpdatePassword(ctx context.Context, id snow.ID, passwordHash string) error
	UpdateAdminFlags(ctx context.Context, id snow.ID, isAdmin, isSuperAdmin bool) error
	Deactivate(ctx context.Context, id snow.ID) error
}

type User struct {
	snowNode snow.Node
	repo     userAdminRepository
	hasher   passwordHasher
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
	return u.repo.GetByEmail(ctx, strings.TrimSpace(email))
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
	name, email, err := validateUserIdentity(name, email)
	if err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
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
	return u.repo.Create(ctx, domain.User{
		ID:       u.snowNode.Generate(),
		Name:     name,
		Email:    email,
		Password: hash,
	})
}

func (u *User) UpdateProfile(ctx context.Context, userID snow.ID, name, photoUrl string) (*domain.User, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID != userID && !claim.IsAdmin && !claim.IsSuperAdmin {
		return nil, domain.NewErrorNoPermission()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("name is required")
	}
	if err := u.repo.UpdateProfile(ctx, userID, name, strings.TrimSpace(photoUrl)); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) UpdateEmail(ctx context.Context, userID snow.ID, email string) (*domain.User, error) {
	if err := requireGlobalAdmin(ctx); err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	if !isEmail(email) {
		return nil, domain.NewErrorUser("invalid email")
	}
	if existing, err := u.repo.GetByEmail(ctx, email); err == nil && existing.ID != userID {
		return nil, domain.NewErrorConflict("email already in use")
	} else if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if err := u.repo.UpdateEmail(ctx, userID, email); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) ChangePassword(ctx context.Context, userID snow.ID, oldPassword, newPassword string) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || claim.UserID != userID {
		return domain.NewErrorNoPermission()
	}
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.hasher.Compare(user.Password, oldPassword) {
		return domain.NewErrorUser("current password is incorrect")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := u.hasher.Hash(newPassword)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	return u.repo.UpdatePassword(ctx, userID, hash)
}

func (u *User) ResetPassword(ctx context.Context, userID snow.ID, newPassword string) error {
	if err := requireGlobalAdmin(ctx); err != nil {
		return err
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := u.hasher.Hash(newPassword)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	return u.repo.UpdatePassword(ctx, userID, hash)
}

func (u *User) SetAdminFlags(ctx context.Context, userID snow.ID, isAdmin, isSuperAdmin bool) (*domain.User, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID == userID && !isSuperAdmin {
		return nil, domain.NewErrorUser("cannot remove your own super admin flag")
	}
	if err := u.repo.UpdateAdminFlags(ctx, userID, isAdmin, isSuperAdmin); err != nil {
		return nil, err
	}
	return u.repo.GetByID(ctx, userID)
}

func (u *User) Deactivate(ctx context.Context, userID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	if err := requireGlobalAdmin(ctx); err != nil {
		return err
	}
	if claim.UserID == userID {
		return domain.NewErrorUser("cannot deactivate yourself")
	}
	return u.repo.Deactivate(ctx, userID)
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

func validateUserIdentity(name, email string) (string, string, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
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

func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return domain.NewErrorUser("password must be at least 8 characters")
	}
	return nil
}
