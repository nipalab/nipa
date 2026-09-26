package domain

import (
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// FileLock is an exclusive claim on a binary path or path prefix. BranchID is
// nil for mainline (project-global) locks and set for development-branch locks.
type FileLock struct {
	ID                 snow.ID   `json:"id"`
	ProjectID          snow.ID   `json:"project_id"`
	BranchID           *snow.ID  `json:"branch_id,omitempty"`
	Branch             string    `json:"branch,omitempty"`
	Path               string    `json:"path"`
	HeldBy             snow.ID   `json:"held_by"`
	HeldByName         string    `json:"held_by_name,omitempty"`
	MergeRequestID     *snow.ID  `json:"merge_request_id,omitempty"`
	MergeRequestNumber *int64    `json:"merge_request_number,omitempty"`
	AcquiredAt         time.Time `json:"acquired_at"`
}
