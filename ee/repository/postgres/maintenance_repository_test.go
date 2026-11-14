package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type MaintenanceRepositorySuite struct {
	baseSuite
}

func TestMaintenanceRepositorySuite(t *testing.T) {
	suite.Run(t, new(MaintenanceRepositorySuite))
}

func maintenanceExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func (s *MaintenanceRepositorySuite) TestServerWindow() {
	t := s.T()
	ctx := context.Background()
	db := s.db
	repo := NewMaintenanceRepository(db)
	now := time.Now().UTC().Truncate(time.Second)

	_, err := repo.GetServer(ctx)
	require.True(t, domain.IsErrorNotFound(err))

	end := now.Add(time.Hour)
	created, err := repo.UpsertServer(ctx, domain.MaintenanceWindow{
		ID: 100, Mode: "read_only", Message: "backup", StartsAt: now, EndsAt: &end,
		Notify: true, CreatedBy: 1, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(100), created.ID)
	require.Nil(t, created.ProjectID)
	require.True(t, created.EndsAt.Equal(end))

	updated, err := repo.UpsertServer(ctx, domain.MaintenanceWindow{
		ID: 200, Mode: "frozen", Message: "move", StartsAt: now, CreatedBy: 2, UpdatedAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(100), updated.ID, "the server window keeps its id")
	require.Equal(t, snow.ID(1), updated.CreatedBy)
	require.Equal(t, "frozen", updated.Mode)
	require.Nil(t, updated.EndsAt)
	require.False(t, updated.Notify)

	got, err := repo.GetServer(ctx)
	require.NoError(t, err)
	require.Equal(t, "move", got.Message)
	require.True(t, got.StartsAt.Equal(now))
	require.True(t, got.UpdatedAt.Equal(now.Add(time.Minute)))

	_, err = db.ExecContext(ctx, `INSERT INTO maintenance_windows (id, mode, starts_at, created_by, updated_at) VALUES (300, 'full', $1, 0, $2)`, now, now)
	require.Error(t, err, "only one server window")

	ok, err := repo.Delete(ctx, 100)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.Delete(ctx, 100)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = repo.GetServer(ctx)
	require.True(t, domain.IsErrorNotFound(err))
}

func (s *MaintenanceRepositorySuite) TestProjectWindows() {
	t := s.T()
	ctx := context.Background()
	db, q := s.db, s.q
	repo := NewMaintenanceRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	orgID := seedOrg(t, q, "Acme", "acme")
	game := seedProject(t, q, orgID.Int64(), "game")
	tools := seedProject(t, q, orgID.Int64(), "tools")

	_, err := repo.UpsertProject(ctx, domain.MaintenanceWindow{ID: 1, Mode: "full"})
	require.Error(t, err)

	_, err = repo.UpsertProject(ctx, domain.MaintenanceWindow{ID: 1, ProjectID: &game, Mode: "frozen", StartsAt: now, UpdatedAt: now})
	require.Error(t, err, "frozen is refused for a project")

	w, err := repo.UpsertProject(ctx, domain.MaintenanceWindow{
		ID: 1, ProjectID: &game, OrgSlug: "acme", ProjectSlug: "game", Mode: "full", StartsAt: now, CreatedBy: 5, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, game, *w.ProjectID)
	require.Equal(t, "game", w.ProjectSlug)

	w, err = repo.UpsertProject(ctx, domain.MaintenanceWindow{ID: 2, ProjectID: &game, Mode: "read_only", Message: "again", StartsAt: now, UpdatedAt: now})
	require.NoError(t, err)
	require.Equal(t, snow.ID(1), w.ID)
	require.Equal(t, snow.ID(5), w.CreatedBy)

	_, err = repo.UpsertProject(ctx, domain.MaintenanceWindow{ID: 3, ProjectID: &tools, Mode: "full", StartsAt: now, UpdatedAt: now})
	require.NoError(t, err)
	_, err = repo.UpsertServer(ctx, domain.MaintenanceWindow{ID: 4, Mode: "read_only", StartsAt: now, UpdatedAt: now})
	require.NoError(t, err)

	got, err := repo.GetProject(ctx, game)
	require.NoError(t, err)
	require.Equal(t, "read_only", got.Mode)
	require.Equal(t, "again", got.Message)
	require.Equal(t, "acme", got.OrgSlug)
	require.Equal(t, "game", got.ProjectSlug)

	_, err = repo.GetProject(ctx, 999)
	require.True(t, domain.IsErrorNotFound(err))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, snow.ID(1), list[0].ID)
	require.Equal(t, "game", list[0].ProjectSlug)
	require.Equal(t, "tools", list[1].ProjectSlug)
	require.Nil(t, list[2].ProjectID)
	require.Empty(t, list[2].OrgSlug)

	maintenanceExec(t, db, `UPDATE projects SET slug = 'renamed' WHERE id = $1`, game.Int64())
	got, err = repo.GetProject(ctx, game)
	require.NoError(t, err)
	require.Equal(t, "renamed", got.ProjectSlug)
}

func (s *MaintenanceRepositorySuite) TestDeletedProjectsAndOrgs() {
	t := s.T()
	ctx := context.Background()
	db, q := s.db, s.q
	repo := NewMaintenanceRepository(db)
	now := time.Now().UTC()
	acme := seedOrg(t, q, "Acme", "acme")
	other := seedOrg(t, q, "Other", "other")
	game := seedProject(t, q, acme.Int64(), "game")
	gone := seedProject(t, q, acme.Int64(), "gone")
	orphan := seedProject(t, q, other.Int64(), "orphan")
	for i, pid := range []snow.ID{game, gone, orphan} {
		_, err := repo.UpsertProject(ctx, domain.MaintenanceWindow{ID: snow.ID(i + 1), ProjectID: &pid, Mode: "full", StartsAt: now, UpdatedAt: now})
		require.NoError(t, err)
	}

	require.NoError(t, q.DeleteProject(ctx, gone.Int64()))
	_, err := repo.GetProject(ctx, gone)
	require.True(t, domain.IsErrorNotFound(err))

	require.NoError(t, q.DeleteOrganization(ctx, other.Int64()))
	_, err = repo.GetProject(ctx, orphan)
	require.True(t, domain.IsErrorNotFound(err))

	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_windows`).Scan(&n))
	require.Equal(t, 1, n, "the triggers removed the windows")

	maintenanceExec(t, db, `INSERT INTO maintenance_windows (id, project_id, mode, starts_at, created_by, updated_at) VALUES (10, $1, 'full', $2, 0, $3)`, gone.Int64(), now, now)
	maintenanceExec(t, db, `INSERT INTO maintenance_windows (id, project_id, mode, starts_at, created_by, updated_at) VALUES (11, $1, 'full', $2, 0, $3)`, orphan.Int64(), now, now)
	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, game, *list[0].ProjectID)

	maintenanceExec(t, db, `DELETE FROM projects WHERE id = $1`, game.Int64())
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_windows WHERE project_id = $1`, game.Int64()).Scan(&n))
	require.Zero(t, n)
}

