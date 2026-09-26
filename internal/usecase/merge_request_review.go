package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=merge_request_review_mock_test.go -package=usecase
type mergeRequestReviewRepository interface {
	UpsertReview(ctx context.Context, review domain.MergeRequestReview) (*domain.MergeRequestReview, error)
	ListReviews(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestReview, error)
	GetReview(ctx context.Context, mergeRequestID int64, reviewID snow.ID) (*domain.MergeRequestReview, error)
	DeleteReview(ctx context.Context, mergeRequestID int64, reviewID snow.ID) error
	DismissReview(ctx context.Context, mergeRequestID int64, reviewID, dismissedBy snow.ID, reason string, at time.Time) error
	DismissStaleReviews(ctx context.Context, mergeRequestID int64, headCommitID, dismissedBy snow.ID, reason string, at time.Time) error
	StaleReviews(ctx context.Context, mergeRequestID int64, headCommitID snow.ID) ([]*domain.MergeRequestReview, error)
	ReviewSummaries(ctx context.Context, projectID snow.ID) (map[int64]*domain.MergeRequestReviewState, error)
	CreateThread(ctx context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error)
	GetThread(ctx context.Context, mergeRequestID int64, threadID snow.ID) (*domain.MergeRequestThread, error)
	ListThreads(ctx context.Context, mergeRequestID int64, resolved *bool) ([]*domain.MergeRequestThread, error)
	SetThreadResolved(ctx context.Context, mergeRequestID int64, threadID snow.ID, resolved bool, resolvedBy *snow.ID, at *time.Time) (*domain.MergeRequestThread, error)
	SetThreadReview(ctx context.Context, mergeRequestID int64, threadID snow.ID, reviewID *snow.ID) error
	DeleteThread(ctx context.Context, mergeRequestID int64, threadID snow.ID) error
	CreateComment(ctx context.Context, comment domain.MergeRequestComment) (*domain.MergeRequestComment, error)
	GetComment(ctx context.Context, threadID, commentID snow.ID) (*domain.MergeRequestComment, error)
	UpdateComment(ctx context.Context, threadID, commentID snow.ID, body string) (*domain.MergeRequestComment, error)
	DeleteComment(ctx context.Context, threadID, commentID snow.ID) error
	ListComments(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestComment, error)
	CreateReviewRequest(ctx context.Context, request domain.MergeRequestReviewRequest) (*domain.MergeRequestReviewRequest, error)
	ListReviewRequests(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestReviewRequest, error)
	DeleteReviewRequest(ctx context.Context, mergeRequestID int64, reviewerID snow.ID) error
	CreateEvent(ctx context.Context, item domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error)
	ListEvents(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestTimelineItem, error)
	ListOpenBySourceBranch(ctx context.Context, projectID, branchID snow.ID) ([]*domain.MergeRequest, error)
}

// ThreadComment is one comment a reviewer attaches while submitting a review,
// optionally anchored to a line of the diff. An empty FilePath is a top-level
// conversation thread.
type ThreadComment struct {
	FilePath string
	OldLine  *int
	NewLine  *int
	Body     string
}

// MergeRequestReview is the review side of a merge request: decisions, inline
// and top-level comment threads, review requests and the activity timeline.
//
// A review is always given for one source head commit, which is what makes
// staleness decidable: once the source branch moves on, the decision no longer
// counts until the reviewer looks at the new head.
type MergeRequestReview struct {
	repo       mergeRequestReviewRepository
	mrRepo     mergeRequestRepository
	branchRepo branchRepository
	merger     branchMerger
	perm       permissionUsecase
	users      userLookup
	snowNode   snow.Node
	now        func() time.Time
}

func NewMergeRequestReview(repo mergeRequestReviewRepository, mrRepo mergeRequestRepository,
	branchRepo branchRepository, merger branchMerger, perm permissionUsecase,
	users userLookup, snowNode snow.Node) *MergeRequestReview {
	return &MergeRequestReview{
		repo:       repo,
		mrRepo:     mrRepo,
		branchRepo: branchRepo,
		merger:     merger,
		perm:       perm,
		users:      users,
		snowNode:   snowNode,
		now:        time.Now,
	}
}

