package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type reviewFixture struct {
	db         *sql.DB
	repo       *MergeRequestReviewRepository
	mrRepo     *MergeRequestRepository
	projectID  snow.ID
	sourceID   snow.ID
	authorID   snow.ID
	reviewerID snow.ID
	headCommit snow.ID
	treeID     int64
	commits    int
	mrID       int64
	mrNumber   int64
}

func newReviewFixture(t *testing.T) reviewFixture {
	t.Helper()
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	projectID := seedProject(t, q, 1, "game")
	authorID := seedPBACUser(t, db, 42)
	reviewerID := seedPBACUser(t, db, 43)
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	headCommit := insertReviewCommit(t, db, projectID, treeID, authorID, "head")
	sourceID := seedBranch(t, db, projectID, "feature", sql.NullInt64{Int64: headCommit.Int64(), Valid: true})
	targetID := seedBranch(t, db, projectID, "main", sql.NullInt64{})

	mrRepo := NewMergeRequestRepository(db)
	created, err := mrRepo.Create(ctx, domain.MergeRequest{
		ID:             5001,
		ProjectID:      projectID,
		SourceBranchID: sourceID,
		TargetBranchID: targetID,
		SourceBranch:   "feature",
		TargetBranch:   "main",
		Title:          "Add feature",
		CreatedBy:      authorID,
	})
	require.NoError(t, err)

	return reviewFixture{
		db:         db,
		repo:       NewMergeRequestReviewRepository(db),
		mrRepo:     mrRepo,
		projectID:  projectID,
		sourceID:   sourceID,
		authorID:   authorID,
		reviewerID: reviewerID,
		headCommit: headCommit,
		treeID:     treeID,
		commits:    1,
		mrID:       created.ID,
		mrNumber:   created.Number,
	}
}

// insertReviewCommit adds a commit authored by an already seeded user, unlike
// seedCommit which reuses a fixed identity and can only run once per database.
func insertReviewCommit(t *testing.T, db *sql.DB, projectID snow.ID, treeID int64, userID snow.ID, message string) snow.ID {
	t.Helper()
	id := newTestNode(t).Generate()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		id.Int64(), testHashBytes(), projectID.Int64(), treeID, userID.Int64(), message,
	)
	require.NoError(t, err)
	return id
}

// pushSourceHead moves the source branch to a new commit, as a client push would.
func (f *reviewFixture) pushSourceHead(t *testing.T) snow.ID {
	t.Helper()
	f.commits++
	commit := insertReviewCommit(t, f.db, f.projectID, f.treeID, f.authorID,
		fmt.Sprintf("push %d", f.commits))
	_, err := f.db.ExecContext(context.Background(),
		`UPDATE branches SET commit_id = ? WHERE id = ?`, commit.Int64(), f.sourceID.Int64())
	require.NoError(t, err)
	return commit
}

func TestMergeRequestReviewRepository_ReviewRoundIsPerHead(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	review, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		Body:           "looks good",
		HeadCommitID:   f.headCommit,
	})
	require.NoError(t, err)
	require.Equal(t, f.reviewerID, review.Reviewer.UserID)
	require.Equal(t, domain.MergeRequestReviewApproved, review.State)
	require.Equal(t, "looks good", review.Body)
	require.Equal(t, f.headCommit, review.HeadCommitID)
	require.Nil(t, review.DismissedAt)

	// re-reviewing the same head replaces the decision instead of adding one
	flipped, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6002,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewChangesRequested,
		Body:           "needs work",
		HeadCommitID:   f.headCommit,
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(6001), flipped.ID)
	require.Equal(t, domain.MergeRequestReviewChangesRequested, flipped.State)
	require.Equal(t, "needs work", flipped.Body)

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.NotEmpty(t, reviews[0].Reviewer.Name)

	// a new head starts a new round
	newHead := f.pushSourceHead(t)
	_, err = f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6003,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   newHead,
	})
	require.NoError(t, err)
	reviews, err = f.repo.ListReviews(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, reviews, 2)
}

