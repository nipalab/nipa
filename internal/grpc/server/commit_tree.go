package server

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (n *nipaServer) GetCommitTree(ctx context.Context, req *pb.GetCommitTreeRequest) (*pb.GetCommitTreeResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	var ref usecase.CommitRef
	if id := req.GetCommitId(); id != "" {
		parsed, err := snow.ParseBase36(id)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid commit id"))
		}
		ref.CommitID = &parsed
	}
	if hash := req.GetCommitHash(); hash != "" {
		parsed, err := domain.ParseHashHex(hash)
		if err != nil {
			return nil, handleError(domain.NewErrorUser(fmt.Sprintf("invalid commit hash %q", hash)))
		}
		ref.CommitHash = &parsed
	}
	tree, err := n.uc.Branch().GetCommitTree(ctx, project.ID, ref)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetCommitTreeResponse{
		CommitId: tree.CommitID.Base36(),
		RootTree: domainTreeToPB(tree.Tree),
	}, nil
}
