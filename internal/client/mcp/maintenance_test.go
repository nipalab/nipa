package mcp

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/maintenance"
)

func TestToolError_Maintenance(t *testing.T) {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	refused := clientDomain.NewMaintenanceError(maintenance.Info{Scope: maintenance.ScopeServer, Mode: maintenance.ReadOnly, EndsAt: &ends}, nil)
	err := toolError(refused)
	require.ErrorIs(t, err, refused)
	require.Equal(t, "server is in maintenance (read-only) (hint: read-only commands still work; next: retry after 2026-11-15T14:00:00Z (an estimate; maintenance ends manually))", err.Error())

	closed := clientDomain.NewMaintenanceError(maintenance.Info{Scope: maintenance.ScopeServer, Mode: maintenance.Full}, nil)
	require.Contains(t, toolError(closed).Error(), "next: retry later; maintenance ends manually")

	acted := clientDomain.NewMaintenanceError(maintenance.Info{Mode: maintenance.Full}, nil).WithHint("", "nipa status")
	require.Contains(t, toolError(acted).Error(), "next: nipa status")
	require.Nil(t, toolError(nil))
	plain := errors.New("plain")
	require.Same(t, plain, toolError(plain))
}
