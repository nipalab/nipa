package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// Review decisions. A review is one decision per reviewer per review round,
// where a round is identified by the source head commit it was given for.
const (
	MergeRequestReviewCommented        = "commented"
	MergeRequestReviewApproved         = "approved"
	MergeRequestReviewChangesRequested = "changes_requested"
)

func IsValidMergeRequestReviewState(state string) bool {
	return state == MergeRequestReviewCommented ||
		state == MergeRequestReviewApproved ||
		state == MergeRequestReviewChangesRequested
}

// Timeline event kinds.
const (
	MergeRequestEventOpened            = "opened"
	MergeRequestEventPushed            = "pushed"
	MergeRequestEventReviewSubmitted   = "review_submitted"
	MergeRequestEventReviewDismissed   = "review_dismissed"
	MergeRequestEventReviewRequested   = "review_requested"
	MergeRequestEventReviewUnrequested = "review_request_removed"
	MergeRequestEventMerged            = "merged"
	MergeRequestEventClosed            = "closed"
	MergeRequestEventReopened          = "reopened"
)

// MergeRequestDismissedNewCommits marks a review that was dismissed because the
// source branch received a new push.
const MergeRequestDismissedNewCommits = "new_commits"

// ReviewActor is the identity behind a review, comment, thread or event.
type ReviewActor struct {
	UserID   snow.ID `json:"user_id"`
	Name     string  `json:"name"`
	PhotoURL string  `json:"photo_url,omitempty"`
}

// MergeRequestReview is one review decision. Stale and the actor fields are
// resolved at read time rather than stored: a review is stale once the source
// branch head no longer matches HeadCommitID.
type MergeRequestReview struct {
	ID              snow.ID      `json:"id"`
	MergeRequestID  int64        `json:"merge_request_id"`
	Reviewer        ReviewActor  `json:"reviewer"`
	State           string       `json:"state"`
	Body            string       `json:"body"`
	HeadCommitID    snow.ID      `json:"head_commit_id"`
	Stale           bool         `json:"stale"`
	DismissedAt     *time.Time   `json:"dismissed_at,omitempty"`
	DismissedBy     *ReviewActor `json:"dismissed_by,omitempty"`
	DismissedReason string       `json:"dismissed_reason,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// MergeRequestComment is one message in a thread.
type MergeRequestComment struct {
	ID        snow.ID     `json:"id"`
	ThreadID  snow.ID     `json:"thread_id"`
	User      ReviewActor `json:"user"`
	Body      string      `json:"body"`
	System    bool        `json:"system"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// MergeRequestThread is a review conversation. A thread without a FilePath is a
// top-level conversation thread rather than a comment on a diff line. Outdated
// is resolved at read time from HeadCommitID versus the live source head.
type MergeRequestThread struct {
	ID             snow.ID                `json:"id"`
	MergeRequestID int64                  `json:"merge_request_id"`
	ReviewID       *snow.ID               `json:"review_id,omitempty"`
	FilePath       string                 `json:"file_path,omitempty"`
	OldLine        *int                   `json:"old_line,omitempty"`
	NewLine        *int                   `json:"new_line,omitempty"`
	BaseCommitID   *snow.ID               `json:"base_commit_id,omitempty"`
	HeadCommitID   *snow.ID               `json:"head_commit_id,omitempty"`
	Outdated       bool                   `json:"outdated"`
	Resolved       bool                   `json:"resolved"`
	ResolvedBy     *ReviewActor           `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	CreatedBy      ReviewActor            `json:"created_by"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Comments       []*MergeRequestComment `json:"comments"`
}

// IsTopLevel reports whether the thread is a conversation thread rather than a
// comment anchored to a line of the diff.
func (t *MergeRequestThread) IsTopLevel() bool {
	return t.FilePath == ""
}

// Side is the diff side the thread is anchored to: "left" for a removed line,
// "right" for an added or context line.
func (t *MergeRequestThread) Side() string {
	if t.OldLine != nil && t.NewLine == nil {
		return "left"
	}
	return "right"
}

// MergeRequestReviewRequest is a pending request for a user to review.
type MergeRequestReviewRequest struct {
	ID             snow.ID     `json:"id"`
	MergeRequestID int64       `json:"merge_request_id"`
	Reviewer       ReviewActor `json:"reviewer"`
	RequestedBy    ReviewActor `json:"requested_by"`
	CreatedAt      time.Time   `json:"created_at"`
}

// MergeRequestTimelineItem is one entry of the merge request activity timeline.
// Subject is the user an event is about rather than performed by, such as the
// reviewer a review request names.
type MergeRequestTimelineItem struct {
	ID             snow.ID      `json:"id"`
	MergeRequestID int64        `json:"merge_request_id"`
	Kind           string       `json:"kind"`
	Actor          ReviewActor  `json:"actor"`
	Subject        *ReviewActor `json:"subject,omitempty"`
	Body           string       `json:"body,omitempty"`
	CommitID       *snow.ID     `json:"commit_id,omitempty"`
	CommitHash     string       `json:"commit_hash,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

// MergeRequestReviewState is the review summary attached to a merge request.
// Only reviews for the current source head count towards Approvals and
// ChangesRequested; a reviewer with such a review is no longer outstanding.
type MergeRequestReviewState struct {
	HeadCommitID         snow.ID   `json:"head_commit_id,omitempty"`
	Approvals            int       `json:"approvals"`
	ChangesRequested     int       `json:"changes_requested"`
	DismissedApprovals   int       `json:"dismissed_approvals"`
	OutstandingReviewers []snow.ID `json:"outstanding_reviewers"`
}
