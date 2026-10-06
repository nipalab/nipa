package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) ListMergeRequestReviews(ctx context.Context, org, project string, number int64) ([]*clientDomain.MergeRequestReview, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequestReviews(ctx, &pb.ListMergeRequestReviewsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	reviews := make([]*clientDomain.MergeRequestReview, 0, len(res.GetReviews()))
	for _, review := range res.GetReviews() {
		if converted := toClientReview(review); converted != nil {
			reviews = append(reviews, converted)
		}
	}
	return reviews, nil
}

func (c *Client) SubmitMergeRequestReview(ctx context.Context, org, project string, number int64, state, body string) (*clientDomain.MergeRequestReview, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.SubmitMergeRequestReview(ctx, &pb.SubmitMergeRequestReviewRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
		State:   state,
		Body:    body,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientReview(res.GetReview()), nil
}

func (c *Client) ListMergeRequestThreads(ctx context.Context, org, project string, number int64) ([]*clientDomain.MergeRequestThread, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequestThreads(ctx, &pb.ListMergeRequestThreadsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	threads := make([]*clientDomain.MergeRequestThread, 0, len(res.GetThreads()))
	for _, thread := range res.GetThreads() {
		if converted := toClientThread(thread); converted != nil {
			threads = append(threads, converted)
		}
	}
	return threads, nil
}

func (c *Client) AddMergeRequestComment(ctx context.Context, org, project string, number int64, filePath string, oldLine, newLine *int64, body string) (*clientDomain.MergeRequestThread, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.AddMergeRequestComment(ctx, &pb.AddMergeRequestCommentRequest{
		Context:  &pb.ProjectContext{Org: org, Project: project},
		Number:   number,
		FilePath: filePath,
		OldLine:  oldLine,
		NewLine:  newLine,
		Body:     body,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientThread(res.GetThread()), nil
}

func (c *Client) ReplyMergeRequestThread(ctx context.Context, org, project string, number int64, threadID, body string) (*clientDomain.MergeRequestComment, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ReplyMergeRequestThread(ctx, &pb.ReplyMergeRequestThreadRequest{
		Context:  &pb.ProjectContext{Org: org, Project: project},
		Number:   number,
		ThreadId: threadID,
		Body:     body,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientComment(res.GetComment()), nil
}

func (c *Client) ResolveMergeRequestThread(ctx context.Context, org, project string, number int64, threadID string, resolved bool) (*clientDomain.MergeRequestThread, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ResolveMergeRequestThread(ctx, &pb.ResolveMergeRequestThreadRequest{
		Context:  &pb.ProjectContext{Org: org, Project: project},
		Number:   number,
		ThreadId: threadID,
		Resolved: resolved,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientThread(res.GetThread()), nil
}

func (c *Client) ListMergeRequestTimeline(ctx context.Context, org, project string, number int64) ([]*clientDomain.MergeRequestTimelineItem, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequestTimeline(ctx, &pb.ListMergeRequestTimelineRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	items := make([]*clientDomain.MergeRequestTimelineItem, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, toClientTimelineItem(item))
	}
	return items, nil
}

func (c *Client) ListMergeRequestReviewRequests(ctx context.Context, org, project string, number int64) ([]*clientDomain.MergeRequestReviewRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListMergeRequestReviewRequests(ctx, &pb.ListMergeRequestReviewRequestsRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	requests := make([]*clientDomain.MergeRequestReviewRequest, 0, len(res.GetReviewRequests()))
	for _, request := range res.GetReviewRequests() {
		requests = append(requests, toClientReviewRequest(request))
	}
	return requests, nil
}

func (c *Client) RequestMergeRequestReview(ctx context.Context, org, project string, number int64, userID string) (*clientDomain.MergeRequestReviewRequest, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.RequestMergeRequestReview(ctx, &pb.RequestMergeRequestReviewRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
		UserId:  userID,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientReviewRequest(res.GetReviewRequest()), nil
}

func (c *Client) RemoveMergeRequestReviewRequest(ctx context.Context, org, project string, number int64, userID string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.RemoveMergeRequestReviewRequest(ctx, &pb.RemoveMergeRequestReviewRequestRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Number:  number,
		UserId:  userID,
	})
	if err != nil {
		return toDomainError(err)
	}
	return nil
}

func toClientReviewActor(actor *pb.ReviewActor) clientDomain.ReviewActor {
	if actor == nil {
		return clientDomain.ReviewActor{}
	}
	return clientDomain.ReviewActor{
		UserID:   actor.GetUserId(),
		Name:     actor.GetName(),
		PhotoURL: actor.GetPhotoUrl(),
	}
}

func toClientReviewActorPtr(actor *pb.ReviewActor) *clientDomain.ReviewActor {
	if actor == nil {
		return nil
	}
	converted := toClientReviewActor(actor)
	return &converted
}

func toClientReview(review *pb.MergeRequestReviewDetail) *clientDomain.MergeRequestReview {
	if review == nil {
		return nil
	}
	converted := &clientDomain.MergeRequestReview{
		ID:              review.GetId(),
		MergeRequestID:  review.GetMergeRequestId(),
		Reviewer:        toClientReviewActor(review.GetReviewer()),
		State:           review.GetState(),
		Body:            review.GetBody(),
		HeadCommitID:    review.GetHeadCommitId(),
		Stale:           review.GetStale(),
		DismissedBy:     toClientReviewActorPtr(review.GetDismissedBy()),
		DismissedReason: review.GetDismissedReason(),
		CreatedAt:       review.GetCreatedAt().AsTime(),
		UpdatedAt:       review.GetUpdatedAt().AsTime(),
	}
	if review.GetDismissedAt() != nil {
		dismissedAt := review.GetDismissedAt().AsTime()
		converted.DismissedAt = &dismissedAt
	}
	return converted
}

func toClientComment(comment *pb.ReviewCommentDetail) *clientDomain.MergeRequestComment {
	if comment == nil {
		return nil
	}
	return &clientDomain.MergeRequestComment{
		ID:        comment.GetId(),
		ThreadID:  comment.GetThreadId(),
		User:      toClientReviewActor(comment.GetUser()),
		Body:      comment.GetBody(),
		System:    comment.GetSystem(),
		Edited:    comment.GetEdited(),
		CreatedAt: comment.GetCreatedAt().AsTime(),
		UpdatedAt: comment.GetUpdatedAt().AsTime(),
	}
}

func toClientThread(thread *pb.MergeRequestThreadDetail) *clientDomain.MergeRequestThread {
	if thread == nil {
		return nil
	}
	converted := &clientDomain.MergeRequestThread{
		ID:             thread.GetId(),
		MergeRequestID: thread.GetMergeRequestId(),
		ReviewID:       thread.GetReviewId(),
		FilePath:       thread.GetFilePath(),
		Side:           thread.GetSide(),
		Outdated:       thread.GetOutdated(),
		Resolved:       thread.GetResolved(),
		ResolvedBy:     toClientReviewActorPtr(thread.GetResolvedBy()),
		CreatedBy:      toClientReviewActor(thread.GetCreatedBy()),
		CreatedAt:      thread.GetCreatedAt().AsTime(),
	}
	if thread.OldLine != nil {
		oldLine := thread.GetOldLine()
		converted.OldLine = &oldLine
	}
	if thread.NewLine != nil {
		newLine := thread.GetNewLine()
		converted.NewLine = &newLine
	}
	if thread.GetResolvedAt() != nil {
		resolvedAt := thread.GetResolvedAt().AsTime()
		converted.ResolvedAt = &resolvedAt
	}
	for _, comment := range thread.GetComments() {
		if convertedComment := toClientComment(comment); convertedComment != nil {
			converted.Comments = append(converted.Comments, convertedComment)
		}
	}
	return converted
}

func toClientReviewRequest(request *pb.ReviewRequestDetail) *clientDomain.MergeRequestReviewRequest {
	if request == nil {
		return nil
	}
	return &clientDomain.MergeRequestReviewRequest{
		ID:             request.GetId(),
		MergeRequestID: request.GetMergeRequestId(),
		Reviewer:       toClientReviewActor(request.GetReviewer()),
		RequestedBy:    toClientReviewActor(request.GetRequestedBy()),
		CreatedAt:      request.GetCreatedAt().AsTime(),
	}
}

func toClientTimelineItem(item *pb.MergeRequestTimelineItem) *clientDomain.MergeRequestTimelineItem {
	if item == nil {
		return nil
	}
	return &clientDomain.MergeRequestTimelineItem{
		ID:         item.GetId(),
		Kind:       item.GetKind(),
		Actor:      toClientReviewActor(item.GetActor()),
		Subject:    toClientReviewActorPtr(item.GetSubject()),
		Body:       item.GetBody(),
		CommitID:   item.GetCommitId(),
		CommitHash: item.GetCommitHash(),
		CreatedAt:  item.GetCreatedAt().AsTime(),
	}
}
