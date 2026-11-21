package usecase

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func deletedAt(t time.Time) *time.Time { return &t }

func TestProject_ListDeleted_FiltersPastTheWindow(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	project.now = func() time.Time { return now }
	project.WithRestoreWindow(30 * 24 * time.Hour)
	require.Equal(t, 30*24*time.Hour, project.RestoreWindow())

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().ListDeletedByOrgID(gomock.Any(), snow.ID(1)).Return([]domain.Project{
		{ID: 10, OrgID: 1, Slug: "recent", DeletedAt: deletedAt(now.Add(-24 * time.Hour))},
		{ID: 11, OrgID: 1, Slug: "expired", DeletedAt: deletedAt(now.Add(-31 * 24 * time.Hour))},
		{ID: 12, OrgID: 1, Slug: "unknown"},
	}, nil)

	got, err := project.ListDeleted(permissionCtx(42), snow.ID(1))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "recent", got[0].Slug)
}

func TestProject_ListDeleted_UnlimitedWindowListsAll(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().ListDeletedByOrgID(gomock.Any(), snow.ID(1)).Return([]domain.Project{
		{ID: 10, OrgID: 1, DeletedAt: deletedAt(time.Unix(0, 0))}, {ID: 11, OrgID: 1},
	}, nil)

	got, err := project.ListDeleted(permissionCtx(42), snow.ID(1))
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestProject_ListDeleted_Errors(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(false, nil)
	_, err := project.ListDeleted(permissionCtx(42), snow.ID(1))
	require.True(t, domain.IsErrorNoPermission(err))

	errList := domain.NewErrorDatabase("boom")
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().ListDeletedByOrgID(gomock.Any(), snow.ID(1)).Return(nil, errList)
	_, err = project.ListDeleted(permissionCtx(42), snow.ID(1))
	require.ErrorIs(t, err, errList)
}

func TestProject_Restore_ChargesAndAudits(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	rec := &captureRecorder{}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	project.now = func() time.Time { return now }
	project.WithStorageLedger(ledger).WithAudit(rec).WithRestoreWindow(time.Hour)

	deleted := &domain.Project{ID: 10, OrgID: 1, Name: "Game", Slug: "game", Deleted: true}
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(deleted, nil)
	gomock.InOrder(
		repo.EXPECT().Restore(gomock.Any(), snow.ID(10), gomock.Any()).DoAndReturn(
			func(_ any, _ snow.ID, since *time.Time) (bool, error) {
				require.NotNil(t, since)
				require.Equal(t, now.Add(-time.Hour), *since, "only projects inside the window come back")
				return true, nil
			}),
		ledger.EXPECT().RestoreProject(gomock.Any(), snow.ID(10)).Return(int64(2048), nil),
	)
	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1, Slug: "game"}, nil)

	restored, err := project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.NoError(t, err)
	require.Equal(t, "game", restored.Slug)
	require.Equal(t, 1, rec.txs)
	entry := rec.only(t)
	require.Equal(t, domain.AuditProjectRestored, entry.Action)
	require.Equal(t, int64(2048), entry.Metadata["charged_bytes"])
}

func TestProject_Restore_QuotaErrorRestoresNothing(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	rec := &captureRecorder{}
	project.WithStorageLedger(ledger).WithAudit(rec)
	errQuota := domain.NewErrorQuotaExceeded("full")

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	repo.EXPECT().Restore(gomock.Any(), snow.ID(10), (*time.Time)(nil)).Return(true, nil)
	ledger.EXPECT().RestoreProject(gomock.Any(), snow.ID(10)).Return(int64(0), errQuota)

	_, err := project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.ErrorIs(t, err, errQuota)
	require.Empty(t, rec.entries)
}

func TestProject_Restore_PastTheWindow(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	project.WithRestoreWindow(time.Hour)

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	repo.EXPECT().Restore(gomock.Any(), snow.ID(10), gomock.Not(gomock.Nil())).Return(false, nil)

	_, err := project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.True(t, domain.IsErrorNotFound(err), "got %v", err)
}

func TestProject_Restore_WithoutLedger(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	rec := &captureRecorder{}
	project.WithAudit(rec)

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	repo.EXPECT().Restore(gomock.Any(), snow.ID(10), (*time.Time)(nil)).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)

	_, err := project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.NoError(t, err)
	require.NotContains(t, rec.only(t).Metadata, "charged_bytes")
}

func TestProject_Restore_Errors(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(false, nil)
	_, err := project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.True(t, domain.IsErrorNoPermission(err))

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(nil, domain.NewErrorRecordNotFound())
	_, err = project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.True(t, domain.IsErrorNotFound(err))

	errRestore := domain.NewErrorDatabase("boom")
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetDeleted(gomock.Any(), snow.ID(1), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	repo.EXPECT().Restore(gomock.Any(), snow.ID(10), gomock.Any()).Return(false, errRestore)
	_, err = project.Restore(permissionCtx(42), snow.ID(1), snow.ID(10))
	require.ErrorIs(t, err, errRestore)
}
