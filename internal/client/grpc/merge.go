package grpc

import (
	"context"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

// GetMergeBase resolves the merge base of two branch or commit refs and
// returns the head commit IDs/hashes plus the recursive manifest of the base
// tree.
func (c *Client) GetMergeBase(ctx context.Context, org, project string, target, source clientDomain.MergeRef) (*clientDomain.MergeBaseInfo, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	req := &pb.GetMergeBaseRequest{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		TargetBranch: target.Branch,
		SourceBranch: source.Branch,
	}
	if target.CommitID != "" {
		req.TargetCommitId = &target.CommitID
	}
	if source.CommitID != "" {
		req.SourceCommitId = &source.CommitID
	}
	res, err := client.GetMergeBase(ctx, req)
	if err != nil {
		return nil, toDomainError(err)
	}
	info := &clientDomain.MergeBaseInfo{
		TargetBranch:      res.GetTargetBranch(),
		SourceBranch:      res.GetSourceBranch(),
		TargetCommitID:    res.GetTargetCommitId(),
		SourceCommitID:    res.GetSourceCommitId(),
		MergeBaseCommitID: res.GetMergeBaseCommitId(),
		TargetCommitHash:  res.GetTargetCommitHash(),
		SourceCommitHash:  res.GetSourceCommitHash(),
	}
	if res.MergeBaseTree != nil {
		info.MergeBaseTree = toServerTreeNode(res.GetMergeBaseTree())
	}
	return info, nil
}

// MergeFastForward moves the target branch head to the source branch head when
// fast-forward is possible, returning the updated target branch.
func (c *Client) MergeFastForward(ctx context.Context, org, project, targetBranch, sourceBranch string) (*serverDomain.Branch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.MergeFastForward(ctx, &pb.MergeFastForwardRequest{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		TargetBranch: targetBranch,
		SourceBranch: sourceBranch,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerBranch(res.GetBranch()), nil
}
