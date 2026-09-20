package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func groupMemberIDs(t *testing.T, db *sql.DB, groupID int64) []int64 {
	t.Helper()

	rows, err := db.QueryContext(context.Background(),
		`SELECT user_id FROM group_members WHERE group_id = ? ORDER BY user_id`, groupID)
	require.NoError(t, err)
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func TestGroupRepositorySQLite_CreateAndGetByID(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewGroupRepository(db)

	created, err := repo.Create(ctx, domain.Group{
		ID: snow.ID(7001), OrgID: 1, Name: "artists", Description: "2d team",
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(7001), created.ID)
	require.Equal(t, snow.ID(1), created.OrgID)
	require.Equal(t, "artists", created.Name)
	require.Equal(t, "2d team", created.Description)
	require.NotZero(t, created.CreatedAt)
	require.False(t, created.Deleted)
	require.Nil(t, created.DeletedAt)

	got, err := repo.GetByID(ctx, snow.ID(7001))
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Name, got.Name)
	require.Equal(t, created.Description, got.Description)
	require.Equal(t, created.CreatedAt, got.CreatedAt)
}

func TestGroupRepositorySQLite_Create_EmptyDescription(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)

	created, err := NewGroupRepository(db).Create(ctx, domain.Group{
		ID: snow.ID(7002), OrgID: 1, Name: "engineers",
	})
	require.NoError(t, err)
	require.Empty(t, created.Description)
}

func TestGroupRepositorySQLite_Create_DuplicateName(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewGroupRepository(db)

	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7003), OrgID: 1, Name: "artists"})
	require.NoError(t, err)

	_, err = repo.Create(ctx, domain.Group{ID: snow.ID(7004), OrgID: 1, Name: "artists"})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestGroupRepositorySQLite_GetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)

	_, err := NewGroupRepository(db).GetByID(ctx, snow.ID(9999))
	requireRecordNotFound(t, err)
}

func TestGroupRepositorySQLite_ListByOrg(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewGroupRepository(db)

	_, err := db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name) VALUES (2, 'other', 'Other')`)
	require.NoError(t, err)

	for _, group := range []domain.Group{
		{ID: snow.ID(7010), OrgID: 1, Name: "zeta"},
		{ID: snow.ID(7011), OrgID: 1, Name: "alpha"},
		{ID: snow.ID(7012), OrgID: 2, Name: "beta"},
		{ID: snow.ID(7013), OrgID: 1, Name: "deleted"},
	} {
		_, err := repo.Create(ctx, group)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `UPDATE groups SET deleted = 1, deleted_at = CURRENT_TIMESTAMP WHERE id = 7013`)
	require.NoError(t, err)

	groups, err := repo.ListByOrg(ctx, snow.ID(1))
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, "alpha", groups[0].Name)
	require.Equal(t, "zeta", groups[1].Name)

	groups, err = repo.ListByOrg(ctx, snow.ID(2))
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "beta", groups[0].Name)

	groups, err = repo.ListByOrg(ctx, snow.ID(99))
	require.NoError(t, err)
	require.Empty(t, groups)
}

func TestGroupRepositorySQLite_AddAndRemoveMember(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewGroupRepository(db)

	userID := seedPBACUser(t, db, 42)
	otherUserID := seedPBACUser(t, db, 43)
	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7020), OrgID: 1, Name: "artists"})
	require.NoError(t, err)

	require.NoError(t, repo.AddMember(ctx, snow.ID(7020), userID))
	require.NoError(t, repo.AddMember(ctx, snow.ID(7020), userID))
	require.Equal(t, []int64{42}, groupMemberIDs(t, db, 7020))

	require.NoError(t, repo.AddMember(ctx, snow.ID(7020), otherUserID))
	require.Equal(t, []int64{42, 43}, groupMemberIDs(t, db, 7020))

	require.NoError(t, repo.RemoveMember(ctx, snow.ID(7020), userID))
	require.NoError(t, repo.RemoveMember(ctx, snow.ID(7020), userID))
	require.Equal(t, []int64{43}, groupMemberIDs(t, db, 7020))
}

func TestGroupRepositorySQLite_QueryErrors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewGroupRepository(db)

	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7030), OrgID: 1, Name: "artists"})
	require.NoError(t, err)
	userID := seedPBACUser(t, db, 44)

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	_, err = repo.GetByID(canceled, snow.ID(7030))
	require.Error(t, err)

	_, err = repo.ListByOrg(canceled, snow.ID(1))
	require.Error(t, err)

	require.Error(t, repo.AddMember(canceled, snow.ID(7030), userID))
	require.Error(t, repo.RemoveMember(canceled, snow.ID(7030), userID))
}
