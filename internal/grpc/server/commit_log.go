package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/typ.v4/slices"
)

func (n *nipaServer) GetCommitLog(ctx context.Context, req *pb.GetCommitLogRequest) (*pb.GetCommitLogResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	var startID *snow.ID
	if req.StartCommitId != nil {
		id, err := snow.ParseBase36(*req.StartCommitId)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid start commit id"))
		}
		startID = &id
	}
	entries, err := n.uc.Branch().GetCommitLog(ctx, project.ID, req.Branch, startID, int(req.Limit))
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetCommitLogResponse{
		Branch:  req.Branch,
		Commits: slices.Map(entries, commitLogEntryToPB),
	}, nil
}

func commitLogEntryToPB(entry *domain.CommitLogEntry) *pb.CommitLogEntry {
	c := &pb.CommitLogEntry{
		CommitId:    entry.ID.Base36(),
		CommitHash:  entry.Hash.String(),
		AuthorName:  entry.AuthorName,
		AuthorEmail: entry.AuthorEmail,
		Message:     entry.Message,
		CreatedAt:   timestamppb.New(entry.CreatedAt),
	}
	if entry.Parent1ID != nil {
		s := entry.Parent1ID.Base36()
		c.Parent_1Id = &s
	}
	if entry.Parent2ID != nil {
		s := entry.Parent2ID.Base36()
		c.Parent_2Id = &s
	}
	return c
}
