package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestServiceTokenRepositorySQLite_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewServiceTokenRepository(db)
	require.NoError(t, repo.LockProject(ctx, 1))
	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")
	userID := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	created, err := repo.Create(ctx, domain.ServiceToken{
		ID: 10, ProjectID: projectID, Name: "bugbase", TokenHash: "h1", Hint: "nipa_st_abcd",
		Scopes: []string{"read", "checks:write"}, CreatedBy: userID, ExpiresAt: &expires,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"read", "checks:write"}, created.Scopes)
	require.True(t, created.ExpiresAt.Equal(expires))
	require.Nil(t, created.RevokedAt)
	require.False(t, created.CreatedAt.IsZero())

	_, err = repo.Create(ctx, domain.ServiceToken{ID: 11, ProjectID: projectID, Name: "dup", TokenHash: "h1", Scopes: []string{"read"}, CreatedBy: userID})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	_, err = repo.Create(ctx, domain.ServiceToken{ID: 12, ProjectID: projectID, Name: "ci", TokenHash: "h2", CreatedBy: userID})
	require.NoError(t, err)

	got, err := repo.Get(ctx, projectID, 10)
	require.NoError(t, err)
	require.Equal(t, "bugbase", got.Name)
	_, err = repo.Get(ctx, otherProjectID, 10)
	requireRecordNotFound(t, err)

	byHash, err := repo.GetByHash(ctx, "h2")
	require.NoError(t, err)
	require.Equal(t, snow.ID(12), byHash.ID)
	require.Empty(t, byHash.Scopes)
	_, err = repo.GetByHash(ctx, "missing")
	requireRecordNotFound(t, err)

	list, err := repo.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, snow.ID(12), list[0].ID)
	empty, err := repo.ListByProject(ctx, otherProjectID)
	require.NoError(t, err)
	require.Empty(t, empty)

	used := time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)
	require.NoError(t, repo.TouchLastUsed(ctx, 10, used))
	got, err = repo.Get(ctx, projectID, 10)
	require.NoError(t, err)
	require.True(t, got.LastUsedAt.Equal(used))

	revoked, err := repo.Revoke(ctx, projectID, 10, used)
	require.NoError(t, err)
	require.True(t, revoked.RevokedAt.Equal(used))
	_, err = repo.Revoke(ctx, projectID, 10, used)
	requireRecordNotFound(t, err)
	_, err = repo.Revoke(ctx, otherProjectID, 12, used)
	requireRecordNotFound(t, err)
}
