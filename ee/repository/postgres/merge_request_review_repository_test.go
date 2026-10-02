package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestReviewRepositorySuite struct {
	baseSuite
}

func TestMergeRequestReviewRepositorySuite(t *testing.T) {
	suite.Run(t, new(MergeRequestReviewRepositorySuite))
}

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

func (s *MergeRequestReviewRepositorySuite) newReviewFixture() reviewFixture {
	s.T().Helper()
	ctx := context.Background()
	projectID := seedProject(s.T(), s.q, 1, "game")
	authorID := seedPBACUser(s.T(), s.db, 42)
	reviewerID := seedPBACUser(s.T(), s.db, 43)
	treeID := seedTreeNode(s.T(), s.db, "root", sql.NullInt64{})
	headCommit := seedCommitRow(s.T(), s.db, projectID, treeID, authorID.Int64(), testDomainHash(), sql.NullInt64{}, "head")
	sourceID := seedBranch(s.T(), s.db, projectID, "feature", sql.NullInt64{Int64: headCommit.Int64(), Valid: true})
	targetID := seedBranch(s.T(), s.db, projectID, "main", sql.NullInt64{})

	mrRepo := NewMergeRequestRepository(s.db)
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
	require.NoError(s.T(), err)

	return reviewFixture{
		db:         s.db,
		repo:       NewMergeRequestReviewRepository(s.db),
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

// pushSourceHead moves the source branch to a new commit, as a client push would.
func (f *reviewFixture) pushSourceHead(t *testing.T) snow.ID {
	t.Helper()
	f.commits++
	commit := seedCommitRow(t, f.db, f.projectID, f.treeID, f.authorID.Int64(), testDomainHash(), sql.NullInt64{}, fmt.Sprintf("push %d", f.commits))
	_, err := f.db.ExecContext(context.Background(),
		`UPDATE branches SET commit_id = $1 WHERE id = $2`, commit.Int64(), f.sourceID.Int64())
	require.NoError(t, err)
	return commit
}

func (s *MergeRequestReviewRepositorySuite) TestReviewRoundIsPerHead() {
	ctx := context.Background()
	f := s.newReviewFixture()

	review, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		Body:           "looks good",
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)
	s.Equal(f.reviewerID, review.Reviewer.UserID)
	s.Equal(domain.MergeRequestReviewApproved, review.State)
	s.Equal("looks good", review.Body)
	s.Equal(f.headCommit, review.HeadCommitID)
	s.Nil(review.DismissedAt)

	// re-reviewing the same head replaces the decision instead of adding one
	flipped, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6002,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewChangesRequested,
		Body:           "needs work",
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(6001), flipped.ID)
	s.Equal(domain.MergeRequestReviewChangesRequested, flipped.State)
	s.Equal("needs work", flipped.Body)

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(reviews, 1)
	s.NotEmpty(reviews[0].Reviewer.Name)

	// a new head starts a new round
	newHead := f.pushSourceHead(s.T())
	_, err = f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6003,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   newHead,
	})
	s.Require().NoError(err)
	reviews, err = f.repo.ListReviews(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(reviews, 2)
}

func (s *MergeRequestReviewRepositorySuite) TestSummariesFollowSourceHead() {
	ctx := context.Background()
	f := s.newReviewFixture()

	review := func(id int64, userID snow.ID, state string, head snow.ID) {
		s.T().Helper()
		_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
			ID:             snow.ID(id),
			MergeRequestID: f.mrID,
			Reviewer:       domain.ReviewActor{UserID: userID},
			State:          state,
			HeadCommitID:   head,
		})
		require.NoError(s.T(), err)
	}
	review(6001, f.reviewerID, domain.MergeRequestReviewApproved, f.headCommit)
	review(6002, f.authorID, domain.MergeRequestReviewChangesRequested, f.headCommit)

	summaries, err := f.repo.ReviewSummaries(ctx, f.projectID)
	s.Require().NoError(err)
	s.Equal(1, summaries[f.mrNumber].Approvals)
	s.Equal(1, summaries[f.mrNumber].ChangesRequested)
	s.Equal(f.headCommit, summaries[f.mrNumber].HeadCommitID)

	// a new head invalidates both decisions, leaving the merge request absent
	// from the summary map until a decision is given for the new head
	newHead := f.pushSourceHead(s.T())
	summaries, err = f.repo.ReviewSummaries(ctx, f.projectID)
	s.Require().NoError(err)
	s.NotContains(summaries, f.mrNumber)

	review(6003, f.reviewerID, domain.MergeRequestReviewApproved, newHead)
	review(6004, f.authorID, domain.MergeRequestReviewApproved, newHead)
	summaries, err = f.repo.ReviewSummaries(ctx, f.projectID)
	s.Require().NoError(err)
	s.Equal(2, summaries[f.mrNumber].Approvals)
	s.Equal(newHead, summaries[f.mrNumber].HeadCommitID)
}

