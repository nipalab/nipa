package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/snow"
)

var maintNow = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

type maintFixture struct {
	uc       *Maintenance
	repo     *MockmaintenanceRepository
	resolver *MockmaintenanceResolver
	orgs     *MockmaintenanceOrgs
	perm     *MockmaintenanceAccess
	mailer   *MockMaintenanceMailer
	svc      *maintenance.Service
	rec      *captureRecorder
}

var (
	maintOrg     = &domain.Organization{ID: 10, Slug: "acme"}
	maintProject = &domain.Project{ID: 20, OrgID: 10, Slug: "game"}
)

func newMaintFixture(t *testing.T) *maintFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	f := &maintFixture{
		repo:     NewMockmaintenanceRepository(ctrl),
		resolver: NewMockmaintenanceResolver(ctrl),
		orgs:     NewMockmaintenanceOrgs(ctrl),
		perm:     NewMockmaintenanceAccess(ctrl),
		mailer:   NewMockMaintenanceMailer(ctrl),
		svc:      maintenance.NewService().WithClock(func() time.Time { return maintNow }),
		rec:      &captureRecorder{},
	}
	f.uc = NewMaintenance(f.repo, f.resolver, f.orgs, f.perm, f.svc, node).WithAudit(f.rec)
	return f
}

func (f *maintFixture) expectList(rows ...domain.MaintenanceWindow) {
	f.repo.EXPECT().List(gomock.Any()).Return(rows, nil)
}

func echoUpsert(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	return &w, nil
}

func notFound() error { return domain.NewErrorNotFound("record not found") }

func TestMaintenance_NewDefaultsService(t *testing.T) {
	uc := NewMaintenance(nil, nil, nil, nil, nil, nil)
	require.NotNil(t, uc.Service())
	state := uc.Status(context.Background())
	require.Nil(t, state.Server)
	require.Equal(t, maintenance.SourceDB, state.Source)
}

func TestMaintenance_SetServerScheduledAndApplied(t *testing.T) {
	f := newMaintFixture(t)
	start := maintNow.Add(time.Hour)
	end := start.Add(30 * time.Minute)
	committed := false
	f.rec = &captureRecorder{}
	f.uc.WithAudit(&commitRecorder{captureRecorder: f.rec, committed: &committed})

	f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
	f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
		require.False(t, committed)
		require.Equal(t, "read_only", w.Mode)
		require.Equal(t, snow.ID(7), w.CreatedBy)
		require.Equal(t, start, w.StartsAt)
		require.Equal(t, end, *w.EndsAt)
		require.Nil(t, w.OrgID)
		require.Nil(t, w.ProjectID)
		w.ID = 99
		return &w, nil
	})
	f.repo.EXPECT().List(gomock.Any()).DoAndReturn(func(context.Context) ([]domain.MaintenanceWindow, error) {
		require.True(t, committed, "applied only after the commit")
		return []domain.MaintenanceWindow{{ID: 99, Mode: "read_only", StartsAt: maintNow.Add(-time.Minute)}}, nil
	})

	st, err := f.uc.SetServer(permissionCtx(7, withSuperAdmin()), MaintenanceInput{Mode: "read_only", Message: "disk swap", StartsAt: &start, EndsAt: &end, Notify: true})
	require.NoError(t, err)
	require.Equal(t, maintenance.ReadOnly, st.Mode)
	require.False(t, st.Active)
	require.Equal(t, "disk swap", st.Message)
	require.NotNil(t, st.Notified)
	require.False(t, *st.Notified, "no mailer wired")

	entry := f.rec.only(t)
	requireActor(t, entry, domain.AuditMaintenanceScheduled)
	require.Equal(t, domain.AuditTargetMaintenance, entry.TargetType)
	require.Equal(t, snow.ID(99).Base36(), entry.TargetID)
	require.Equal(t, "server", entry.Metadata["scope"])
	require.Equal(t, "read_only", entry.Metadata["mode"])
	require.Equal(t, start.Format(time.RFC3339), entry.Metadata["starts_at"])
	require.Equal(t, end.Format(time.RFC3339), entry.Metadata["ends_at"])
	require.Equal(t, true, entry.Metadata["notify"])
	require.Nil(t, entry.OrgID)

	w, scope := f.svc.For("", "")
	require.Equal(t, maintenance.ReadOnly, w.Mode)
	require.Equal(t, maintenance.ScopeServer, scope)

	state := f.uc.Status(context.Background())
	require.Equal(t, maintenance.ReadOnly, state.Server.Mode)
	require.True(t, state.Server.Active)
}