// Reviews returns every review of a merge request, with staleness resolved
// against the current source head.
func (r *MergeRequestReview) Reviews(ctx context.Context, projectID snow.ID, number int64) ([]*domain.MergeRequestReview, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return r.reviews(ctx, mr)
}

// ReviewState returns the live review summary of a merge request.
func (r *MergeRequestReview) ReviewState(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequestReviewState, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return r.state(ctx, mr)
}

// AttachSummaries fills in the review summary of every merge request of a
// project, for list views.
func (r *MergeRequestReview) AttachSummaries(ctx context.Context, projectID snow.ID, requests []*domain.MergeRequest) error {
	summaries, err := r.repo.ReviewSummaries(ctx, projectID)
	if err != nil {
		return err
	}
	for _, mr := range requests {
		if state, ok := summaries[mr.Number]; ok {
			mr.Review = state
		}
	}
	return nil
}

// SubmitReview records a review decision for the current source head together
// with the comments the reviewer made. A comment without a file path becomes a
// top-level conversation thread; the rest are anchored to diff lines.
func (r *MergeRequestReview) SubmitReview(ctx context.Context, projectID snow.ID, number int64, state, body string, comments []ThreadComment) (*domain.MergeRequestReview, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !domain.IsValidMergeRequestReviewState(state) {
		return nil, domain.NewErrorUser("invalid review state")
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	if mr.Status != domain.MergeRequestOpen {
		return nil, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", mr.Status))
	}
	// reviewing your own change is fine, approving it is not
	if claim.UserID == mr.CreatedBy && state != domain.MergeRequestReviewCommented {
		return nil, domain.NewErrorUser("you cannot review your own merge request")
	}
	body = strings.TrimSpace(body)
	if body == "" && len(comments) == 0 {
		return nil, domain.NewErrorUser("a review needs a body or a comment")
	}
	head, err := r.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, err
	}
	if head == nil {
		return nil, domain.NewErrorUser("source branch has no commits")
	}

	review, err := r.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             r.snowNode.Generate(),
		MergeRequestID: mr.ID,
		Reviewer:       domain.ReviewActor{UserID: claim.UserID},
		State:          state,
		Body:           body,
		HeadCommitID:   *head,
	})
	if err != nil {
		return nil, err
	}
	if err := r.attachThreads(ctx, projectID, mr, review.ID, head, claim.UserID, comments); err != nil {
		return nil, err
	}
	if err := r.addEvent(ctx, mr, domain.MergeRequestTimelineItem{
		Kind:     domain.MergeRequestEventReviewSubmitted,
		Actor:    domain.ReviewActor{UserID: claim.UserID},
		Body:     body,
		CommitID: head,
	}); err != nil {
		return nil, err
	}
	// reviewing answers the pending request for the reviewer
	if err := r.repo.DeleteReviewRequest(ctx, mr.ID, claim.UserID); err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	review.Stale = false
	return review, nil
}

// WithdrawReview removes a review the caller submitted.
func (r *MergeRequestReview) WithdrawReview(ctx context.Context, projectID snow.ID, number int64, reviewID snow.ID) error {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	review, err := r.repo.GetReview(ctx, mr.ID, reviewID)
	if err != nil {
		return r.wrapNotFound(err, "review")
	}
	if review.Reviewer.UserID != claim.UserID && !r.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return r.repo.DeleteReview(ctx, mr.ID, reviewID)
}