func (s *MergeRequestReviewRepositorySuite) TestStaleReviews() {
	ctx := context.Background()
	f := s.newReviewFixture()

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)

	stale, err := f.repo.StaleReviews(ctx, f.mrID, f.headCommit)
	s.Require().NoError(err)
	s.Empty(stale)

	newHead := f.pushSourceHead(s.T())
	stale, err = f.repo.StaleReviews(ctx, f.mrID, newHead)
	s.Require().NoError(err)
	s.Len(stale, 1)
	s.Equal(f.headCommit, stale[0].HeadCommitID)

	dismissedAt := time.Now()
	s.Require().NoError(f.repo.DismissStaleReviews(ctx, f.mrID, newHead, f.authorID, domain.MergeRequestDismissedNewCommits, dismissedAt))

	stale, err = f.repo.StaleReviews(ctx, f.mrID, newHead)
	s.Require().NoError(err)
	s.Empty(stale)

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(reviews, 1)
	s.NotNil(reviews[0].DismissedAt)
	s.Equal(domain.MergeRequestDismissedNewCommits, reviews[0].DismissedReason)
	s.Equal(f.authorID, reviews[0].DismissedBy.UserID)
}

func (s *MergeRequestReviewRepositorySuite) TestCommentOnlySurvivesDismissal() {
	ctx := context.Background()
	f := s.newReviewFixture()

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewCommented,
		Body:           "nit: typo",
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)

	newHead := f.pushSourceHead(s.T())
	s.Require().NoError(f.repo.DismissStaleReviews(ctx, f.mrID, newHead, f.authorID, domain.MergeRequestDismissedNewCommits, time.Now()))

	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(reviews, 1)
	s.Nil(reviews[0].DismissedAt)
}

func (s *MergeRequestReviewRepositorySuite) TestManualDismissAndWithdraw() {
	ctx := context.Background()
	f := s.newReviewFixture()

	_, err := f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)

	s.Require().NoError(f.repo.DismissReview(ctx, f.mrID, snow.ID(6001), f.authorID, "manual", time.Now()))
	got, err := f.repo.GetReview(ctx, f.mrID, snow.ID(6001))
	s.Require().NoError(err)
	s.Equal("manual", got.DismissedReason)
	s.Equal(f.authorID, got.DismissedBy.UserID)

	s.Require().NoError(f.repo.DeleteReview(ctx, f.mrID, snow.ID(6001)))
	reviews, err := f.repo.ListReviews(ctx, f.mrID)
	s.Require().NoError(err)
	s.Empty(reviews)

	_, err = f.repo.GetReview(ctx, f.mrID, snow.ID(6001))
	requireRecordNotFound(s.T(), err)
}

