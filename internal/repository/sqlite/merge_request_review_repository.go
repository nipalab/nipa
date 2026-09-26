package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// MergeRequestReviewRepository persists review decisions, review threads, their
// comments, pending review requests and the activity timeline.
//
// Every method takes the merge request row id (merge_requests.id, the
// domain.MergeRequest.ID), not its per-project number, because that is what the
// review tables reference. ReviewSummaries is the exception: it aggregates for a
// whole project and is therefore keyed by merge request number, and it leaves
// merge requests out that carry no live decision.
type MergeRequestReviewRepository struct {
	queries *sqlcSqlite.Queries
}

func NewMergeRequestReviewRepository(db *sql.DB) *MergeRequestReviewRepository {
	return &MergeRequestReviewRepository{queries: sqlcSqlite.New(db)}
}

func (r *MergeRequestReviewRepository) UpsertReview(ctx context.Context, review domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
	row, err := r.queries.MergeRequestReviewUpsert(ctx, sqlcSqlite.MergeRequestReviewUpsertParams{
		ID:             review.ID.Int64(),
		MergeRequestID: review.MergeRequestID,
		ReviewerID:     review.Reviewer.UserID.Int64(),
		State:          review.State,
		Body:           review.Body,
		HeadCommitID:   review.HeadCommitID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestReviewToDomain(row), nil
}

func (r *MergeRequestReviewRepository) ListReviews(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestReview, error) {
	rows, err := r.queries.MergeRequestReviewList(ctx, mergeRequestID)
	if err != nil {
		return nil, handleError(err)
	}
	reviews := make([]*domain.MergeRequestReview, 0, len(rows))
	for _, row := range rows {
		reviews = append(reviews, mergeRequestReviewRowToDomain(row))
	}
	return reviews, nil
}

func (r *MergeRequestReviewRepository) GetReview(ctx context.Context, mergeRequestID int64, reviewID snow.ID) (*domain.MergeRequestReview, error) {
	row, err := r.queries.MergeRequestReviewGet(ctx, sqlcSqlite.MergeRequestReviewGetParams{
		MergeRequestID: mergeRequestID,
		ID:             reviewID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestReviewToDomain(row), nil
}

// DeleteReview withdraws a review outright. History that is meant to survive a
// withdrawn review belongs in the timeline, not here.
func (r *MergeRequestReviewRepository) DeleteReview(ctx context.Context, mergeRequestID int64, reviewID snow.ID) error {
	rows, err := r.queries.MergeRequestReviewDelete(ctx, sqlcSqlite.MergeRequestReviewDeleteParams{
		MergeRequestID: mergeRequestID,
		ID:             reviewID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return handleError(sql.ErrNoRows)
	}
	return nil
}

func (r *MergeRequestReviewRepository) DismissReview(ctx context.Context, mergeRequestID int64, reviewID, dismissedBy snow.ID, reason string, at time.Time) error {
	err := r.queries.MergeRequestReviewDismiss(ctx, sqlcSqlite.MergeRequestReviewDismissParams{
		DismissedAt:     sql.NullTime{Time: at, Valid: true},
		DismissedBy:     nullSnowID(&dismissedBy),
		DismissedReason: sql.NullString{String: reason, Valid: true},
		MergeRequestID:  mergeRequestID,
		ID:              reviewID.Int64(),
	})
	return handleError(err)
}

// DismissStaleReviews dismisses every non-stale decision that was given for a
// head other than headCommitID. Comment-only reviews are left alone: they carry
// no decision, so a new push does not invalidate them.
func (r *MergeRequestReviewRepository) DismissStaleReviews(ctx context.Context, mergeRequestID int64, headCommitID, dismissedBy snow.ID, reason string, at time.Time) error {
	err := r.queries.MergeRequestReviewDismissStale(ctx, sqlcSqlite.MergeRequestReviewDismissStaleParams{
		DismissedAt:     sql.NullTime{Time: at, Valid: true},
		DismissedBy:     nullSnowID(&dismissedBy),
		DismissedReason: sql.NullString{String: reason, Valid: true},
		MergeRequestID:  mergeRequestID,
		HeadCommitID:    headCommitID.Int64(),
	})
	return handleError(err)
}

func (r *MergeRequestReviewRepository) StaleReviews(ctx context.Context, mergeRequestID int64, headCommitID snow.ID) ([]*domain.MergeRequestReview, error) {
	rows, err := r.queries.MergeRequestReviewStaleList(ctx, sqlcSqlite.MergeRequestReviewStaleListParams{
		MergeRequestID: mergeRequestID,
		HeadCommitID:   headCommitID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	reviews := make([]*domain.MergeRequestReview, 0, len(rows))
	for _, row := range rows {
		reviews = append(reviews, mergeRequestReviewToDomain(row))
	}
	return reviews, nil
}

// ReviewSummaries aggregates the live review decisions of a project's merge
// requests, keyed by merge request number. Reviews given for a head other than
// the current source head are excluded, matching the read-time staleness rule.
func (r *MergeRequestReviewRepository) ReviewSummaries(ctx context.Context, projectID snow.ID) (map[int64]*domain.MergeRequestReviewState, error) {
	rows, err := r.queries.MergeRequestReviewSummary(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	summaries := make(map[int64]*domain.MergeRequestReviewState, len(rows))
	for _, row := range rows {
		state := summaries[row.MergeRequestNumber]
		if state == nil {
			state = &domain.MergeRequestReviewState{OutstandingReviewers: []snow.ID{}}
			if row.HeadCommitID.Valid {
				state.HeadCommitID = snow.ID(row.HeadCommitID.Int64)
			}
			summaries[row.MergeRequestNumber] = state
		}
		switch row.State {
		case domain.MergeRequestReviewApproved:
			state.Approvals += int(row.Count)
		case domain.MergeRequestReviewChangesRequested:
			state.ChangesRequested += int(row.Count)
		}
	}
	return summaries, nil
}

func (r *MergeRequestReviewRepository) CreateThread(ctx context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
	row, err := r.queries.MergeRequestThreadCreate(ctx, sqlcSqlite.MergeRequestThreadCreateParams{
		ID:             thread.ID.Int64(),
		MergeRequestID: thread.MergeRequestID,
		FilePath:       optionalString(thread.FilePath),
		OldLine:        optionalInt64(thread.OldLine),
		NewLine:        optionalInt64(thread.NewLine),
		BaseCommitID:   nullSnowID(thread.BaseCommitID),
		HeadCommitID:   nullSnowID(thread.HeadCommitID),
		CreatedBy:      thread.CreatedBy.UserID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestThreadToDomain(row), nil
}

func (r *MergeRequestReviewRepository) GetThread(ctx context.Context, mergeRequestID int64, threadID snow.ID) (*domain.MergeRequestThread, error) {
	row, err := r.queries.MergeRequestThreadGet(ctx, sqlcSqlite.MergeRequestThreadGetParams{
		MergeRequestID: mergeRequestID,
		ID:             threadID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestThreadToDomain(row), nil
}

// ListThreads returns the threads of a merge request with their comments
// attached. A nil resolved filter returns both resolved and unresolved threads.
func (r *MergeRequestReviewRepository) ListThreads(ctx context.Context, mergeRequestID int64, resolved *bool) ([]*domain.MergeRequestThread, error) {
	var filter interface{}
	if resolved != nil {
		filter = *resolved
	}
	rows, err := r.queries.MergeRequestThreadList(ctx, sqlcSqlite.MergeRequestThreadListParams{
		MergeRequestID: mergeRequestID,
		Resolved:       filter,
	})
	if err != nil {
		return nil, handleError(err)
	}
	comments, err := r.listThreadComments(ctx, mergeRequestID)
	if err != nil {
		return nil, err
	}
	threads := make([]*domain.MergeRequestThread, 0, len(rows))
	for _, row := range rows {
		thread := mergeRequestThreadRowToDomain(row)
		thread.Comments = comments[thread.ID]
		if thread.Comments == nil {
			thread.Comments = []*domain.MergeRequestComment{}
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func (r *MergeRequestReviewRepository) SetThreadResolved(ctx context.Context, mergeRequestID int64, threadID snow.ID, resolved bool, resolvedBy *snow.ID, at *time.Time) (*domain.MergeRequestThread, error) {
	row, err := r.queries.MergeRequestThreadSetResolved(ctx, sqlcSqlite.MergeRequestThreadSetResolvedParams{
		Resolved:       resolved,
		ResolvedBy:     nullSnowID(resolvedBy),
		ResolvedAt:     timePtrToNullTime(at),
		MergeRequestID: mergeRequestID,
		ID:             threadID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestThreadToDomain(row), nil
}

func (r *MergeRequestReviewRepository) SetThreadReview(ctx context.Context, mergeRequestID int64, threadID snow.ID, reviewID *snow.ID) error {
	err := r.queries.MergeRequestThreadSetReview(ctx, sqlcSqlite.MergeRequestThreadSetReviewParams{
		ReviewID:       nullSnowID(reviewID),
		MergeRequestID: mergeRequestID,
		ID:             threadID.Int64(),
	})
	return handleError(err)
}

func (r *MergeRequestReviewRepository) DeleteThread(ctx context.Context, mergeRequestID int64, threadID snow.ID) error {
	rows, err := r.queries.MergeRequestThreadDelete(ctx, sqlcSqlite.MergeRequestThreadDeleteParams{
		MergeRequestID: mergeRequestID,
		ID:             threadID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return handleError(sql.ErrNoRows)
	}
	return nil
}

func (r *MergeRequestReviewRepository) CreateComment(ctx context.Context, comment domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
	row, err := r.queries.MergeRequestCommentCreate(ctx, sqlcSqlite.MergeRequestCommentCreateParams{
		ID:       comment.ID.Int64(),
		ThreadID: comment.ThreadID.Int64(),
		UserID:   comment.User.UserID.Int64(),
		Body:     comment.Body,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestCommentToDomain(row), nil
}

func (r *MergeRequestReviewRepository) GetComment(ctx context.Context, threadID, commentID snow.ID) (*domain.MergeRequestComment, error) {
	row, err := r.queries.MergeRequestCommentGet(ctx, sqlcSqlite.MergeRequestCommentGetParams{
		ThreadID: threadID.Int64(),
		ID:       commentID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestCommentToDomain(row), nil
}

func (r *MergeRequestReviewRepository) UpdateComment(ctx context.Context, threadID, commentID snow.ID, body string) (*domain.MergeRequestComment, error) {
	row, err := r.queries.MergeRequestCommentUpdate(ctx, sqlcSqlite.MergeRequestCommentUpdateParams{
		Body:     body,
		ThreadID: threadID.Int64(),
		ID:       commentID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestCommentToDomain(row), nil
}

func (r *MergeRequestReviewRepository) DeleteComment(ctx context.Context, threadID, commentID snow.ID) error {
	rows, err := r.queries.MergeRequestCommentDelete(ctx, sqlcSqlite.MergeRequestCommentDeleteParams{
		ThreadID: threadID.Int64(),
		ID:       commentID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return handleError(sql.ErrNoRows)
	}
	return nil
}

func (r *MergeRequestReviewRepository) ListComments(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestComment, error) {
	byThread, err := r.listThreadComments(ctx, mergeRequestID)
	if err != nil {
		return nil, err
	}
	comments := make([]*domain.MergeRequestComment, 0, len(byThread))
	for _, threadComments := range byThread {
		comments = append(comments, threadComments...)
	}
	return comments, nil
}

func (r *MergeRequestReviewRepository) CreateReviewRequest(ctx context.Context, request domain.MergeRequestReviewRequest) (*domain.MergeRequestReviewRequest, error) {
	row, err := r.queries.MergeRequestReviewRequestCreate(ctx, sqlcSqlite.MergeRequestReviewRequestCreateParams{
		ID:             request.ID.Int64(),
		MergeRequestID: request.MergeRequestID,
		ReviewerID:     request.Reviewer.UserID.Int64(),
		RequestedBy:    request.RequestedBy.UserID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.MergeRequestReviewRequest{
		ID:             snow.ID(row.ID),
		MergeRequestID: row.MergeRequestID,
		Reviewer:       domain.ReviewActor{UserID: snow.ID(row.ReviewerID)},
		RequestedBy:    domain.ReviewActor{UserID: snow.ID(row.RequestedBy)},
		CreatedAt:      row.CreatedAt,
	}, nil
}

func (r *MergeRequestReviewRepository) ListReviewRequests(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestReviewRequest, error) {
	rows, err := r.queries.MergeRequestReviewRequestList(ctx, mergeRequestID)
	if err != nil {
		return nil, handleError(err)
	}
	requests := make([]*domain.MergeRequestReviewRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, &domain.MergeRequestReviewRequest{
			ID:             snow.ID(row.ID),
			MergeRequestID: row.MergeRequestID,
			Reviewer:       reviewActor(row.ReviewerID, row.ReviewerName, row.ReviewerPhotoUrl),
			RequestedBy:    reviewActor(row.RequestedBy, row.RequestedByName, sql.NullString{}),
			CreatedAt:      row.CreatedAt,
		})
	}
	return requests, nil
}

func (r *MergeRequestReviewRepository) DeleteReviewRequest(ctx context.Context, mergeRequestID int64, reviewerID snow.ID) error {
	rows, err := r.queries.MergeRequestReviewRequestDelete(ctx, sqlcSqlite.MergeRequestReviewRequestDeleteParams{
		MergeRequestID: mergeRequestID,
		ReviewerID:     reviewerID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return handleError(sql.ErrNoRows)
	}
	return nil
}

func (r *MergeRequestReviewRepository) CreateEvent(ctx context.Context, item domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
	row, err := r.queries.MergeRequestEventCreate(ctx, sqlcSqlite.MergeRequestEventCreateParams{
		ID:             item.ID.Int64(),
		MergeRequestID: item.MergeRequestID,
		ActorID:        item.Actor.UserID.Int64(),
		SubjectUserID:  optionalSnowID(item.Subject),
		Kind:           item.Kind,
		Body:           item.Body,
		CommitID:       nullSnowID(item.CommitID),
		CommitHash:     optionalString(item.CommitHash),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return mergeRequestEventToDomain(row), nil
}

func (r *MergeRequestReviewRepository) ListEvents(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestTimelineItem, error) {
	rows, err := r.queries.MergeRequestEventList(ctx, mergeRequestID)
	if err != nil {
		return nil, handleError(err)
	}
	items := make([]*domain.MergeRequestTimelineItem, 0, len(rows))
	for _, row := range rows {
		item := &domain.MergeRequestTimelineItem{
			ID:             snow.ID(row.ID),
			MergeRequestID: row.MergeRequestID,
			Kind:           row.Kind,
			Actor:          reviewActor(row.ActorID, row.ActorName, row.ActorPhotoUrl),
			Body:           row.Body,
			CommitID:       nullInt64SnowIDPtr(row.CommitID),
			CommitHash:     row.CommitHash.String,
			CreatedAt:      row.CreatedAt,
		}
		if row.SubjectUserID.Valid {
			item.Subject = &domain.ReviewActor{
				UserID:   snow.ID(row.SubjectUserID.Int64),
				Name:     row.SubjectName.String,
				PhotoURL: row.SubjectPhotoUrl.String,
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// ListOpenBySourceBranch returns the open merge requests that take their source
// from a branch, used to invalidate reviews after the branch is pushed to.
func (r *MergeRequestReviewRepository) ListOpenBySourceBranch(ctx context.Context, projectID snow.ID, branchID snow.ID) ([]*domain.MergeRequest, error) {
	rows, err := r.queries.MergeRequestListOpenBySourceBranch(ctx, sqlcSqlite.MergeRequestListOpenBySourceBranchParams{
		ProjectID: projectID.Int64(),
		Status:    domain.MergeRequestOpen,
		BranchID:  branchID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	requests := make([]*domain.MergeRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, mergeRequestToDomain(row))
	}
	return requests, nil
}

func (r *MergeRequestReviewRepository) listThreadComments(ctx context.Context, mergeRequestID int64) (map[snow.ID][]*domain.MergeRequestComment, error) {
	rows, err := r.queries.MergeRequestCommentListByThread(ctx, mergeRequestID)
	if err != nil {
		return nil, handleError(err)
	}
	byThread := make(map[snow.ID][]*domain.MergeRequestComment, len(rows))
	for _, row := range rows {
		threadID := snow.ID(row.ThreadID)
		byThread[threadID] = append(byThread[threadID], &domain.MergeRequestComment{
			ID:        snow.ID(row.ID),
			ThreadID:  threadID,
			User:      reviewActor(row.UserID, row.UserName, row.UserPhotoUrl),
			Body:      row.Body,
			System:    row.System,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return byThread, nil
}

func reviewActor(userID int64, name string, photoURL sql.NullString) domain.ReviewActor {
	return domain.ReviewActor{
		UserID:   snow.ID(userID),
		Name:     name,
		PhotoURL: photoURL.String,
	}
}

func mergeRequestReviewToDomain(row sqlcSqlite.MergeRequestReview) *domain.MergeRequestReview {
	review := &domain.MergeRequestReview{
		ID:             snow.ID(row.ID),
		MergeRequestID: row.MergeRequestID,
		Reviewer:       domain.ReviewActor{UserID: snow.ID(row.ReviewerID)},
		State:          row.State,
		Body:           row.Body,
		HeadCommitID:   snow.ID(row.HeadCommitID),
		DismissedAt:    nullTimePtr(row.DismissedAt),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if row.DismissedBy.Valid {
		review.DismissedBy = &domain.ReviewActor{UserID: snow.ID(row.DismissedBy.Int64)}
	}
	review.DismissedReason = row.DismissedReason.String
	return review
}

func mergeRequestReviewRowToDomain(row sqlcSqlite.MergeRequestReviewListRow) *domain.MergeRequestReview {
	review := mergeRequestReviewToDomain(sqlcSqlite.MergeRequestReview{
		ID:              row.ID,
		MergeRequestID:  row.MergeRequestID,
		ReviewerID:      row.ReviewerID,
		State:           row.State,
		Body:            row.Body,
		HeadCommitID:    row.HeadCommitID,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		DismissedAt:     row.DismissedAt,
		DismissedBy:     row.DismissedBy,
		DismissedReason: row.DismissedReason,
	})
	review.Reviewer = reviewActor(row.ReviewerID, row.ReviewerName, row.ReviewerPhotoUrl)
	if row.DismissedBy.Valid {
		review.DismissedBy = &domain.ReviewActor{
			UserID: snow.ID(row.DismissedBy.Int64),
			Name:   row.DismissedByName.String,
		}
	}
	return review
}

func mergeRequestThreadToDomain(row sqlcSqlite.MergeRequestThread) *domain.MergeRequestThread {
	thread := &domain.MergeRequestThread{
		ID:             snow.ID(row.ID),
		MergeRequestID: row.MergeRequestID,
		ReviewID:       nullInt64SnowIDPtr(row.ReviewID),
		FilePath:       row.FilePath.String,
		OldLine:        nullInt64IntPtr(row.OldLine),
		NewLine:        nullInt64IntPtr(row.NewLine),
		BaseCommitID:   nullInt64SnowIDPtr(row.BaseCommitID),
		HeadCommitID:   nullInt64SnowIDPtr(row.HeadCommitID),
		Resolved:       row.Resolved,
		CreatedBy:      domain.ReviewActor{UserID: snow.ID(row.CreatedBy)},
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Comments:       []*domain.MergeRequestComment{},
	}
	if row.ResolvedBy.Valid {
		thread.ResolvedBy = &domain.ReviewActor{UserID: snow.ID(row.ResolvedBy.Int64)}
	}
	thread.ResolvedAt = nullTimePtr(row.ResolvedAt)
	return thread
}

func mergeRequestThreadRowToDomain(row sqlcSqlite.MergeRequestThreadListRow) *domain.MergeRequestThread {
	thread := mergeRequestThreadToDomain(sqlcSqlite.MergeRequestThread{
		ID:             row.ID,
		MergeRequestID: row.MergeRequestID,
		FilePath:       row.FilePath,
		OldLine:        row.OldLine,
		NewLine:        row.NewLine,
		BaseCommitID:   row.BaseCommitID,
		HeadCommitID:   row.HeadCommitID,
		Resolved:       row.Resolved,
		ResolvedBy:     row.ResolvedBy,
		ResolvedAt:     row.ResolvedAt,
		CreatedBy:      row.CreatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		ReviewID:       row.ReviewID,
	})
	thread.CreatedBy = reviewActor(row.CreatedBy, row.CreatedByName, row.CreatedByPhotoUrl)
	if row.ResolvedBy.Valid {
		thread.ResolvedBy = &domain.ReviewActor{
			UserID: snow.ID(row.ResolvedBy.Int64),
			Name:   row.ResolvedByName.String,
		}
	}
	thread.ResolvedAt = nullTimePtr(row.ResolvedAt)
	return thread
}

func mergeRequestCommentToDomain(row sqlcSqlite.MergeRequestComment) *domain.MergeRequestComment {
	return &domain.MergeRequestComment{
		ID:        snow.ID(row.ID),
		ThreadID:  snow.ID(row.ThreadID),
		User:      domain.ReviewActor{UserID: snow.ID(row.UserID)},
		Body:      row.Body,
		System:    row.System,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func mergeRequestEventToDomain(row sqlcSqlite.MergeRequestEvent) *domain.MergeRequestTimelineItem {
	item := &domain.MergeRequestTimelineItem{
		ID:             snow.ID(row.ID),
		MergeRequestID: row.MergeRequestID,
		Kind:           row.Kind,
		Actor:          domain.ReviewActor{UserID: snow.ID(row.ActorID)},
		Body:           row.Body,
		CommitID:       nullInt64SnowIDPtr(row.CommitID),
		CommitHash:     row.CommitHash.String,
		CreatedAt:      row.CreatedAt,
	}
	if row.SubjectUserID.Valid {
		item.Subject = &domain.ReviewActor{UserID: snow.ID(row.SubjectUserID.Int64)}
	}
	return item
}

func optionalSnowID(actor *domain.ReviewActor) sql.NullInt64 {
	if actor == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: actor.UserID.Int64(), Valid: true}
}

func optionalString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func optionalInt64(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func nullInt64IntPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	line := int(value.Int64)
	return &line
}
