package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/maintenance"
)

type maintenanceProbe interface {
	GetMaintenanceStatus(ctx context.Context, org, project string) (*domain.MaintenanceStatus, error)
}

// signInPaused returns the maintenance refusal when the server is frozen, the
// only mode that refuses sign-in, so callers never prompt for a password the
// server will refuse. A failed or unsupported probe is no answer.
func signInPaused(ctx context.Context, executor any) error {
	probe, ok := executor.(maintenanceProbe)
	if !ok {
		return nil
	}
	st, err := probe.GetMaintenanceStatus(ctx, "", "")
	if err != nil || st == nil || st.Server == nil || !st.Server.Active || st.Server.Mode != string(maintenance.Frozen) {
		return nil
	}
	return domain.NewMaintenanceError(st.Server.Info(), nil)
}
