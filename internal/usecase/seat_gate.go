package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/snow"
)

// SeatGate admits a user into an organization's billed seats. Admit runs
// insert inside a transaction that holds the org owner's seat lock, after
// checking that userID already counts or a seat is free. A nil gate (free
// edition, billing off) runs insert directly.
//
//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=seat_gate_mock_test.go -package=usecase
type SeatGate interface {
	Admit(ctx context.Context, orgID, userID snow.ID, insert func(ctx context.Context) error) error
}

func admitSeat(ctx context.Context, gate SeatGate, orgID, userID snow.ID, insert func(ctx context.Context) error) error {
	if gate == nil {
		return insert(ctx)
	}
	return gate.Admit(ctx, orgID, userID, insert)
}
