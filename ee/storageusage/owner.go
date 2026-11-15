package storageusage

import (
	"context"
	"database/sql"
	"errors"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/snow"
)

// BillingOwner resolves the owner an organization is billed to, seeding the
// mapping lazily from the organization's creator on first use. The upsert
// keeps a concurrent transfer: it never overwrites an existing mapping.
func (l *Ledger) BillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, error) {
	owner, stored, err := l.lookupBillingOwner(ctx, orgID)
	if err != nil || stored {
		return owner, err
	}
	seeded, err := l.queries.EnsureBillingOwner(ctx, sqlcPostgres.EnsureBillingOwnerParams{
		OrgID:  orgID.Int64(),
		UserID: owner.Int64(),
	})
	if err != nil {
		return 0, dbError(err)
	}
	return snow.ID(seeded), nil
}

// lookupBillingOwner reads the mapping and falls back to the organization's
// creator without storing it, so reports never write.
func (l *Ledger) lookupBillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, bool, error) {
	owner, err := l.queries.GetBillingOwner(ctx, orgID.Int64())
	if err == nil {
		return snow.ID(owner), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, dbError(err)
	}
	creator, err := l.queries.GetOrgCreatedBy(ctx, orgID.Int64())
	if err != nil {
		return 0, false, dbError(err)
	}
	return snow.ID(creator), false, nil
}

// ownerQuota resolves an organization's billing owner and that owner's
// effective quota; zero means unlimited.
func (l *Ledger) ownerQuota(ctx context.Context, orgID snow.ID) (snow.ID, int64, error) {
	owner, err := l.BillingOwner(ctx, orgID)
	if err != nil {
		return 0, 0, err
	}
	quota, err := l.ownerQuotaBytes(ctx, owner)
	if err != nil {
		return 0, 0, err
	}
	return owner, quota, nil
}
