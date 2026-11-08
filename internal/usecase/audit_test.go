package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// captureRecorder collects entries and runs writes in a counted pseudo
// transaction.
type captureRecorder struct {
	entries []domain.AuditEntry
	err     error
	txs     int
}

func (c *captureRecorder) Record(_ context.Context, entry domain.AuditEntry) error {
	if c.err != nil {
		return c.err
	}
	c.entries = append(c.entries, entry)
	return nil
}

func (c *captureRecorder) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	c.txs++
	return fn(ctx)
}

func (c *captureRecorder) only(t *testing.T) domain.AuditEntry {
	t.Helper()
	require.Len(t, c.entries, 1)
	return c.entries[0]
}

func newTestAudit(t *testing.T) (*Audit, *MockauditRepository, *MockauditProjectOrgs) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockauditRepository(ctrl)
	projects := NewMockauditProjectOrgs(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	audit := NewAudit(repo, node, nil).WithProjectOrgs(projects)
	audit.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)) }
	return audit, repo, projects
}

func TestAudit_RecordFillsActorAndRequestMeta(t *testing.T) {
	audit, repo, projects := newTestAudit(t)
	ctx := domain.ContextWithRequestMeta(permissionCtx(7), domain.RequestMeta{ClientIP: "10.0.0.1", UserAgent: "nipa/1"})
	projects.EXPECT().ProjectOrgID(gomock.Any(), snow.ID(5)).Return(snow.ID(3), nil)
	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, entry domain.AuditEntry) error {
		require.NotZero(t, entry.ID)
		require.Equal(t, domain.AuditActorUser, entry.ActorType)
		require.Equal(t, snow.ID(7), *entry.ActorUserID)
		require.Equal(t, snow.ID(3), *entry.OrgID)
		require.Equal(t, "10.0.0.1", entry.ClientIP)
		require.Equal(t, "nipa/1", entry.UserAgent)
		require.Equal(t, time.UTC, entry.OccurredAt.Location())
		require.Equal(t, map[string]any{}, entry.Metadata)
		return nil
	})

	require.NoError(t, audit.Record(ctx, domain.AuditEntry{Action: domain.AuditProjectUpdated, ProjectID: domain.IDPtr(5)}))
}

func TestAudit_RecordSystemActorAndLookupErrors(t *testing.T) {
	audit, repo, projects := newTestAudit(t)

	projects.EXPECT().ProjectOrgID(gomock.Any(), snow.ID(5)).Return(snow.ID(0), domain.NewErrorNotFound("gone"))
	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, entry domain.AuditEntry) error {
		require.Equal(t, domain.AuditActorSystem, entry.ActorType)
		require.Nil(t, entry.ActorUserID)
		require.Nil(t, entry.OrgID)
		return nil
	})
	require.NoError(t, audit.Record(context.Background(), domain.AuditEntry{Action: domain.AuditProjectDeleted, ProjectID: domain.IDPtr(5)}))

	boom := errors.New("db down")
	projects.EXPECT().ProjectOrgID(gomock.Any(), snow.ID(5)).Return(snow.ID(0), boom)
	require.ErrorIs(t, audit.Record(context.Background(), domain.AuditEntry{ProjectID: domain.IDPtr(5)}), boom)

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, entry domain.AuditEntry) error {
		require.Equal(t, domain.AuditActorProvider, entry.ActorType)
		require.Equal(t, "paddle", entry.ActorLabel)
		return nil
	})
	require.NoError(t, audit.Record(permissionCtx(7), domain.AuditEntry{ActorType: domain.AuditActorProvider, ActorLabel: "paddle"}))
}

func TestAudit_WithinTx(t *testing.T) {
	audit, _, _ := newTestAudit(t)
	called := false
	require.NoError(t, audit.WithinTx(context.Background(), func(context.Context) error {
		called = true
		return nil
	}))
	require.True(t, called)

	ctrl := gomock.NewController(t)
	tx := NewMocktransactor(ctrl)
	tx.EXPECT().WithinTx(gomock.Any(), gomock.Any()).Return(errors.New("tx"))
	audit.tx = tx
	require.EqualError(t, audit.WithinTx(context.Background(), func(context.Context) error { return nil }), "tx")
}

func TestAudit_List(t *testing.T) {
	audit, repo, _ := newTestAudit(t)

	_, _, err := audit.List(permissionCtx(7), AuditListOptions{})
	require.True(t, domain.IsErrorNoPermission(err))
	_, _, err = audit.List(context.Background(), AuditListOptions{})
	require.True(t, domain.IsErrorNoPermission(err))

	super := permissionCtx(1, withSuperAdmin())
	since := time.Now()
	_, _, err = audit.List(super, AuditListOptions{Since: &since, Until: &since})
	require.Error(t, err)

	actor := snow.ID(7)
	repo.EXPECT().List(gomock.Any(), domain.AuditFilter{ActorUserID: &actor, ActionPrefix: "org.member", TargetID: "x", Limit: 3}).
		Return([]*domain.AuditEntry{{ID: 9}, {ID: 8}, {ID: 7}}, nil)
	entries, next, err := audit.List(super, AuditListOptions{Actor: &actor, ActionPrefix: " Org.Member ", Target: " x ", Limit: 2})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, snow.ID(8).Base36(), next)

	repo.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, f domain.AuditFilter) ([]*domain.AuditEntry, error) {
		require.Equal(t, int64(defaultAuditPageSize+1), f.Limit)
		return nil, nil
	})
	entries, next, err = audit.List(super, AuditListOptions{})
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NotNil(t, entries)
	require.Empty(t, next)

	repo.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, f domain.AuditFilter) ([]*domain.AuditEntry, error) {
		require.Equal(t, int64(maxAuditPageSize+1), f.Limit)
		return nil, errors.New("boom")
	})
	_, _, err = audit.List(super, AuditListOptions{Limit: 1000})
	require.Error(t, err)
}

func TestAuditedWrite(t *testing.T) {
	ctx := context.Background()
	write := func(context.Context) error { return nil }
	entry := func() *domain.AuditEntry { return &domain.AuditEntry{Action: "x"} }

	require.NoError(t, auditedWrite(ctx, nil, write, func() *domain.AuditEntry {
		t.Fatal("entry built without a recorder")
		return nil
	}))

	rec := &captureRecorder{}
	require.NoError(t, auditedWrite(ctx, rec, write, entry))
	require.Equal(t, 1, rec.txs)
	require.Equal(t, "x", rec.only(t).Action)

	rec = &captureRecorder{}
	require.NoError(t, auditedWrite(ctx, rec, write, func() *domain.AuditEntry { return nil }))
	require.Empty(t, rec.entries)

	rec = &captureRecorder{}
	require.EqualError(t, auditedWrite(ctx, rec, func(context.Context) error { return errors.New("write") }, entry), "write")
	require.Empty(t, rec.entries)

	ctrl := gomock.NewController(t)
	plain := NewMockAuditRecorder(ctrl)
	plain.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errors.New("record"))
	require.EqualError(t, auditedWrite(ctx, plain, write, entry), "record")
}

func TestAuditedWrite_Exported(t *testing.T) {
	rec := &captureRecorder{}
	require.NoError(t, AuditedWrite(context.Background(), rec, func(context.Context) error { return nil },
		func() *domain.AuditEntry { return &domain.AuditEntry{Action: "x"} }))
	require.Equal(t, 1, rec.txs)
	require.Equal(t, "x", rec.only(t).Action)
}