// DismissReview marks a review as no longer counting without deleting it.
func (r *MergeRequestReview) DismissReview(ctx context.Context, projectID snow.ID, number int64, reviewID snow.ID) (*domain.MergeRequestReview, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	review, err := r.repo.GetReview(ctx, mr.ID, reviewID)
	if err != nil {
		return nil, r.wrapNotFound(err, "review")
	}
	now := r.now()
	if err := r.repo.DismissReview(ctx, mr.ID, reviewID, claim.UserID, "manual", now); err != nil {
		return nil, err
	}
	if err := r.addEvent(ctx, mr, domain.MergeRequestTimelineItem{
		Kind:    domain.MergeRequestEventReviewDismissed,
		Actor:   domain.ReviewActor{UserID: claim.UserID},
		Subject: &domain.ReviewActor{UserID: review.Reviewer.UserID},
	}); err != nil {
		return nil, err
	}
	review.DismissedAt = &now
	review.DismissedReason = "manual"
	review.DismissedBy = &domain.ReviewActor{UserID: claim.UserID}
	review.Stale = true
	return review, nil
}

// AddComment starts a thread with its first comment. An empty filePath creates a
// top-level conversation thread, otherwise the comment must anchor to a line the
// current diff shows.
func (r *MergeRequestReview) AddComment(ctx context.Context, projectID snow.ID, number int64, comment ThreadComment) (*domain.MergeRequestThread, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	thread, created, err := r.createThread(ctx, projectID, mr, nil, claim.UserID, comment)
	if err != nil {
		return nil, err
	}
	thread.Comments = []*domain.MergeRequestComment{created}
	return thread, nil
}

// Reply adds a comment to an existing thread.
func (r *MergeRequestReview) Reply(ctx context.Context, projectID snow.ID, number int64, threadID snow.ID, body string) (*domain.MergeRequestComment, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, domain.NewErrorUser("comment body is required")
	}
	thread, err := r.thread(ctx, mr, threadID)
	if err != nil {
		return nil, err
	}
	return r.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       r.snowNode.Generate(),
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: claim.UserID},
		Body:     body,
	})
}

// UpdateComment edits a comment; only its author may do so.
func (r *MergeRequestReview) UpdateComment(ctx context.Context, projectID snow.ID, number int64, threadID, commentID snow.ID, body string) (*domain.MergeRequestComment, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	comment, err := r.comment(ctx, mr, threadID, commentID)
	if err != nil {
		return nil, err
	}
	if comment.User.UserID != claim.UserID {
		return nil, domain.NewErrorNoPermission()
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, domain.NewErrorUser("comment body is required")
	}
	return r.repo.UpdateComment(ctx, threadID, commentID, body)
}

// DeleteComment removes a comment; only its author or a project admin may.
func (r *MergeRequestReview) DeleteComment(ctx context.Context, projectID snow.ID, number int64, threadID, commentID snow.ID) error {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	comment, err := r.comment(ctx, mr, threadID, commentID)
	if err != nil {
		return err
	}
	if comment.User.UserID != claim.UserID && !r.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return r.repo.DeleteComment(ctx, threadID, commentID)
}

// Threads returns the review threads of a merge request, with outdatedness
// resolved against the current source head.
func (r *MergeRequestReview) Threads(ctx context.Context, projectID snow.ID, number int64, resolved *bool) ([]*domain.MergeRequestThread, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	threads, err := r.repo.ListThreads(ctx, mr.ID, resolved)
	if err != nil {
		return nil, err
	}
	head, err := r.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, err
	}
	for _, thread := range threads {
		// only a comment on a diff line can go stale; a top-level conversation
		// thread is not about the diff
		thread.Outdated = !thread.IsTopLevel() && head != nil &&
			thread.HeadCommitID != nil && *thread.HeadCommitID != *head
	}
	return threads, nil
}

// SetThreadResolved marks a thread resolved or reopened.
func (r *MergeRequestReview) SetThreadResolved(ctx context.Context, projectID snow.ID, number int64, threadID snow.ID, resolved bool) (*domain.MergeRequestThread, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	if _, err := r.thread(ctx, mr, threadID); err != nil {
		return nil, err
	}
	if !resolved {
		return r.repo.SetThreadResolved(ctx, mr.ID, threadID, false, nil, nil)
	}
	at := r.now()
	return r.repo.SetThreadResolved(ctx, mr.ID, threadID, true, &claim.UserID, &at)
}

