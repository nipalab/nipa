package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/desktop/internal/lang"
	"github.com/nipalab/nipa/desktop/internal/workspace"
	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

var (
	maintStart = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	maintEnd   = time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
)

func TestToMaintenance(t *testing.T) {
	require.Nil(t, toMaintenance(nil))
	got := toMaintenance(&daemonpb.MaintenanceState{
		Server:  &pb.MaintenanceWindow{Mode: "full", StartsAt: timestamppb.New(maintStart)},
		Project: &pb.MaintenanceWindow{Mode: "read_only", Active: true, Message: "moving", StartsAt: timestamppb.New(maintStart), EndsAt: timestamppb.New(maintEnd), Org: "acme", Project: "acme/game"},
	})
	require.Equal(t, []MaintenanceWindow{
		{Scope: ScopeServer, Mode: "full", StartsAt: "2026-11-16T09:00:00Z"},
		{Scope: ScopeProject, Mode: "read_only", Active: true, Message: "moving", StartsAt: "2026-11-16T09:00:00Z", EndsAt: "2026-11-16T11:00:00Z", Org: "acme", Project: "acme/game"},
	}, got)
	require.Equal(t, ScopeOrg, toWindow("", &pb.MaintenanceWindow{Org: "acme"}).Scope)
	require.Equal(t, ScopeProject, toWindow("", &pb.MaintenanceWindow{Org: "acme", Project: "acme/game"}).Scope)
	require.Equal(t, ScopeServer, toWindow("", &pb.MaintenanceWindow{}).Scope)
}

func TestFailureError(t *testing.T) {
	require.EqualError(t, failureError(&daemonpb.OpFailure{Code: 409, Message: "rejected"}), "rejected")
	err := failureError(&daemonpb.OpFailure{Code: 503, Message: "organization acme is in maintenance (closed): db", Maintenance: &pb.MaintenanceWindow{Mode: "full", Active: true, Org: "acme"}})
	var m *MaintenanceError
	require.ErrorAs(t, err, &m)
	require.Equal(t, MaintenanceWindow{Scope: ScopeOrg, Mode: "full", Active: true, Org: "acme"}, m.Window)
	require.Equal(t, "organization acme is in maintenance (closed): db", m.Error())

	var e error = m
	raw, jerr := json.Marshal(&e)
	require.NoError(t, jerr)
	require.JSONEq(t, `{"message":"organization acme is in maintenance (closed): db","maintenance":{"scope":"org","mode":"full","active":true,"message":"","startsAt":"","endsAt":"","org":"acme","project":""}}`, string(raw))
}

func TestCleanErrorKeepsMaintenance(t *testing.T) {
	end := maintEnd
	refusal := (&maintenance.Error{Info: maintenance.Info{Scope: maintenance.ScopeProject, Mode: maintenance.ReadOnly, Message: "backup", StartsAt: maintStart, EndsAt: &end, Org: "acme", Project: "acme/game"}}).GRPCError(maintStart)
	err := cleanError(refusal)
	var m *MaintenanceError
	require.ErrorAs(t, err, &m)
	require.Equal(t, MaintenanceWindow{Scope: ScopeProject, Mode: "read_only", Active: true, Message: "backup", StartsAt: "2026-11-16T09:00:00Z", EndsAt: "2026-11-16T11:00:00Z", Org: "acme", Project: "acme/game"}, m.Window)
	require.NotContains(t, m.Error(), "rpc error")

	bare := (&maintenance.Error{Info: maintenance.Info{Mode: maintenance.Frozen, Org: "acme"}}).GRPCError(maintStart)
	require.ErrorAs(t, cleanError(bare), &m)
	require.Equal(t, ScopeOrg, m.Window.Scope)
	require.Empty(t, m.Window.StartsAt)

	require.EqualError(t, cleanError(status.Error(codes.Unavailable, "down")), "down")
	require.EqualError(t, cleanError(errors.New("plain")), "plain")
}

