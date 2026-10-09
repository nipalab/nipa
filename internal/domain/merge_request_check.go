package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// Status check states reported for a merge request head commit.
const (
	MergeRequestCheckPending = "pending"
	MergeRequestCheckSuccess = "success"
	MergeRequestCheckFailed  = "failed"
)

func IsValidMergeRequestCheckState(state string) bool {
	switch state {
	case MergeRequestCheckPending, MergeRequestCheckSuccess, MergeRequestCheckFailed:
		return true
	}
	return false
}

// MergeRequestCheck is one status check reported for a merge request's head
// commit. The latest report per (request, head, name) wins.
type MergeRequestCheck struct {
	ID             snow.ID     `json:"id"`
	MergeRequestID int64       `json:"merge_request_id"`
	HeadCommitID   snow.ID     `json:"head_commit_id"`
	Name           string      `json:"name"`
	State          string      `json:"state"`
	DetailsURL     string      `json:"details_url,omitempty"`
	Reporter       ReviewActor `json:"reporter"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}
