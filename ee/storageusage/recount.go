package storageusage

import (
	"context"

	"github.com/nipalab/nipa/internal/snow"
)

// RecountOrg rebuilds one organization's usage counters from its projects'
// chunk rows in a single transaction: every billed project's counter becomes
// the sum of its rows, then the org counter is derived. It takes the same
// per-owner advisory lock as Attribute, so a recount cannot interleave with an
// in-flight confirm and leave the counters disagreeing with the rows. Deleted
// projects and projects on their own storage server are never attributed:
// they count nothing.
func (l *Ledger) RecountOrg(ctx context.Context, orgID snow.ID) error {
	return l.transactor.WithinTx(ctx, func(ctx context.Context) error {
		owner, err := l.BillingOwner(ctx, orgID)
		if err != nil {
			return err
		}
		if err := l.queries.LockBillingOwner(ctx, owner.Int64()); err != nil {
			return dbError(err)
		}
		projects, err := l.queries.ListProjectIDsByOrg(ctx, orgID.Int64())
		if err != nil {
			return dbError(err)
		}
		for _, projectID := range projects {
			if err := l.queries.SetProjectUsage(ctx, projectID); err != nil {
				return dbError(err)
			}
		}
		unbilled, err := l.queries.ListUnbilledProjectIDsByOrg(ctx, orgID.Int64())
		if err != nil {
			return dbError(err)
		}
		for _, projectID := range unbilled {
			if err := l.queries.ResetProjectUsage(ctx, projectID); err != nil {
				return dbError(err)
			}
		}
		return dbError(l.queries.SetOrgUsageFromProjects(ctx, orgID.Int64()))
	})
}
