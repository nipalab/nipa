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

func (n *nipaServer) GetBranchByName(ctx context.Context, req *pb.GetBranchByNameRequest) (*pb.GetBranchByNameResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().GetBranchByName(ctx, project.ID, req.GetName())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetBranchByNameResponse{
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

func (n *nipaServer) RenameBranch(ctx context.Context, req *pb.RenameBranchRequest) (*pb.RenameBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().Rename(ctx, project.ID, req.GetName(), req.GetNewName())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.RenameBranchResponse{Branch: domainBranchToPB(branch)}, nil
}

func (n *nipaServer) DeleteBranch(ctx context.Context, req *pb.DeleteBranchRequest) (*pb.DeleteBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.Branch().Delete(ctx, project.ID, req.GetName()); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeleteBranchResponse{}, nil
}

func (n *nipaServer) SetDefaultBranch(ctx context.Context, req *pb.SetDefaultBranchRequest) (*pb.SetDefaultBranchResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().SetDefault(ctx, project.ID, req.GetName())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.SetDefaultBranchResponse{Branch: domainBranchToPB(branch)}, nil
}

func (n *nipaServer) SetBranchProtection(ctx context.Context, req *pb.SetBranchProtectionRequest) (*pb.SetBranchProtectionResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	branch, err := n.uc.Branch().SetProtection(ctx, project.ID, req.GetName(), req.GetIsProtected())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.SetBranchProtectionResponse{Branch: domainBranchToPB(branch)}, nil
}

func (n *nipaServer) GetTreeManifest(ctx context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}

	paths := req.GetPaths()
	if len(paths) == 0 && req.GetPath() != "" {
		paths = []string{req.GetPath()}
	}
	root, err := n.uc.Branch().GetTreeManifest(ctx, project.ID, req.Branch, paths, req.GetTreeHash(), req.Recursive)
	if err != nil {
		return nil, handleError(err)
	}

	return &pb.GetTreeManifestResponse{
		Branch:   req.Branch,
		RootTree: domainTreeToPB(root),
	}, nil
}

func (n *nipaServer) GetMergeBase(ctx context.Context, req *pb.GetMergeBaseRequest) (*pb.GetMergeBaseResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}

	target, err := mergeRef(req.GetTargetCommitId(), req.GetTargetBranch())
	if err != nil {
		return nil, handleError(err)
	}
	source, err := mergeRef(req.GetSourceCommitId(), req.GetSourceBranch())
	if err != nil {
		return nil, handleError(err)
	}
	info, err := n.uc.Branch().GetMergeBase(ctx, project.ID, target, source)
	if err != nil {
		return nil, handleError(err)
	}

	resp := &pb.GetMergeBaseResponse{
		TargetBranch:  info.TargetBranch,
		SourceBranch:  info.SourceBranch,
		MergeBaseTree: domainTreeToPB(info.MergeBaseTree),
	}
	if info.TargetCommitID != nil {
		resp.TargetCommitId = info.TargetCommitID.Base36()
	}
	if info.SourceCommitID != nil {
		resp.SourceCommitId = info.SourceCommitID.Base36()
	}
	if info.MergeBaseCommitID != nil {
		resp.MergeBaseCommitId = info.MergeBaseCommitID.Base36()
	}
	if info.TargetCommitHash != nil {
		resp.TargetCommitHash = info.TargetCommitHash.String()
	}
	if info.SourceCommitHash != nil {
		resp.SourceCommitHash = info.SourceCommitHash.String()
	}
	return resp, nil
}

func (n *nipaServer) MergeFastForward(ctx context.Context, req *pb.MergeFastForwardRequest) (*pb.MergeFastForwardResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}

	branch, err := n.uc.Branch().FastForward(ctx, project.ID, req.TargetBranch, req.SourceBranch)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.MergeFastForwardResponse{
		Branch: domainBranchToPB(branch),
	}
	if branch.CommitID != nil {
		resp.MovedToCommitId = branch.CommitID.Base36()
	}
	return resp, nil
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

func mergeRef(commitID, branchName string) (usecase.MergeRef, error) {
	ref := usecase.MergeRef{BranchName: branchName}
	if commitID == "" {
		return ref, nil
	}
	id, err := snow.ParseBase36(commitID)
	if err != nil {
		return ref, domain.NewErrorUser("invalid commit id")
	}
	ref.CommitID = &id
	return ref, nil
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
		Encoding:  file.Encoding,
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
