package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) GetCommit(ctx context.Context, req *pb.GetCommitRequest) (*pb.GetCommitResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	commitID, err := snow.ParseBase36(req.CommitId)
	if err != nil {
		return nil, handleError(domain.NewErrorUser("invalid commit id"))
	}
	commit, root, err := n.uc.Branch().GetCommit(ctx, project.ID, commitID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetCommitResponse{
		Commit:   domainCommitToDetailPB(commit, root),
		RootTree: domainTreeToPB(root),
	}, nil
}

func (n *nipaServer) WalkCommits(ctx context.Context, req *pb.WalkCommitsRequest) (*pb.WalkCommitsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	startID, err := snow.ParseBase36(req.StartCommitId)
	if err != nil {
		return nil, handleError(domain.NewErrorUser("invalid start commit id"))
	}
	var stopID *snow.ID
	if req.StopCommitId != nil {
		id, err := snow.ParseBase36(*req.StopCommitId)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid stop commit id"))
		}
		stopID = &id
	}
	commits, err := n.uc.Branch().WalkCommits(ctx, project.ID, startID, stopID, int(req.Limit))
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.WalkCommitsResponse{Commits: make([]*pb.CommitWalkEntry, 0, len(commits))}
	for _, commit := range commits {
		resp.Commits = append(resp.Commits, domainCommitToWalkEntryPB(commit))
	}
	return resp, nil
}

func domainCommitToDetailPB(commit *domain.Commit, root *domain.TreeNode) *pb.CommitDetail {
	detail := &pb.CommitDetail{
		CommitId:   commit.ID.Base36(),
		CommitHash: commit.Hash.String(),
		Parent_1Id: snowPtrToStringPtr(commit.Parent1ID),
		Parent_2Id: snowPtrToStringPtr(commit.Parent2ID),
		Message:    commit.Message,
		CreatedAt:  timestamppb.New(commit.CreatedAt),
	}
	if root != nil {
		detail.TreeHash = root.Hash.String()
	}
	return detail
}

func domainCommitToWalkEntryPB(commit *domain.Commit) *pb.CommitWalkEntry {
	return &pb.CommitWalkEntry{
		CommitId:   commit.ID.Base36(),
		CommitHash: commit.Hash.String(),
		Parent_1Id: snowPtrToStringPtr(commit.Parent1ID),
		Parent_2Id: snowPtrToStringPtr(commit.Parent2ID),
		Message:    commit.Message,
		CreatedAt:  timestamppb.New(commit.CreatedAt),
	}
}
