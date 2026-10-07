package output

import (
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type MergeRequests struct {
	MergeRequests []MergeRequest `json:"merge_requests"`
	// NextCursor is set when more results are available; pass it as --after.
	NextCursor string `json:"next_cursor,omitempty"`
}

type MergeRequest struct {
	ID                string                                `json:"id"`
	Number            int64                                 `json:"number"`
	SourceBranch      string                                `json:"source_branch"`
	TargetBranch      string                                `json:"target_branch"`
	Title             string                                `json:"title"`
	Description       string                                `json:"description,omitempty"`
	Status            string                                `json:"status"`
	Draft             bool                                  `json:"draft,omitempty"`
	MergeCommitID     string                                `json:"merge_commit_id,omitempty"`
	MergeBaseCommitID string                                `json:"merge_base_commit_id,omitempty"`
	CreatedBy         string                                `json:"created_by,omitempty"`
	CreatedAt         string                                `json:"created_at,omitempty"`
	UpdatedAt         string                                `json:"updated_at,omitempty"`
	Review            *clientDomain.MergeRequestReviewState `json:"review,omitempty"`
}

func NewMergeRequest(mr *clientDomain.MergeRequest) MergeRequest {
	if mr == nil {
		return MergeRequest{}
	}
	return MergeRequest{
		ID:                mr.ID,
		Number:            mr.Number,
		SourceBranch:      mr.SourceBranch,
		TargetBranch:      mr.TargetBranch,
		Title:             mr.Title,
		Description:       mr.Description,
		Status:            mr.Status,
		Draft:             mr.Draft,
		MergeCommitID:     mr.MergeCommitID,
		MergeBaseCommitID: mr.MergeBaseCommitID,
		CreatedBy:         mr.CreatedBy,
		CreatedAt:         formatTime(mr.CreatedAt),
		UpdatedAt:         formatTime(mr.UpdatedAt),
		Review:            mr.Review,
	}
}

func NewMergeRequests(mrs []*clientDomain.MergeRequest) MergeRequests {
	out := MergeRequests{MergeRequests: make([]MergeRequest, 0, len(mrs))}
	for _, mr := range mrs {
		if mr == nil {
			continue
		}
		out.MergeRequests = append(out.MergeRequests, NewMergeRequest(mr))
	}
	return out
}

// MergeRequestView is one merge request with its live mergeability, the
// reviews given for the current source head and the commits it adds.
type MergeRequestView struct {
	MergeRequest MergeRequest                       `json:"merge_request"`
	Mergeability *clientDomain.Mergeability         `json:"mergeability,omitempty"`
	Reviews      []*clientDomain.MergeRequestReview `json:"reviews,omitempty"`
	Commits      []*serverDomain.CommitLogEntry     `json:"commits,omitempty"`
}

func NewMergeRequestView(mr *clientDomain.MergeRequest, info *clientDomain.Mergeability,
	reviews []*clientDomain.MergeRequestReview, commits []*serverDomain.CommitLogEntry,
) MergeRequestView {
	return MergeRequestView{
		MergeRequest: NewMergeRequest(mr),
		Mergeability: info,
		Reviews:      reviews,
		Commits:      commits,
	}
}

type MergeRequestThreads struct {
	Threads []*clientDomain.MergeRequestThread `json:"threads"`
}

type MergeRequestTimeline struct {
	Timeline []*clientDomain.MergeRequestTimelineItem `json:"timeline"`
}

type MergeRequestReviewRequests struct {
	ReviewRequests []*clientDomain.MergeRequestReviewRequest `json:"review_requests"`
}

type MergeRequestDiff struct {
	Files []*clientDomain.MergeRequestDiffFile `json:"files"`
}
