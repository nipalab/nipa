package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/maintenance"
)

func TestMaintenanceStatus_Refusal(t *testing.T) {
	var none *MaintenanceStatus
	require.Nil(t, none.Windows())
	require.Nil(t, none.Refusal())
	require.Nil(t, none.NextStart())

	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	st := &MaintenanceStatus{
		Server:  &MaintenanceWindow{Mode: "read_only", Active: true, Message: "storage"},
		Org:     &MaintenanceWindow{Mode: "full", Active: false, StartsAt: ends.Add(-2 * time.Hour), Org: "acme"},
		Project: &MaintenanceWindow{Mode: "read_only", Active: true, Org: "acme", Project: "acme/game", EndsAt: &ends},
	}
	require.Len(t, st.Windows(), 3)
	refused := st.Refusal()
	require.NotNil(t, refused)
	require.Equal(t, 503, refused.Code)
	require.Equal(t, "server", refused.Maintenance.Scope, "a tie reports the broadest scope")
	require.Equal(t, "server is in maintenance (read-only): storage", refused.Message)

	st.Org.Active = true
	refused = st.Refusal()
	require.Equal(t, "organization acme is in maintenance (closed)", refused.Message)

	st.Server, st.Org.Active = nil, false
	require.Equal(t, "project", st.Refusal().Maintenance.Scope)
	require.Same(t, st.Org, st.NextStart())

	st.Project.Active = false
	require.Nil(t, st.Refusal())
	require.Same(t, st.Org, st.NextStart(), "a scheduled window without a start is skipped")
	st.Project.StartsAt = ends.Add(-3 * time.Hour)
	require.Same(t, st.Project, st.NextStart(), "the earliest start wins")
}

func TestMaintenanceWindow_Describe(t *testing.T) {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	starts := ends.Add(-time.Hour)
	cases := []struct {
		w    MaintenanceWindow
		want string
	}{
		{MaintenanceWindow{Mode: "read_only", Active: true}, "server read-only"},
		{MaintenanceWindow{Mode: "full", Active: true, EndsAt: &ends, Message: "db"}, "server closed until ~2026-11-15 14:00 UTC: db"},
		{MaintenanceWindow{Mode: "frozen", StartsAt: starts, Org: "acme"}, "organization acme frozen from 2026-11-15 13:00 UTC (scheduled)"},
		{MaintenanceWindow{Mode: "read_only", Active: true, StartsAt: starts, Org: "acme", Project: "acme/game"}, "project acme/game read-only"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tc.w.Describe())
	}
}

func TestNewMaintenanceError(t *testing.T) {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	info := maintenance.Info{Scope: maintenance.ScopeProject, Mode: maintenance.Full, Org: "acme", Project: "acme/game", EndsAt: &ends}
	err := NewMaintenanceError(info, nil)
	require.True(t, IsMaintenance(err))
	require.Equal(t, "project acme/game is in maintenance (closed)", err.Message)
	require.Equal(t, "try again after ~"+ends.Local().Format(time.DateTime), err.Hint)
	back, ok := maintenance.FromGRPC(err)
	require.True(t, ok, "a nil cause is replaced by the gRPC refusal")
	require.Equal(t, "acme/game", back.Info.Project)

	require.Equal(t, "try again later", MaintenanceHint(maintenance.Info{Mode: maintenance.Full}))
	require.Equal(t, "read-only commands still work", MaintenanceHint(maintenance.Info{Mode: maintenance.ReadOnly}))
	require.Contains(t, MaintenanceHint(maintenance.Info{Mode: maintenance.Frozen}), "sign-in is paused")

	text := err.Maintenance.Describe(err.Message, err.Hint)
	require.Equal(t, "project acme/game is in maintenance (closed)\nestimated end: 2026-11-15 14:00 UTC (maintenance ends manually)\nhint: "+err.Hint, text)
	require.Equal(t, "msg", (&MaintenanceInfo{}).Describe("msg", ""))
}