func TestService_PushRefusedForMaintenance(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.op = opEvents(&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Failure{Failure: &daemonpb.OpFailure{Code: 503, Message: "server is in maintenance (read-only)", Maintenance: &pb.MaintenanceWindow{Mode: "read_only", Active: true}}}})
	_, err := svc.Push(ctx, "/work/game", "m")
	var m *MaintenanceError
	require.ErrorAs(t, err, &m)
	require.Equal(t, ScopeServer, m.Window.Scope)
}

func TestMaintenanceNotifications(t *testing.T) {
	on := workspace.NotifySettings{Maintenance: true}
	project := &MaintenanceWindow{Scope: ScopeProject, Mode: "read_only", Message: "Moving storage", StartsAt: "2026-11-16T09:00:00Z", EndsAt: "2026-11-16T11:00:00Z", Org: "acme", Project: "acme/game"}
	local := func(s string) string {
		t, _ := time.Parse(time.RFC3339, s)
		return t.Local().Format("Jan 2 15:04")
	}

	n, ok := notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "scheduled", Maintenance: project}, on)
	require.True(t, ok)
	require.Equal(t, "game: maintenance scheduled", n.Title)
	require.Equal(t, "Project acme/game will be read-only for maintenance from "+local(project.StartsAt)+"\nMoving storage\nEstimated end: "+local(project.EndsAt), n.Body)
	require.Equal(t, "maintenance:/r:project:scheduled:2026-11-16T09:00:00Z", n.ID)
	require.Equal(t, Navigation{Root: "/r", Tab: "changes"}, n.Target)

	server := &MaintenanceWindow{Scope: ScopeServer, Mode: "frozen", Active: true}
	n, _ = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "started", Maintenance: server}, on)
	require.Equal(t, "game: maintenance started", n.Title)
	require.Equal(t, "The server is frozen for maintenance", n.Body)

	org := &MaintenanceWindow{Scope: ScopeOrg, Mode: "full", Active: true, Org: "acme", Message: "gone"}
	n, _ = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "ended", Maintenance: org}, on)
	require.Equal(t, "game: maintenance ended", n.Title)
	require.Equal(t, "Organization acme is back to normal", n.Body)

	n, _ = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Maintenance: org}, on)
	require.Equal(t, "game: maintenance updated", n.Title)
	require.Equal(t, "Organization acme is closed for maintenance\ngone", n.Body)

	n, _ = notificationFor(lang.Japanese, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "started", Maintenance: server}, on)
	require.Equal(t, "game: メンテナンスが始まりました", n.Title)

	bad := &MaintenanceWindow{Scope: ScopeServer, Mode: "full", StartsAt: "soon"}
	n, _ = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "scheduled", Maintenance: bad}, on)
	require.Equal(t, "The server will be closed for maintenance from soon", n.Body)

	_, ok = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "started", Maintenance: server}, workspace.NotifySettings{Pushes: true})
	require.False(t, ok)
	_, ok = notificationFor(lang.English, "game", "/r", TeamEvent{Kind: daemon.EventMaintenance, Message: "started"}, on)
	require.False(t, ok)
}

func TestService_ActivityCarriesMaintenance(t *testing.T) {
	h := newHarness()
	h.daemon.events = make(chan *daemonpb.RepoActivity, 4)
	h.svc.Prefs = newPrefs()
	rec := newActivityRecorder()
	rec.attach(h.svc)

	_, err := h.svc.AddRepo(ctx, "/work/game")
	require.NoError(t, err)
	window := &pb.MaintenanceWindow{Mode: "read_only", Active: true, Org: "acme", Project: "acme/game"}
	h.daemon.events <- &daemonpb.RepoActivity{
		Maintenance: &daemonpb.MaintenanceState{Project: window},
		Events:      []*daemonpb.TeamEvent{{Kind: daemon.EventMaintenance, Message: "started", Maintenance: window}},
	}
	list := waitFor(t, rec.lists)
	want := []MaintenanceWindow{{Scope: ScopeProject, Mode: "read_only", Active: true, Org: "acme", Project: "acme/game"}}
	require.Equal(t, want, list[0].Maintenance)
	require.Equal(t, want, h.svc.Activity()[0].Maintenance)
	note := waitFor(t, rec.notes)
	require.Equal(t, "game: maintenance started", note.Title)
	require.Equal(t, "Project acme/game is read-only for maintenance", note.Body)
}
