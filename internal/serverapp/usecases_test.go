package serverapp

import (
	"context"
	"encoding/base64"
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
		Org:           sqlite.NewOrgRepository(database),
		OrgInvitation: sqlite.NewOrgInvitationRepository(database),
		Project:       sqlite.NewProjectRepository(database),
		User:          sqlite.NewUserRepository(database),
		PBAC:          sqlite.NewPBACRepository(database),
		Group:         sqlite.NewGroupRepository(database),
		Audit:         sqlite.NewAuditRepository(database),
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

func TestBuildUsecases_MFAKey(t *testing.T) {
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "nipa.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	require.NoError(t, db.MigrateUp(database, "sqlite3"))
	node, err := snow.NewNode(0)
	require.NoError(t, err)

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	for name, tc := range map[string]struct {
		cfg       config.Config
		eeDefault bool
		available bool
	}{
		"free default is off":         {cfg: config.Config{MFAEncryptionKey: key}},
		"free opt-in":                 {cfg: config.Config{MFAEncryptionKey: key, MFAEnabled: true, MFAEnabledSet: true}, available: true},
		"enterprise default is on":    {cfg: config.Config{MFAEncryptionKey: key}, eeDefault: true, available: true},
		"enterprise opt-out":          {cfg: config.Config{MFAEncryptionKey: key, MFAEnabledSet: true}, eeDefault: true},
		"enabled without a key":       {cfg: config.Config{}, eeDefault: true},
		"enabled with an invalid key": {cfg: config.Config{MFAEncryptionKey: "short"}, eeDefault: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := tc.cfg
			uc := BuildUsecases(usecase.Repositories{
				User: sqlite.NewUserRepository(database),
				Auth: sqlite.NewAuthRepository(database),
				MFA:  sqlite.NewMFARepository(database),
			}, UsecaseDeps{Config: &cfg, DB: database, Snow: node, MFAEnabledByDefault: tc.eeDefault})
			require.Equal(t, tc.available, uc.MFA.Available())
			_, err := uc.Auth.CompleteLoginMFA(context.Background(), "unknown", "123456")
			require.Error(t, err)
			if !tc.available {
				_, err = uc.MFA.StartTOTP(domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1}), "pw")
				require.Equal(t, 503, errorCode(err))
			}
		})
	}
}