// DeleteThread removes a whole thread; its author or a project admin may.
func (r *MergeRequestReview) DeleteThread(ctx context.Context, projectID snow.ID, number int64, threadID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return err
	}
	thread, err := r.thread(ctx, mr, threadID)
	if err != nil {
		return err
	}
	if thread.CreatedBy.UserID != claim.UserID && !r.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return r.repo.DeleteThread(ctx, mr.ID, threadID)
}

// RequestReview asks a user to review a merge request.
func (r *MergeRequestReview) RequestReview(ctx context.Context, projectID snow.ID, number int64, reviewerID snow.ID) (*domain.MergeRequestReviewRequest, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	if reviewerID == claim.UserID {
		return nil, domain.NewErrorUser("you cannot request a review from yourself")
	}
	if mr.Status != domain.MergeRequestOpen {
		return nil, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", mr.Status))
	}
	if _, err := r.users.GetByID(ctx, reviewerID); err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorUser("reviewer not found")
		}
		return nil, err
	}
	request, err := r.repo.CreateReviewRequest(ctx, domain.MergeRequestReviewRequest{
		ID:             r.snowNode.Generate(),
		MergeRequestID: mr.ID,
		Reviewer:       domain.ReviewActor{UserID: reviewerID},
		RequestedBy:    domain.ReviewActor{UserID: claim.UserID},
	})
	if err != nil {
		return nil, err
	}
	if err := r.addEvent(ctx, mr, domain.MergeRequestTimelineItem{
		Kind:    domain.MergeRequestEventReviewRequested,
		Actor:   domain.ReviewActor{UserID: claim.UserID},
		Subject: &domain.ReviewActor{UserID: reviewerID},
	}); err != nil {
		return nil, err
	}
	return request, nil
}

// RemoveReviewRequest withdraws a pending review request.
func (r *MergeRequestReview) RemoveReviewRequest(ctx context.Context, projectID snow.ID, number int64, reviewerID snow.ID) error {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return domain.NewErrorNoPermission()
	}
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return err
	}
	if reviewerID != claim.UserID && !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return domain.NewErrorNoPermission()
	}
	if err := r.repo.DeleteReviewRequest(ctx, mr.ID, reviewerID); err != nil {
		return err
	}
	return r.addEvent(ctx, mr, domain.MergeRequestTimelineItem{
		Kind:    domain.MergeRequestEventReviewUnrequested,
		Actor:   domain.ReviewActor{UserID: claim.UserID},
		Subject: &domain.ReviewActor{UserID: reviewerID},
	})
}

// ReviewRequests returns the pending review requests of a merge request.
func (r *MergeRequestReview) ReviewRequests(ctx context.Context, projectID snow.ID, number int64) ([]*domain.MergeRequestReviewRequest, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return r.repo.ListReviewRequests(ctx, mr.ID)
}

// Timeline returns the activity timeline of a merge request, newest last.
func (r *MergeRequestReview) Timeline(ctx context.Context, projectID snow.ID, number int64) ([]*domain.MergeRequestTimelineItem, error) {
	mr, err := r.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return r.repo.ListEvents(ctx, mr.ID)
}

