package grpc

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/maintenance"
)

func refusal(w maintenance.Window, scope maintenance.Scope) error {
	return maintenance.NewError(w, scope).GRPCError(time.Now())
}

func TestToDomainError_Maintenance(t *testing.T) {
	ends := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	starts := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	raw := refusal(maintenance.Window{Mode: maintenance.Frozen, Message: "moving the database", StartsAt: starts, EndsAt: &ends}, maintenance.ScopeServer)

	err := toDomainError(raw)
	require.True(t, domain.IsMaintenance(err))
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Equal(t, 503, de.Code)
	require.Equal(t, "server is in maintenance (frozen): moving the database", de.Message)
	require.Contains(t, de.Hint, "sign-in is paused")
	require.Equal(t, &domain.MaintenanceInfo{Scope: "server", Mode: "frozen", Message: "moving the database", StartsAt: starts, EndsAt: &ends}, de.Maintenance)
	again, ok := maintenance.FromGRPC(err)
	require.True(t, ok, "the gRPC detail stays reachable through the cause")
	require.Equal(t, maintenance.Frozen, again.Info.Mode)
	require.Same(t, err, toDomainError(err), "a domain error passes through")
	require.True(t, domain.IsMaintenance(toDomainError(fmt.Errorf("wrapped: %w", raw))))
}

func TestMaintenanceHints(t *testing.T) {
	ends := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		w     maintenance.Window
		scope maintenance.Scope
		hint  string
	}{
		{maintenance.Window{Mode: maintenance.ReadOnly}, maintenance.ScopeServer, "read-only commands still work"},
		{maintenance.Window{Mode: maintenance.Full}, maintenance.ScopeServer, "try again later"},
		{maintenance.Window{Mode: maintenance.Full, EndsAt: &ends}, maintenance.ScopeServer, "try again after ~" + ends.Local().Format(time.DateTime)},
		{maintenance.Window{Mode: maintenance.Full, Org: "acme", Project: "game"}, maintenance.ScopeProject, "try again later"},
	}
	for _, tc := range cases {
		de, ok := maintenanceError(refusal(tc.w, tc.scope))
		require.True(t, ok)
		require.Equal(t, tc.hint, de.Hint)
		require.Equal(t, string(tc.scope), de.Maintenance.Scope)
	}
	_, ok := maintenanceError(status.Error(codes.Unavailable, "network down"))
	require.False(t, ok)
	_, ok = maintenanceError(errors.New("plain"))
	require.False(t, ok)
}
