package domain

import (
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type LocalCommit struct {
	CommitID   string `json:"commit_id,omitempty"`
	CommitHash string `json:"commit_hash,omitempty"`
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
}

type CommitWalkEntry struct {
	ID        string
	Hash      string
	Parent1ID string
	Parent2ID string
	Message   string
	CreatedAt time.Time
}

type CommitRef struct {
	ID      string `json:"id"`
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}
