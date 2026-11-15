package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/serverapp"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func TestEndToEnd_MaintenanceWindowsPropagate(t *testing.T) {
	dbConn := openTestDB(t)
	_, uc := startTestServerWithUsecases(t, dbConn)
	ctx := serverDomain.ContextWithClaim(context.Background(), serverDomain.Claims{UserID: 1, IsSuperAdmin: true})

	_, err := uc.Maintenance.SetServer(ctx, serverusecase.MaintenanceInput{Mode: "read_only", Message: "backup"})
	require.NoError(t, err)
	_, err = uc.Maintenance.SetProject(ctx, "default", "default", serverusecase.MaintenanceInput{Mode: "full"})
	require.NoError(t, err)

	w, scope := uc.Maintenance.Service().For("default", "default")
	require.Equal(t, maintenance.Full, w.Mode, "the writer applies its change locally")
	require.Equal(t, maintenance.ScopeProject, scope)

	other, poller := serverapp.NewMaintenance(&config.Config{}, sqlite.NewMaintenanceRepository(dbConn), nil)
	require.NoError(t, poller.Poll(context.Background()))
	w, scope = other.For("default", "default")
	require.Equal(t, maintenance.Full, w.Mode, "another instance catches up on its poll")
	require.Equal(t, maintenance.ScopeProject, scope)
	w, _ = other.For("", "")
	require.Equal(t, maintenance.ReadOnly, w.Mode)

	require.NoError(t, uc.Maintenance.EndProject(ctx, "default", "default"))
	require.NoError(t, uc.Maintenance.EndServer(ctx))
	require.NoError(t, poller.Poll(context.Background()))
	w, scope = other.For("default", "default")
	require.Equal(t, maintenance.Off, w.Mode)
	require.Empty(t, scope)
}