func TestMergeRequestReviewRepository_SummariesFollowSourceHead(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	review := func(id int64, userID snow.ID, state string, head snow.ID) {
		t.Helper()
		_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
			ID:             snow.ID(id),
			MergeRequestID: f.mrID,
			Reviewer:       domain.ReviewActor{UserID: userID},
			State:          state,
			HeadCommitID:   head,
		})
		require.NoError(t, err)
	}
	review(6001, f.reviewerID, domain.MergeRequestReviewApproved, f.headCommit)
	review(6002, f.authorID, domain.MergeRequestReviewChangesRequested, f.headCommit)

	summaries, err := f.repo.ReviewSummaries(ctx, f.projectID)
	require.NoError(t, err)
	require.Equal(t, 1, summaries[f.mrNumber].Approvals)
	require.Equal(t, 1, summaries[f.mrNumber].ChangesRequested)
	require.Equal(t, f.headCommit, summaries[f.mrNumber].HeadCommitID)

	// a new head invalidates both decisions, leaving the merge request absent
	// from the summary map until a decision is given for the new head
	newHead := f.pushSourceHead(t)
	summaries, err = f.repo.ReviewSummaries(ctx, f.projectID)
	require.NoError(t, err)
	require.NotContains(t, summaries, f.mrNumber)

	review(6003, f.reviewerID, domain.MergeRequestReviewApproved, newHead)
	review(6004, f.authorID, domain.MergeRequestReviewApproved, newHead)
	summaries, err = f.repo.ReviewSummaries(ctx, f.projectID)
	require.NoError(t, err)
	require.Equal(t, 2, summaries[f.mrNumber].Approvals)
	require.Equal(t, newHead, summaries[f.mrNumber].HeadCommitID)
}

func TestMergeRequestReviewRepository_StaleReviews(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   f.headCommit,
	})
	require.NoError(t, err)

	stale, err := f.repo.StaleReviews(ctx, f.mrID, f.headCommit)
	require.NoError(t, err)
	require.Empty(t, stale)

	newHead := f.pushSourceHead(t)
	stale, err = f.repo.StaleReviews(ctx, f.mrID, newHead)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	require.Equal(t, f.headCommit, stale[0].HeadCommitID)

	dismissedAt := time.Now()
	require.NoError(t, f.repo.DismissStaleReviews(ctx, f.mrID, newHead, f.authorID, domain.MergeRequestDismissedNewCommits, dismissedAt))

	stale, err = f.repo.StaleReviews(ctx, f.mrID, newHead)
	require.NoError(t, err)
	require.Empty(t, stale)

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.NotNil(t, reviews[0].DismissedAt)
	require.Equal(t, domain.MergeRequestDismissedNewCommits, reviews[0].DismissedReason)
	require.Equal(t, f.authorID, reviews[0].DismissedBy.UserID)
}

func TestMergeRequestReviewRepository_CommentOnlySurvivesDismissal(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewCommented,
		Body:           "nit: typo",
		HeadCommitID:   f.headCommit,
	})
	require.NoError(t, err)

	newHead := f.pushSourceHead(t)
	require.NoError(t, f.repo.DismissStaleReviews(ctx, f.mrID, newHead, f.authorID, domain.MergeRequestDismissedNewCommits, time.Now()))

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Nil(t, reviews[0].DismissedAt)
}

func TestMergeRequestReviewRepository_ManualDismissAndWithdraw(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   f.headCommit,
	})
	require.NoError(t, err)

	require.NoError(t, f.repo.DismissReview(ctx, f.mrID, snow.ID(6001), f.authorID, "manual", time.Now()))
	got, err := f.repo.GetReview(ctx, f.mrID, snow.ID(6001))
	require.NoError(t, err)
	require.Equal(t, "manual", got.DismissedReason)
	require.Equal(t, f.authorID, got.DismissedBy.UserID)

	require.NoError(t, f.repo.DeleteReview(ctx, f.mrID, snow.ID(6001)))
	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	require.NoError(t, err)
	require.Empty(t, reviews)

	_, err = f.repo.GetReview(ctx, f.mrID, snow.ID(6001))
	requireRecordNotFound(t, err)
}

