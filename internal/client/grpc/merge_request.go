package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) CreateMergeRequest(ctx context.Context, org, project, title, description, sourceBranch, targetBranch string) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CreateMergeRequest(ctx, &pb.CreateMergeRequestRequest{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		Title:        title,
		Description:  description,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) UpdateMergeRequest(ctx context.Context, org, project, id, title, description string) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context:     &pb.ProjectContext{Org: org, Project: project},
		Id:          id,
		Title:       title,
		Description: description,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) ListMergeRequests(ctx context.Context, org, project, status string, limit int) ([]*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequests(ctx, &pb.ListMergeRequestsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Status:  status,
		Limit:   int32(limit),
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	requests := make([]*clientDomain.MergeRequest, 0, len(res.GetMergeRequests()))
	for _, mr := range res.GetMergeRequests() {
		if converted := toClientMergeRequest(mr); converted != nil {
			requests = append(requests, converted)
		}
	}
	return requests, nil
}

func (c *Client) MergeMergeRequest(ctx context.Context, org, project, id string) (*clientDomain.MergeRequest, *clientDomain.Mergeability, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, nil, err
	}
	res, err := client.MergeMergeRequest(ctx, &pb.MergeMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Id:      id,
	})
	if err != nil {
		return nil, nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), toClientMergeability(res.GetMergeability()), nil
}

func (c *Client) CloseMergeRequest(ctx context.Context, org, project, id string) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CloseMergeRequest(ctx, &pb.CloseMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Id:      id,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func toClientMergeRequest(mr *pb.MergeRequestDetail) *clientDomain.MergeRequest {
	if mr == nil {
		return nil
	}
	return &clientDomain.MergeRequest{
		ID:                mr.GetId(),
		ProjectID:         mr.GetProjectId(),
		SourceBranch:      mr.GetSourceBranch(),
		TargetBranch:      mr.GetTargetBranch(),
		Title:             mr.GetTitle(),
		Description:       mr.GetDescription(),
		Status:            mr.GetStatus(),
		MergeCommitID:     mr.GetMergeCommitId(),
		MergeBaseCommitID: mr.GetMergeBaseCommitId(),
		CreatedBy:         mr.GetCreatedBy(),
		CreatedAt:         mr.GetCreatedAt().AsTime(),
		UpdatedAt:         mr.GetUpdatedAt().AsTime(),
	}
}

func toClientMergeability(info *pb.MergeabilityDetail) *clientDomain.Mergeability {
	if info == nil {
		return nil
	}
	return &clientDomain.Mergeability{
		Status:            info.GetStatus(),
		SourceCommitID:    info.GetSourceCommitId(),
		TargetCommitID:    info.GetTargetCommitId(),
		MergeBaseCommitID: info.GetMergeBaseCommitId(),
	}
}
