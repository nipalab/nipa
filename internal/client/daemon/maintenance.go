package daemon

import (
	"context"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

const EventMaintenance = "maintenance"

const (
	maintenanceScheduled = "scheduled"
	maintenanceStarted   = "started"
	maintenanceUpdated   = "updated"
	maintenanceEnded     = "ended"
)

// pollMaintenance asks for the windows covering the clone. ok is false when
// the server did not answer, so the previous state is kept.
func pollMaintenance(ctx context.Context, client pb.NipaServiceClient, project *pb.ProjectContext) (*daemonpb.MaintenanceState, bool) {
	res, err := client.GetMaintenanceStatus(ctx, &pb.GetMaintenanceStatusRequest{Context: project})
	if err != nil {
		return nil, false
	}
	state := &daemonpb.MaintenanceState{Server: res.GetServer(), Org: res.GetOrg(), Project: res.GetProject()}
	if state.Server == nil && state.Org == nil && state.Project == nil {
		return nil, true
	}
	return state, true
}

// maintenanceEvents reports how each scope's window changed between polls.
func maintenanceEvents(prev, next *daemonpb.MaintenanceState) []*daemonpb.TeamEvent {
	var events []*daemonpb.TeamEvent
	pairs := [][2]*pb.MaintenanceWindow{
		{prev.GetServer(), next.GetServer()},
		{prev.GetOrg(), next.GetOrg()},
		{prev.GetProject(), next.GetProject()},
	}
	for _, p := range pairs {
		if phase := maintenancePhase(p[0], p[1]); phase != "" {
			w := p[1]
			if w == nil {
				w = p[0]
			}
			events = append(events, &daemonpb.TeamEvent{Kind: EventMaintenance, Message: phase, Maintenance: w})
		}
	}
	return events
}

func maintenancePhase(prev, next *pb.MaintenanceWindow) string {
	switch {
	case prev == nil && next == nil:
		return ""
	case next == nil:
		return maintenanceEnded
	case prev == nil && next.GetActive(), prev != nil && !prev.GetActive() && next.GetActive():
		return maintenanceStarted
	case prev == nil:
		return maintenanceScheduled
	case !proto.Equal(prev, next):
		return maintenanceUpdated
	default:
		return ""
	}
}

func isMaintenance(err error) bool {
	_, ok := maintenance.FromGRPC(err)
	return ok
}

// maintenanceWindowOf returns the window of a maintenance refusal, nil for
// any other error.
func maintenanceWindowOf(err error) *pb.MaintenanceWindow {
	refused, ok := maintenance.FromGRPC(err)
	if !ok {
		return nil
	}
	info := refused.Info
	w := &pb.MaintenanceWindow{Mode: string(info.Mode), Active: true, Message: info.Message, Org: info.Org, Project: info.Project}
	if !info.StartsAt.IsZero() {
		w.StartsAt = timestamppb.New(info.StartsAt)
	}
	if info.EndsAt != nil {
		w.EndsAt = timestamppb.New(*info.EndsAt)
	}
	return w
}