func TestMergeRequestReviewRepository_ThreadAndComments(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)
	newLine := 12

	thread, err := f.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             7001,
		MergeRequestID: f.mrID,
		FilePath:       "src/main.go",
		NewLine:        &newLine,
		BaseCommitID:   &f.headCommit,
		HeadCommitID:   &f.headCommit,
		CreatedBy:      domain.ReviewActor{UserID: f.reviewerID},
	})
	require.NoError(t, err)
	require.Equal(t, "src/main.go", thread.FilePath)
	require.Equal(t, newLine, *thread.NewLine)
	require.Nil(t, thread.OldLine)
	require.False(t, thread.Resolved)
	require.False(t, thread.IsTopLevel())
	require.Equal(t, "right", thread.Side())

	got, err := f.repo.GetThread(ctx, f.mrID, thread.ID)
	require.NoError(t, err)
	require.Equal(t, thread.ID, got.ID)

	comment, err := f.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       8001,
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: f.reviewerID},
		Body:     "rename this",
	})
	require.NoError(t, err)
	require.Equal(t, "rename this", comment.Body)
	require.False(t, comment.System)

	reply, err := f.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       8002,
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: f.authorID},
		Body:     "done",
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(8002), reply.ID)

	updated, err := f.repo.UpdateComment(ctx, thread.ID, comment.ID, "rename this var")
	require.NoError(t, err)
	require.Equal(t, "rename this var", updated.Body)

	threads, err := f.repo.ListThreads(ctx, f.mrID, nil)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0].Comments, 2)
	require.Equal(t, "done", threads[0].Comments[1].Body)
	require.Equal(t, f.reviewerID, threads[0].CreatedBy.UserID)

	require.NoError(t, f.repo.SetThreadReview(ctx, f.mrID, thread.ID, ptr(snow.ID(6001))))
	threads, err = f.repo.ListThreads(ctx, f.mrID, nil)
	require.NoError(t, err)
	require.Equal(t, snow.ID(6001), *threads[0].ReviewID)
	require.NoError(t, f.repo.SetThreadReview(ctx, f.mrID, thread.ID, nil))

	resolvedAt := time.Now()
	resolved, err := f.repo.SetThreadResolved(ctx, f.mrID, thread.ID, true, &f.authorID, &resolvedAt)
	require.NoError(t, err)
	require.True(t, resolved.Resolved)
	require.Equal(t, f.authorID, resolved.ResolvedBy.UserID)
	require.NotNil(t, resolved.ResolvedAt)

	threads, err = f.repo.ListThreads(ctx, f.mrID, ptr(true))
	require.NoError(t, err)
	require.Len(t, threads, 1)
	threads, err = f.repo.ListThreads(ctx, f.mrID, ptr(false))
	require.NoError(t, err)
	require.Empty(t, threads)

	reopened, err := f.repo.SetThreadResolved(ctx, f.mrID, thread.ID, false, nil, nil)
	require.NoError(t, err)
	require.False(t, reopened.Resolved)
	require.Nil(t, reopened.ResolvedBy)
	require.Nil(t, reopened.ResolvedAt)

	comments, err := f.repo.ListComments(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	require.NoError(t, f.repo.DeleteComment(ctx, thread.ID, reply.ID))
	comments, err = f.repo.ListComments(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	requireRecordNotFound(t, f.repo.DeleteComment(ctx, thread.ID, reply.ID))
	requireRecordNotFound(t, f.repo.DeleteComment(ctx, thread.ID, snow.ID(9999)))

	require.NoError(t, f.repo.DeleteThread(ctx, f.mrID, thread.ID))
	threads, err = f.repo.ListThreads(ctx, f.mrID, nil)
	require.NoError(t, err)
	require.Empty(t, threads)
	requireRecordNotFound(t, f.repo.DeleteThread(ctx, f.mrID, thread.ID))
}

func TestMergeRequestReviewRepository_TopLevelThreadIsLeftAnchored(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)
	oldLine := 4

	conversation, err := f.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             7001,
		MergeRequestID: f.mrID,
		CreatedBy:      domain.ReviewActor{UserID: f.reviewerID},
	})
	require.NoError(t, err)
	require.True(t, conversation.IsTopLevel())
	require.Equal(t, "right", conversation.Side())

	removed, err := f.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             7002,
		MergeRequestID: f.mrID,
		FilePath:       "src/main.go",
		OldLine:        &oldLine,
		CreatedBy:      domain.ReviewActor{UserID: f.reviewerID},
	})
	require.NoError(t, err)
	require.Equal(t, "left", removed.Side())
}

