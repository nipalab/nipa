package service

import (
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

const memberLimit = 50

// Members lists the users who can read the clone's project, matched on name,
// for reviewer and assignee pickers.
func (s *Service) Members(ctx context.Context, root, query string) ([]Person, error) {
	var res *daemonpb.ProxyProjectMembersResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyProjectMembers(ctx, &daemonpb.ProxyProjectMembersRequest{Root: root, Request: &pb.ListProjectMembersRequest{Query: strings.TrimSpace(query), Limit: memberLimit}})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Person, 0, len(res.GetResponse().GetMembers()))
	for _, m := range res.GetResponse().GetMembers() {
		out = append(out, Person{ID: m.GetId(), Name: m.GetName()})
	}
	return out, nil
}

// AssignMergeRequest replaces the assignees of a request.
func (s *Service) AssignMergeRequest(ctx context.Context, root string, number int64, userIDs []string) (*MergeRequest, error) {
	var res *daemonpb.ProxyMergeRequestAssignResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyMergeRequestAssign(ctx, &daemonpb.ProxyMergeRequestAssignRequest{Root: root, Request: &pb.SetMergeRequestAssigneesRequest{Number: number, UserIds: userIDs}})
		return err
	})
	if err != nil {
		return nil, err
	}
	mr := toMergeRequest(res.GetResponse().GetMergeRequest())
	return &mr, nil
}

// RequestReview asks one more person to review a request.
func (s *Service) RequestReview(ctx context.Context, root string, number int64, userID string) error {
	if userID == "" {
		return errors.New("choose who should review")
	}
	return s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		_, err := c.ProxyMergeRequestRequestReview(ctx, &daemonpb.ProxyMergeRequestRequestReviewRequest{Root: root, Request: &pb.RequestMergeRequestReviewRequest{Number: number, UserId: userID}})
		return err
	})
}
