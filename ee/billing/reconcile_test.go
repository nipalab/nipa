package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func quota(v int64) *int64 { return &v }

func reconcileService() (*Service, *stubRepo, *stubLedger, *stubTx, *captureRecorder) {
	svc, repo, ledger, tx := newStubService()
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	ledger.stored = map[snow.ID]*int64{}
	return svc, repo, ledger, tx, rec
}

func TestReconcileQuotas_NoChangeRecordsNothing(t *testing.T) {
	svc, repo, ledger, tx, rec := reconcileService()
	repo.owners = []snow.ID{1, 2}
	ledger.stored[1] = quota(testConfig.FreeQuotaBytes)
	ledger.stored[2] = quota(testConfig.FreeQuotaBytes)

	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Zero(t, updated)
	require.Empty(t, ledger.quotas)
	require.Empty(t, rec.entries)
	require.Equal(t, 2, tx.began, "one transaction per owner")
	require.Equal(t, []snow.ID{1, 2}, repo.locked)
}

func TestReconcileQuotas_UpdatesDifferingAndMissing(t *testing.T) {
	svc, repo, ledger, _, rec := reconcileService()
	repo.owners = []snow.ID{1, 2, 3}
	ledger.stored[1] = quota(10)
	ledger.stored[2] = nil
	ledger.stored[3] = quota(testConfig.FreeQuotaBytes)

	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, updated)
	require.Equal(t, map[snow.ID]int64{1: testConfig.FreeQuotaBytes, 2: testConfig.FreeQuotaBytes}, ledger.quotas)
	require.Len(t, rec.entries, 1)
	entry := rec.entries[0]
	require.Equal(t, domain.AuditBillingQuotasReconciled, entry.Action)
	require.Equal(t, domain.AuditActorSystem, entry.ActorType)
	require.Equal(t, map[string]any{"updated": 2}, entry.Metadata)
	require.False(t, rec.inTx[0], "recorded once after the per-owner transactions")
}

func TestReconcileQuotas_UsesLiveSubscription(t *testing.T) {
	svc, repo, ledger, _, _ := reconcileService()
	repo.owners = []snow.ID{4}
	repo.live = &Subscription{Status: StatusActive, Seats: 5, StoragePacks: 1}
	ledger.stored[4] = quota(testConfig.FreeQuotaBytes)

	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.Equal(t, testConfig.Entitlement(repo.live).QuotaBytes, ledger.quotas[4])
}

func TestReconcileQuotas_SkipsDeletedUsers(t *testing.T) {
	svc, repo, ledger, _, rec := reconcileService()
	repo.owners = []snow.ID{1, 2}
	ledger.stored[1] = quota(10)
	ledger.stored[2] = quota(10)
	ledger.deleted = map[snow.ID]bool{1: true}

	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.Equal(t, map[snow.ID]int64{2: testConfig.FreeQuotaBytes}, ledger.quotas)
	require.Len(t, rec.entries, 1)
}

func TestReconcileQuotas_PagesThroughEveryOwner(t *testing.T) {
	svc, repo, ledger, _, _ := reconcileService()
	for i := 1; i <= reconcilePageSize+3; i++ {
		repo.owners = append(repo.owners, snow.ID(i))
	}

	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcilePageSize+3, updated)
	require.Len(t, ledger.quotas, reconcilePageSize+3)
	require.Equal(t, 2, repo.ownerPages)
}

func TestReconcileQuotas_ExactPageQueriesTheNextPage(t *testing.T) {
	svc, repo, _, _, _ := reconcileService()
	for i := 1; i <= reconcilePageSize; i++ {
		repo.owners = append(repo.owners, snow.ID(i))
	}
	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcilePageSize, updated)
	require.Equal(t, 2, repo.ownerPages, "a full page is followed by an empty one")
}

func TestReconcileQuotas_WithoutAudit(t *testing.T) {
	svc, repo, ledger, _ := newStubService()
	repo.owners = []snow.ID{1}
	updated, err := svc.ReconcileQuotas(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	require.Equal(t, testConfig.FreeQuotaBytes, ledger.quotas[1])
}

func TestReconcileQuotas_Errors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*stubRepo, *stubLedger, *stubTx, *captureRecorder){
		"list":     func(r *stubRepo, _ *stubLedger, _ *stubTx, _ *captureRecorder) { r.ownersErr = boom },
		"tx":       func(_ *stubRepo, _ *stubLedger, tx *stubTx, _ *captureRecorder) { tx.err = boom },
		"lock":     func(r *stubRepo, _ *stubLedger, _ *stubTx, _ *captureRecorder) { r.lockErr = boom },
		"live":     func(r *stubRepo, _ *stubLedger, _ *stubTx, _ *captureRecorder) { r.liveErr = boom },
		"computed": func(_ *stubRepo, l *stubLedger, _ *stubTx, _ *captureRecorder) { l.compErr = boom },
		"set":      func(_ *stubRepo, l *stubLedger, _ *stubTx, _ *captureRecorder) { l.quotaErr = boom },
		"audit":    func(_ *stubRepo, _ *stubLedger, _ *stubTx, rec *captureRecorder) { rec.err = boom },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, ledger, tx, rec := reconcileService()
			repo.owners = []snow.ID{1}
			setup(repo, ledger, tx, rec)
			_, err := svc.ReconcileQuotas(context.Background())
			require.ErrorIs(t, err, boom)
		})
	}
}

type ctxRecorder struct {
	captureRecorder
	ctxErr error
}

func (c *ctxRecorder) Record(ctx context.Context, entry domain.AuditEntry) error {
	c.ctxErr = ctx.Err()
	return c.captureRecorder.Record(ctx, entry)
}

func TestReconcileQuotas_PartialFailureStillAudits(t *testing.T) {
	svc, repo, ledger, _ := newStubService()
	rec := &ctxRecorder{}
	svc.WithAudit(rec)
	boom := errors.New("boom")
	repo.owners = []snow.ID{1, 2, 3}
	ledger.failFor = map[snow.ID]error{2: boom}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	updated, err := svc.ReconcileQuotas(ctx)
	require.ErrorIs(t, err, boom)
	require.Equal(t, 1, updated)
	require.Len(t, rec.entries, 1)
	require.Equal(t, map[string]any{"updated": 1, "complete": false}, rec.entries[0].Metadata)
	require.NoError(t, rec.ctxErr, "the audit entry survives a cancelled run")
}

func TestReconcileQuotas_PartialFailureAndAuditFailure(t *testing.T) {
	svc, repo, ledger, _, rec := reconcileService()
	boom, auditErr := errors.New("boom"), errors.New("audit")
	repo.owners = []snow.ID{1, 2}
	ledger.failFor = map[snow.ID]error{2: boom}
	rec.err = auditErr

	_, err := svc.ReconcileQuotas(context.Background())
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, auditErr)
}
