package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type GroupRepositorySuite struct {
	baseSuite
}

func TestGroupRepositorySuite(t *testing.T) {
	suite.Run(t, new(GroupRepositorySuite))
}

func groupMemberIDs(t *testing.T, db *sql.DB, groupID int64) []int64 {
	t.Helper()

	rows, err := db.QueryContext(context.Background(),
		`SELECT user_id FROM group_members WHERE group_id = $1 ORDER BY user_id`, groupID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func (s *GroupRepositorySuite) TestCreateAndGetByID() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	created, err := repo.Create(ctx, domain.Group{
		ID: snow.ID(7001), OrgID: 1, Name: "artists", Description: "2d team",
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(7001), created.ID)
	s.Equal(snow.ID(1), created.OrgID)
	s.Equal("artists", created.Name)
	s.Equal("2d team", created.Description)
	s.NotZero(created.CreatedAt)
	s.False(created.Deleted)
	s.Nil(created.DeletedAt)

	got, err := repo.GetByID(ctx, snow.ID(7001))
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	s.Equal(created.Name, got.Name)
	s.Equal(created.Description, got.Description)
	s.Equal(created.CreatedAt, got.CreatedAt)
}

func (s *GroupRepositorySuite) TestCreate_EmptyDescription() {
	ctx := context.Background()

	created, err := NewGroupRepository(s.db).Create(ctx, domain.Group{
		ID: snow.ID(7002), OrgID: 1, Name: "engineers",
	})
	s.Require().NoError(err)
	s.Empty(created.Description)
}

func (s *GroupRepositorySuite) TestCreate_DuplicateName() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7003), OrgID: 1, Name: "artists"})
	s.Require().NoError(err)

	_, err = repo.Create(ctx, domain.Group{ID: snow.ID(7004), OrgID: 1, Name: "artists"})
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)
}

func (s *GroupRepositorySuite) TestGetByID_NotFound() {
	ctx := context.Background()

	_, err := NewGroupRepository(s.db).GetByID(ctx, snow.ID(9999))
	requireRecordNotFound(s.T(), err)
}

func (s *GroupRepositorySuite) TestListByOrg() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	_, err := s.db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES (2, 'other', 'Other', 1)`)
	s.Require().NoError(err)

	for _, group := range []domain.Group{
		{ID: snow.ID(7010), OrgID: 1, Name: "zeta"},
		{ID: snow.ID(7011), OrgID: 1, Name: "alpha"},
		{ID: snow.ID(7012), OrgID: 2, Name: "beta"},
		{ID: snow.ID(7013), OrgID: 1, Name: "deleted"},
	} {
		_, err := repo.Create(ctx, group)
		s.Require().NoError(err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE groups SET deleted = TRUE, deleted_at = CURRENT_TIMESTAMP WHERE id = 7013`)
	s.Require().NoError(err)

	groups, err := repo.ListByOrg(ctx, snow.ID(1))
	s.Require().NoError(err)
	s.Len(groups, 2)
	s.Equal("alpha", groups[0].Name)
	s.Equal("zeta", groups[1].Name)

	groups, err = repo.ListByOrg(ctx, snow.ID(2))
	s.Require().NoError(err)
	s.Len(groups, 1)
	s.Equal("beta", groups[0].Name)

	groups, err = repo.ListByOrg(ctx, snow.ID(99))
	s.Require().NoError(err)
	s.Empty(groups)
}

func (s *GroupRepositorySuite) TestAddAndRemoveMember() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	userID := seedPBACUser(s.T(), s.db, 42)
	otherUserID := seedPBACUser(s.T(), s.db, 43)
	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7020), OrgID: 1, Name: "artists"})
	s.Require().NoError(err)

	s.Require().NoError(repo.AddMember(ctx, snow.ID(7020), userID))
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7020), userID))
	s.Equal([]int64{42}, groupMemberIDs(s.T(), s.db, 7020))

	s.Require().NoError(repo.AddMember(ctx, snow.ID(7020), otherUserID))
	s.Equal([]int64{42, 43}, groupMemberIDs(s.T(), s.db, 7020))

	s.Require().NoError(repo.RemoveMember(ctx, snow.ID(7020), userID))
	s.Require().NoError(repo.RemoveMember(ctx, snow.ID(7020), userID))
	s.Equal([]int64{43}, groupMemberIDs(s.T(), s.db, 7020))
}

func (s *GroupRepositorySuite) TestQueryErrors() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7030), OrgID: 1, Name: "artists"})
	s.Require().NoError(err)
	userID := seedPBACUser(s.T(), s.db, 44)

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	_, err = repo.GetByID(canceled, snow.ID(7030))
	s.Require().Error(err)

	_, err = repo.ListByOrg(canceled, snow.ID(1))
	s.Require().Error(err)

	s.Require().Error(repo.AddMember(canceled, snow.ID(7030), userID))
	s.Require().Error(repo.RemoveMember(canceled, snow.ID(7030), userID))
}

func (s *GroupRepositorySuite) TestListMembers() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	userA := seedPBACUser(s.T(), s.db, 42)
	userB := seedPBACUser(s.T(), s.db, 43)
	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7030), OrgID: 1, Name: "artists"})
	s.Require().NoError(err)
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7030), userA))
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7030), userB))

	members, err := repo.ListMembers(ctx, snow.ID(7030))
	s.Require().NoError(err)
	s.Equal([]domain.GroupMember{
		{UserID: userA, Name: "user42", Email: "user42@example.com"},
		{UserID: userB, Name: "user43", Email: "user43@example.com"},
	}, members)
}

func (s *GroupRepositorySuite) TestMemberCounts() {
	ctx := context.Background()
	repo := NewGroupRepository(s.db)

	userA := seedPBACUser(s.T(), s.db, 52)
	_, err := repo.Create(ctx, domain.Group{ID: snow.ID(7040), OrgID: 1, Name: "artists"})
	s.Require().NoError(err)
	_, err = repo.Create(ctx, domain.Group{ID: snow.ID(7041), OrgID: 1, Name: "empty"})
	s.Require().NoError(err)
	_, err = repo.Create(ctx, domain.Group{ID: snow.ID(7042), OrgID: 1, Name: "two-members"})
	s.Require().NoError(err)
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7040), userA))
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7042), userA))
	s.Require().NoError(repo.AddMember(ctx, snow.ID(7042), seedPBACUser(s.T(), s.db, 53)))

	counts, err := repo.MemberCounts(ctx, 1)
	s.Require().NoError(err)
	s.Equal(map[snow.ID]int64{
		snow.ID(7040): 1,
		snow.ID(7041): 0,
		snow.ID(7042): 2,
	}, counts)

	_, err = s.db.ExecContext(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES (2, 'other', 'Other', 1)`)
	s.Require().NoError(err)
	otherOrg, err := repo.Create(ctx, domain.Group{ID: snow.ID(7043), OrgID: 2, Name: "elsewhere"})
	s.Require().NoError(err)
	s.Require().NoError(repo.AddMember(ctx, otherOrg.ID, seedPBACUser(s.T(), s.db, 54)))

	counts, err = repo.MemberCounts(ctx, 1)
	s.Require().NoError(err)
	s.NotContains(counts, otherOrg.ID)
}