// NoteBranchPush invalidates the reviews of every open merge request that takes
// its source from a branch that just moved. Decisions given for the previous
// head are dismissed, which keeps them in history but stops them from counting.
func (r *MergeRequestReview) NoteBranchPush(ctx context.Context, projectID, branchID, newHead, actor snow.ID, commitHash string) error {
	requests, err := r.repo.ListOpenBySourceBranch(ctx, projectID, branchID)
	if err != nil {
		return err
	}
	now := r.now()
	for _, mr := range requests {
		if err := r.repo.DismissStaleReviews(ctx, mr.ID, newHead, actor, domain.MergeRequestDismissedNewCommits, now); err != nil {
			return err
		}
		if err := r.addEvent(ctx, mr, domain.MergeRequestTimelineItem{
			Kind:       domain.MergeRequestEventPushed,
			Actor:      domain.ReviewActor{UserID: actor},
			CommitID:   &newHead,
			CommitHash: commitHash,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *MergeRequestReview) reviews(ctx context.Context, mr *domain.MergeRequest) ([]*domain.MergeRequestReview, error) {
	reviews, err := r.repo.ListReviews(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	head, err := r.sourceHead(ctx, mr.ProjectID, mr)
	if err != nil {
		return nil, err
	}
	for _, review := range reviews {
		review.Stale = head != nil && review.HeadCommitID != *head
	}
	return reviews, nil
}

// state builds the review summary: live decisions count, a reviewer with a live
// decision is no longer outstanding, and dismissed approvals are reported so the
// UI can explain why a previous approval stopped counting.
func (r *MergeRequestReview) state(ctx context.Context, mr *domain.MergeRequest) (*domain.MergeRequestReviewState, error) {
	reviews, err := r.reviews(ctx, mr)
	if err != nil {
		return nil, err
	}
	requests, err := r.repo.ListReviewRequests(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	head, err := r.sourceHead(ctx, mr.ProjectID, mr)
	if err != nil {
		return nil, err
	}
	state := &domain.MergeRequestReviewState{OutstandingReviewers: []snow.ID{}}
	if head != nil {
		state.HeadCommitID = *head
	}
	decided := make(map[snow.ID]bool, len(reviews))
	for _, review := range reviews {
		switch {
		case review.Stale:
			if review.State == domain.MergeRequestReviewApproved {
				state.DismissedApprovals++
			}
		case review.DismissedAt != nil:
			if review.State == domain.MergeRequestReviewApproved {
				state.DismissedApprovals++
			}
		case review.State == domain.MergeRequestReviewApproved:
			state.Approvals++
			decided[review.Reviewer.UserID] = true
		case review.State == domain.MergeRequestReviewChangesRequested:
			state.ChangesRequested++
			decided[review.Reviewer.UserID] = true
		}
	}
	for _, request := range requests {
		if !decided[request.Reviewer.UserID] {
			state.OutstandingReviewers = append(state.OutstandingReviewers, request.Reviewer.UserID)
		}
	}
	return state, nil
}

// attachThreads creates the threads of a submitted review. A failure leaves the
// review itself in place, so the timeline still shows what was decided.
func (r *MergeRequestReview) attachThreads(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, reviewID snow.ID, head *snow.ID, author snow.ID, comments []ThreadComment) error {
	for _, comment := range comments {
		if _, _, err := r.createThread(ctx, projectID, mr, &reviewID, author, comment); err != nil {
			return err
		}
	}
	return nil
}

func (r *MergeRequestReview) createThread(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, reviewID *snow.ID, author snow.ID, comment ThreadComment) (*domain.MergeRequestThread, *domain.MergeRequestComment, error) {
	body := strings.TrimSpace(comment.Body)
	if body == "" {
		return nil, nil, domain.NewErrorUser("comment body is required")
	}
	filePath := strings.TrimSpace(comment.FilePath)
	var oldLine, newLine *int
	if filePath != "" {
		normalized, o, n, err := r.anchor(ctx, projectID, mr, filePath, comment.OldLine, comment.NewLine)
		if err != nil {
			return nil, nil, err
		}
		filePath, oldLine, newLine = normalized, o, n
	} else if comment.OldLine != nil || comment.NewLine != nil {
		return nil, nil, domain.NewErrorUser("a line comment needs a file path")
	}

	head, err := r.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, nil, err
	}
	thread, err := r.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             r.snowNode.Generate(),
		MergeRequestID: mr.ID,
		ReviewID:       reviewID,
		FilePath:       filePath,
		OldLine:        oldLine,
		NewLine:        newLine,
		HeadCommitID:   head,
		CreatedBy:      domain.ReviewActor{UserID: author},
	})
	if err != nil {
		return nil, nil, err
	}
	created, err := r.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       r.snowNode.Generate(),
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: author},
		Body:     body,
	})
	if err != nil {
		return nil, nil, err
	}
	return thread, created, nil
}