type commitRecorder struct {
	*captureRecorder
	committed *bool
}

func (c *commitRecorder) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := c.captureRecorder.WithinTx(ctx, fn); err != nil {
		return err
	}
	*c.committed = true
	return nil
}

func TestMaintenance_SetServerPermissions(t *testing.T) {
	f := newMaintFixture(t)
	for _, ctx := range []context.Context{context.Background(), permissionCtx(3), permissionCtx(3, withAdmin())} {
		_, err := f.uc.SetServer(ctx, MaintenanceInput{Mode: "full"})
		requireCode(t, err, 403)
		requireCode(t, f.uc.EndServer(ctx), 403)
		_, err = f.uc.AdminStatus(ctx)
		requireCode(t, err, 403)
	}
}

func TestMaintenance_FileSourceConflict(t *testing.T) {
	f := newMaintFixture(t)
	snap := maintenance.NewSnapshot(nil, maintNow)
	snap.Server, snap.Source = &maintenance.Window{Mode: maintenance.Full}, maintenance.SourceFile
	require.NoError(t, f.svc.Apply(context.Background(), snap))

	_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full"})
	requireCode(t, err, 409)
	require.Contains(t, err.Error(), "MAINTENANCE_FILE")
	requireCode(t, f.uc.EndServer(superCtx()), 409)
	requireCode(t, f.uc.WithSystemActor().EndServer(context.Background()), 409)

	state := f.uc.Status(context.Background())
	require.Equal(t, maintenance.SourceFile, state.Source)
	require.Equal(t, maintenance.Full, state.Server.Mode)

	f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
	f.repo.EXPECT().GetOrg(gomock.Any(), maintOrg.ID).Return(nil, notFound())
	f.repo.EXPECT().UpsertOrg(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
	f.expectList(domain.MaintenanceWindow{ID: 5, OrgID: domain.IDPtr(maintOrg.ID), OrgSlug: "acme", Mode: "full", StartsAt: maintNow})
	_, err = f.uc.SetOrg(superCtx(), "acme", MaintenanceInput{Mode: "full"})
	require.NoError(t, err)
	cur := f.svc.Snapshot()
	require.Equal(t, maintenance.SourceFile, cur.Source, "a local apply keeps the file override")
	require.Equal(t, maintenance.Full, cur.Server.Mode)
	require.Contains(t, cur.Orgs, "acme")
}

func TestMaintenance_Validation(t *testing.T) {
	far := maintNow.Add(91 * 24 * time.Hour)
	past := maintNow.Add(-time.Hour)
	cases := []struct {
		name string
		in   MaintenanceInput
		org  bool
		msg  string
	}{
		{name: "unknown mode", in: MaintenanceInput{Mode: "closed"}, msg: "mode must be"},
		{name: "off", in: MaintenanceInput{Mode: "off"}, msg: "mode must be"},
		{name: "frozen org", in: MaintenanceInput{Mode: "frozen"}, org: true, msg: "frozen is only"},
		{name: "long message", in: MaintenanceInput{Mode: "full", Message: strings.Repeat("é", 501)}, msg: "at most 500"},
		{name: "start too far", in: MaintenanceInput{Mode: "full", StartsAt: &far}, msg: "within 90 days"},
		{name: "end before start", in: MaintenanceInput{Mode: "full", EndsAt: &past}, msg: "ends_at must be after"},
		{name: "end equals start", in: MaintenanceInput{Mode: "full", StartsAt: &past, EndsAt: &past}, msg: "ends_at must be after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintFixture(t)
			var err error
			if tc.org {
				f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
				_, err = f.uc.SetOrg(superCtx(), "acme", tc.in)
			} else {
				f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound()).AnyTimes()
				_, err = f.uc.SetServer(superCtx(), tc.in)
			}
			requireCode(t, err, 400)
			require.Contains(t, err.Error(), tc.msg)
			require.Empty(t, f.rec.entries)
		})
	}
}

