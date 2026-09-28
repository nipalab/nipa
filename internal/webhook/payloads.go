package webhook

import "time"

// Push file operation values.
const (
	PushFileAdded    = "added"
	PushFileModified = "modified"
	PushFileDeleted  = "deleted"
)

// PushPayload is the body of a push event.
type PushPayload struct {
	Envelope
	Changes []PushChange `json:"changes"`
}

type PushChange struct {
	Branch         string     `json:"branch"`
	BranchID       string     `json:"branch_id"`
	Created        bool       `json:"created"`
	Deleted        bool       `json:"deleted"`
	Forced         bool       `json:"forced"`
	Before         string     `json:"before"`
	After          string     `json:"after"`
	Message        string     `json:"message,omitempty"`
	Files          []PushFile `json:"files"`
	FilesTruncated bool       `json:"files_truncated,omitempty"`
}

type PushFile struct {
	Path      string `json:"path"`
	Operation string `json:"op"`
	Binary    bool   `json:"binary"`
	SizeBytes int64  `json:"size_bytes"`
}

// MergeRequestPayload is the body of an mr.* event.
type MergeRequestPayload struct {
	Envelope
	MergeRequest MergeRequestInfo `json:"merge_request"`
}

type MergeRequestInfo struct {
	ID            string    `json:"id"`
	Number        int64     `json:"number"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	State         string    `json:"state"`
	SourceBranch  string    `json:"source_branch"`
	TargetBranch  string    `json:"target_branch"`
	AuthorID      string    `json:"author_id"`
	MergeCommitID string    `json:"merge_commit_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// BranchPayload is the body of a branch.* event.
type BranchPayload struct {
	Envelope
	Branch BranchInfo `json:"branch"`
}

type BranchInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CommitID string `json:"commit_id,omitempty"`
	Default  bool   `json:"default"`
}
