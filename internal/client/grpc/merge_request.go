package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) CreateMergeRequest(ctx context.Context, org, project, title, description, sourceBranch, targetBranch string, draft bool) (*clientDomain.MergeRequest, error) {
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
		Draft:        draft,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) UpdateMergeRequest(ctx context.Context, org, project string, number int64, title, description string, draft *bool) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.UpdateMergeRequest(ctx, &pb.UpdateMergeRequestRequest{
		Context:     &pb.ProjectContext{Org: org, Project: project},
		Number:      number,
		Title:       title,
		Description: description,
		Draft:       draft,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) ListMergeRequests(ctx context.Context, org, project string, opts clientDomain.ListMergeRequestOptions) ([]*clientDomain.MergeRequest, int64, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, 0, err
	}
	request := &pb.ListMergeRequestsRequest{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		Status:       opts.Status,
		Limit:        int32(opts.Limit),
		Author:       opts.Author,
		SourceBranch: opts.SourceBranch,
		TargetBranch: opts.TargetBranch,
		Draft:        opts.Draft,
	}
	if opts.After > 0 {
		request.AfterNumber = &opts.After
	}
	res, err := client.ListMergeRequests(ctx, request)
	if err != nil {
		return nil, 0, toDomainError(err)
	}
	requests := make([]*clientDomain.MergeRequest, 0, len(res.GetMergeRequests()))
	for _, mr := range res.GetMergeRequests() {
		if converted := toClientMergeRequest(mr); converted != nil {
			requests = append(requests, converted)
		}
	}
	return requests, res.GetNextCursor(), nil
}

func (c *Client) MergeMergeRequest(ctx context.Context, org, project string, number int64) (*clientDomain.MergeRequest, *clientDomain.Mergeability, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, nil, err
	}
	res, err := client.MergeMergeRequest(ctx, &pb.MergeMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), toClientMergeability(res.GetMergeability()), nil
}

func (c *Client) CloseMergeRequest(ctx context.Context, org, project string, number int64) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CloseMergeRequest(ctx, &pb.CloseMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) GetMergeRequest(ctx context.Context, org, project string, number int64) (*clientDomain.MergeRequest, *clientDomain.Mergeability, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, nil, err
	}
	res, err := client.GetMergeRequest(ctx, &pb.GetMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), toClientMergeability(res.GetMergeability()), nil
}

func (c *Client) ReopenMergeRequest(ctx context.Context, org, project string, number int64) (*clientDomain.MergeRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ReopenMergeRequest(ctx, &pb.ReopenMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeRequest(res.GetMergeRequest()), nil
}

func (c *Client) CheckMergeRequest(ctx context.Context, org, project string, number int64) (*clientDomain.Mergeability, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CheckMergeRequest(ctx, &pb.CheckMergeRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientMergeability(res.GetMergeability()), nil
}

func (c *Client) ListMergeRequestCommits(ctx context.Context, org, project string, number int64) ([]*serverDomain.CommitLogEntry, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequestCommits(ctx, &pb.ListMergeRequestCommitsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	commits := make([]*serverDomain.CommitLogEntry, 0, len(res.GetCommits()))
	for _, commit := range res.GetCommits() {
		if converted := toServerCommitLogEntry(commit); converted != nil {
			commits = append(commits, converted)
		}
	}
	return commits, nil
}

func (c *Client) GetMergeRequestDiff(ctx context.Context, org, project string, number int64) ([]*clientDomain.MergeRequestDiffFile, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetMergeRequestDiff(ctx, &pb.GetMergeRequestDiffRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	files := make([]*clientDomain.MergeRequestDiffFile, 0, len(res.GetFiles()))
	for _, file := range res.GetFiles() {
		files = append(files, &clientDomain.MergeRequestDiffFile{
			Path:      file.GetPath(),
			OldPath:   file.GetOldPath(),
			Status:    file.GetStatus(),
			Binary:    file.GetBinary(),
			Additions: file.GetAdditions(),
			Deletions: file.GetDeletions(),
			Patch:     file.GetPatch(),
		})
	}
	return files, nil
}

func toClientMergeRequest(mr *pb.MergeRequestDetail) *clientDomain.MergeRequest {
	if mr == nil {
		return nil
	}
	return &clientDomain.MergeRequest{
		ID:                mr.GetId(),
		Number:            mr.GetNumber(),
		ProjectID:         mr.GetProjectId(),
		SourceBranch:      mr.GetSourceBranch(),
		TargetBranch:      mr.GetTargetBranch(),
		Title:             mr.GetTitle(),
		Description:       mr.GetDescription(),
		Status:            mr.GetStatus(),
		Draft:             mr.GetDraft(),
		MergeCommitID:     mr.GetMergeCommitId(),
		MergeBaseCommitID: mr.GetMergeBaseCommitId(),
		CreatedBy:         mr.GetCreatedBy(),
		CreatedAt:         mr.GetCreatedAt().AsTime(),
		UpdatedAt:         mr.GetUpdatedAt().AsTime(),
		Review:            toClientReviewState(mr.GetReview()),
	}
}

func toClientReviewState(state *pb.MergeRequestReviewState) *clientDomain.MergeRequestReviewState {
	if state == nil {
		return nil
	}
	outstanding := make([]string, 0, len(state.GetOutstandingReviewers()))
	outstanding = append(outstanding, state.GetOutstandingReviewers()...)
	return &clientDomain.MergeRequestReviewState{
		HeadCommitID:         state.GetHeadCommitId(),
		Approvals:            int(state.GetApprovals()),
		ChangesRequested:     int(state.GetChangesRequested()),
		DismissedApprovals:   int(state.GetDismissedApprovals()),
		OutstandingReviewers: outstanding,
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
		BlockedBy:         info.GetBlockedBy(),
	}
}