func TestMaintenance_SetOrgPermissions(t *testing.T) {
	t.Run("member denied", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil).Times(2)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(false, nil).Times(2)
		_, err := f.uc.SetOrg(permissionCtx(3), "acme", MaintenanceInput{Mode: "full"})
		requireCode(t, err, 403)
		requireCode(t, f.uc.EndOrg(permissionCtx(3), "acme"), 403)
	})
	t.Run("owner check fails", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(false, errors.New("db"))
		_, err := f.uc.SetOrg(permissionCtx(3), "acme", MaintenanceInput{Mode: "full"})
		require.EqualError(t, err, "db")
	})
	t.Run("anonymous denied", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		_, err := f.uc.SetOrg(context.Background(), "acme", MaintenanceInput{Mode: "full"})
		requireCode(t, err, 403)
	})
	t.Run("unknown org", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "nope").Return(nil, notFound()).Times(3)
		_, err := f.uc.SetOrg(superCtx(), "nope", MaintenanceInput{Mode: "full"})
		requireCode(t, err, 404)
		requireCode(t, f.uc.EndOrg(superCtx(), "nope"), 404)
		_, err = f.uc.OrgStatus(superCtx(), "nope")
		requireCode(t, err, 404)
	})
	t.Run("owner sets", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(true, nil)
		f.repo.EXPECT().GetOrg(gomock.Any(), maintOrg.ID).Return(nil, notFound())
		f.repo.EXPECT().UpsertOrg(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
			require.Equal(t, maintOrg.ID, *w.OrgID)
			require.Equal(t, maintNow, w.StartsAt, "a missing start means now")
			return &w, nil
		})
		f.expectList()
		st, err := f.uc.SetOrg(permissionCtx(3), "acme", MaintenanceInput{Mode: "full"})
		require.NoError(t, err)
		require.True(t, st.Active)
		require.Equal(t, "acme", st.Org)
		entry := f.rec.only(t)
		require.Equal(t, "org", entry.Metadata["scope"])
		require.Equal(t, maintOrg.ID.Base36(), entry.Metadata["org_id"])
		require.Equal(t, maintOrg.ID, *entry.OrgID)
	})
}

func TestMaintenance_SetProject(t *testing.T) {
	t.Run("project admin who is not an org owner is denied", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil).Times(2)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(false, nil).Times(2)
		_, err := f.uc.SetProject(permissionCtx(4), "acme", "game", MaintenanceInput{Mode: "read_only"})
		requireCode(t, err, 403)
		requireCode(t, f.uc.EndProject(permissionCtx(4), "acme", "game"), 403)
	})
	t.Run("unknown project", func(t *testing.T) {
		f := newMaintFixture(t)
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "nope").Return(nil, nil, notFound()).Times(3)
		_, err := f.uc.SetProject(superCtx(), "acme", "nope", MaintenanceInput{Mode: "full"})
		requireCode(t, err, 404)
		requireCode(t, f.uc.EndProject(superCtx(), "acme", "nope"), 404)
		_, err = f.uc.ProjectStatus(superCtx(), "acme", "nope")
		requireCode(t, err, 404)
	})
	t.Run("update keeps the start and announces", func(t *testing.T) {
		f := newMaintFixture(t)
		f.uc.WithMailer(f.mailer)
		start := maintNow.Add(2 * time.Hour)
		existing := &domain.MaintenanceWindow{ID: 50, ProjectID: domain.IDPtr(maintProject.ID), Mode: "full", StartsAt: start, CreatedBy: 3}
		recipients := []domain.MaintenanceRecipient{{UserID: 3, Email: "o@example.com"}}
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(true, nil)
		f.repo.EXPECT().GetProject(gomock.Any(), maintProject.ID).Return(existing, nil)
		f.repo.EXPECT().UpsertProject(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
			require.Equal(t, start, w.StartsAt)
			w.ID = existing.ID
			return &w, nil
		})
		f.repo.EXPECT().RecipientsProject(gomock.Any(), maintProject.ID).Return(recipients, nil)
		f.mailer.EXPECT().Announce(gomock.Any(), MaintenanceNoticeUpdated, gomock.Any(), recipients).DoAndReturn(
			func(_ context.Context, _ MaintenanceNotice, w domain.MaintenanceWindow, _ []domain.MaintenanceRecipient) error {
				require.Equal(t, "acme", w.OrgSlug)
				require.Equal(t, "game", w.ProjectSlug)
				return nil
			})
		f.expectList(domain.MaintenanceWindow{ID: 50, ProjectID: domain.IDPtr(maintProject.ID), OrgSlug: "acme", ProjectSlug: "game", Mode: "read_only", StartsAt: maintNow})
		st, err := f.uc.SetProject(permissionCtx(3), "acme", "game", MaintenanceInput{Mode: "read_only", Notify: true})
		require.NoError(t, err)
		require.True(t, *st.Notified)
		require.Equal(t, "acme/game", st.Project)
		entry := f.rec.only(t)
		require.Equal(t, domain.AuditMaintenanceUpdated, entry.Action)
		require.Equal(t, "project", entry.Metadata["scope"])
		require.Equal(t, maintProject.ID.Base36(), entry.Metadata["project_id"])
		require.Equal(t, maintOrg.ID.Base36(), entry.Metadata["org_id"])
		require.Equal(t, maintProject.ID, *entry.ProjectID)

		w, scope := f.svc.For("acme", "game")
		require.Equal(t, maintenance.ReadOnly, w.Mode)
		require.Equal(t, maintenance.ScopeProject, scope)
	})
}

