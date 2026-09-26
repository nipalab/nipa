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

type DiffLineResponse struct {
	Kind string `json:"kind"`
	Old  int    `json:"old_line,omitempty"`
	New  int    `json:"new_line,omitempty"`
	Text string `json:"text"`
	// NoNewline marks the last line of a file that has no trailing newline.
	NoNewline bool `json:"no_newline,omitempty"`
}

type DiffHunkResponse struct {
	OldStart int                `json:"old_start"`
	OldLines int                `json:"old_lines"`
	NewStart int                `json:"new_start"`
	NewLines int                `json:"new_lines"`
	Lines    []DiffLineResponse `json:"lines"`
}

type DiffFileResponse struct {
	Path      string             `json:"path"`
	OldPath   string             `json:"old_path,omitempty"`
	Status    string             `json:"status"`
	Binary    bool               `json:"binary"`
	Additions int                `json:"additions"`
	Deletions int                `json:"deletions"`
	Patch     []string           `json:"patch,omitempty"`
	Hunks     []DiffHunkResponse `json:"hunks,omitempty"`
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
