package domain

import "time"

// MergeRequestCheck is one status check reported for a merge request head.
type MergeRequestCheck struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	State      string      `json:"state"`
	DetailsURL string      `json:"details_url,omitempty"`
	Reporter   ReviewActor `json:"reporter"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Status check states.
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
