package server

import (
	"context"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/usecase"
)

// GetMaintenanceStatus is public: it always answers the server window and
// adds the org and project windows only for a valid bearer token with access
// to them. An invalid token or missing access leaves them empty.
func (n *nipaServer) GetMaintenanceStatus(ctx context.Context, req *pb.GetMaintenanceStatusRequest) (*pb.GetMaintenanceStatusResponse, error) {
	uc := n.uc.Maintenance()
	state := uc.Status(ctx)
	org, project := req.GetContext().GetOrg(), req.GetContext().GetProject()
	if authed, ok := n.maintenanceClaims(ctx); ok && org != "" {
		var scoped usecase.MaintenanceState
		var err error
		if project != "" {
			scoped, err = uc.ProjectStatus(authed, org, project)
		} else {
			scoped, err = uc.OrgStatus(authed, org)
		}
		if err == nil {
			state.Org, state.Project = scoped.Org, scoped.Project
		}
	}
	return &pb.GetMaintenanceStatusResponse{
		Server:  maintenanceWindowToPB(state.Server),
		Org:     maintenanceWindowToPB(state.Org),
		Project: maintenanceWindowToPB(state.Project),
		Source:  state.Source,
	}, nil
}

func (n *nipaServer) maintenanceClaims(ctx context.Context) (context.Context, bool) {
	md, _ := metadata.FromIncomingContext(ctx)
	header := md.Get("authorization")
	if len(header) == 0 {
		return ctx, false
	}
	token, ok := bearerToken(header[0])
	if !ok {
		return ctx, false
	}
	claims, err := n.uc.Auth().ValidateToken(ctx, token)
	if err != nil {
		return ctx, false
	}
	return domain.ContextWithClaim(ctx, *claims), true
}

func maintenanceWindowToPB(st *maintenance.Status) *pb.MaintenanceWindow {
	if st == nil {
		return nil
	}
	return &pb.MaintenanceWindow{
		Mode:     string(st.Mode),
		Active:   st.Active,
		Message:  st.Message,
		StartsAt: optionalTimestamp(st.StartsAt),
		EndsAt:   optionalTimestampPtr(st.EndsAt),
		Org:      st.Org,
		Project:  st.Project,
	}
}

func optionalTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func optionalTimestampPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return optionalTimestamp(*t)
}
