package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (n *nipaServer) SubmitMergeRequestReview(ctx context.Context, req *pb.SubmitMergeRequestReviewRequest) (*pb.SubmitMergeRequestReviewResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	comments := make([]usecase.ThreadComment, 0, len(req.GetComments()))
	for _, comment := range req.GetComments() {
		comments = append(comments, usecase.ThreadComment{
			FilePath: comment.GetFilePath(),
			OldLine:  int64ToIntPtr(comment.OldLine),
			NewLine:  int64ToIntPtr(comment.NewLine),
			Body:     comment.GetBody(),
		})
	}
	review, err := n.uc.MergeRequestReview().SubmitReview(
		ctx, project.ID, req.GetNumber(), req.GetState(), req.GetBody(), comments,
	)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.SubmitMergeRequestReviewResponse{Review: domainReviewToPB(review)}, nil
}

func (n *nipaServer) ListMergeRequestReviews(ctx context.Context, req *pb.ListMergeRequestReviewsRequest) (*pb.ListMergeRequestReviewsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	reviews, err := n.uc.MergeRequestReview().Reviews(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestReviewsResponse{Reviews: make([]*pb.MergeRequestReviewDetail, 0, len(reviews))}
	for _, review := range reviews {
		resp.Reviews = append(resp.Reviews, domainReviewToPB(review))
	}
	return resp, nil
}

func (n *nipaServer) GetMergeRequestReviewState(ctx context.Context, req *pb.GetMergeRequestReviewStateRequest) (*pb.GetMergeRequestReviewStateResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	state, err := n.uc.MergeRequestReview().ReviewState(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.GetMergeRequestReviewStateResponse{State: domainReviewStateToPB(state)}, nil
}

func (n *nipaServer) WithdrawMergeRequestReview(ctx context.Context, req *pb.WithdrawMergeRequestReviewRequest) (*pb.WithdrawMergeRequestReviewResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	reviewID, err := parseBase36ID(req.GetReviewId())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.MergeRequestReview().WithdrawReview(ctx, project.ID, req.GetNumber(), reviewID); err != nil {
		return nil, handleError(err)
	}
	return &pb.WithdrawMergeRequestReviewResponse{}, nil
}

func (n *nipaServer) DismissMergeRequestReview(ctx context.Context, req *pb.DismissMergeRequestReviewRequest) (*pb.DismissMergeRequestReviewResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	reviewID, err := parseBase36ID(req.GetReviewId())
	if err != nil {
		return nil, handleError(err)
	}
	review, err := n.uc.MergeRequestReview().DismissReview(ctx, project.ID, req.GetNumber(), reviewID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.DismissMergeRequestReviewResponse{Review: domainReviewToPB(review)}, nil
}

func (n *nipaServer) ListMergeRequestThreads(ctx context.Context, req *pb.ListMergeRequestThreadsRequest) (*pb.ListMergeRequestThreadsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threads, err := n.uc.MergeRequestReview().Threads(ctx, project.ID, req.GetNumber(), req.Resolved)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ListMergeRequestThreadsResponse{Threads: domainThreadsToPB(threads)}, nil
}

func (n *nipaServer) AddMergeRequestComment(ctx context.Context, req *pb.AddMergeRequestCommentRequest) (*pb.AddMergeRequestCommentResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	thread, err := n.uc.MergeRequestReview().AddComment(ctx, project.ID, req.GetNumber(), usecase.ThreadComment{
		FilePath: req.GetFilePath(),
		OldLine:  int64ToIntPtr(req.OldLine),
		NewLine:  int64ToIntPtr(req.NewLine),
		Body:     req.GetBody(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.AddMergeRequestCommentResponse{Thread: domainThreadToPB(thread)}, nil
}

func (n *nipaServer) ReplyMergeRequestThread(ctx context.Context, req *pb.ReplyMergeRequestThreadRequest) (*pb.ReplyMergeRequestThreadResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threadID, err := parseBase36ID(req.GetThreadId())
	if err != nil {
		return nil, handleError(err)
	}
	comment, err := n.uc.MergeRequestReview().Reply(ctx, project.ID, req.GetNumber(), threadID, req.GetBody())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ReplyMergeRequestThreadResponse{Comment: domainReviewCommentToPB(comment)}, nil
}

func (n *nipaServer) UpdateMergeRequestComment(ctx context.Context, req *pb.UpdateMergeRequestCommentRequest) (*pb.UpdateMergeRequestCommentResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threadID, err := parseBase36ID(req.GetThreadId())
	if err != nil {
		return nil, handleError(err)
	}
	commentID, err := parseBase36ID(req.GetCommentId())
	if err != nil {
		return nil, handleError(err)
	}
	comment, err := n.uc.MergeRequestReview().UpdateComment(ctx, project.ID, req.GetNumber(), threadID, commentID, req.GetBody())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.UpdateMergeRequestCommentResponse{Comment: domainReviewCommentToPB(comment)}, nil
}

func (n *nipaServer) DeleteMergeRequestComment(ctx context.Context, req *pb.DeleteMergeRequestCommentRequest) (*pb.DeleteMergeRequestCommentResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threadID, err := parseBase36ID(req.GetThreadId())
	if err != nil {
		return nil, handleError(err)
	}
	commentID, err := parseBase36ID(req.GetCommentId())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.MergeRequestReview().DeleteComment(ctx, project.ID, req.GetNumber(), threadID, commentID); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeleteMergeRequestCommentResponse{}, nil
}

func (n *nipaServer) ResolveMergeRequestThread(ctx context.Context, req *pb.ResolveMergeRequestThreadRequest) (*pb.ResolveMergeRequestThreadResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threadID, err := parseBase36ID(req.GetThreadId())
	if err != nil {
		return nil, handleError(err)
	}
	thread, err := n.uc.MergeRequestReview().SetThreadResolved(ctx, project.ID, req.GetNumber(), threadID, req.GetResolved())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ResolveMergeRequestThreadResponse{Thread: domainThreadToPB(thread)}, nil
}

func (n *nipaServer) DeleteMergeRequestThread(ctx context.Context, req *pb.DeleteMergeRequestThreadRequest) (*pb.DeleteMergeRequestThreadResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	threadID, err := parseBase36ID(req.GetThreadId())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.MergeRequestReview().DeleteThread(ctx, project.ID, req.GetNumber(), threadID); err != nil {
		return nil, handleError(err)
	}
	return &pb.DeleteMergeRequestThreadResponse{}, nil
}

func (n *nipaServer) ListMergeRequestReviewRequests(ctx context.Context, req *pb.ListMergeRequestReviewRequestsRequest) (*pb.ListMergeRequestReviewRequestsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	requests, err := n.uc.MergeRequestReview().ReviewRequests(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestReviewRequestsResponse{
		ReviewRequests: make([]*pb.ReviewRequestDetail, 0, len(requests)),
	}
	for _, request := range requests {
		resp.ReviewRequests = append(resp.ReviewRequests, domainReviewRequestToPB(request))
	}
	return resp, nil
}

func (n *nipaServer) RequestMergeRequestReview(ctx context.Context, req *pb.RequestMergeRequestReviewRequest) (*pb.RequestMergeRequestReviewResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	userID, err := parseBase36ID(req.GetUserId())
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequestReview().RequestReview(ctx, project.ID, req.GetNumber(), userID)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.RequestMergeRequestReviewResponse{ReviewRequest: domainReviewRequestToPB(request)}, nil
}

func (n *nipaServer) RemoveMergeRequestReviewRequest(ctx context.Context, req *pb.RemoveMergeRequestReviewRequestRequest) (*pb.RemoveMergeRequestReviewRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	userID, err := parseBase36ID(req.GetUserId())
	if err != nil {
		return nil, handleError(err)
	}
	if err := n.uc.MergeRequestReview().RemoveReviewRequest(ctx, project.ID, req.GetNumber(), userID); err != nil {
		return nil, handleError(err)
	}
	return &pb.RemoveMergeRequestReviewRequestResponse{}, nil
}

func (n *nipaServer) ListMergeRequestTimeline(ctx context.Context, req *pb.ListMergeRequestTimelineRequest) (*pb.ListMergeRequestTimelineResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	items, err := n.uc.MergeRequestReview().Timeline(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestTimelineResponse{Items: make([]*pb.MergeRequestTimelineItem, 0, len(items))}
	for _, item := range items {
		entry := &pb.MergeRequestTimelineItem{
			Id:         snow.ID(item.ID).Base36(),
			Kind:       item.Kind,
			Actor:      domainReviewActorToPB(&item.Actor),
			Subject:    domainReviewActorToPB(item.Subject),
			Body:       item.Body,
			CommitHash: item.CommitHash,
			CreatedAt:  timestamppb.New(item.CreatedAt),
		}
		if item.CommitID != nil {
			entry.CommitId = item.CommitID.Base36()
		}
		resp.Items = append(resp.Items, entry)
	}
	return resp, nil
}

func domainReviewToPB(review *domain.MergeRequestReview) *pb.MergeRequestReviewDetail {
	if review == nil {
		return nil
	}
	detail := &pb.MergeRequestReviewDetail{
		Id:              snow.ID(review.ID).Base36(),
		MergeRequestId:  review.MergeRequestID,
		Reviewer:        domainReviewActorToPB(&review.Reviewer),
		State:           review.State,
		Body:            review.Body,
		HeadCommitId:    review.HeadCommitID.Base36(),
		Stale:           review.Stale,
		DismissedReason: review.DismissedReason,
		CreatedAt:       timestamppb.New(review.CreatedAt),
		UpdatedAt:       timestamppb.New(review.UpdatedAt),
	}
	if review.DismissedAt != nil {
		detail.DismissedAt = timestamppb.New(*review.DismissedAt)
		detail.DismissedBy = domainReviewActorToPB(review.DismissedBy)
	}
	return detail
}

func domainReviewStateToPB(state *domain.MergeRequestReviewState) *pb.MergeRequestReviewState {
	if state == nil {
		return nil
	}
	outstanding := make([]string, 0, len(state.OutstandingReviewers))
	for _, id := range state.OutstandingReviewers {
		outstanding = append(outstanding, id.Base36())
	}
	return &pb.MergeRequestReviewState{
		Approvals:            int32(state.Approvals),
		ChangesRequested:     int32(state.ChangesRequested),
		DismissedApprovals:   int32(state.DismissedApprovals),
		OutstandingReviewers: outstanding,
		HeadCommitId:         snow.ID(state.HeadCommitID).Base36(),
	}
}

func domainReviewRequestToPB(request *domain.MergeRequestReviewRequest) *pb.ReviewRequestDetail {
	if request == nil {
		return nil
	}
	return &pb.ReviewRequestDetail{
		Id:             snow.ID(request.ID).Base36(),
		MergeRequestId: request.MergeRequestID,
		Reviewer:       domainReviewActorToPB(&request.Reviewer),
		RequestedBy:    domainReviewActorToPB(&request.RequestedBy),
		CreatedAt:      timestamppb.New(request.CreatedAt),
	}
}

func domainThreadsToPB(threads []*domain.MergeRequestThread) []*pb.MergeRequestThreadDetail {
	out := make([]*pb.MergeRequestThreadDetail, 0, len(threads))
	for _, thread := range threads {
		out = append(out, domainThreadToPB(thread))
	}
	return out
}

func domainThreadToPB(thread *domain.MergeRequestThread) *pb.MergeRequestThreadDetail {
	if thread == nil {
		return nil
	}
	detail := &pb.MergeRequestThreadDetail{
		Id:             snow.ID(thread.ID).Base36(),
		MergeRequestId: thread.MergeRequestID,
		FilePath:       thread.FilePath,
		OldLine:        intToInt64Ptr(thread.OldLine),
		NewLine:        intToInt64Ptr(thread.NewLine),
		Resolved:       thread.Resolved,
		Outdated:       thread.Outdated,
		CreatedBy:      domainReviewActorToPB(&thread.CreatedBy),
		CreatedAt:      timestamppb.New(thread.CreatedAt),
		Comments:       make([]*pb.ReviewCommentDetail, 0, len(thread.Comments)),
	}
	if !thread.IsTopLevel() {
		detail.Side = thread.Side()
	}
	if thread.ReviewID != nil {
		detail.ReviewId = thread.ReviewID.Base36()
	}
	if thread.ResolvedAt != nil {
		detail.ResolvedAt = timestamppb.New(*thread.ResolvedAt)
	}
	detail.ResolvedBy = domainReviewActorToPB(thread.ResolvedBy)
	for _, comment := range thread.Comments {
		detail.Comments = append(detail.Comments, domainReviewCommentToPB(comment))
	}
	return detail
}

func domainReviewCommentToPB(comment *domain.MergeRequestComment) *pb.ReviewCommentDetail {
	if comment == nil {
		return nil
	}
	return &pb.ReviewCommentDetail{
		Id:        snow.ID(comment.ID).Base36(),
		ThreadId:  comment.ThreadID.Base36(),
		User:      domainReviewActorToPB(&comment.User),
		Body:      comment.Body,
		System:    comment.System,
		CreatedAt: timestamppb.New(comment.CreatedAt),
		UpdatedAt: timestamppb.New(comment.UpdatedAt),
		Edited:    comment.UpdatedAt.After(comment.CreatedAt),
	}
}

func domainReviewActorToPB(actor *domain.ReviewActor) *pb.ReviewActor {
	if actor == nil {
		return nil
	}
	return &pb.ReviewActor{
		UserId:   actor.UserID.Base36(),
		Name:     actor.Name,
		PhotoUrl: actor.PhotoURL,
	}
}

func parseBase36ID(value string) (snow.ID, error) {
	if value == "" {
		return 0, domain.NewErrorUser("id is required")
	}
	id, err := snow.ParseBase36(value)
	if err != nil {
		return 0, domain.NewErrorUser("invalid id: " + value)
	}
	return id, nil
}

func int64ToIntPtr(value *int64) *int {
	if value == nil {
		return nil
	}
	out := int(*value)
	return &out
}

func intToInt64Ptr(value *int) *int64 {
	if value == nil {
		return nil
	}
	out := int64(*value)
	return &out
}
