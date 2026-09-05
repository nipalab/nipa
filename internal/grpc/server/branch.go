package server

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/typ.v4/slices"
)

func (n *nipaServer) GetListBranch(ctx context.Context, req *pb.GetListBranchRequest) (*pb.GetListBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, err
	}
	var lastUpdate *time.Time
	if req.LastUpdatedAt != nil {
		t := req.LastUpdatedAt.AsTime()
		lastUpdate = &t
	}
	var lastID snow.ID
	if req.LastId != nil {
		lastID, err = snow.ParseBase36(*req.LastId)
		if err != nil {
			return nil, handleError(err)
		}
	}
	branches, err := n.uc.Branch().ListBranches(ctx, project.ID, int(req.Limit), lastUpdate, lastID)
	if err != nil {
		return nil, handleError(err)
	}

	return &pb.GetListBranchResponse{
		Branches: slices.Map(branches, func(b *domain.Branch) *pb.Branch {
			return domainBranchToPB(b)
		}),
	}, nil
}

func (n *nipaServer) GetBranch(ctx context.Context, req *pb.GetBranchRequest) (*pb.GetBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, err
	}
	branchID, err := snow.ParseBase36(req.BranchId)
	if err != nil {
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().GetByProjectIDAndID(ctx, project.ID, branchID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetBranchResponse{
		Branch: domainBranchToPB(branch),
	}, nil
}

func (n *nipaServer) GetDefaultBranch(ctx context.Context, req *pb.GetDefaultBranchRequest) (*pb.GetBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, err
	}
	branch, err := n.uc.Branch().GetDefault(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return &pb.GetBranchResponse{
		Branch: domainBranchToPB(branch),
	}, nil
}

func domainBranchToPB(branch *domain.Branch) *pb.Branch {
	return &pb.Branch{
		Id:          branch.ID.Base36(),
		Name:        branch.Name,
		IsProtected: branch.IsProtected,
		IsDefault:   branch.IsDefault,
		CommitId:    branch.CommitID.Base36(),
		UpdatedAt:   timestamppb.New(branch.UpdatedAt),
		CreatedAt:   timestamppb.New(branch.CreatedAt),
	}
}