func (s *MergeRequestReviewRepositorySuite) TestThreadAndComments() {
	ctx := context.Background()
	f := s.newReviewFixture()
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
	s.Require().NoError(err)
	s.Equal("src/main.go", thread.FilePath)
	s.Equal(newLine, *thread.NewLine)
	s.Nil(thread.OldLine)
	s.False(thread.Resolved)
	s.False(thread.IsTopLevel())
	s.Equal("right", thread.Side())

	got, err := f.repo.GetThread(ctx, f.mrID, thread.ID)
	s.Require().NoError(err)
	s.Equal(thread.ID, got.ID)

	comment, err := f.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       8001,
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: f.reviewerID},
		Body:     "rename this",
	})
	s.Require().NoError(err)
	s.Equal("rename this", comment.Body)
	s.False(comment.System)

	reply, err := f.repo.CreateComment(ctx, domain.MergeRequestComment{
		ID:       8002,
		ThreadID: thread.ID,
		User:     domain.ReviewActor{UserID: f.authorID},
		Body:     "done",
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(8002), reply.ID)

	updated, err := f.repo.UpdateComment(ctx, thread.ID, comment.ID, "rename this var")
	s.Require().NoError(err)
	s.Equal("rename this var", updated.Body)

	threads, err := f.repo.ListThreads(ctx, f.mrID, nil)
	s.Require().NoError(err)
	s.Len(threads, 1)
	s.Len(threads[0].Comments, 2)
	s.Equal("done", threads[0].Comments[1].Body)
	s.Equal(f.reviewerID, threads[0].CreatedBy.UserID)

	_, err = f.repo.UpsertReview(ctx, domain.MergeRequestReview{
		ID:             6001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		State:          domain.MergeRequestReviewApproved,
		HeadCommitID:   f.headCommit,
	})
	s.Require().NoError(err)
	s.Require().NoError(f.repo.SetThreadReview(ctx, f.mrID, thread.ID, ptr(snow.ID(6001))))
	threads, err = f.repo.ListThreads(ctx, f.mrID, nil)
	s.Require().NoError(err)
	s.Equal(snow.ID(6001), *threads[0].ReviewID)
	s.Require().NoError(f.repo.SetThreadReview(ctx, f.mrID, thread.ID, nil))

	resolvedAt := time.Now()
	resolved, err := f.repo.SetThreadResolved(ctx, f.mrID, thread.ID, true, &f.authorID, &resolvedAt)
	s.Require().NoError(err)
	s.True(resolved.Resolved)
	s.Equal(f.authorID, resolved.ResolvedBy.UserID)
	s.NotNil(resolved.ResolvedAt)

	threads, err = f.repo.ListThreads(ctx, f.mrID, ptr(true))
	s.Require().NoError(err)
	s.Len(threads, 1)
	threads, err = f.repo.ListThreads(ctx, f.mrID, ptr(false))
	s.Require().NoError(err)
	s.Empty(threads)

	reopened, err := f.repo.SetThreadResolved(ctx, f.mrID, thread.ID, false, nil, nil)
	s.Require().NoError(err)
	s.False(reopened.Resolved)
	s.Nil(reopened.ResolvedBy)
	s.Nil(reopened.ResolvedAt)

	comments, err := f.repo.ListComments(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(comments, 2)
	s.Require().NoError(f.repo.DeleteComment(ctx, thread.ID, reply.ID))
	comments, err = f.repo.ListComments(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(comments, 1)
	requireRecordNotFound(s.T(), f.repo.DeleteComment(ctx, thread.ID, reply.ID))
	requireRecordNotFound(s.T(), f.repo.DeleteComment(ctx, thread.ID, snow.ID(9999)))

	s.Require().NoError(f.repo.DeleteThread(ctx, f.mrID, thread.ID))
	threads, err = f.repo.ListThreads(ctx, f.mrID, nil)
	s.Require().NoError(err)
	s.Empty(threads)
	requireRecordNotFound(s.T(), f.repo.DeleteThread(ctx, f.mrID, thread.ID))
}

func (s *MergeRequestReviewRepositorySuite) TestTopLevelThreadIsLeftAnchored() {
	ctx := context.Background()
	f := s.newReviewFixture()
	oldLine := 4

	conversation, err := f.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             7001,
		MergeRequestID: f.mrID,
		CreatedBy:      domain.ReviewActor{UserID: f.reviewerID},
	})
	s.Require().NoError(err)
	s.True(conversation.IsTopLevel())
	s.Equal("right", conversation.Side())

	removed, err := f.repo.CreateThread(ctx, domain.MergeRequestThread{
		ID:             7002,
		MergeRequestID: f.mrID,
		FilePath:       "src/main.go",
		OldLine:        &oldLine,
		CreatedBy:      domain.ReviewActor{UserID: f.reviewerID},
	})
	s.Require().NoError(err)
	s.Equal("left", removed.Side())
}

