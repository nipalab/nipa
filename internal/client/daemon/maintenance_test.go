package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

func maintenanceRefusal(mode maintenance.Mode) error {
	ends := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	w := maintenance.Window{Mode: mode, Message: "storage move", StartsAt: ends.Add(-time.Hour), EndsAt: &ends}
	return maintenance.NewError(w, maintenance.ScopeServer).GRPCError(ends.Add(-time.Minute))
}

func TestMaintenancePhase(t *testing.T) {
	scheduled := &pb.MaintenanceWindow{Mode: "read_only"}
	active := &pb.MaintenanceWindow{Mode: "read_only", Active: true}
	closed := &pb.MaintenanceWindow{Mode: "full", Active: true}
	cases := []struct {
		prev, next *pb.MaintenanceWindow
		want       string
	}{
		{nil, nil, ""},
		{nil, scheduled, maintenanceScheduled},
		{nil, active, maintenanceStarted},
		{scheduled, active, maintenanceStarted},
		{active, closed, maintenanceUpdated},
		{active, active, ""},
		{active, nil, maintenanceEnded},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, maintenancePhase(tc.prev, tc.next), "%v -> %v", tc.prev, tc.next)
	}

	events := maintenanceEvents(
		&daemonpb.MaintenanceState{Server: active},
		&daemonpb.MaintenanceState{Project: &pb.MaintenanceWindow{Mode: "full", Project: "studio/game"}},
	)
	require.Len(t, events, 2)
	require.Equal(t, EventMaintenance, events[0].GetKind())
	require.Equal(t, maintenanceEnded, events[0].GetMessage())
	require.True(t, proto.Equal(active, events[0].GetMaintenance()), "an ended event carries the old window")
	require.Equal(t, maintenanceScheduled, events[1].GetMessage())
	require.Equal(t, "studio/game", events[1].GetMaintenance().GetProject())
}

func TestTeamWatch_Maintenance(t *testing.T) {
	svc := &teamService{
		commits: []*pb.CommitLogEntry{commit("c1", "Me", "mine")},
		maint:   &pb.GetMaintenanceStatusResponse{},
	}
	in := pollInput{branch: "main", pinned: "c1", user: "me"}
	project := &pb.ProjectContext{Org: "studio", Project: "game"}
	w := &teamWatch{}
	ctx := context.Background()

	act, err := w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.NotNil(t, act)
	require.Nil(t, act.GetMaintenance())
	require.True(t, proto.Equal(project, svc.maintAsk[0]), "the status is asked for the clone's project")

	readOnly := &pb.MaintenanceWindow{Mode: "read_only", Active: true, Message: "storage move", EndsAt: timestamppb.New(time.Now().Add(time.Hour))}
	svc.set(func(s *teamService) { s.maint = &pb.GetMaintenanceStatusResponse{Server: readOnly} })
	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.NotNil(t, act, "a new window is reported")
	require.True(t, proto.Equal(readOnly, act.GetMaintenance().GetServer()))
	require.Len(t, act.GetEvents(), 1)
	require.Equal(t, maintenanceStarted, act.GetEvents()[0].GetMessage())

	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.Nil(t, act, "an unchanged window is quiet")

	closed := &pb.MaintenanceWindow{Mode: "full", Active: true}
	svc.set(func(s *teamService) {
		s.maint = &pb.GetMaintenanceStatusResponse{Server: closed}
		s.logErr = maintenanceRefusal(maintenance.Full)
		s.commits = append([]*pb.CommitLogEntry{commit("c2", "Alice", "new")}, s.commits...)
	})
	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.NotNil(t, act)
	require.Empty(t, act.GetError(), "a maintenance refusal is not an offline error")
	require.Len(t, act.GetEvents(), 1)
	require.Equal(t, maintenanceUpdated, act.GetEvents()[0].GetMessage())
	require.Zero(t, act.GetIncoming(), "counts keep their previous values")

	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.Nil(t, act, "a still refused poll with the same window is quiet")

	svc.set(func(s *teamService) {
		s.maint = &pb.GetMaintenanceStatusResponse{}
		s.logErr = nil
	})
	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.NotNil(t, act)
	require.Nil(t, act.GetMaintenance())
	kinds := []string{}
	for _, e := range act.GetEvents() {
		kinds = append(kinds, e.GetKind()+":"+e.GetMessage())
	}
	require.Equal(t, []string{"maintenance:ended", "pushed:new"}, kinds)
	require.EqualValues(t, 1, act.GetIncoming())

	svc.set(func(s *teamService) { s.logErr = errors.New("boom") })
	_, err = w.poll(ctx, svc, project, in)
	require.Error(t, err, "other failures still fail the poll")
}

