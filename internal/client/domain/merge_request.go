package domain

import "time"

// MergeRequest mirrors the server merge-request record. ID is a base36 snow ID;
// Number is the sequential per-project reference used to address the request.
type MergeRequest struct {
	ID                string
	Number            int64
	ProjectID         string
	SourceBranch      string
	TargetBranch      string
	Title             string
	Description       string
	Status            string
	Draft             bool
	MergeCommitID     string
	MergeBaseCommitID string
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// Review is resolved per request, not stored: it counts only the reviews
	// given for the current source head.
	Review *MergeRequestReviewState
}

// MergeRequestReviewState is the live review summary of a merge request.
type MergeRequestReviewState struct {
	HeadCommitID         string   `json:"head_commit_id,omitempty"`
	Approvals            int      `json:"approvals"`
	ChangesRequested     int      `json:"changes_requested"`
	DismissedApprovals   int      `json:"dismissed_approvals"`
	OutstandingReviewers []string `json:"outstanding_reviewers"`
}

// ListMergeRequestOptions filters and paginates a merge request listing.
// After is the cursor returned by the previous page (0 starts from the newest).
type ListMergeRequestOptions struct {
	Status       string
	Author       string
	SourceBranch string
	TargetBranch string
	Draft        *bool
	After        int64
	Limit        int
}

// Mergeability is the live, computed state of an open merge request.
type Mergeability struct {
	Status            string `json:"status"`
	SourceCommitID    string `json:"source_commit_id,omitempty"`
	TargetCommitID    string `json:"target_commit_id,omitempty"`
	MergeBaseCommitID string `json:"merge_base_commit_id,omitempty"`
	// BlockedBy is empty when the request can merge; otherwise it names the
	// review reason the target branch policy refuses the merge for.
	BlockedBy string `json:"blocked_by,omitempty"`
}

const (
	MergeRequestOpen   = "open"
	MergeRequestMerged = "merged"
	MergeRequestClosed = "closed"
)

func IsValidMergeRequestStatus(status string) bool {
	return status == MergeRequestOpen || status == MergeRequestMerged || status == MergeRequestClosed
}
