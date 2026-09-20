package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type User struct {
	queries *sqlcSqlite.Queries
}

func NewUserRepository(db *sql.DB) *User {
	return &User{queries: sqlcSqlite.New(db)}
}

func (a *User) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	user, err := a.queries.UserGetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewErrorRecordNotFound()
		}
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainUser(user), nil
}

func (a *User) GetByID(ctx context.Context, id snow.ID) (*domain.User, error) {
	user, err := a.queries.UserGetById(ctx, id.Int64())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.NewErrorRecordNotFound()
		}
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return toDomainUser(user), nil
}

func (a *User) Create(ctx context.Context, user domain.User) (*domain.User, error) {
	id, err := a.queries.UserCreate(ctx, sqlcSqlite.UserCreateParams{
		ID:       user.ID.Int64(),
		Name:     user.Name,
		Email:    user.Email,
		Password: user.Password,
		PhotoUrl: sql.NullString{String: user.PhotoUrl, Valid: user.PhotoUrl != ""},
		IsAdmin:  user.IsAdmin,
	})
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	return a.GetByID(ctx, snow.ID(id))
}

func (a *User) List(ctx context.Context) ([]*domain.User, error) {
	rows, err := a.queries.UserList(ctx)
	if err != nil {
		return nil, domain.NewErrorDatabase(err.Error())
	}
	users := make([]*domain.User, 0, len(rows))
	for _, row := range rows {
		users = append(users, toDomainUser(row))
	}
	return users, nil
}

func (a *User) UpdateProfile(ctx context.Context, id snow.ID, name, photoUrl string) error {
	err := a.queries.UserUpdateProfile(ctx, sqlcSqlite.UserUpdateProfileParams{
		Name:     name,
		PhotoUrl: sql.NullString{String: photoUrl, Valid: photoUrl != ""},
		ID:       id.Int64(),
	})
	return handleError(err)
}

func (a *User) UpdateEmail(ctx context.Context, id snow.ID, email string) error {
	err := a.queries.UserUpdateEmail(ctx, sqlcSqlite.UserUpdateEmailParams{
		Email: email,
		ID:    id.Int64(),
	})
	return handleError(err)
}

func (a *User) UpdatePassword(ctx context.Context, id snow.ID, passwordHash string) error {
	err := a.queries.UserUpdatePassword(ctx, sqlcSqlite.UserUpdatePasswordParams{
		Password: passwordHash,
		ID:       id.Int64(),
	})
	return handleError(err)
}

func (a *User) UpdateAdminFlags(ctx context.Context, id snow.ID, isAdmin, isSuperAdmin bool) error {
	err := a.queries.UserUpdateAdminFlags(ctx, sqlcSqlite.UserUpdateAdminFlagsParams{
		IsAdmin:      isAdmin,
		IsSuperAdmin: isSuperAdmin,
		ID:           id.Int64(),
	})
	return handleError(err)
}

func (a *User) Deactivate(ctx context.Context, id snow.ID) error {
	err := a.queries.UserDeleteByID(ctx, id.Int64())
	return handleError(err)
}

func toDomainUser(row sqlcSqlite.User) *domain.User {
	user := domain.User{
		ID:           snow.ID(row.ID),
		Name:         row.Name,
		Email:        row.Email,
		Password:     row.Password,
		PhotoUrl:     row.PhotoUrl.String,
		IsSuperAdmin: row.IsSuperAdmin,
		IsAdmin:      row.IsAdmin,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Deleted:      row.Deleted,
		DeletedAt:    nullTimePtr(row.DeletedAt),
	}
	return &user
}
