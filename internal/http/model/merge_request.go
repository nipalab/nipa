package model

import "time"

type MergeabilityResponse struct {
	Status            string `json:"status"`
	SourceCommitID    string `json:"source_commit_id,omitempty"`
	TargetCommitID    string `json:"target_commit_id,omitempty"`
	MergeBaseCommitID string `json:"merge_base_commit_id,omitempty"`
	BlockedBy         string `json:"blocked_by,omitempty"`
}

type MergeRequestResponse struct {
	ID                string                `json:"id"`
	Number            int64                 `json:"number"`
	ProjectID         string                `json:"project_id"`
	SourceBranch      string                `json:"source_branch"`
	TargetBranch      string                `json:"target_branch"`
	Title             string                `json:"title"`
	Description       string                `json:"description"`
	Status            string                `json:"status"`
	Draft             bool                  `json:"draft"`
	MergeCommitID     string                `json:"merge_commit_id,omitempty"`
	MergeBaseCommitID string                `json:"merge_base_commit_id,omitempty"`
	CreatedBy         string                `json:"created_by"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Mergeability      *MergeabilityResponse `json:"mergeability,omitempty"`
	Review            *ReviewStateResponse  `json:"review,omitempty"`
}

type MergeRequestListResponse struct {
	MergeRequests []MergeRequestResponse `json:"merge_requests"`
	// NextCursor is set when the page was full; pass it as the after query
	// parameter to fetch the next page.
	NextCursor string `json:"next_cursor,omitempty"`
}

type CreateMergeRequestRequest struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Draft        bool   `json:"draft"`
}

type UpdateMergeRequestRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Draft is optional: absent leaves the draft state untouched.
	Draft *bool `json:"draft,omitempty"`
}

type MergeMergeRequestRequest struct {
	// Strategy is ff (default), merge, squash or rebase.
	Strategy     string `json:"strategy"`
	DeleteSource bool   `json:"delete_source"`
}

type MergeRequestDiffResponse struct {
	BaseID string             `json:"base_id,omitempty"`
	Files  []DiffFileResponse `json:"files"`
}
