package server

import (
	"context"
	"fmt"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/typ.v4/slices"
)

func (n *nipaServer) GetListBranch(ctx context.Context, req *pb.GetListBranchRequest) (*pb.GetListBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
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
		return nil, handleError(err)
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
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().GetDefault(ctx, project.ID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetBranchResponse{
		Branch: domainBranchToPB(branch),
	}, nil
}

func (n *nipaServer) CreateBranch(ctx context.Context, req *pb.CreateBranchRequest) (*pb.CreateBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	fork := usecase.BranchForkPoint{BranchName: req.GetFromBranch()}
	if commitID := req.GetFromCommitId(); commitID != "" {
		id, err := snow.ParseBase36(commitID)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid commit id"))
		}
		fork.CommitID = &id
	}
	if commitHash := req.GetFromCommitHash(); commitHash != "" {
		hash, err := domain.ParseHashHex(commitHash)
		if err != nil {
			return nil, handleError(domain.NewErrorUser(fmt.Sprintf("invalid commit hash %q", commitHash)))
		}
		fork.CommitHash = &hash
	}
	branch, err := n.uc.Branch().CreateBranch(ctx, project.ID, req.GetName(), fork)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreateBranchResponse{
		Branch: domainBranchToPB(branch),
	}, nil
}

func (n *nipaServer) GetTreeManifest(ctx context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}

	root, err := n.uc.Branch().GetTreeManifest(ctx, project.ID, req.Branch, req.Path, req.GetTreeHash(), req.Recursive)
	if err != nil {
		return nil, handleError(err)
	}

	return &pb.GetTreeManifestResponse{
		Branch:   req.Branch,
		RootTree: domainTreeToPB(root),
	}, nil
}

func domainBranchToPB(branch *domain.Branch) *pb.Branch {
	return &pb.Branch{
		Id:          branch.ID.Base36(),
		Name:        branch.Name,
		IsProtected: branch.IsProtected,
		IsDefault:   branch.IsDefault,
		CommitId:    snowPtrToStringPtr(branch.CommitID),
		UpdatedAt:   timestamppb.New(branch.UpdatedAt),
		CreatedAt:   timestamppb.New(branch.CreatedAt),
	}
}

func snowPtrToStringPtr(ID *snow.ID) *string {
	if ID == nil {
		return nil
	}
	val := ID.Base36()
	return &val
}

func domainTreeToPB(node *domain.TreeNode) *pb.TreeManifest {
	if node == nil {
		return nil
	}
	manifest := &pb.TreeManifest{
		TreeHash: node.Hash.String(),
		Path:     node.Name,
	}
	for _, file := range node.FileChildren {
		manifest.Files = append(manifest.Files, domainFileToPB(file))
	}
	for _, child := range node.TreeChildren {
		manifest.SubTrees = append(manifest.SubTrees, domainTreeToPB(child))
	}
	return manifest
}

func domainFileToPB(file *domain.File) *pb.FileNode {
	node := &pb.FileNode{
		Path:      file.Name,
		Mode:      domainFileModeToPB(file.Mode),
		SizeBytes: file.SizeBytes,
		IsBinary:  file.IsBinary,
	}
	for _, chunk := range file.Chunks {
		node.ChunkHashes = append(node.ChunkHashes, chunk.Hash.String())
	}
	return node
}

func domainFileModeToPB(mode int) pb.FileMode {
	switch mode {
	case 444, 0o444:
		return pb.FileMode_FILE_MODE_READ_ONLY
	case 755, 0o755:
		return pb.FileMode_FILE_MODE_EXECUTABLE
	default:
		return pb.FileMode_FILE_MODE_READ_WRITE
	}
}
