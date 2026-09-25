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
	MergeCommitID     string
	MergeBaseCommitID string
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Mergeability is the live, computed state of an open merge request.
type Mergeability struct {
	Status            string
	SourceCommitID    string
	TargetCommitID    string
	MergeBaseCommitID string
}

const (
	MergeRequestOpen   = "open"
	MergeRequestMerged = "merged"
	MergeRequestClosed = "closed"
)

func IsValidMergeRequestStatus(status string) bool {
	return status == MergeRequestOpen || status == MergeRequestMerged || status == MergeRequestClosed
}
