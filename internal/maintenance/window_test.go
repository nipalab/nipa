package maintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestWindowActive(t *testing.T) {
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	w := &Window{Mode: ReadOnly, StartsAt: start}
	require.False(t, w.Active(start.Add(-time.Nanosecond)))
	require.True(t, w.Active(start))
	require.True(t, w.Active(start.Add(time.Hour)))

	end := start.Add(time.Minute)
	w.EndsAt = &end
	require.True(t, w.Active(end.Add(time.Hour)), "ends_at never ends a window")

	var nilWindow *Window
	require.False(t, nilWindow.Active(start))
	require.False(t, (&Window{Mode: Off}).Active(start))
}

func TestWindowStatusAndKey(t *testing.T) {
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	pid := snow.ID(7)
	project := &Window{ProjectID: &pid, Org: "acme", Project: "game", Mode: Full, Message: "m", StartsAt: start, EndsAt: &end}
	require.Equal(t, "acme/game", project.Key())
	require.Equal(t, ScopeProject, project.Scope())
	require.Equal(t, Status{Mode: Full, Active: true, Message: "m", StartsAt: start, EndsAt: &end, Org: "acme", Project: "acme/game"}, project.Status(start))

	oid := snow.ID(3)
	org := &Window{OrgID: &oid, Org: "acme", Mode: ReadOnly, StartsAt: start}
	require.Equal(t, ScopeOrg, org.Scope())
	require.Equal(t, "acme", org.Key())
	require.Equal(t, Status{Mode: ReadOnly, Active: true, StartsAt: start, Org: "acme"}, org.Status(start))

	server := &Window{Mode: ReadOnly, StartsAt: start}
	require.Equal(t, "", server.Key())
	require.Equal(t, ScopeServer, server.Scope())
	st := server.Status(start.Add(-time.Second))
	require.False(t, st.Active)
	require.Empty(t, st.Project)
}

func TestFromDomain(t *testing.T) {
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	pid := snow.ID(9)
	w := FromDomain(domain.MaintenanceWindow{
		ID: 1, ProjectID: &pid, OrgSlug: "acme", ProjectSlug: "game", Mode: "read_only",
		Message: "hi", StartsAt: start, Notify: true, CreatedBy: 3, UpdatedAt: start,
	})
	require.Equal(t, Window{
		ID: 1, ProjectID: &pid, Org: "acme", Project: "game", Mode: ReadOnly,
		Message: "hi", StartsAt: start, Notify: true, CreatedBy: 3, UpdatedAt: start,
	}, w)

	oid := snow.ID(4)
	w = FromDomain(domain.MaintenanceWindow{ID: 2, OrgID: &oid, OrgSlug: "acme", Mode: "full", StartsAt: start})
	require.Equal(t, ScopeOrg, w.Scope())
	require.Equal(t, oid, *w.OrgID)
	require.Equal(t, "acme", w.Key())
}

func TestParseFile(t *testing.T) {
	w, err := ParseFile(nil)
	require.NoError(t, err)
	require.Equal(t, Full, w.Mode)
	require.True(t, w.Active(time.Now()))

	w, err = ParseFile([]byte("  \n"))
	require.NoError(t, err)
	require.Equal(t, Full, w.Mode)

	w, err = ParseFile([]byte(`{"mode":"read_only","message":"backup","ends_at":"2026-11-14T12:00:00Z"}`))
	require.NoError(t, err)
	require.Equal(t, ReadOnly, w.Mode)
	require.Equal(t, "backup", w.Message)
	require.NotNil(t, w.EndsAt)
	require.Equal(t, time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), w.EndsAt.UTC())

	w, err = ParseFile([]byte(`{"message":"no mode"}`))
	require.NoError(t, err)
	require.Equal(t, Full, w.Mode)

	w, err = ParseFile([]byte(`{"mode":"frozen"}`))
	require.NoError(t, err)
	require.Equal(t, Frozen, w.Mode)

	for _, bad := range []string{`{`, `{"mode":"off"}`, `{"mode":"later"}`, `{"mode":"full","extra":1}`, `{"mode":"full"} {}`, `{"mode":"full"}}`, `{"mode":"full"}]`, `{"ends_at":"tomorrow"}`} {
		_, err := ParseFile([]byte(bad))
		require.Error(t, err, bad)
	}
}