func TestMergeRequestReviewRepository_ReviewRequests(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	request, err := f.repo.CreateReviewRequest(ctx, domain.MergeRequestReviewRequest{
		ID:             9001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		RequestedBy:    domain.ReviewActor{UserID: f.authorID},
	})
	require.NoError(t, err)
	require.Equal(t, f.reviewerID, request.Reviewer.UserID)

	// requesting the same reviewer twice keeps the original request
	again, err := f.repo.CreateReviewRequest(ctx, domain.MergeRequestReviewRequest{
		ID:             9002,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		RequestedBy:    domain.ReviewActor{UserID: f.reviewerID},
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(9001), again.ID)

	requests, err := f.repo.ListReviewRequests(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, f.authorID, requests[0].RequestedBy.UserID)
	require.NotEmpty(t, requests[0].Reviewer.Name)

	require.NoError(t, f.repo.DeleteReviewRequest(ctx, f.mrID, f.reviewerID))
	requests, err = f.repo.ListReviewRequests(ctx, f.mrID)
	require.NoError(t, err)
	require.Empty(t, requests)
	requireRecordNotFound(t, f.repo.DeleteReviewRequest(ctx, f.mrID, f.reviewerID))
}

func TestMergeRequestReviewRepository_Timeline(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)
	head := f.headCommit

	_, err := f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10001,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventOpened,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		Body:           "Add feature",
	})
	require.NoError(t, err)
	_, err = f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10002,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventPushed,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		CommitID:       &head,
		CommitHash:     "abc123",
	})
	require.NoError(t, err)
	_, err = f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10003,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventReviewRequested,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		Subject:        &domain.ReviewActor{UserID: f.reviewerID},
	})
	require.NoError(t, err)

	events, err := f.repo.ListEvents(ctx, f.mrID)
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Equal(t, domain.MergeRequestEventOpened, events[0].Kind)
	require.Equal(t, "Add feature", events[0].Body)
	require.NotEmpty(t, events[0].Actor.Name)
	require.Nil(t, events[0].Subject)
	require.Equal(t, domain.MergeRequestEventPushed, events[1].Kind)
	require.Equal(t, "abc123", events[1].CommitHash)
	require.Equal(t, head, *events[1].CommitID)
	require.Equal(t, domain.MergeRequestEventReviewRequested, events[2].Kind)
	require.Equal(t, f.reviewerID, events[2].Subject.UserID)
	require.NotEmpty(t, events[2].Subject.Name)
}

func TestMergeRequestReviewRepository_ListOpenBySourceBranch(t *testing.T) {
	ctx := context.Background()
	f := newReviewFixture(t)

	open, err := f.repo.ListOpenBySourceBranch(ctx, f.projectID, f.sourceID)
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.Equal(t, f.mrNumber, open[0].Number)

	unrelated := seedBranch(t, f.db, f.projectID, "unrelated", sql.NullInt64{})
	none, err := f.repo.ListOpenBySourceBranch(ctx, f.projectID, unrelated)
	require.NoError(t, err)
	require.Empty(t, none)

	require.NoError(t, f.mrRepo.UpdateStatus(ctx, f.projectID, f.mrNumber, domain.MergeRequestMerged, nil))
	none, err = f.repo.ListOpenBySourceBranch(ctx, f.projectID, f.sourceID)
	require.NoError(t, err)
	require.Empty(t, none)
}
