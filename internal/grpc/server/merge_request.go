package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) CreateMergeRequest(ctx context.Context, req *pb.CreateMergeRequestRequest) (*pb.CreateMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Create(
		ctx, project.ID, req.GetTitle(), req.GetDescription(), req.GetSourceBranch(), req.GetTargetBranch(),
	)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreateMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) UpdateMergeRequest(ctx context.Context, req *pb.UpdateMergeRequestRequest) (*pb.UpdateMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	id, err := mergeRequestID(req.GetId())
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Update(ctx, project.ID, id, req.GetTitle(), req.GetDescription())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.UpdateMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) ListMergeRequests(ctx context.Context, req *pb.ListMergeRequestsRequest) (*pb.ListMergeRequestsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	requests, err := n.uc.MergeRequest().List(ctx, project.ID, req.GetStatus(), int(req.GetLimit()))
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestsResponse{MergeRequests: make([]*pb.MergeRequestDetail, 0, len(requests))}
	for _, request := range requests {
		resp.MergeRequests = append(resp.MergeRequests, domainMergeRequestToPB(request))
	}
	return resp, nil
}

func (n *nipaServer) MergeMergeRequest(ctx context.Context, req *pb.MergeMergeRequestRequest) (*pb.MergeMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	id, err := mergeRequestID(req.GetId())
	if err != nil {
		return nil, handleError(err)
	}
	request, info, err := n.uc.MergeRequest().Merge(ctx, project.ID, id)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.MergeMergeRequestResponse{
		MergeRequest: domainMergeRequestToPB(request),
		Mergeability: domainMergeabilityToPB(info),
	}, nil
}

func (n *nipaServer) CloseMergeRequest(ctx context.Context, req *pb.CloseMergeRequestRequest) (*pb.CloseMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	id, err := mergeRequestID(req.GetId())
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Close(ctx, project.ID, id)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CloseMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func mergeRequestID(raw string) (int64, error) {
	id, err := snow.ParseBase36(raw)
	if err != nil {
		return 0, domain.NewErrorUser("invalid merge request id")
	}
	return id.Int64(), nil
}

func domainMergeRequestToPB(request *domain.MergeRequest) *pb.MergeRequestDetail {
	return &pb.MergeRequestDetail{
		Id:                snow.ID(request.ID).Base36(),
		ProjectId:         request.ProjectID.Base36(),
		SourceBranch:      request.SourceBranch,
		TargetBranch:      request.TargetBranch,
		Title:             request.Title,
		Description:       request.Description,
		Status:            request.Status,
		MergeCommitId:     snowPtrToStringPtr(request.MergeCommitID),
		MergeBaseCommitId: snowPtrToStringPtr(request.MergeBaseCommitID),
		CreatedBy:         request.CreatedBy.Base36(),
		CreatedAt:         timestamppb.New(request.CreatedAt),
		UpdatedAt:         timestamppb.New(request.UpdatedAt),
	}
}

func domainMergeabilityToPB(info *domain.Mergeability) *pb.MergeabilityDetail {
	if info == nil {
		return nil
	}
	return &pb.MergeabilityDetail{
		Status:            info.Status,
		SourceCommitId:    snowPtrToStringPtr(info.SourceCommitID),
		TargetCommitId:    snowPtrToStringPtr(info.TargetCommitID),
		MergeBaseCommitId: snowPtrToStringPtr(info.MergeBaseCommitID),
	}
}
