package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupMergeRequestReviewRouter(ws *restful.WebService, h *handler.Handler) {
	tags := []string{"MergeRequestReviews"}
	project := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug"))
	}
	request := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return project(b).Param(ws.PathParameter("id", "merge request number"))
	}
	review := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return request(b).Param(ws.PathParameter("reviewId", "review id"))
	}
	thread := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return request(b).Param(ws.PathParameter("threadId", "thread id"))
	}
	comment := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return thread(b).Param(ws.PathParameter("commentId", "comment id"))
	}

	ws.Route(request(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/reviews").
			To(wrap(h.ListMergeRequestReviews)).
			Doc("List the reviews of a merge request, with staleness against the source head (project read)").
			Returns(http.StatusOK, "reviews", []model.ReviewResponse{}).
			Operation("listMergeRequestReviews").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/review-state").
			To(wrap(h.GetMergeRequestReviewState)).
			Doc("Live review summary: approvals, changes requested and outstanding reviewers (project read)").
			Returns(http.StatusOK, "review state", model.ReviewStateResponse{}).
			Operation("getMergeRequestReviewState").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/reviews").
			To(wrap(h.SubmitMergeRequestReview)).
			Reads(model.SubmitReviewRequest{}).
			Doc("Submit a review decision with its comments for the current source head (project write; a decision on your own merge request is rejected)").
			Returns(http.StatusOK, "submitted review", model.ReviewResponse{}).
			Operation("submitMergeRequestReview").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(review(
		ws.DELETE("/orgs/{org}/projects/{project}/merge-requests/{id}/reviews/{reviewId}").
			To(wrap(h.WithdrawMergeRequestReview)).
			Doc("Withdraw one of your own reviews (reviewer or project admin)").
			Returns(http.StatusOK, "withdrawn", nil).
			Operation("withdrawMergeRequestReview").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(review(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/reviews/{reviewId}/dismiss").
			To(wrap(h.DismissMergeRequestReview)).
			AllowedMethodsWithoutContentType([]string{"POST"}).
			Doc("Stop counting a review without deleting it (project write)").
			Returns(http.StatusOK, "dismissed", nil).
			Operation("dismissMergeRequestReview").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/threads").
			To(wrap(h.ListMergeRequestThreads)).
			Param(ws.QueryParameter("resolved", "filter by resolution state")).
			Doc("List review threads, top-level and inline, with outdatedness against the source head (project read)").
			Returns(http.StatusOK, "threads", []model.ThreadResponse{}).
			Operation("listMergeRequestThreads").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/threads").
			To(wrap(h.AddMergeRequestComment)).
			Reads(model.AddCommentRequest{}).
			Doc("Start a conversation thread, top-level or on a diff line (project write)").
			Returns(http.StatusOK, "thread", model.ThreadResponse{}).
			Operation("addMergeRequestComment").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(thread(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/threads/{threadId}/comments").
			To(wrap(h.ReplyMergeRequestThread)).
			Reads(model.AddCommentRequest{}).
			Doc("Reply in a thread (project write)").
			Returns(http.StatusOK, "comment", model.CommentResponse{}).
			Operation("replyMergeRequestThread").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(thread(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/threads/{threadId}/resolve").
			To(wrap(h.ResolveMergeRequestThread)).
			Reads(model.ResolveThreadRequest{}).
			Doc("Resolve or reopen a thread (project write)").
			Returns(http.StatusOK, "thread", model.ThreadResponse{}).
			Operation("resolveMergeRequestThread").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(thread(
		ws.DELETE("/orgs/{org}/projects/{project}/merge-requests/{id}/threads/{threadId}").
			To(wrap(h.DeleteMergeRequestThread)).
			Doc("Delete a thread and its comments (author or project admin)").
			Returns(http.StatusOK, "deleted", nil).
			Operation("deleteMergeRequestThread").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(comment(
		ws.PATCH("/orgs/{org}/projects/{project}/merge-requests/{id}/threads/{threadId}/comments/{commentId}").
			To(wrap(h.UpdateMergeRequestComment)).
			Reads(model.AddCommentRequest{}).
			Doc("Edit your own comment (author)").
			Returns(http.StatusOK, "comment", model.CommentResponse{}).
			Operation("updateMergeRequestComment").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(comment(
		ws.DELETE("/orgs/{org}/projects/{project}/merge-requests/{id}/threads/{threadId}/comments/{commentId}").
			To(wrap(h.DeleteMergeRequestComment)).
			Doc("Delete a comment (author or project admin)").
			Returns(http.StatusOK, "deleted", nil).
			Operation("deleteMergeRequestComment").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/review-requests").
			To(wrap(h.ListMergeRequestReviewRequests)).
			Doc("List pending review requests (project read)").
			Returns(http.StatusOK, "review requests", []model.ReviewRequestResponse{}).
			Operation("listMergeRequestReviewRequests").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.POST("/orgs/{org}/projects/{project}/merge-requests/{id}/review-requests").
			To(wrap(h.RequestMergeRequestReview)).
			Reads(model.ReviewRequestUserRequest{}).
			Doc("Ask a user to review the merge request (project write)").
			Returns(http.StatusOK, "review request", model.ReviewRequestResponse{}).
			Operation("requestMergeRequestReview").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.DELETE("/orgs/{org}/projects/{project}/merge-requests/{id}/review-requests").
			To(wrap(h.RemoveMergeRequestReviewRequest)).
			Reads(model.ReviewRequestUserRequest{}).
			Doc("Withdraw a review request (the requested reviewer or project write)").
			Returns(http.StatusOK, "withdrawn", nil).
			Operation("removeMergeRequestReviewRequest").
			Metadata(restfulspec.KeyOpenAPITags, tags)))

	ws.Route(request(
		ws.GET("/orgs/{org}/projects/{project}/merge-requests/{id}/timeline").
			To(wrap(h.MergeRequestTimeline)).
			Doc("Merge request activity timeline, oldest first (project read)").
			Returns(http.StatusOK, "timeline", []model.TimelineItemResponse{}).
			Operation("mergeRequestTimeline").
			Metadata(restfulspec.KeyOpenAPITags, tags)))
}
