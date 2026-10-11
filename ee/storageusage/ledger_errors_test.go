package storageusage

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func newErrorLedger(t *testing.T, drop ...string) (*sql.DB, *Ledger) {
	t.Helper()
	db := newTestDB(t)
	for _, table := range drop {
		_, err := db.ExecContext(context.Background(), "DROP TABLE "+table+" CASCADE")
		require.NoError(t, err)
	}
	return db, NewLedger(db, Config{})
}

func TestLedger_AttributedSizes_Empty(t *testing.T) {
	_, ledger := newErrorLedger(t)
	sizes, err := ledger.AttributedSizes(context.Background(), snow.ID(1), nil)
	require.NoError(t, err)
	require.Empty(t, sizes)
}

func TestLedger_AttributedSizes_DatabaseError(t *testing.T) {
	_, ledger := newErrorLedger(t, "project_chunks")
	_, err := ledger.AttributedSizes(context.Background(), snow.ID(1), []domain.Hash{{1}})
	require.Equal(t, 500, errorCode(err))
}

func TestLedger_Headroom_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: 1}

	_, ledger := newErrorLedger(t)
	require.NoError(t, ledger.Headroom(ctx, org, project, 0), "nothing projected, nothing to check")

	_, ledger = newErrorLedger(t)
	require.Equal(t, 500, errorCode(ledger.Headroom(ctx, &domain.Organization{ID: 999}, project, 10)), "unknown org")

	_, ledger = newErrorLedger(t, "org_usage")
	quota := int64(100)
	require.NoError(t, ledger.SetOwnerQuota(ctx, snow.ID(1), &quota))
	require.Equal(t, 500, errorCode(ledger.Headroom(ctx, org, project, 10)), "usage lookup error")

	_, ledger = newErrorLedger(t, "owner_quota")
	require.Equal(t, 500, errorCode(ledger.Headroom(ctx, org, project, 10)), "quota lookup error")
}

func TestLedger_Attribute_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	org := &domain.Organization{ID: 1}
	project := &domain.Project{ID: 1}
	chunk := usecase.LedgerChunk{Hash: domain.Hash{1}, SizeBytes: 1}

	_, ledger := newErrorLedger(t)
	require.NoError(t, ledger.Attribute(ctx, org, project, nil), "empty batch")
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, &domain.Organization{ID: 999}, project, []usecase.LedgerChunk{chunk})), "unknown org")

	_, ledger = newErrorLedger(t, "org_usage")
	quota := int64(1000)
	require.NoError(t, ledger.SetOwnerQuota(ctx, snow.ID(1), &quota))
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{chunk})), "usage lookup error")

	_, ledger = newErrorLedger(t, "chunks")
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{chunk})), "chunk lookup error")

	db, ledger := newErrorLedger(t, "project_chunks")
	hash := seedAPIChunk(t, db, 10)
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})), "link insert error")

	db, ledger = newErrorLedger(t, "project_usage")
	hash = seedAPIChunk(t, db, 10)
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})), "project counter error")

	db, ledger = newErrorLedger(t, "org_usage")
	hash = seedAPIChunk(t, db, 10)
	require.Equal(t, 500, errorCode(ledger.Attribute(ctx, org, project, []usecase.LedgerChunk{{Hash: hash, SizeBytes: 10}})), "org counter error")
}

func TestLedger_AttributeProjectChunks_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	_, ledger := newErrorLedger(t)
	require.NoError(t, ledger.AttributeProjectChunks(ctx, snow.ID(1), nil), "empty batch")
	require.Equal(t, 500, errorCode(ledger.AttributeProjectChunks(ctx, snow.ID(999), []usecase.LedgerChunk{{Hash: domain.Hash{1}, SizeBytes: 1}})), "unknown project")
}

func TestLedger_OrgStorage_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	_, ledger := newErrorLedger(t)
	_, err := ledger.OrgStorage(ctx, snow.ID(999))
	require.Equal(t, 500, errorCode(err), "unknown org")

	_, ledger = newErrorLedger(t, "org_usage")
	_, err = ledger.OrgStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "org usage error")

	_, ledger = newErrorLedger(t, "project_usage")
	_, err = ledger.OrgStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "project usage error")
}

func TestLedger_OwnerStorage_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	_, ledger := newErrorLedger(t, "org_usage")
	_, err := ledger.OwnerStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "owner usage error")

	_, ledger = newErrorLedger(t, "org_billing_owner")
	_, err = ledger.OwnerStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "billing owner lookup error")

	_, ledger = newErrorLedger(t, "organizations")
	_, err = ledger.OwnerStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "org list error")
}

func TestLedger_BillingOwner_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	_, ledger := newErrorLedger(t, "org_billing_owner")
	_, err := ledger.OrgStorage(ctx, snow.ID(1))
	require.Equal(t, 500, errorCode(err), "owner mapping lookup error")
}

func TestLedger_RecountOrg_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	_, ledger := newErrorLedger(t, "projects")
	require.Equal(t, 500, errorCode(ledger.RecountOrg(ctx, snow.ID(1))), "project listing error")

	_, ledger = newErrorLedger(t, "project_chunks")
	require.Equal(t, 500, errorCode(ledger.RecountOrg(ctx, snow.ID(1))), "link delete error")

	_, ledger = newErrorLedger(t, "project_usage")
	require.Equal(t, 500, errorCode(ledger.RecountOrg(ctx, snow.ID(1))), "project counter error")
}

func TestLedger_SetBillingOwner_UserLookupError(t *testing.T) {
	_, ledger := newErrorLedger(t, "users")
	require.Equal(t, 500, errorCode(ledger.SetBillingOwner(context.Background(), snow.ID(1), snow.ID(1))))
}

func TestLedger_Attribute_ConcurrentQuota(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	ledger := NewLedger(db, Config{})

	quota := int64(150)
	require.NoError(t, ledger.SetOwnerQuota(ctx, snow.ID(1), &quota))

	projectA := snow.ID(1)
	projectB := snow.ID(testIDCounter.Add(1) + 60000)
	_, err := db.ExecContext(ctx,
		`INSERT INTO projects (id, org_id, slug, name) VALUES ($1, 1, 'race-project', 'race')`,
		projectB.Int64(),
	)
	require.NoError(t, err)

	hashA := seedAPIChunk(t, db, 100)
	hashB := seedAPIChunk(t, db, 100)
	org := &domain.Organization{ID: 1}

	results := make(chan error, 2)
	go func() {
		results <- ledger.Attribute(ctx, org, &domain.Project{ID: projectA}, []usecase.LedgerChunk{{Hash: hashA, SizeBytes: 100}})
	}()
	go func() {
		results <- ledger.Attribute(ctx, org, &domain.Project{ID: projectB}, []usecase.LedgerChunk{{Hash: hashB, SizeBytes: 100}})
	}()

	first, second := <-results, <-results
	quotaErrors := 0
	for _, err := range []error{first, second} {
		if isQuotaExceeded(err) {
			quotaErrors++
		} else {
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, quotaErrors, "exactly one concurrent confirm may pass the quota check")
	var used int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT used_bytes FROM org_usage WHERE org_id = 1), 0)`).Scan(&used))
	require.Equal(t, int64(100), used)
}
