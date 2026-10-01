package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Client) CreateTag(ctx context.Context, org, project, name, message, fromBranch, fromCommitID, fromCommitHash string) (*clientDomain.Tag, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CreateTag(ctx, &pb.CreateTagRequest{
		Context:        &pb.ProjectContext{Org: org, Project: project},
		Name:           name,
		Message:        message,
		FromBranch:     fromBranch,
		FromCommitId:   fromCommitID,
		FromCommitHash: fromCommitHash,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientTag(res.GetTag()), nil
}

func (c *Client) ListTags(ctx context.Context, org, project string) ([]*clientDomain.Tag, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	const pageSize = 100
	tags := make([]*clientDomain.Tag, 0)
	var lastCreatedAt *timestamppb.Timestamp
	var lastID string
	for {
		req := &pb.ListTagsRequest{
			Context: &pb.ProjectContext{Org: org, Project: project},
			Limit:   pageSize,
		}
		if lastCreatedAt != nil {
			req.LastCreatedAt = lastCreatedAt
		}
		if lastID != "" {
			req.LastId = &lastID
		}
		res, err := client.ListTags(ctx, req)
		if err != nil {
			return nil, toDomainError(err)
		}
		page := res.GetTags()
		for _, tag := range page {
			if converted := toClientTag(tag); converted != nil {
				tags = append(tags, converted)
			}
		}
		if len(page) < pageSize {
			return tags, nil
		}
		last := page[len(page)-1]
		lastCreatedAt = last.GetCreatedAt()
		lastID = last.GetId()
		if lastCreatedAt == nil || lastID == "" {
			return tags, nil
		}
	}
}

func (c *Client) DeleteTag(ctx context.Context, org, project, name string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.DeleteTag(ctx, &pb.DeleteTagRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Name:    name,
	})
	return toDomainError(err)
}

func toClientTag(tag *pb.Tag) *clientDomain.Tag {
	if tag == nil {
		return nil
	}
	return &clientDomain.Tag{
		ID:        tag.GetId(),
		Name:      tag.GetName(),
		CommitID:  tag.GetCommitId(),
		Message:   tag.GetMessage(),
		UserID:    tag.GetUserId(),
		CreatedAt: tag.GetCreatedAt().AsTime(),
	}
}
