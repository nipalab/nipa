package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

const (
	MergeRequestOpen   = "open"
	MergeRequestMerged = "merged"
	MergeRequestClosed = "closed"
)

func IsValidMergeRequestStatus(status string) bool {
	return status == MergeRequestOpen || status == MergeRequestMerged || status == MergeRequestClosed
}

// MergeRequestListOptions filters and paginates a project's merge requests.
// Results are ordered by number descending; After is the number of the last
// item of the previous page (0 starts from the newest).
type MergeRequestListOptions struct {
	Status       string
	Author       *snow.ID
	SourceBranch string
	TargetBranch string
	Draft        *bool
	After        int64
	Limit        int
}

// Mergeability is the live, computed state of an open merge request.
type Mergeability struct {
	Status            string   `json:"status"`
	MergeBaseCommitID *snow.ID `json:"merge_base_commit_id,omitempty"`
	SourceCommitID    *snow.ID `json:"source_commit_id,omitempty"`
	TargetCommitID    *snow.ID `json:"target_commit_id,omitempty"`
	// BlockedBy is empty when the request can merge; otherwise it names the
	// policy reason the target branch refuses the merge for.
	BlockedBy string `json:"blocked_by,omitempty"`
}

type MergeRequest struct {
	ID                int64     `json:"id"`
	Number            int64     `json:"number"`
	ProjectID         snow.ID   `json:"project_id"`
	SourceBranchID    snow.ID   `json:"source_branch_id"`
	TargetBranchID    snow.ID   `json:"target_branch_id"`
	SourceBranch      string    `json:"source_branch"`
	TargetBranch      string    `json:"target_branch"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Status            string    `json:"status"`
	Draft             bool      `json:"draft"`
	MergeCommitID     *snow.ID  `json:"merge_commit_id,omitempty"`
	MergeBaseCommitID *snow.ID  `json:"merge_base_commit_id,omitempty"`
	CreatedBy         snow.ID   `json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	// Review is resolved per request, not stored: it counts only the reviews
	// given for the current source head.
	Review *MergeRequestReviewState `json:"review,omitempty"`
}

const (
	MergeabilityMergeable = "mergeable"
	MergeabilityBehind    = "behind_target"
	MergeabilityUpToDate  = "up_to_date"
	MergeabilityInvalid   = "invalid"

	// Policy block reasons carried by Mergeability.BlockedBy.
	MergeabilityBlockedChangesRequested = "changes_requested"
	MergeabilityBlockedApprovals        = "insufficient_approvals"
	MergeabilityBlockedDraft            = "draft"
)