func TestMaintenance_WriteFailures(t *testing.T) {
	t.Run("audit failure rolls back and skips the apply", func(t *testing.T) {
		f := newMaintFixture(t)
		f.uc.WithAudit(&captureRecorder{err: errAudit})
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full"})
		require.ErrorIs(t, err, errAudit)
		w, _ := f.svc.For("", "")
		require.Equal(t, maintenance.Off, w.Mode)
	})
	t.Run("lookup error", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, errors.New("db")).Times(2)
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full"})
		require.EqualError(t, err, "db")
		require.EqualError(t, f.uc.EndServer(superCtx()), "db")
	})
	t.Run("upsert error", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full"})
		require.EqualError(t, err, "db")
		require.Empty(t, f.rec.entries)
	})
	t.Run("recipients error", func(t *testing.T) {
		f := newMaintFixture(t)
		f.uc.WithMailer(f.mailer)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
		f.repo.EXPECT().RecipientsServer(gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full", Notify: true})
		require.EqualError(t, err, "db")
	})
	t.Run("mailer error", func(t *testing.T) {
		f := newMaintFixture(t)
		f.uc.WithMailer(f.mailer)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
		f.repo.EXPECT().RecipientsServer(gomock.Any()).Return(nil, nil)
		f.mailer.EXPECT().Announce(gomock.Any(), MaintenanceNoticeScheduled, gomock.Any(), gomock.Any()).Return(errors.New("smtp"))
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full", Notify: true})
		require.EqualError(t, err, "smtp")
	})
	t.Run("reload failure keeps the write", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
		f.repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db"))
		_, err := f.uc.SetServer(superCtx(), MaintenanceInput{Mode: "full"})
		require.NoError(t, err)
	})
	t.Run("resume hook failure keeps the write", func(t *testing.T) {
		f := newMaintFixture(t)
		require.NoError(t, f.svc.Apply(context.Background(), maintenance.NewSnapshot([]maintenance.Window{{ID: 1, Mode: maintenance.Frozen}}, maintNow)))
		f.svc.OnResume(func(context.Context) error { return errors.New("lease") })
		f.repo.EXPECT().GetServer(gomock.Any()).Return(&domain.MaintenanceWindow{ID: 1, Mode: "frozen"}, nil)
		f.repo.EXPECT().Delete(gomock.Any(), snow.ID(1)).Return(true, nil)
		f.expectList()
		require.NoError(t, f.uc.EndServer(superCtx()))
		require.True(t, f.svc.Frozen())
	})
}

