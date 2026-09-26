package model

import "time"

// ReviewActorResponse identifies the user behind a review, comment or event.
type ReviewActorResponse struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	PhotoURL string `json:"photo_url,omitempty"`
}

// ReviewStateResponse is the live review summary of a merge request. Only
// reviews given for the current source head count.
type ReviewStateResponse struct {
	HeadCommitID         string   `json:"head_commit_id,omitempty"`
	Approvals            int      `json:"approvals"`
	ChangesRequested     int      `json:"changes_requested"`
	DismissedApprovals   int      `json:"dismissed_approvals"`
	OutstandingReviewers []string `json:"outstanding_reviewers"`
}

// ReviewResponse is one review decision. Stale reports that the source branch
// moved on since the review was given, so the decision no longer counts.
type ReviewResponse struct {
	ID              string               `json:"id"`
	MergeRequestID  int64                `json:"merge_request_id"`
	Reviewer        ReviewActorResponse  `json:"reviewer"`
	State           string               `json:"state"`
	Body            string               `json:"body"`
	HeadCommitID    string               `json:"head_commit_id"`
	Stale           bool                 `json:"stale"`
	DismissedAt     *time.Time           `json:"dismissed_at,omitempty"`
	DismissedBy     *ReviewActorResponse `json:"dismissed_by,omitempty"`
	DismissedReason string               `json:"dismissed_reason,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// SubmitReviewRequest carries a review decision with the comments made while
// submitting it. A comment without a file path is a top-level conversation
// thread; otherwise it must anchor to a line the current diff shows.
type SubmitReviewRequest struct {
	State    string               `json:"state"`
	Body     string               `json:"body"`
	Comments []ThreadCommentInput `json:"comments"`
}

// ThreadCommentInput is one comment of a review submission.
type ThreadCommentInput struct {
	FilePath string `json:"file_path"`
	OldLine  *int   `json:"old_line"`
	NewLine  *int   `json:"new_line"`
	Body     string `json:"body"`
}

// AddCommentRequest starts a thread: top-level when FilePath is empty, otherwise
// anchored to OldLine or NewLine of the current diff.
type AddCommentRequest struct {
	FilePath string `json:"file_path"`
	OldLine  *int   `json:"old_line"`
	NewLine  *int   `json:"new_line"`
	Body     string `json:"body"`
}

// CommentResponse is one message in a thread.
type CommentResponse struct {
	ID        string              `json:"id"`
	ThreadID  string              `json:"thread_id"`
	User      ReviewActorResponse `json:"user"`
	Body      string              `json:"body"`
	System    bool                `json:"system"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// ThreadResponse is a review conversation. Outdated reports that the thread was
// created against an earlier source head.
type ThreadResponse struct {
	ID             string               `json:"id"`
	MergeRequestID int64                `json:"merge_request_id"`
	ReviewID       string               `json:"review_id,omitempty"`
	FilePath       string               `json:"file_path,omitempty"`
	OldLine        *int                 `json:"old_line,omitempty"`
	NewLine        *int                 `json:"new_line,omitempty"`
	Side           string               `json:"side"`
	Outdated       bool                 `json:"outdated"`
	Resolved       bool                 `json:"resolved"`
	ResolvedBy     *ReviewActorResponse `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time           `json:"resolved_at,omitempty"`
	CreatedBy      ReviewActorResponse  `json:"created_by"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	Comments       []CommentResponse    `json:"comments"`
}

// ReviewRequestResponse is a pending request for a user to review.
type ReviewRequestResponse struct {
	ID             string              `json:"id"`
	MergeRequestID int64               `json:"merge_request_id"`
	Reviewer       ReviewActorResponse `json:"reviewer"`
	RequestedBy    ReviewActorResponse `json:"requested_by"`
	CreatedAt      time.Time           `json:"created_at"`
}

// TimelineItemResponse is one activity timeline entry. Subject is the user an
// event is about rather than performed by, such as a requested reviewer.
type TimelineItemResponse struct {
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	Actor      ReviewActorResponse  `json:"actor"`
	Subject    *ReviewActorResponse `json:"subject,omitempty"`
	Body       string               `json:"body,omitempty"`
	CommitID   string               `json:"commit_id,omitempty"`
	CommitHash string               `json:"commit_hash,omitempty"`
	CreatedAt  time.Time            `json:"created_at"`
}

// ResolveThreadRequest resolves or reopens a conversation thread.
type ResolveThreadRequest struct {
	Resolved bool `json:"resolved"`
}

// ReviewRequestUserRequest asks one user to review a merge request.
type ReviewRequestUserRequest struct {
	UserID string `json:"user_id"`
}
