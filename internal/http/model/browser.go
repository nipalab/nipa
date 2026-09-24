package model

import "time"

type TreeEntryResponse struct {
	Name       string          `json:"name"`
	Path       string          `json:"path"`
	Type       string          `json:"type"`
	Mode       int             `json:"mode,omitempty"`
	SizeBytes  int64           `json:"size_bytes,omitempty"`
	IsBinary   bool            `json:"is_binary,omitempty"`
	Hash       string          `json:"hash,omitempty"`
	LastCommit *CommitResponse `json:"last_commit,omitempty"`
}

type TreeResponse struct {
	Path         string              `json:"path"`
	Entries      []TreeEntryResponse `json:"entries"`
	LatestCommit *CommitResponse     `json:"latest_commit,omitempty"`
}

type BranchResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	IsDefault   bool      `json:"is_default"`
	IsProtected bool      `json:"is_protected"`
	CommitID    string    `json:"commit_id,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CommitResponse struct {
	ID          string    `json:"id"`
	Parent1ID   string    `json:"parent_1_id,omitempty"`
	Parent2ID   string    `json:"parent_2_id,omitempty"`
	Message     string    `json:"message"`
	AuthorName  string    `json:"author_name,omitempty"`
	AuthorEmail string    `json:"author_email,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type DiffFileResponse struct {
	Path      string   `json:"path"`
	OldPath   string   `json:"old_path,omitempty"`
	Status    string   `json:"status"`
	Binary    bool     `json:"binary"`
	Additions int      `json:"additions"`
	Deletions int      `json:"deletions"`
	Patch     []string `json:"patch,omitempty"`
}

type CommitDiffResponse struct {
	CommitID string             `json:"commit_id"`
	BaseID   string             `json:"base_id,omitempty"`
	Files    []DiffFileResponse `json:"files"`
}

type CreateBranchRequest struct {
	Name string `json:"name"`
	From string `json:"from"`
}

type RenameBranchRequest struct {
	Name string `json:"name"`
}

type SetBranchProtectionRequest struct {
	Protected bool `json:"protected"`
}
