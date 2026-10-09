package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) ReportMergeRequestCheck(ctx context.Context, req *pb.ReportMergeRequestCheckRequest) (*pb.ReportMergeRequestCheckResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	checks := n.uc.MergeRequestCheck()
	if checks == nil {
		return nil, handleError(domain.NewErrorInternalServer("status checks are not configured"))
	}
	check, err := checks.Report(ctx, project.ID, req.GetNumber(), req.GetName(), req.GetState(), req.GetDetailsUrl())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ReportMergeRequestCheckResponse{Check: domainMergeRequestCheckToPB(check)}, nil
}

func (n *nipaServer) ListMergeRequestChecks(ctx context.Context, req *pb.ListMergeRequestChecksRequest) (*pb.ListMergeRequestChecksResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	checks := n.uc.MergeRequestCheck()
	if checks == nil {
		return nil, handleError(domain.NewErrorInternalServer("status checks are not configured"))
	}
	list, err := checks.List(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestChecksResponse{Checks: make([]*pb.MergeRequestCheckDetail, 0, len(list))}
	for _, check := range list {
		resp.Checks = append(resp.Checks, domainMergeRequestCheckToPB(check))
	}
	return resp, nil
}

func domainMergeRequestCheckToPB(check *domain.MergeRequestCheck) *pb.MergeRequestCheckDetail {
	if check == nil {
		return nil
	}
	return &pb.MergeRequestCheckDetail{
		Id:         check.ID.Base36(),
		Name:       check.Name,
		State:      check.State,
		DetailsUrl: check.DetailsURL,
		Reporter:   domainReviewActorToPB(&check.Reporter),
		CreatedAt:  timestamppb.New(check.CreatedAt),
		UpdatedAt:  timestamppb.New(check.UpdatedAt),
	}
}
