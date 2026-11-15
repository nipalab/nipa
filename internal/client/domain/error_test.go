package domain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsMaintenance(t *testing.T) {
	require.False(t, IsMaintenance(nil))
	require.False(t, IsMaintenance(errors.New("boom")))
	require.False(t, IsMaintenance(NewUserError("bad")))
	refused := &Error{Code: 503, Message: "server is in maintenance (frozen)", Maintenance: &MaintenanceInfo{Scope: "server", Mode: "frozen"}}
	require.True(t, IsMaintenance(refused))
	require.True(t, IsMaintenance(fmt.Errorf("refresh: %w", refused)))
}
