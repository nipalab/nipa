package model

import "time"

type FileLockResponse struct {
	ID                 string                    `json:"id"`
	Path               string                    `json:"path"`
	Branch             string                    `json:"branch,omitempty"`
	Global             bool                      `json:"global"`
	HeldBy             string                    `json:"held_by"`
	HeldByName         string                    `json:"held_by_name"`
	MergeRequestNumber *int64                    `json:"merge_request_number,omitempty"`
	AcquiredAt         time.Time                 `json:"acquired_at"`
	ExpiresAt          *time.Time                `json:"expires_at,omitempty"`
	Requests           []FileLockRequestResponse `json:"requests"`
}

type FileLockRequestResponse struct {
	ID            string    `json:"id"`
	RequesterID   string    `json:"requester_id"`
	RequesterName string    `json:"requester_name"`
	Message       string    `json:"message"`
	RequestedAt   time.Time `json:"requested_at"`
}

type RequestLockReleaseRequest struct {
	Message string `json:"message"`
}

type LockFileRequest struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	TTLSeconds *int64 `json:"ttl_seconds,omitempty"`
}
