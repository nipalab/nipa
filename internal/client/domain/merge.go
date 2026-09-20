package domain

import "github.com/nipalab/nipa/internal/domain"

// MergeBaseInfo describes the relationship between two branch heads, mirroring
// the server's GetMergeBase response. Commit IDs are base36 snow IDs and commit
// hashes are hex strings; either is empty when the respective branch has no
// commits. MergeBaseTree is the recursive manifest of the common ancestor tree.
type MergeBaseInfo struct {
	TargetBranch      string
	SourceBranch      string
	TargetCommitID    string
	SourceCommitID    string
	TargetCommitHash  string
	SourceCommitHash  string
	MergeBaseCommitID string
	MergeBaseTree     *domain.TreeNode
}

// MergeRef identifies one side of a merge-base lookup. A commit ID (base36)
// takes precedence over a branch name.
type MergeRef struct {
	Branch   string
	CommitID string
}

// MergeState is the persisted pending-merge metadata in localrepo meta. It is
// written when a merge stops on conflicts and consumed by the follow-up push so
// the merge commit records the source head as its second parent.
type MergeState struct {
	SourceBranch     string `json:"source_branch"`
	SourceCommitID   string `json:"source_commit_id"`
	SourceCommitHash string `json:"source_commit_hash"`
	BaseCommitID     string `json:"base_commit_id"`
	BaseTreeHash     string `json:"base_tree_hash"`
	// TargetTreeHash is the tree hash of the target branch head the merge is
	// based on. The push that completes the merge commits on top of it, so it
	// is sent as the push's base tree hash instead of the conflicted marker
	// tree recorded in the local snapshot.
	TargetTreeHash string `json:"target_tree_hash,omitempty"`
	// Conflicts is the list of paths that were left with merge markers for the
	// user to resolve before the merge commit is pushed.
	Conflicts []string `json:"conflicts,omitempty"`
}