func TestMaintenance_End(t *testing.T) {
	t.Run("nothing to end", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
		requireCode(t, f.uc.EndServer(superCtx()), 404)
	})
	t.Run("deleted meanwhile", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(&domain.MaintenanceWindow{ID: 1, Mode: "full"}, nil)
		f.repo.EXPECT().Delete(gomock.Any(), snow.ID(1)).Return(false, nil)
		requireCode(t, f.uc.EndServer(superCtx()), 404)
	})
	t.Run("delete error", func(t *testing.T) {
		f := newMaintFixture(t)
		f.repo.EXPECT().GetServer(gomock.Any()).Return(&domain.MaintenanceWindow{ID: 1, Mode: "full"}, nil)
		f.repo.EXPECT().Delete(gomock.Any(), snow.ID(1)).Return(false, errors.New("db"))
		require.EqualError(t, f.uc.EndServer(superCtx()), "db")
	})
	t.Run("org window with notify announces finished", func(t *testing.T) {
		f := newMaintFixture(t)
		f.uc.WithMailer(f.mailer)
		end := maintNow.Add(time.Hour)
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.repo.EXPECT().GetOrg(gomock.Any(), maintOrg.ID).Return(&domain.MaintenanceWindow{ID: 2, OrgID: domain.IDPtr(maintOrg.ID), Mode: "full", StartsAt: maintNow, EndsAt: &end, Notify: true}, nil)
		f.repo.EXPECT().Delete(gomock.Any(), snow.ID(2)).Return(true, nil)
		f.repo.EXPECT().RecipientsOrg(gomock.Any(), maintOrg.ID).Return(nil, nil)
		f.mailer.EXPECT().Announce(gomock.Any(), MaintenanceNoticeFinished, gomock.Any(), gomock.Any()).Return(nil)
		f.expectList()
		require.NoError(t, f.uc.EndOrg(superCtx(), "acme"))
		entry := f.rec.only(t)
		require.Equal(t, domain.AuditMaintenanceEnded, entry.Action)
		require.Equal(t, end.Format(time.RFC3339), entry.Metadata["ends_at"])
	})
	t.Run("project window by the system actor", func(t *testing.T) {
		f := newMaintFixture(t)
		sys := f.uc.WithSystemActor()
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
		f.repo.EXPECT().GetProject(gomock.Any(), maintProject.ID).Return(&domain.MaintenanceWindow{ID: 3, ProjectID: domain.IDPtr(maintProject.ID), Mode: "full"}, nil)
		f.repo.EXPECT().Delete(gomock.Any(), snow.ID(3)).Return(true, nil)
		f.expectList()
		require.NoError(t, sys.EndProject(context.Background(), "acme", "game"))
		entry := f.rec.only(t)
		require.Equal(t, domain.AuditActorSystem, entry.ActorType)
		require.Equal(t, "cli", entry.Metadata["via"])
	})
}

func TestMaintenance_SystemActor(t *testing.T) {
	f := newMaintFixture(t)
	sys := f.uc.WithSystemActor()
	require.Same(t, f.svc, sys.Service())
	f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
	f.repo.EXPECT().GetProject(gomock.Any(), maintProject.ID).Return(nil, notFound())
	f.repo.EXPECT().UpsertProject(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
		require.Equal(t, snow.ID(0), w.CreatedBy)
		return &w, nil
	})
	f.expectList()
	_, err := sys.SetProject(context.Background(), "acme", "game", MaintenanceInput{Mode: "full"})
	require.NoError(t, err)
	entry := f.rec.only(t)
	require.Equal(t, domain.AuditActorSystem, entry.ActorType)
	require.Equal(t, "cli", entry.Metadata["via"])

	f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
	f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
		require.Equal(t, "frozen", w.Mode)
		return &w, nil
	})
	f.expectList()
	_, err = sys.SetServer(context.Background(), MaintenanceInput{Mode: "frozen"})
	require.NoError(t, err)
	f.repo.EXPECT().GetServer(gomock.Any()).Return(nil, notFound())
	f.repo.EXPECT().UpsertServer(gomock.Any(), gomock.Any()).DoAndReturn(echoUpsert)
	f.expectList()
	_, err = sys.SetServer(context.Background(), MaintenanceInput{Mode: "read_only"})
	require.NoError(t, err)

	_, err = sys.AdminStatus(context.Background())
	require.NoError(t, err)
	f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
	_, err = sys.OrgStatus(context.Background(), "acme")
	require.NoError(t, err)
	f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
	_, err = sys.ProjectStatus(context.Background(), "acme", "game")
	require.NoError(t, err)
}

