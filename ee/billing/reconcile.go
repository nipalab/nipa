package billing

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const reconcilePageSize = 500

// ReconcileQuotas recomputes every stored owner quota from the current
// entitlement settings and returns how many changed. Deleted users are
// skipped and the admin override is never touched.
func (s *Service) ReconcileQuotas(ctx context.Context) (int, error) {
	updated := 0
	var after snow.ID
	for {
		owners, err := s.repo.QuotaOwners(ctx, after, reconcilePageSize)
		if err != nil {
			return updated, err
		}
		for _, owner := range owners {
			changed, err := s.reconcileOwner(ctx, owner)
			if domain.IsErrorNotFound(err) {
				continue
			}
			if err != nil {
				return updated, err
			}
			if changed {
				updated++
			}
		}
		if len(owners) < reconcilePageSize {
			break
		}
		after = owners[len(owners)-1]
	}
	if updated == 0 {
		return 0, nil
	}
	return updated, s.record(ctx, domain.AuditEntry{
		ActorType: domain.AuditActorSystem,
		Action:    domain.AuditBillingQuotasReconciled,
		Metadata:  map[string]any{"updated": updated},
	})
}

func (s *Service) reconcileOwner(ctx context.Context, owner snow.ID) (bool, error) {
	changed := false
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockOwner(ctx, owner); err != nil {
			return err
		}
		entitlement, err := s.Entitlement(ctx, owner)
		if err != nil {
			return err
		}
		current, err := s.ledger.ComputedQuota(ctx, owner)
		if err != nil {
			return err
		}
		if current != nil && *current == entitlement.QuotaBytes {
			return nil
		}
		if err := s.ledger.SetComputedQuota(ctx, owner, entitlement.QuotaBytes); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}
