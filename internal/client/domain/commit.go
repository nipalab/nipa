package domain

import (
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type LocalCommit struct {
	CommitID   string `json:"commit_id,omitempty"`
	CommitHash string `json:"commit_hash,omitempty"`
}

// SyncedTree says which commit's tree the local snapshot holds and the sparse
// set (sorted, empty for a full checkout) it was fetched with.
type SyncedTree struct {
	CommitID string
	Sparse   []string
}

type CommitDetail struct {
	ID        string
	Hash      string
	TreeHash  string
	Parent1ID string
	Parent2ID string
	Message   string
	CreatedAt time.Time
	Tree      *serverDomain.TreeNode

	AuthorName  string
	AuthorEmail string
	Origin      string
}

type CommitWalkEntry struct {
	ID        string
	Hash      string
	Parent1ID string
	Parent2ID string
	Message   string
	CreatedAt time.Time

	AuthorName  string
	AuthorEmail string
	Origin      string
}

type CommitRef struct {
	ID      string `json:"id"`
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}
