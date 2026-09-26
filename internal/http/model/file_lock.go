package model

import "time"

type FileLockResponse struct {
	ID                 string    `json:"id"`
	Path               string    `json:"path"`
	Branch             string    `json:"branch,omitempty"`
	Global             bool      `json:"global"`
	HeldBy             string    `json:"held_by"`
	HeldByName         string    `json:"held_by_name"`
	MergeRequestNumber *int64    `json:"merge_request_number,omitempty"`
	AcquiredAt         time.Time `json:"acquired_at"`
}

type LockFileRequest struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}
