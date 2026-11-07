package billing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

var _ usecase.SeatGate = (*Service)(nil)

// Admit runs insert in one transaction holding the org owner's billing lock,
// after checking that userID already holds a seat or a seat is free.
// A refusal is audited after the transaction, outside any transaction ctx
// carries, so it survives the rollback; a failed record is only logged.
func (s *Service) Admit(ctx context.Context, orgID, userID snow.ID, insert func(ctx context.Context) error) error {
	var denied *domain.AuditEntry
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		owner, err := s.ledger.BillingOwner(ctx, orgID)
		if err != nil {
			return err
		}
		if err := s.repo.LockOwner(ctx, owner); err != nil {
			return err
		}
		seat, err := s.repo.IsSeat(ctx, owner, userID)
		if err != nil {
			return err
		}
		if !seat {
			used, err := s.repo.CountSeats(ctx, owner)
			if err != nil {
				return err
			}
			entitlement, err := s.Entitlement(ctx, owner)
			if err != nil {
				return err
			}
			if used >= entitlement.SeatLimit {
				denied = &domain.AuditEntry{
					Action:     domain.AuditBillingSeatDenied,
					OrgID:      domain.IDPtr(orgID),
					TargetType: domain.AuditTargetUser,
					TargetID:   userID.Base36(),
					Metadata: map[string]any{
						"billing_owner_id": owner.Base36(),
						"used":             used,
						"limit":            entitlement.SeatLimit,
					},
				}
				return domain.NewErrorQuotaExceeded(fmt.Sprintf(
					"seat limit reached: %d of %d seats in use; upgrade the plan to add more people", used, entitlement.SeatLimit))
			}
		}
		return insert(ctx)
	})
	if denied != nil {
		if recErr := s.record(dbtx.Detach(ctx), *denied); recErr != nil {
			slog.Warn("audit seat refusal failed", "org_id", orgID.Base36(), "user_id", userID.Base36(), "error", recErr)
		}
	}
	return err
}
