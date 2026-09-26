package server

import (
	"testing"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/stretchr/testify/require"
)

func TestDomainReviewToPB(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	dismissed := time.Unix(300, 0).UTC()

	detail := domainReviewToPB(&domain.MergeRequestReview{
		ID:             snow.ID(7),
		MergeRequestID: 5,
		Reviewer:       domain.ReviewActor{UserID: snow.ID(8), Name: "Rev", PhotoURL: "p.png"},
		State:          domain.MergeRequestReviewApproved,
		Body:           "ok",
		HeadCommitID:   snow.ID(11),
		CreatedAt:      created,
		UpdatedAt:      created,
	})
	require.Equal(t, snow.ID(7).Base36(), detail.Id)
	require.EqualValues(t, 5, detail.MergeRequestId)
	require.Equal(t, snow.ID(8).Base36(), detail.Reviewer.UserId)
	require.Equal(t, "Rev", detail.Reviewer.Name)
	require.Equal(t, "p.png", detail.Reviewer.PhotoUrl)
	require.Equal(t, domain.MergeRequestReviewApproved, detail.State)
	require.Equal(t, snow.ID(11).Base36(), detail.HeadCommitId)
	require.Nil(t, detail.DismissedAt)
	require.Nil(t, detail.DismissedBy)

	detail = domainReviewToPB(&domain.MergeRequestReview{
		ID:              snow.ID(7),
		Reviewer:        domain.ReviewActor{UserID: snow.ID(8)},
		State:           domain.MergeRequestReviewApproved,
		Stale:           true,
		DismissedAt:     &dismissed,
		DismissedReason: domain.MergeRequestDismissedNewCommits,
		DismissedBy:     &domain.ReviewActor{UserID: snow.ID(9), Name: "Author"},
	})
	require.True(t, detail.Stale)
	require.Equal(t, dismissed, detail.DismissedAt.AsTime())
	require.Equal(t, domain.MergeRequestDismissedNewCommits, detail.DismissedReason)
	require.Equal(t, snow.ID(9).Base36(), detail.DismissedBy.UserId)

	require.Nil(t, domainReviewToPB(nil))
}

func TestDomainReviewStateToPB(t *testing.T) {
	require.Nil(t, domainReviewStateToPB(nil))

	state := domainReviewStateToPB(&domain.MergeRequestReviewState{
		HeadCommitID:         snow.ID(11),
		Approvals:            2,
		ChangesRequested:     1,
		DismissedApprovals:   3,
		OutstandingReviewers: []snow.ID{snow.ID(8), snow.ID(9)},
	})
	require.EqualValues(t, 2, state.Approvals)
	require.EqualValues(t, 1, state.ChangesRequested)
	require.EqualValues(t, 3, state.DismissedApprovals)
	require.Equal(t, []string{snow.ID(8).Base36(), snow.ID(9).Base36()}, state.OutstandingReviewers)
	require.Equal(t, snow.ID(11).Base36(), state.HeadCommitId)

	empty := domainReviewStateToPB(&domain.MergeRequestReviewState{})
	require.NotNil(t, empty.OutstandingReviewers, "an empty outstanding list is still a list")
}