func seedMaintSnapshot(t *testing.T, svc *maintenance.Service) {
	t.Helper()
	snap := maintenance.NewSnapshot(WindowsFromDomain([]domain.MaintenanceWindow{
		{ID: 1, Mode: "read_only", StartsAt: maintNow.Add(-time.Hour)},
		{ID: 2, OrgID: domain.IDPtr(10), OrgSlug: "acme", Mode: "full", StartsAt: maintNow.Add(time.Hour)},
		{ID: 3, OrgID: domain.IDPtr(11), OrgSlug: "beta", Mode: "read_only", StartsAt: maintNow},
		{ID: 4, ProjectID: domain.IDPtr(20), OrgSlug: "acme", ProjectSlug: "game", Mode: "full", StartsAt: maintNow},
		{ID: 5, ProjectID: domain.IDPtr(21), OrgSlug: "acme", ProjectSlug: "art", Mode: "read_only", StartsAt: maintNow},
		{ID: 6, ProjectID: domain.IDPtr(30), OrgSlug: "beta", ProjectSlug: "x", Mode: "full", StartsAt: maintNow},
	}), maintNow)
	require.NoError(t, svc.Apply(context.Background(), snap))
}

func TestMaintenance_Statuses(t *testing.T) {
	f := newMaintFixture(t)
	seedMaintSnapshot(t, f.svc)

	pub := f.uc.Status(context.Background())
	require.Equal(t, maintenance.ReadOnly, pub.Server.Mode)
	require.Nil(t, pub.Org)
	require.Empty(t, pub.Projects)

	admin, err := f.uc.AdminStatus(superCtx())
	require.NoError(t, err)
	require.Len(t, admin.Orgs, 2)
	require.Equal(t, "acme", admin.Orgs[0].Org)
	require.False(t, admin.Orgs[0].Active, "scheduled")
	require.Len(t, admin.Projects, 3)
	require.Equal(t, "acme/art", admin.Projects[0].Project)

	t.Run("org member", func(t *testing.T) {
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.orgs.EXPECT().IsOrgMember(gomock.Any(), maintOrg.ID).Return(true, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(false, nil)
		st, err := f.uc.OrgStatus(permissionCtx(3), "acme")
		require.NoError(t, err)
		require.Equal(t, maintenance.Full, st.Org.Mode)
		require.NotNil(t, st.Server)
		require.Empty(t, st.Projects)
	})
	t.Run("org owner sees the org's projects", func(t *testing.T) {
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.orgs.EXPECT().IsOrgMember(gomock.Any(), maintOrg.ID).Return(true, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(true, nil)
		st, err := f.uc.OrgStatus(permissionCtx(3), "acme")
		require.NoError(t, err)
		require.Len(t, st.Projects, 2)
		require.Equal(t, "acme/art", st.Projects[0].Project)
		require.Equal(t, "acme/game", st.Projects[1].Project)
	})
	t.Run("non member denied", func(t *testing.T) {
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil)
		f.orgs.EXPECT().IsOrgMember(gomock.Any(), maintOrg.ID).Return(false, nil)
		_, err := f.uc.OrgStatus(permissionCtx(3), "acme")
		requireCode(t, err, 403)
	})
	t.Run("membership errors", func(t *testing.T) {
		f.resolver.EXPECT().ResolveOrg(gomock.Any(), "acme").Return(maintOrg, nil).Times(2)
		f.orgs.EXPECT().IsOrgMember(gomock.Any(), maintOrg.ID).Return(false, errors.New("db"))
		_, err := f.uc.OrgStatus(permissionCtx(3), "acme")
		require.EqualError(t, err, "db")
		f.orgs.EXPECT().IsOrgMember(gomock.Any(), maintOrg.ID).Return(true, nil)
		f.orgs.EXPECT().IsOrgOwner(gomock.Any(), maintOrg.ID).Return(false, errors.New("db"))
		_, err = f.uc.OrgStatus(permissionCtx(3), "acme")
		require.EqualError(t, err, "db")
	})
	t.Run("project reader", func(t *testing.T) {
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), maintProject.ID, domain.PermissionRead).Return(true)
		st, err := f.uc.ProjectStatus(permissionCtx(3), "acme", "game")
		require.NoError(t, err)
		require.Equal(t, maintenance.ReadOnly, st.Server.Mode)
		require.Equal(t, maintenance.Full, st.Org.Mode)
		require.Equal(t, "acme/game", st.Project.Project)
	})
	t.Run("project without read", func(t *testing.T) {
		f.resolver.EXPECT().ResolveBySlug(gomock.Any(), "acme", "game").Return(maintOrg, maintProject, nil)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), maintProject.ID, domain.PermissionRead).Return(false)
		_, err := f.uc.ProjectStatus(permissionCtx(3), "acme", "game")
		requireCode(t, err, 403)
	})
}