func TestTeamWatch_MaintenanceStatusFailureKeepsState(t *testing.T) {
	svc := &teamService{maint: &pb.GetMaintenanceStatusResponse{Server: &pb.MaintenanceWindow{Mode: "read_only", Active: true}}}
	w := &teamWatch{}
	act, err := w.poll(context.Background(), svc, nil, pollInput{})
	require.NoError(t, err)
	require.NotNil(t, act.GetMaintenance())

	svc.set(func(s *teamService) { s.maint = nil })
	act, err = w.poll(context.Background(), svc, nil, pollInput{})
	require.NoError(t, err)
	require.Nil(t, act, "an unanswered status call keeps the last windows")
	require.NotNil(t, w.maint)
}

func TestTeamWatch_FailedIgnoresMaintenance(t *testing.T) {
	w := &teamWatch{maint: &daemonpb.MaintenanceState{Server: &pb.MaintenanceWindow{Mode: "full"}}}
	require.Nil(t, w.failed(maintenanceRefusal(maintenance.Full)))
	act := w.failed(status.Error(codes.Unavailable, "connection refused"))
	require.Equal(t, "connection refused", act.GetError())
	require.Equal(t, "full", act.GetMaintenance().GetServer().GetMode())
}

func TestOpFailure_Maintenance(t *testing.T) {
	raw := maintenanceRefusal(maintenance.ReadOnly)
	f := opFailure(raw)
	require.EqualValues(t, 503, f.GetCode())
	require.Equal(t, "read_only", f.GetMaintenance().GetMode())
	require.True(t, f.GetMaintenance().GetActive())
	require.NotNil(t, f.GetMaintenance().GetStartsAt())
	require.NotNil(t, f.GetMaintenance().GetEndsAt())

	wrapped := clientDomain.NewMaintenanceError(maintenance.Info{Scope: maintenance.ScopeProject, Mode: maintenance.Full, Org: "studio", Project: "studio/game"}, nil)
	f = opFailure(wrapped)
	require.EqualValues(t, 503, f.GetCode())
	require.Equal(t, "studio/game", f.GetMaintenance().GetProject())
	require.Nil(t, f.GetMaintenance().GetStartsAt())

	f = opFailure(errors.New("plain"))
	require.Nil(t, f.GetMaintenance())
	require.EqualValues(t, 500, f.GetCode())
}

func TestJSONBridge_MaintenanceBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeGRPCError(rec, maintenanceRefusal(maintenance.ReadOnly))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body jsonError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "Unavailable", body.Code)
	require.Equal(t, "server is in maintenance (read-only): storage move", body.Message)
	require.Equal(t, maintenance.ReadOnly, body.Maintenance.Mode)
	require.Equal(t, "storage move", body.Maintenance.Message)

	rec = httptest.NewRecorder()
	writeStreamError(rec, maintenanceRefusal(maintenance.Full))
	var line map[string]jsonError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &line))
	require.Equal(t, maintenance.Full, line["error"].Maintenance.Mode)

	rec = httptest.NewRecorder()
	writeGRPCError(rec, status.Error(codes.Unavailable, "down"))
	require.NotContains(t, rec.Body.String(), "maintenance")
}
