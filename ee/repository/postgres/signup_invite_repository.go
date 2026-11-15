package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// SignupInviteRepository stores the emails a super admin allowed to sign up
// while open registration is closed.
type SignupInviteRepository struct {
	queries *sqlcPostgres.Queries
}

func NewSignupInviteRepository(db *sql.DB) *SignupInviteRepository {
	return &SignupInviteRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

func normalizeInviteEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Upsert allows the email, replacing the expiry of an existing invite.
func (r *SignupInviteRepository) Upsert(ctx context.Context, invite domain.SignupInvite) (*domain.SignupInvite, error) {
	var expires sql.NullTime
	if invite.ExpiresAt != nil {
		expires = sql.NullTime{Time: *invite.ExpiresAt, Valid: true}
	}
	row, err := r.queries.SignupInviteUpsert(ctx, sqlcPostgres.SignupInviteUpsertParams{
		ID:        invite.ID.Int64(),
		Email:     normalizeInviteEmail(invite.Email),
		InvitedBy: invite.InvitedBy.Int64(),
		ExpiresAt: expires,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toSignupInvite(row), nil
}

func (r *SignupInviteRepository) List(ctx context.Context) ([]*domain.SignupInvite, error) {
	rows, err := r.queries.SignupInviteList(ctx)
	if err != nil {
		return nil, handleError(err)
	}
	invites := make([]*domain.SignupInvite, 0, len(rows))
	for _, row := range rows {
		invites = append(invites, toSignupInvite(row))
	}
	return invites, nil
}

// Delete revokes an invite; it reports whether a row existed.
func (r *SignupInviteRepository) Delete(ctx context.Context, id snow.ID) (bool, error) {
	n, err := r.queries.SignupInviteDelete(ctx, id.Int64())
	if err != nil {
		return false, handleError(err)
	}
	return n > 0, nil
}

// Allowed reports whether the email holds an unexpired invite.
func (r *SignupInviteRepository) Allowed(ctx context.Context, email string, now time.Time) (bool, error) {
	allowed, err := r.queries.SignupInviteAllowed(ctx, sqlcPostgres.SignupInviteAllowedParams{
		Email: normalizeInviteEmail(email),
		Now:   now,
	})
	return allowed, handleError(err)
}

func toSignupInvite(row sqlcPostgres.SignupInvite) *domain.SignupInvite {
	invite := &domain.SignupInvite{
		ID:        snow.ID(row.ID),
		Email:     row.Email,
		InvitedBy: snow.ID(row.InvitedBy),
		CreatedAt: row.CreatedAt,
	}
	if row.ExpiresAt.Valid {
		invite.ExpiresAt = &row.ExpiresAt.Time
	}
	return invite
}