// anchor resolves a comment position against the current diff: a line comment
// must name a file the diff touches and a line the diff actually shows, and an
// omitted side defaults to the added side.
func (r *MergeRequestReview) anchor(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, filePath string, oldLine, newLine *int) (string, *int, *int, error) {
	if oldLine == nil && newLine == nil {
		return "", nil, nil, domain.NewErrorUser("a line comment needs old_line or new_line")
	}
	if (oldLine != nil && *oldLine <= 0) || (newLine != nil && *newLine <= 0) {
		return "", nil, nil, domain.NewErrorUser("comment line must be positive")
	}
	files, err := r.diff(ctx, projectID, mr)
	if err != nil {
		return "", nil, nil, err
	}
	for _, file := range files {
		if file.Change.Path != filePath && file.Change.Old.Path != filePath {
			continue
		}
		hunks := diff.FileHunks(file, diff.Options{Context: diff.DefaultContext})
		if oldLine != nil {
			if _, _, ok := diff.HunkLineAt(hunks, diff.SideLeft, *oldLine); !ok {
				return "", nil, nil, domain.NewErrorUser(fmt.Sprintf("line %d is not part of the diff of %s", *oldLine, filePath))
			}
		}
		if newLine != nil {
			if _, _, ok := diff.HunkLineAt(hunks, diff.SideRight, *newLine); !ok {
				return "", nil, nil, domain.NewErrorUser(fmt.Sprintf("line %d is not part of the diff of %s", *newLine, filePath))
			}
		}
		// a comment on a removed line belongs to the old path of a rename
		if oldLine != nil && newLine == nil && file.Change.Status == diff.Renamed {
			filePath = file.Change.Old.Path
		}
		return filePath, oldLine, newLine, nil
	}
	return "", nil, nil, domain.NewErrorNotFound(fmt.Sprintf("%s is not part of the diff", filePath))
}

func (r *MergeRequestReview) addEvent(ctx context.Context, mr *domain.MergeRequest, event domain.MergeRequestTimelineItem) error {
	event.ID = r.snowNode.Generate()
	event.MergeRequestID = mr.ID
	_, err := r.repo.CreateEvent(ctx, event)
	return err
}

func (r *MergeRequestReview) load(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	if number <= 0 {
		return nil, domain.NewErrorUser("invalid merge request number")
	}
	if !r.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := r.mrRepo.Get(ctx, projectID, number)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("merge request #%d not found", number))
	}
	if err != nil {
		return nil, err
	}
	return mr, nil
}

func (r *MergeRequestReview) sourceHead(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest) (*snow.ID, error) {
	source, err := r.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return source.CommitID, nil
}

func (r *MergeRequestReview) thread(ctx context.Context, mr *domain.MergeRequest, threadID snow.ID) (*domain.MergeRequestThread, error) {
	thread, err := r.repo.GetThread(ctx, mr.ID, threadID)
	if err != nil {
		return nil, r.wrapNotFound(err, "thread")
	}
	return thread, nil
}

func (r *MergeRequestReview) comment(ctx context.Context, mr *domain.MergeRequest, threadID, commentID snow.ID) (*domain.MergeRequestComment, error) {
	if _, err := r.thread(ctx, mr, threadID); err != nil {
		return nil, err
	}
	comment, err := r.repo.GetComment(ctx, threadID, commentID)
	if err != nil {
		return nil, r.wrapNotFound(err, "comment")
	}
	return comment, nil
}

func (r *MergeRequestReview) diff(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest) ([]diff.FileDiff, error) {
	source, err := r.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, nil
	}
	var baseID *snow.ID
	target, err := r.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
	if err == nil && target.CommitID != nil {
		info, err := r.merger.GetMergeBase(ctx, projectID,
			MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source})
		if err != nil {
			return nil, err
		}
		baseID = info.MergeBaseCommitID
	} else if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	return r.merger.TreeDiffBetween(ctx, projectID, baseID, *source)
}

func (r *MergeRequestReview) wrapNotFound(err error, what string) error {
	if domain.IsErrorNotFound(err) {
		return domain.NewErrorNotFound(fmt.Sprintf("%s not found", what))
	}
	return err
}
