package model

import "time"

type MergeabilityResponse struct {
	Status            string `json:"status"`
	SourceCommitID    string `json:"source_commit_id,omitempty"`
	TargetCommitID    string `json:"target_commit_id,omitempty"`
	MergeBaseCommitID string `json:"merge_base_commit_id,omitempty"`
}

type MergeRequestResponse struct {
	ID                string                `json:"id"`
	ProjectID         string                `json:"project_id"`
	SourceBranch      string                `json:"source_branch"`
	TargetBranch      string                `json:"target_branch"`
	Title             string                `json:"title"`
	Description       string                `json:"description"`
	Status            string                `json:"status"`
	MergeCommitID     string                `json:"merge_commit_id,omitempty"`
	MergeBaseCommitID string                `json:"merge_base_commit_id,omitempty"`
	CreatedBy         string                `json:"created_by"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Mergeability      *MergeabilityResponse `json:"mergeability,omitempty"`
}

type CreateMergeRequestRequest struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

type UpdateMergeRequestRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type MergeRequestDiffResponse struct {
	BaseID string             `json:"base_id,omitempty"`
	Files  []DiffFileResponse `json:"files"`
}
