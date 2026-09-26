package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListMergeRequestReviews(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	reviews, err := h.useCase.MergeRequestReview().Reviews(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.ReviewResponse, 0, len(reviews))
	for _, review := range reviews {
		resp = append(resp, toReviewResponse(review))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) GetMergeRequestReviewState(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	state, err := h.useCase.MergeRequestReview().ReviewState(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toReviewStateResponse(state))
}

func (h *Handler) SubmitMergeRequestReview(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	body := &model.SubmitReviewRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	comments := make([]usecase.ThreadComment, 0, len(body.Comments))
	for _, comment := range body.Comments {
		comments = append(comments, usecase.ThreadComment{
			FilePath: comment.FilePath,
			OldLine:  comment.OldLine,
			NewLine:  comment.NewLine,
			Body:     comment.Body,
		})
	}
	review, err := h.useCase.MergeRequestReview().SubmitReview(
		appCtx.Context(), project.ID, number, body.State, body.Body, comments,
	)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toReviewResponse(review))
}

func (h *Handler) WithdrawMergeRequestReview(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	reviewID, err := parseSnowPath(appCtx, "reviewId")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MergeRequestReview().WithdrawReview(appCtx.Context(), project.ID, number, reviewID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) DismissMergeRequestReview(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	reviewID, err := parseSnowPath(appCtx, "reviewId")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	review, err := h.useCase.MergeRequestReview().DismissReview(appCtx.Context(), project.ID, number, reviewID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toReviewResponse(review))
}

func (h *Handler) ListMergeRequestThreads(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threads, err := h.useCase.MergeRequestReview().Threads(appCtx.Context(), project.ID, number, queryBool(appCtx, "resolved"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toThreadResponses(threads))
}

func (h *Handler) AddMergeRequestComment(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	body := &model.AddCommentRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	thread, err := h.useCase.MergeRequestReview().AddComment(appCtx.Context(), project.ID, number, usecase.ThreadComment{
		FilePath: body.FilePath,
		OldLine:  body.OldLine,
		NewLine:  body.NewLine,
		Body:     body.Body,
	})
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toThreadResponse(thread))
}

func (h *Handler) ReplyMergeRequestThread(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threadID, err := parseSnowPath(appCtx, "threadId")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.AddCommentRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	comment, err := h.useCase.MergeRequestReview().Reply(appCtx.Context(), project.ID, number, threadID, body.Body)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toCommentResponse(comment))
}

func (h *Handler) UpdateMergeRequestComment(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threadID, commentID, err := parseThreadComment(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.AddCommentRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	comment, err := h.useCase.MergeRequestReview().UpdateComment(appCtx.Context(), project.ID, number, threadID, commentID, body.Body)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toCommentResponse(comment))
}

func (h *Handler) DeleteMergeRequestComment(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threadID, commentID, err := parseThreadComment(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MergeRequestReview().DeleteComment(appCtx.Context(), project.ID, number, threadID, commentID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) ResolveMergeRequestThread(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threadID, err := parseSnowPath(appCtx, "threadId")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.ResolveThreadRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	thread, err := h.useCase.MergeRequestReview().SetThreadResolved(appCtx.Context(), project.ID, number, threadID, body.Resolved)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toThreadResponse(thread))
}

func (h *Handler) DeleteMergeRequestThread(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	threadID, err := parseSnowPath(appCtx, "threadId")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.MergeRequestReview().DeleteThread(appCtx.Context(), project.ID, number, threadID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) ListMergeRequestReviewRequests(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	requests, err := h.useCase.MergeRequestReview().ReviewRequests(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.ReviewRequestResponse, 0, len(requests))
	for _, request := range requests {
		resp = append(resp, model.ReviewRequestResponse{
			ID:             request.ID.Base36(),
			MergeRequestID: request.MergeRequestID,
			Reviewer:       toReviewActorResponse(request.Reviewer),
			RequestedBy:    toReviewActorResponse(request.RequestedBy),
			CreatedAt:      request.CreatedAt,
		})
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) RequestMergeRequestReview(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	body := &model.ReviewRequestUserRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	reviewerID, err := snow.ParseBase36(body.UserID)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid user id"))
		return
	}
	request, err := h.useCase.MergeRequestReview().RequestReview(appCtx.Context(), project.ID, number, reviewerID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.ReviewRequestResponse{
		ID:             request.ID.Base36(),
		MergeRequestID: request.MergeRequestID,
		Reviewer:       toReviewActorResponse(request.Reviewer),
		RequestedBy:    toReviewActorResponse(request.RequestedBy),
		CreatedAt:      request.CreatedAt,
	})
}

func (h *Handler) RemoveMergeRequestReviewRequest(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	body := &model.ReviewRequestUserRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	reviewerID, err := snow.ParseBase36(body.UserID)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid user id"))
		return
	}
	if err := h.useCase.MergeRequestReview().RemoveReviewRequest(appCtx.Context(), project.ID, number, reviewerID); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) MergeRequestTimeline(appCtx http.AppContext) {
	project, number, ok := h.resolveMergeRequest(appCtx)
	if !ok {
		return
	}
	items, err := h.useCase.MergeRequestReview().Timeline(appCtx.Context(), project.ID, number)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.TimelineItemResponse, 0, len(items))
	for _, item := range items {
		entry := model.TimelineItemResponse{
			ID:         item.ID.Base36(),
			Kind:       item.Kind,
			Actor:      toReviewActorResponse(item.Actor),
			Body:       item.Body,
			CommitHash: item.CommitHash,
			CreatedAt:  item.CreatedAt,
		}
		if item.Subject != nil {
			subject := toReviewActorResponse(*item.Subject)
			entry.Subject = &subject
		}
		if item.CommitID != nil {
			entry.CommitID = item.CommitID.Base36()
		}
		resp = append(resp, entry)
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

// resolveMergeRequest resolves the project and merge request number every review
// route needs.
func (h *Handler) resolveMergeRequest(appCtx http.AppContext) (*domain.Project, int64, bool) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return nil, 0, false
	}
	number, err := parseMergeRequestNumber(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return nil, 0, false
	}
	return project, number, true
}

