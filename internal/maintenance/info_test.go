package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInfoAndError(t *testing.T) {
	end := testNow.Add(30 * time.Minute)
	server := Window{Mode: ReadOnly, Message: "storage work", StartsAt: testNow, EndsAt: &end}
	e := NewError(server, ScopeServer)
	require.Equal(t, "server is in maintenance (read-only): storage work", e.Error())
	require.Empty(t, e.Info.Project)
	require.Equal(t, 30*time.Minute, e.Info.RetryAfter(testNow))
	require.Zero(t, e.Info.RetryAfter(end.Add(time.Second)))

	project := projectWindow(2, 10, "acme", "game", Full, testNow)
	e = NewError(project, ScopeProject)
	require.Equal(t, "project acme/game is in maintenance (closed)", e.Error())
	require.Equal(t, "acme/game", e.Info.Project)
	require.Equal(t, "acme", e.Info.Org)
	require.Zero(t, e.Info.RetryAfter(testNow))

	org := orgWindow(3, 20, "acme", Full, testNow)
	org.Message = "migrating"
	e2 := NewError(org, ScopeOrg)
	require.Equal(t, "organization acme is in maintenance (closed): migrating", e2.Error())
	require.Equal(t, "acme", e2.Info.Org)
	require.Empty(t, e2.Info.Project)

	wrapped := fmt.Errorf("push: %w", e)
	got, ok := AsError(wrapped)
	require.True(t, ok)
	require.Same(t, e, got)
	_, ok = AsError(errors.New("other"))
	require.False(t, ok)
}

func TestInfoMetadataRoundTrip(t *testing.T) {
	end := testNow.Add(time.Hour)
	in := Info{Scope: ScopeProject, Mode: Full, Message: "m", StartsAt: testNow, EndsAt: &end, Org: "acme", Project: "acme/game"}
	md := in.Metadata()
	require.Equal(t, "2026-11-14T11:00:00Z", md["ends_at"])
	out := InfoFromMetadata(md)
	require.Equal(t, in.Scope, out.Scope)
	require.Equal(t, in.Mode, out.Mode)
	require.Equal(t, in.Message, out.Message)
	require.Equal(t, in.Org, out.Org)
	require.Equal(t, in.Project, out.Project)
	require.True(t, in.StartsAt.Equal(out.StartsAt))
	require.True(t, in.EndsAt.Equal(*out.EndsAt))

	bare := Info{Scope: ScopeServer, Mode: ReadOnly, StartsAt: testNow}
	md = bare.Metadata()
	require.NotContains(t, md, "ends_at")
	require.NotContains(t, md, "project")
	require.NotContains(t, md, "org")
	require.Nil(t, InfoFromMetadata(md).EndsAt)
	require.True(t, InfoFromMetadata(map[string]string{}).StartsAt.IsZero())
}

func TestInfoJSON(t *testing.T) {
	data, err := json.Marshal(NewInfo(Window{Mode: Frozen, StartsAt: testNow}, ScopeServer))
	require.NoError(t, err)
	require.JSONEq(t, `{"scope":"server","mode":"frozen","message":"","starts_at":"2026-11-14T10:00:00Z"}`, string(data))
}
