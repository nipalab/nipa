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

func (n *nipaServer) ListTags(ctx context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	var createdBefore *time.Time
	if req.LastCreatedAt != nil {
		t := req.LastCreatedAt.AsTime()
		createdBefore = &t
	}
	var lastID snow.ID
	if req.LastId != nil {
		lastID, err = snow.ParseBase36(*req.LastId)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid last id"))
		}
	}
	tags, err := n.uc.Tag().ListTags(ctx, project.ID, int(req.GetLimit()), createdBefore, lastID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ListTagsResponse{
		Tags: slices.Map(tags, func(t *domain.Tag) *pb.Tag {
			return domainTagToPB(t)
		}),
	}, nil
}

func (n *nipaServer) GetTagByName(ctx context.Context, req *pb.GetTagByNameRequest) (*pb.GetTagByNameResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	tag, err := n.uc.Tag().GetTagByName(ctx, project.ID, req.GetName())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetTagByNameResponse{
		Tag: domainTagToPB(tag),
	}, nil
}

func (n *nipaServer) CreateTag(ctx context.Context, req *pb.CreateTagRequest) (*pb.CreateTagResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	target := usecase.TagTarget{BranchName: req.GetFromBranch()}
	if commitID := req.GetFromCommitId(); commitID != "" {
		id, err := snow.ParseBase36(commitID)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid commit id"))
		}
		target.CommitID = &id
	}
	if commitHash := req.GetFromCommitHash(); commitHash != "" {
		hash, err := domain.ParseHashHex(commitHash)
		if err != nil {
			return nil, handleError(domain.NewErrorUser(fmt.Sprintf("invalid commit hash %q", commitHash)))
		}
		target.CommitHash = &hash
	}
	tag, err := n.uc.Tag().CreateTag(ctx, project.ID, req.GetName(), target, req.GetMessage())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreateTagResponse{
		Tag: domainTagToPB(tag),
	}, nil
}

func (n *nipaServer) DeleteTag(ctx context.Context, req *pb.DeleteTagRequest) (*pb.DeleteTagResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.Tag().DeleteTag(ctx, project.ID, req.GetName()); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeleteTagResponse{}, nil
}

func domainTagToPB(tag *domain.Tag) *pb.Tag {
	return &pb.Tag{
		Id:        tag.ID.Base36(),
		Name:      tag.Name,
		CommitId:  tag.CommitID.Base36(),
		Message:   tag.Message,
		UserId:    tag.UserID.Base36(),
		CreatedAt: timestamppb.New(tag.CreatedAt),
		UpdatedAt: timestamppb.New(tag.UpdatedAt),
	}
}
