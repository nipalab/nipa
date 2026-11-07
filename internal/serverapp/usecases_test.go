package serverapp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type refusingSeatGate struct {
	calls []snow.ID
}

func (g *refusingSeatGate) Admit(_ context.Context, _, userID snow.ID, _ func(ctx context.Context) error) error {
	g.calls = append(g.calls, userID)
	return domain.NewErrorQuotaExceeded("seat limit reached")
}

func TestBuildUsecases_WiresSeatGate(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "nipa.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	require.NoError(t, db.MigrateUp(database, "sqlite3"))

	node, err := snow.NewNode(0)
	require.NoError(t, err)
	gate := &refusingSeatGate{}
	uc := BuildUsecases(usecase.Repositories{
		Org:     sqlite.NewOrgRepository(database),
		Project: sqlite.NewProjectRepository(database),
		User:    sqlite.NewUserRepository(database),
		PBAC:    sqlite.NewPBACRepository(database),
		Group:   sqlite.NewGroupRepository(database),
		Audit:   sqlite.NewAuditRepository(database),
	}, UsecaseDeps{Config: &config.Config{}, DB: database, Snow: node, SeatGate: gate})

	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1, IsSuperAdmin: true})
	user := snow.ID(1)

	require.Equal(t, 402, errorCode(uc.Org.AddMember(ctx, 1, user, domain.OrgRoleOwner)))

	group, err := uc.Group.Create(ctx, 1, "artists", "")
	require.NoError(t, err)
	require.Equal(t, 402, errorCode(uc.Group.AddMember(ctx, 1, group.ID, user)))

	_, err = uc.Permission.CreateRule(ctx, domain.PBACRule{UserID: &user, OrgID: 1, Permission: domain.PermissionRead})
	require.Equal(t, 402, errorCode(err))

	_, err = uc.Permission.CreateRule(ctx, domain.PBACRule{GroupID: &group.ID, OrgID: 1, Permission: domain.PermissionRead})
	require.NoError(t, err, "group rules add no seat")

	require.Equal(t, []snow.ID{user, user, user}, gate.calls)
}

func errorCode(err error) int {
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		return 0
	}
	return domErr.Code
}
