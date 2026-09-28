package output

import (
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

type MergeRequests struct {
	MergeRequests []MergeRequest `json:"merge_requests"`
}

type MergeRequest struct {
	ID                string `json:"id"`
	Number            int64  `json:"number"`
	SourceBranch      string `json:"source_branch"`
	TargetBranch      string `json:"target_branch"`
	Title             string `json:"title"`
	Description       string `json:"description,omitempty"`
	Status            string `json:"status"`
	MergeCommitID     string `json:"merge_commit_id,omitempty"`
	MergeBaseCommitID string `json:"merge_base_commit_id,omitempty"`
	CreatedBy         string `json:"created_by,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

func NewMergeRequests(mrs []*clientDomain.MergeRequest) MergeRequests {
	out := MergeRequests{MergeRequests: make([]MergeRequest, 0, len(mrs))}
	for _, mr := range mrs {
		if mr == nil {
			continue
		}
		out.MergeRequests = append(out.MergeRequests, MergeRequest{
			ID:                mr.ID,
			Number:            mr.Number,
			SourceBranch:      mr.SourceBranch,
			TargetBranch:      mr.TargetBranch,
			Title:             mr.Title,
			Description:       mr.Description,
			Status:            mr.Status,
			MergeCommitID:     mr.MergeCommitID,
			MergeBaseCommitID: mr.MergeBaseCommitID,
			CreatedBy:         mr.CreatedBy,
			CreatedAt:         formatTime(mr.CreatedAt),
			UpdatedAt:         formatTime(mr.UpdatedAt),
		})
	}
	return out
}
