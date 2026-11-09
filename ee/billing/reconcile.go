package billing

import (
	"context"
	"errors"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const reconcilePageSize = 500

// ReconcileQuotas recomputes every stored owner quota from the current
// entitlement settings and returns how many changed. Deleted users are
// skipped and the admin override is never touched. Changes that landed are
// audited even when the run stops early.
func (s *Service) ReconcileQuotas(ctx context.Context) (int, error) {
	updated, err := s.reconcileAll(ctx)
	if updated == 0 {
		return 0, err
	}
	metadata := map[string]any{"updated": updated}
	if err != nil {
		metadata["complete"] = false
	}
	recordErr := s.record(context.WithoutCancel(ctx), domain.AuditEntry{
		ActorType: domain.AuditActorSystem,
		Action:    domain.AuditBillingQuotasReconciled,
		Metadata:  metadata,
	})
	return updated, errors.Join(err, recordErr)
}

func (s *Service) reconcileAll(ctx context.Context) (int, error) {
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
			return updated, nil
		}
		after = owners[len(owners)-1]
	}
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