func (s *MaintenanceRepositorySuite) TestRecipients() {
	t := s.T()
	ctx := context.Background()
	db, q := s.db, s.q
	repo := NewMaintenanceRepository(db)
	acme := seedOrg(t, q, "Acme", "acme")
	other := seedOrg(t, q, "Other", "other")
	dead := seedOrg(t, q, "Dead", "dead")
	game := seedProject(t, q, acme.Int64(), "game")
	tools := seedProject(t, q, acme.Int64(), "tools")
	deadProject := seedProject(t, q, dead.Int64(), "old")

	owner := seedPBACUser(t, db, 10)
	both := seedPBACUser(t, db, 11)
	ruleAdmin := seedPBACUser(t, db, 12)
	groupAdmin := seedPBACUser(t, db, 13)
	orgAdmin := seedPBACUser(t, db, 14)
	writer := seedPBACUser(t, db, 15)
	deletedOwner := seedPBACUser(t, db, 16)
	unverifiedOwner := seedPBACUser(t, db, 17)
	deadOrgOwner := seedPBACUser(t, db, 18)
	otherOwner := seedPBACUser(t, db, 19)
	subPathAdmin := seedPBACUser(t, db, 20)
	toolsAdmin := seedPBACUser(t, db, 21)
	deletedGroupAdmin := seedPBACUser(t, db, 22)
	maintenanceExec(t, db, `UPDATE users SET deleted = true WHERE id = $1`, deletedOwner.Int64())
	maintenanceExec(t, db, `UPDATE users SET email_verified = false WHERE id = $1`, unverifiedOwner.Int64())

	owners := map[snow.ID][]snow.ID{
		acme:  {owner, both, deletedOwner, unverifiedOwner},
		dead:  {deadOrgOwner},
		other: {otherOwner},
	}
	for org, users := range owners {
		for _, u := range users {
			require.NoError(t, q.OrgMemberUpsert(ctx, sqlcPostgres.OrgMemberUpsertParams{OrgID: org.Int64(), UserID: u.Int64(), Role: "owner"}))
		}
	}
	require.NoError(t, q.OrgMemberUpsert(ctx, sqlcPostgres.OrgMemberUpsertParams{OrgID: acme.Int64(), UserID: writer.Int64(), Role: "member"}))
	require.NoError(t, q.DeleteOrganization(ctx, dead.Int64()))

	admin := int64(domain.PermissionAll)
	write := int64(domain.PermissionRead | domain.PermissionWrite)
	team := seedPBACGroup(t, db, 50, acme.Int64(), "team", groupAdmin.Int64(), both.Int64())
	oldTeam := seedPBACGroup(t, db, 51, acme.Int64(), "old-team", deletedGroupAdmin.Int64())
	maintenanceExec(t, db, `UPDATE groups SET deleted = true WHERE id = $1`, oldTeam.Int64())
	rule := `INSERT INTO pbac_rules (user_id, group_id, org_id, project_id, path_prefix, permission) VALUES ($1, $2, $3, $4, $5, $6)`
	maintenanceExec(t, db, rule, ruleAdmin.Int64(), nil, acme.Int64(), game.Int64(), "", admin)
	maintenanceExec(t, db, rule, both.Int64(), nil, acme.Int64(), game.Int64(), "", admin)
	maintenanceExec(t, db, rule, nil, team.Int64(), acme.Int64(), game.Int64(), "", admin)
	maintenanceExec(t, db, rule, nil, oldTeam.Int64(), acme.Int64(), game.Int64(), "", admin)
	maintenanceExec(t, db, rule, orgAdmin.Int64(), nil, acme.Int64(), nil, "", admin)
	maintenanceExec(t, db, rule, writer.Int64(), nil, acme.Int64(), game.Int64(), "", write)
	maintenanceExec(t, db, rule, subPathAdmin.Int64(), nil, acme.Int64(), game.Int64(), "assets", admin)
	maintenanceExec(t, db, rule, toolsAdmin.Int64(), nil, acme.Int64(), tools.Int64(), "", admin)

	ids := func(rs []domain.MaintenanceRecipient) []snow.ID {
		out := make([]snow.ID, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.UserID)
		}
		return out
	}

	server, err := repo.RecipientsServer(ctx)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{1, owner, both, otherOwner}, ids(server))
	require.Equal(t, "user10@example.com", server[1].Email)
	require.Equal(t, "user10", server[1].Name)

	project, err := repo.RecipientsProject(ctx, game)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{owner, both, ruleAdmin, groupAdmin, orgAdmin}, ids(project))

	project, err = repo.RecipientsProject(ctx, deadProject)
	require.NoError(t, err)
	require.Empty(t, project)

	require.NoError(t, q.DeleteProject(ctx, game.Int64()))
	project, err = repo.RecipientsProject(ctx, game)
	require.NoError(t, err)
	require.Empty(t, project)
}

func (s *MaintenanceRepositorySuite) TestJoinsTransaction() {
	t := s.T()
	ctx := context.Background()
	db := s.db
	repo := NewMaintenanceRepository(db)
	now := time.Now().UTC()

	rollback := errors.New("rollback")
	err := dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		if _, err := repo.UpsertServer(ctx, domain.MaintenanceWindow{ID: 1, Mode: "full", StartsAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	_, err = repo.GetServer(ctx)
	require.True(t, domain.IsErrorNotFound(err))
}

func (s *MaintenanceRepositorySuite) TestErrors() {
	t := s.T()
	ctx := context.Background()
	db := s.db
	repo := NewMaintenanceRepository(db)
	require.NoError(t, db.Close())

	_, err := repo.List(ctx)
	require.Error(t, err)
	_, err = repo.GetProject(ctx, 1)
	require.Error(t, err)
	_, err = repo.UpsertServer(ctx, domain.MaintenanceWindow{})
	require.Error(t, err)
	_, err = repo.RecipientsServer(ctx)
	require.Error(t, err)
	_, err = repo.RecipientsProject(ctx, 1)
	require.Error(t, err)
	_, err = repo.Delete(ctx, 1)
	require.Error(t, err)
}
