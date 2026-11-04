package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// ListMyProjects lists the readable projects of every organization the caller
// belongs to, for clients that browse before cloning.
func (n *nipaServer) ListMyProjects(ctx context.Context, _ *pb.ListMyProjectsRequest) (*pb.ListMyProjectsResponse, error) {
	claims, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, handleError(domain.NewErrorUnauthorized("authentication required"))
	}
	memberships, err := n.uc.Org().ListForUser(ctx, claims.UserID)
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMyProjectsResponse{Projects: []*pb.ProjectSummary{}}
	for _, m := range memberships {
		projects, err := n.uc.Project().List(ctx, m.Org.ID)
		if err != nil {
			return nil, handleError(err)
		}
		for _, p := range projects {
			resp.Projects = append(resp.Projects, &pb.ProjectSummary{
				Org: m.Org.Slug, OrgName: m.Org.Name, OrgRole: m.Role,
				Project: p.Slug, Name: p.Name, Description: p.Description,
			})
		}
	}
	return resp, nil
}

// ListDirectory lists the readable direct children of one directory.
func (n *nipaServer) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	node, err := n.uc.Branch().TreeAt(ctx, project.ID, req.GetRevision(), req.GetPath())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListDirectoryResponse{Entries: []*pb.DirectoryEntry{}}
	for _, dir := range node.TreeChildren {
		resp.Entries = append(resp.Entries, &pb.DirectoryEntry{Name: dir.Name, IsDir: true})
	}
	for _, f := range node.FileChildren {
		resp.Entries = append(resp.Entries, &pb.DirectoryEntry{Name: f.Name, SizeBytes: f.SizeBytes})
	}
	return resp, nil
}
