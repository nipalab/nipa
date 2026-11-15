package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type maintFakes struct {
	member bool
	read   bool
}

func (f *maintFakes) ResolveOrg(_ context.Context, slug string) (*domain.Organization, error) {
	if slug != "acme" {
		return nil, domain.NewErrorNotFound("organization not found")
	}
	return &domain.Organization{ID: 20, Slug: "acme"}, nil
}

func (f *maintFakes) ResolveBySlug(ctx context.Context, org, project string) (*domain.Organization, *domain.Project, error) {
	o, err := f.ResolveOrg(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	return o, &domain.Project{ID: 10, Slug: project}, nil
}

func (f *maintFakes) IsOrgOwner(context.Context, snow.ID) (bool, error)  { return false, nil }
func (f *maintFakes) IsOrgMember(context.Context, snow.ID) (bool, error) { return f.member, nil }
func (f *maintFakes) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return f.read
}

func newMaintenanceServer(t *testing.T, fakes *maintFakes) (*nipaServer, string) {
	t.Helper()
	end := maintNow.Add(time.Hour)
	server := serverWin(maintenance.ReadOnly)
	server.EndsAt = &end
	svc := maintSvc(t, server, orgWin("acme", maintenance.Full), projectWin("acme", "game", maintenance.ReadOnly))
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	uc := usecase.NewMaintenance(nil, fakes, fakes, fakes, svc, node)
	user := &domain.User{ID: snow.ID(42), Email: "alice@example.com", EmailVerified: true, Password: "hash"}
	auth := usecase.NewAuth("test-secret", stubPasswordHasher{}, &stubUserRepo{user: user}, &stubAuthRepo{})
	srv := New(&loginMockContainer{auth: auth, maintenance: uc})
	login, err := srv.LoginWithUsernamePassword(context.Background(), &pb.LoginUsernamePasswordRequest{Username: "alice@example.com", Password: "x"})
	require.NoError(t, err)
	return srv, login.GetAccessToken()
}

func bearerCtx(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestGetMaintenanceStatus(t *testing.T) {
	fakes := &maintFakes{member: true, read: true}
	srv, token := newMaintenanceServer(t, fakes)
	project := &pb.GetMaintenanceStatusRequest{Context: pctx("acme", "game")}

	anon, err := srv.GetMaintenanceStatus(context.Background(), project)
	require.NoError(t, err)
	require.Equal(t, "read_only", anon.GetServer().GetMode())
	require.True(t, anon.GetServer().GetActive())
	require.Equal(t, "upgrade", anon.GetServer().GetMessage())
	require.Equal(t, maintNow.Add(-time.Minute), anon.GetServer().GetStartsAt().AsTime())
	require.Equal(t, maintNow.Add(time.Hour), anon.GetServer().GetEndsAt().AsTime())
	require.Equal(t, "db", anon.GetSource())
	require.Nil(t, anon.GetOrg())
	require.Nil(t, anon.GetProject())

	full, err := srv.GetMaintenanceStatus(bearerCtx(token), project)
	require.NoError(t, err)
	require.Equal(t, "full", full.GetOrg().GetMode())
	require.Equal(t, "acme", full.GetOrg().GetOrg())
	require.Equal(t, "acme/game", full.GetProject().GetProject())
	require.Nil(t, full.GetProject().GetEndsAt())

	org, err := srv.GetMaintenanceStatus(bearerCtx(token), &pb.GetMaintenanceStatusRequest{Context: pctx("acme", "")})
	require.NoError(t, err)
	require.Equal(t, "full", org.GetOrg().GetMode())
	require.Nil(t, org.GetProject())

	for name, ctx := range map[string]context.Context{
		"invalid token": bearerCtx("garbage"),
		"bad header":    metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Basic x")),
	} {
		res, err := srv.GetMaintenanceStatus(ctx, project)
		require.NoError(t, err, name)
		require.Nil(t, res.GetProject(), name)
		require.NotNil(t, res.GetServer(), name)
	}

	fakes.read, fakes.member = false, false
	denied, err := srv.GetMaintenanceStatus(bearerCtx(token), project)
	require.NoError(t, err)
	require.Nil(t, denied.GetProject())
	require.Nil(t, denied.GetOrg())
	denied, err = srv.GetMaintenanceStatus(bearerCtx(token), &pb.GetMaintenanceStatusRequest{Context: pctx("acme", "")})
	require.NoError(t, err)
	require.Nil(t, denied.GetOrg())
}

func TestMaintenanceWindowToPBFileOverride(t *testing.T) {
	require.Nil(t, maintenanceWindowToPB(nil))
	w := maintenance.Window{Mode: maintenance.Full}
	st := w.Status(maintNow)
	got := maintenanceWindowToPB(&st)
	require.Nil(t, got.GetStartsAt(), "a file override has no start")
	require.Nil(t, got.GetEndsAt())
	require.True(t, got.GetActive())
}
