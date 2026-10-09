package domain

import (
	"encoding/hex"
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

type Hash [32]byte

func (h Hash) Bytes() []byte {
	return h[:]
}

func (h Hash) String() string {
	return hex.EncodeToString(h.Bytes())
}

func ParseHashHex(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, err
	}
	if len(b) != len(h) {
		return h, NewErrorUser("invalid hash length")
	}
	copy(h[:], b)
	return h, nil
}

type TreeNode struct {
	ID           int64       `json:"id"`
	Hash         Hash        `json:"hash"`
	Name         string      `json:"name"`
	Mode         int         `json:"mode"`
	ParentID     *int64      `json:"parent_id,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	TreeChildren []*TreeNode `json:"tree_children,omitempty"`
	FileChildren []*File     `json:"file_children,omitempty"`
}

type Chunk struct {
	ID        int64     `json:"id"`
	Hash      Hash      `json:"hash"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type File struct {
	ID        int64     `json:"id"`
	Hash      Hash      `json:"hash"`
	Name      string    `json:"name"`
	Mode      int       `json:"mode"`
	TreeID    int64     `json:"tree_id"`
	SizeBytes int64     `json:"size_bytes"`
	IsBinary  bool      `json:"is_binary"`
	Encoding  string    `json:"encoding"`
	CreatedAt time.Time `json:"created_at"`
	Chunks    []Chunk   `json:"chunks"`
}

type FileChunk struct {
	FileID     int64 `json:"file_id"`
	ChunkID    int64 `json:"chunk_id"`
	ChunkIndex int   `json:"chunk_index"`
}

type Commit struct {
	ID        snow.ID   `json:"id"`
	Hash      Hash      `json:"hash"`
	ProjectID snow.ID   `json:"project_id"`
	TreeID    int64     `json:"tree_id"`
	Parent1ID *snow.ID  `json:"parent_1_id,omitempty"`
	Parent2ID *snow.ID  `json:"parent_2_id,omitempty"`
	UserID    snow.ID   `json:"user_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Branch struct {
	ID                snow.ID `json:"id"`
	ProjectID         snow.ID `json:"project_id"`
	Name              string  `json:"name"`
	IsProtected       bool    `json:"is_protected"`
	RequiredApprovals int64   `json:"required_approvals"`
	// DismissStaleApprovals controls whether a push to the source branch
	// dismisses decisions given for the previous head.
	DismissStaleApprovals bool `json:"dismiss_stale_approvals"`
	// RequireStatusChecks gates merging on the required checks passing for the
	// source head.
	RequireStatusChecks bool       `json:"require_status_checks"`
	IsDefault           bool       `json:"is_default"`
	CommitID            *snow.ID   `json:"commit_id"`
	UpdatedAt           time.Time  `json:"updated_at"`
	CreatedAt           time.Time  `json:"created_at"`
	Deleted             bool       `json:"deleted"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
	// RequiredReviewers and RequiredChecks are resolved per request.
	RequiredReviewers []ReviewActor `json:"required_reviewers,omitempty"`
	RequiredChecks    []string      `json:"required_checks,omitempty"`
}

// BranchProtection is the full protection configuration written in one go.
type BranchProtection struct {
	Protected             bool
	RequiredApprovals     int64
	DismissStaleApprovals bool
	RequireStatusChecks   bool
	RequiredReviewers     []snow.ID
	RequiredChecks        []string
}