func parseSnowPath(appCtx http.AppContext, name string) (snow.ID, error) {
	id, err := snow.ParseBase36(appCtx.PathParameter(name))
	if err != nil {
		return 0, domain.NewErrorUser("invalid " + name)
	}
	return id, nil
}

func parseThreadComment(appCtx http.AppContext) (snow.ID, snow.ID, error) {
	threadID, err := parseSnowPath(appCtx, "threadId")
	if err != nil {
		return 0, 0, err
	}
	commentID, err := parseSnowPath(appCtx, "commentId")
	if err != nil {
		return 0, 0, err
	}
	return threadID, commentID, nil
}

func queryBool(appCtx http.AppContext, name string) *bool {
	switch appCtx.QueryParameter(name) {
	case "true", "1":
		value := true
		return &value
	case "false", "0":
		value := false
		return &value
	default:
		return nil
	}
}

func toReviewActorResponse(actor domain.ReviewActor) model.ReviewActorResponse {
	return model.ReviewActorResponse{
		UserID:   actor.UserID.Base36(),
		Name:     actor.Name,
		PhotoURL: actor.PhotoURL,
	}
}

func toReviewStateResponse(state *domain.MergeRequestReviewState) model.ReviewStateResponse {
	if state == nil {
		return model.ReviewStateResponse{OutstandingReviewers: []string{}}
	}
	outstanding := make([]string, 0, len(state.OutstandingReviewers))
	for _, id := range state.OutstandingReviewers {
		outstanding = append(outstanding, id.Base36())
	}
	resp := model.ReviewStateResponse{
		Approvals:            state.Approvals,
		ChangesRequested:     state.ChangesRequested,
		DismissedApprovals:   state.DismissedApprovals,
		OutstandingReviewers: outstanding,
	}
	if state.HeadCommitID != 0 {
		resp.HeadCommitID = state.HeadCommitID.Base36()
	}
	return resp
}

func toReviewResponse(review *domain.MergeRequestReview) model.ReviewResponse {
	resp := model.ReviewResponse{
		ID:              review.ID.Base36(),
		MergeRequestID:  review.MergeRequestID,
		Reviewer:        toReviewActorResponse(review.Reviewer),
		State:           review.State,
		Body:            review.Body,
		HeadCommitID:    review.HeadCommitID.Base36(),
		Stale:           review.Stale,
		DismissedAt:     review.DismissedAt,
		DismissedReason: review.DismissedReason,
		CreatedAt:       review.CreatedAt,
		UpdatedAt:       review.UpdatedAt,
	}
	if review.DismissedBy != nil {
		dismissedBy := toReviewActorResponse(*review.DismissedBy)
		resp.DismissedBy = &dismissedBy
	}
	return resp
}

func toThreadResponses(threads []*domain.MergeRequestThread) []model.ThreadResponse {
	resp := make([]model.ThreadResponse, 0, len(threads))
	for _, thread := range threads {
		resp = append(resp, toThreadResponse(thread))
	}
	return resp
}

func toThreadResponse(thread *domain.MergeRequestThread) model.ThreadResponse {
	resp := model.ThreadResponse{
		ID:             thread.ID.Base36(),
		MergeRequestID: thread.MergeRequestID,
		FilePath:       thread.FilePath,
		OldLine:        thread.OldLine,
		NewLine:        thread.NewLine,
		Side:           thread.Side(),
		Outdated:       thread.Outdated,
		Resolved:       thread.Resolved,
		ResolvedAt:     thread.ResolvedAt,
		CreatedBy:      toReviewActorResponse(thread.CreatedBy),
		CreatedAt:      thread.CreatedAt,
		UpdatedAt:      thread.UpdatedAt,
		Comments:       make([]model.CommentResponse, 0, len(thread.Comments)),
	}
	if thread.ReviewID != nil {
		resp.ReviewID = thread.ReviewID.Base36()
	}
	if thread.ResolvedBy != nil {
		resolvedBy := toReviewActorResponse(*thread.ResolvedBy)
		resp.ResolvedBy = &resolvedBy
	}
	for _, comment := range thread.Comments {
		resp.Comments = append(resp.Comments, toCommentResponse(comment))
	}
	return resp
}

func toCommentResponse(comment *domain.MergeRequestComment) model.CommentResponse {
	return model.CommentResponse{
		ID:        comment.ID.Base36(),
		ThreadID:  comment.ThreadID.Base36(),
		User:      toReviewActorResponse(comment.User),
		Body:      comment.Body,
		System:    comment.System,
		CreatedAt: comment.CreatedAt,
		UpdatedAt: comment.UpdatedAt,
	}
}
