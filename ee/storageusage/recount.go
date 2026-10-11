package storageusage

import (
	"context"

	"github.com/nipalab/nipa/internal/snow"
)

// RecountOrg rebuilds one organization's chunk links and usage counters from
// the stored history in a single transaction: it recomputes every project's
// link set and usage from the commit trees, then derives the org counter. It
// takes the same per-owner advisory lock as Attribute, so a recount cannot
// interleave with an in-flight confirm and leave the counters disagreeing with
// the links.
func (l *Ledger) RecountOrg(ctx context.Context, orgID snow.ID) error {
	return l.transactor.WithinTx(ctx, func(ctx context.Context) error {
		owner, err := l.billingOwner(ctx, orgID)
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
			if err := l.queries.DeleteProjectChunks(ctx, projectID); err != nil {
				return dbError(err)
			}
			if err := l.queries.RebuildProjectChunks(ctx, projectID); err != nil {
				return dbError(err)
			}
			if err := l.queries.SetProjectUsage(ctx, projectID); err != nil {
				return dbError(err)
			}
		}
		return dbError(l.queries.SetOrgUsageFromProjects(ctx, orgID.Int64()))
	})
}
