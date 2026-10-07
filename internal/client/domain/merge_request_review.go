package domain

import "time"

// Review decisions. A review is one decision per reviewer per review round.
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

// ReviewActor is the identity behind a review, comment, thread or event.
type ReviewActor struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	PhotoURL string `json:"photo_url,omitempty"`
}

// MergeRequestReview is one review decision. Stale reports that the source
// branch moved on since the review was given.
type MergeRequestReview struct {
	ID              string       `json:"id"`
	MergeRequestID  int64        `json:"merge_request_id"`
	Reviewer        ReviewActor  `json:"reviewer"`
	State           string       `json:"state"`
	Body            string       `json:"body,omitempty"`
	HeadCommitID    string       `json:"head_commit_id,omitempty"`
	Stale           bool         `json:"stale,omitempty"`
	DismissedAt     *time.Time   `json:"dismissed_at,omitempty"`
	DismissedBy     *ReviewActor `json:"dismissed_by,omitempty"`
	DismissedReason string       `json:"dismissed_reason,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// MergeRequestComment is one comment of a review thread.
type MergeRequestComment struct {
	ID        string      `json:"id"`
	ThreadID  string      `json:"thread_id"`
	User      ReviewActor `json:"user"`
	Body      string      `json:"body"`
	Edited    bool        `json:"edited,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// MergeRequestThread is a top-level conversation thread (empty FilePath) or an
// inline thread anchored to a diff line.
type MergeRequestThread struct {
	ID             string                 `json:"id"`
	MergeRequestID int64                  `json:"merge_request_id"`
	ReviewID       string                 `json:"review_id,omitempty"`
	FilePath       string                 `json:"file_path,omitempty"`
	OldLine        *int64                 `json:"old_line,omitempty"`
	NewLine        *int64                 `json:"new_line,omitempty"`
	Side           string                 `json:"side,omitempty"`
	Outdated       bool                   `json:"outdated,omitempty"`
	Resolved       bool                   `json:"resolved"`
	ResolvedBy     *ReviewActor           `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	CreatedBy      ReviewActor            `json:"created_by"`
	CreatedAt      time.Time              `json:"created_at"`
	Comments       []*MergeRequestComment `json:"comments,omitempty"`
}

// MergeRequestReviewRequest is a pending request for a user to review.
type MergeRequestReviewRequest struct {
	ID             string      `json:"id"`
	MergeRequestID int64       `json:"merge_request_id"`
	Reviewer       ReviewActor `json:"reviewer"`
	RequestedBy    ReviewActor `json:"requested_by"`
	CreatedAt      time.Time   `json:"created_at"`
}

// MergeRequestTimelineItem is one entry of the merge request activity timeline.
type MergeRequestTimelineItem struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	Actor      ReviewActor  `json:"actor"`
	Subject    *ReviewActor `json:"subject,omitempty"`
	Body       string       `json:"body,omitempty"`
	CommitID   string       `json:"commit_id,omitempty"`
	CommitHash string       `json:"commit_hash,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
}

// MergeRequestDiffFile is one changed file of a merge request diff.
type MergeRequestDiffFile struct {
	Path      string   `json:"path"`
	OldPath   string   `json:"old_path,omitempty"`
	Status    string   `json:"status"`
	Binary    bool     `json:"binary,omitempty"`
	Additions int64    `json:"additions"`
	Deletions int64    `json:"deletions"`
	Patch     []string `json:"patch,omitempty"`
}
