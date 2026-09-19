package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) GetCommit(ctx context.Context, org, project, commitID string) (*clientDomain.CommitDetail, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetCommit(ctx, &pb.GetCommitRequest{
		Context:  &pb.ProjectContext{Org: org, Project: project},
		CommitId: commitID,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toCommitDetail(res), nil
}

func (c *Client) WalkCommits(ctx context.Context, org, project, startCommitID, stopCommitID string, limit int) ([]*clientDomain.CommitWalkEntry, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	req := &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: org, Project: project},
		StartCommitId: startCommitID,
		Limit:         int32(limit),
	}
	if stopCommitID != "" {
		req.StopCommitId = &stopCommitID
	}
	res, err := client.WalkCommits(ctx, req)
	if err != nil {
		return nil, toDomainError(err)
	}
	entries := make([]*clientDomain.CommitWalkEntry, 0, len(res.GetCommits()))
	for _, e := range res.GetCommits() {
		if e == nil {
			continue
		}
		entries = append(entries, &clientDomain.CommitWalkEntry{
			ID:        e.GetCommitId(),
			Hash:      e.GetCommitHash(),
			Parent1ID: e.GetParent_1Id(),
			Parent2ID: e.GetParent_2Id(),
			Message:   e.GetMessage(),
			CreatedAt: e.GetCreatedAt().AsTime(),
		})
	}
	return entries, nil
}

func toCommitDetail(res *pb.GetCommitResponse) *clientDomain.CommitDetail {
	if res == nil || res.GetCommit() == nil {
		return nil
	}
	commit := res.GetCommit()
	return &clientDomain.CommitDetail{
		ID:        commit.GetCommitId(),
		Hash:      commit.GetCommitHash(),
		TreeHash:  commit.GetTreeHash(),
		Parent1ID: commit.GetParent_1Id(),
		Parent2ID: commit.GetParent_2Id(),
		Message:   commit.GetMessage(),
		CreatedAt: commit.GetCreatedAt().AsTime(),
		Tree:      toServerTreeNode(res.GetRootTree()),
	}
}
