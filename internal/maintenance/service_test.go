package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/snow"
)

var testNow = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func serverWindow(id snow.ID, mode Mode, start time.Time) Window {
	return Window{ID: id, Mode: mode, StartsAt: start}
}

func projectWindow(id, projectID snow.ID, org, project string, mode Mode, start time.Time) Window {
	pid := projectID
	return Window{ID: id, ProjectID: &pid, Org: org, Project: project, Mode: mode, StartsAt: start}
}

func TestServiceForEmpty(t *testing.T) {
	svc := NewService().WithClock(fixedClock(testNow))
	w, scope := svc.For("acme", "game")
	require.Equal(t, Off, w.Mode)
	require.Empty(t, scope)
	require.False(t, svc.Frozen())
	require.Equal(t, testNow, svc.Now())
	require.Equal(t, SourceDB, svc.Snapshot().Source)
}

func TestServiceFor(t *testing.T) {
	past := testNow.Add(-time.Hour)
	future := testNow.Add(time.Hour)
	cases := []struct {
		name      string
		windows   []Window
		org, proj string
		mode      Mode
		scope     Scope
	}{
		{"server only", []Window{serverWindow(1, ReadOnly, past)}, "acme", "game", ReadOnly, ScopeServer},
		{"server for no project", []Window{serverWindow(1, Full, past), projectWindow(2, 10, "acme", "game", ReadOnly, past)}, "", "", Full, ScopeServer},
		{"project only", []Window{projectWindow(2, 10, "acme", "game", Full, past)}, "acme", "game", Full, ScopeProject},
		{"other project", []Window{projectWindow(2, 10, "acme", "game", Full, past)}, "acme", "other", Off, ""},
		{"unknown project", []Window{serverWindow(1, ReadOnly, past)}, "nope", "nope", ReadOnly, ScopeServer},
		{"both project stricter", []Window{serverWindow(1, ReadOnly, past), projectWindow(2, 10, "acme", "game", Full, past)}, "acme", "game", Full, ScopeProject},
		{"both server stricter", []Window{serverWindow(1, Full, past), projectWindow(2, 10, "acme", "game", ReadOnly, past)}, "acme", "game", Full, ScopeServer},
		{"tie keeps server", []Window{serverWindow(1, ReadOnly, past), projectWindow(2, 10, "acme", "game", ReadOnly, past)}, "acme", "game", ReadOnly, ScopeServer},
		{"scheduled server", []Window{serverWindow(1, Full, future)}, "acme", "game", Off, ""},
		{"scheduled project", []Window{serverWindow(1, ReadOnly, past), projectWindow(2, 10, "acme", "game", Full, future)}, "acme", "game", ReadOnly, ScopeServer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewService().WithClock(fixedClock(testNow))
			require.NoError(t, svc.Apply(context.Background(), NewSnapshot(c.windows, testNow)))
			w, scope := svc.For(c.org, c.proj)
			require.Equal(t, c.mode, w.Mode)
			require.Equal(t, c.scope, scope)
		})
	}
}

func TestNewSnapshotIndexes(t *testing.T) {
	snap := NewSnapshot([]Window{serverWindow(1, ReadOnly, testNow), projectWindow(2, 10, "acme", "game", Full, testNow)}, testNow)
	require.Equal(t, snow.ID(1), snap.Server.ID)
	require.Equal(t, snow.ID(2), snap.Projects["acme/game"].ID)
	require.Len(t, snap.ByID, 2)
	require.Len(t, snap.Windows(), 2)
	require.Equal(t, testNow, snap.LoadedAt)
}

func TestServiceFrozenFollowsClock(t *testing.T) {
	now := testNow
	svc := NewService().WithClock(func() time.Time { return now })
	require.NoError(t, svc.Apply(context.Background(), NewSnapshot([]Window{serverWindow(1, Frozen, testNow.Add(time.Minute))}, testNow)))
	require.False(t, svc.Frozen())
	now = testNow.Add(time.Minute)
	require.True(t, svc.Frozen())
}

func TestServiceApplyResumeHooks(t *testing.T) {
	ctx := context.Background()
	svc := NewService().WithClock(fixedClock(testNow))
	frozen := NewSnapshot([]Window{serverWindow(1, Frozen, testNow)}, testNow)

	calls := 0
	failing := true
	svc.OnResume(func(context.Context) error {
		calls++
		if failing {
			return errors.New("lease lost")
		}
		return nil
	})

	require.NoError(t, svc.Apply(ctx, frozen))
	require.Zero(t, calls, "entering frozen runs no hook")
	require.True(t, svc.Frozen())

	require.NoError(t, svc.Apply(ctx, NewSnapshot([]Window{serverWindow(1, Frozen, testNow)}, testNow)))
	require.Zero(t, calls, "staying frozen runs no hook")

	err := svc.Apply(ctx, NewSnapshot(nil, testNow))
	require.ErrorContains(t, err, "lease lost")
	require.Equal(t, 1, calls)
	require.True(t, svc.Frozen(), "a failed hook keeps the frozen snapshot")

	failing = false
	require.NoError(t, svc.Apply(ctx, NewSnapshot([]Window{serverWindow(1, ReadOnly, testNow)}, testNow)))
	require.Equal(t, 2, calls)
	require.False(t, svc.Frozen())
	w, scope := svc.For("", "")
	require.Equal(t, ReadOnly, w.Mode)
	require.Equal(t, ScopeServer, scope)

	require.NoError(t, svc.Apply(ctx, NewSnapshot(nil, testNow)))
	require.Equal(t, 2, calls, "leaving a non-frozen mode runs no hook")
}