func (s *MergeRequestReviewRepositorySuite) TestReviewRequests() {
	ctx := context.Background()
	f := s.newReviewFixture()

	request, err := f.repo.CreateReviewRequest(ctx, domain.MergeRequestReviewRequest{
		ID:             9001,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		RequestedBy:    domain.ReviewActor{UserID: f.authorID},
	})
	s.Require().NoError(err)
	s.Equal(f.reviewerID, request.Reviewer.UserID)

	// requesting the same reviewer twice keeps the original request
	again, err := f.repo.CreateReviewRequest(ctx, domain.MergeRequestReviewRequest{
		ID:             9002,
		MergeRequestID: f.mrID,
		Reviewer:       domain.ReviewActor{UserID: f.reviewerID},
		RequestedBy:    domain.ReviewActor{UserID: f.reviewerID},
	})
	s.Require().NoError(err)
	s.Equal(snow.ID(9001), again.ID)

	requests, err := f.repo.ListReviewRequests(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(requests, 1)
	s.Equal(f.authorID, requests[0].RequestedBy.UserID)
	s.NotEmpty(requests[0].Reviewer.Name)

	s.Require().NoError(f.repo.DeleteReviewRequest(ctx, f.mrID, f.reviewerID))
	requests, err = f.repo.ListReviewRequests(ctx, f.mrID)
	s.Require().NoError(err)
	s.Empty(requests)
	requireRecordNotFound(s.T(), f.repo.DeleteReviewRequest(ctx, f.mrID, f.reviewerID))
}

func (s *MergeRequestReviewRepositorySuite) TestTimeline() {
	ctx := context.Background()
	f := s.newReviewFixture()
	head := f.headCommit

	_, err := f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10001,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventOpened,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		Body:           "Add feature",
	})
	s.Require().NoError(err)
	_, err = f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10002,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventPushed,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		CommitID:       &head,
		CommitHash:     "abc123",
	})
	s.Require().NoError(err)
	_, err = f.repo.CreateEvent(ctx, domain.MergeRequestTimelineItem{
		ID:             10003,
		MergeRequestID: f.mrID,
		Kind:           domain.MergeRequestEventReviewRequested,
		Actor:          domain.ReviewActor{UserID: f.authorID},
		Subject:        &domain.ReviewActor{UserID: f.reviewerID},
	})
	s.Require().NoError(err)

	events, err := f.repo.ListEvents(ctx, f.mrID)
	s.Require().NoError(err)
	s.Len(events, 3)
	s.Equal(domain.MergeRequestEventOpened, events[0].Kind)
	s.Equal("Add feature", events[0].Body)
	s.NotEmpty(events[0].Actor.Name)
	s.Nil(events[0].Subject)
	s.Equal(domain.MergeRequestEventPushed, events[1].Kind)
	s.Equal("abc123", events[1].CommitHash)
	s.Equal(head, *events[1].CommitID)
	s.Equal(domain.MergeRequestEventReviewRequested, events[2].Kind)
	s.Equal(f.reviewerID, events[2].Subject.UserID)
	s.NotEmpty(events[2].Subject.Name)
}

func (s *MergeRequestReviewRepositorySuite) TestListOpenBySourceBranch() {
	ctx := context.Background()
	f := s.newReviewFixture()

	open, err := f.repo.ListOpenBySourceBranch(ctx, f.projectID, f.sourceID)
	s.Require().NoError(err)
	s.Len(open, 1)
	s.Equal(f.mrNumber, open[0].Number)

	unrelated := seedBranch(s.T(), f.db, f.projectID, "unrelated", sql.NullInt64{})
	none, err := f.repo.ListOpenBySourceBranch(ctx, f.projectID, unrelated)
	s.Require().NoError(err)
	s.Empty(none)

	s.Require().NoError(f.mrRepo.UpdateStatus(ctx, f.projectID, f.mrNumber, domain.MergeRequestMerged, nil))
	none, err = f.repo.ListOpenBySourceBranch(ctx, f.projectID, f.sourceID)
	s.Require().NoError(err)
	s.Empty(none)
}
