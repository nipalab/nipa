package billing

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

var _ usecase.SeatGate = (*Service)(nil)

// Admit runs insert in one transaction holding the org owner's billing lock,
// after checking that userID already holds a seat or a seat is free.
func (s *Service) Admit(ctx context.Context, orgID, userID snow.ID, insert func(ctx context.Context) error) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
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
				return domain.NewErrorQuotaExceeded(fmt.Sprintf(
					"seat limit reached: %d of %d seats in use; upgrade the plan to add more people", used, entitlement.SeatLimit))
			}
		}
		return insert(ctx)
	})
}