func TestDomainThreadToPB(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	edited := time.Unix(200, 0).UTC()
	reviewID := snow.ID(7)
	resolvedAt := time.Unix(300, 0).UTC()
	resolvedBy := domain.ReviewActor{UserID: snow.ID(9), Name: "Author"}
	newLine, oldLine := 12, 4

	thread := domainThreadToPB(&domain.MergeRequestThread{
		ID:             snow.ID(3),
		MergeRequestID: 5,
		ReviewID:       &reviewID,
		FilePath:       "a.txt",
		NewLine:        &newLine,
		CreatedBy:      domain.ReviewActor{UserID: snow.ID(8), Name: "Rev"},
		CreatedAt:      created,
		Comments: []*domain.MergeRequestComment{
			{ID: snow.ID(31), ThreadID: snow.ID(3), User: domain.ReviewActor{UserID: snow.ID(8)}, Body: "hi", CreatedAt: created, UpdatedAt: created},
			{ID: snow.ID(32), ThreadID: snow.ID(3), User: domain.ReviewActor{UserID: snow.ID(9)}, Body: "edited", CreatedAt: created, UpdatedAt: edited},
		},
	})
	require.Equal(t, snow.ID(3).Base36(), thread.Id)
	require.Equal(t, snow.ID(7).Base36(), thread.ReviewId)
	require.Equal(t, "a.txt", thread.FilePath)
	require.EqualValues(t, 12, thread.GetNewLine())
	require.Equal(t, "right", thread.Side, "a context or added line is on the right")
	require.Equal(t, snow.ID(8).Base36(), thread.Comments[0].User.UserId)
	require.False(t, thread.Comments[0].Edited)
	require.True(t, thread.Comments[1].Edited)

	removal := domainThreadToPB(&domain.MergeRequestThread{ID: snow.ID(4), OldLine: &oldLine, FilePath: "a.txt"})
	require.Equal(t, "left", removal.Side, "a removed line is on the left")
	require.Equal(t, snow.ID(4).Base36(), removal.Id)
	require.NotNil(t, removal.Comments)
	require.Empty(t, removal.Comments)

	topLevel := domainThreadToPB(&domain.MergeRequestThread{ID: snow.ID(5), NewLine: &newLine})
	require.Empty(t, topLevel.Side, "a conversation thread is not anchored to a diff side")
	require.Equal(t, snow.ID(5).Base36(), topLevel.Id)

	resolved := domainThreadToPB(&domain.MergeRequestThread{
		ID:         snow.ID(6),
		Resolved:   true,
		ResolvedAt: &resolvedAt,
		ResolvedBy: &resolvedBy,
	})
	require.True(t, resolved.Resolved)
	require.Equal(t, resolvedAt, resolved.ResolvedAt.AsTime())
	require.Equal(t, snow.ID(9).Base36(), resolved.ResolvedBy.UserId)

	require.Nil(t, domainThreadToPB(nil))
}

func TestDomainReviewCommentToPB(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	require.Nil(t, domainReviewCommentToPB(nil))

	comment := domainReviewCommentToPB(&domain.MergeRequestComment{
		ID:        snow.ID(31),
		ThreadID:  snow.ID(3),
		User:      domain.ReviewActor{UserID: snow.ID(8), Name: "Rev"},
		Body:      "nit",
		System:    true,
		CreatedAt: created,
		UpdatedAt: created,
	})
	require.Equal(t, snow.ID(31).Base36(), comment.Id)
	require.Equal(t, snow.ID(3).Base36(), comment.ThreadId)
	require.Equal(t, "Rev", comment.User.Name)
	require.Equal(t, "nit", comment.Body)
	require.True(t, comment.System)
	require.False(t, comment.Edited)
}

func TestDomainReviewRequestToPB(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	require.Nil(t, domainReviewRequestToPB(nil))

	request := domainReviewRequestToPB(&domain.MergeRequestReviewRequest{
		ID:             snow.ID(4),
		MergeRequestID: 5,
		Reviewer:       domain.ReviewActor{UserID: snow.ID(8), Name: "Rev"},
		RequestedBy:    domain.ReviewActor{UserID: snow.ID(9), Name: "Author"},
		CreatedAt:      created,
	})
	require.Equal(t, snow.ID(4).Base36(), request.Id)
	require.EqualValues(t, 5, request.MergeRequestId)
	require.Equal(t, snow.ID(8).Base36(), request.Reviewer.UserId)
	require.Equal(t, snow.ID(9).Base36(), request.RequestedBy.UserId)
}

func TestDomainReviewActorToPB(t *testing.T) {
	require.Nil(t, domainReviewActorToPB(nil))
	actor := domainReviewActorToPB(&domain.ReviewActor{})
	require.NotNil(t, actor)
	require.Equal(t, snow.ID(0).Base36(), actor.UserId)
}

func TestParseBase36ID(t *testing.T) {
	id, err := parseBase36ID(snow.ID(12).Base36())
	require.NoError(t, err)
	require.Equal(t, snow.ID(12), id)

	_, err = parseBase36ID("")
	require.Error(t, err)
	require.Equal(t, 400, err.(*domain.Error).Code)

	_, err = parseBase36ID("not-base36!")
	require.Error(t, err)
	require.Equal(t, 400, err.(*domain.Error).Code)
}

func TestIDPointerConversion(t *testing.T) {
	require.Nil(t, int64ToIntPtr(nil))
	require.Nil(t, intToInt64Ptr(nil))

	line := int64(7)
	got := int64ToIntPtr(&line)
	require.NotNil(t, got)
	require.Equal(t, 7, *got)
	require.Equal(t, line, *intToInt64Ptr(got))
}
