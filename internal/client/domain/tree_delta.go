package domain

import (
	"errors"
	"fmt"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treedelta"
)

// TreeDeltaApply describes a delta manifest the server answered with, for
// applying it to the stored tree instead of saving the target's manifest.
type TreeDeltaApply struct {
	// Delta is the answer as the transport decoded it.
	Delta *treedelta.Delta
	// BaseCommitID is the commit the delta was asked against, CommitID the
	// commit it leads to.
	BaseCommitID string
	CommitID     string
	// Sparse is the sparse set both manifests belong to.
	Sparse []string
	// View is the view token the answer carried; it is stored with the result.
	View []byte
}

// TreeDeltaFile is a file a delta adds or modifies.
type TreeDeltaFile struct {
	// Path is the working-copy relative path.
	Path string
	// File is the file as the target manifest carries it.
	File *serverDomain.File
	// Base is the row the stored tree holds for the path, nil when the file
	// is new.
	Base *SnapshotFile
}

// TreeDeltaChanges is what a delta does to the stored tree: the only paths a
// caller has to bring up to date in the working copy.
type TreeDeltaChanges struct {
	// Written are the added and modified files, Removed the rows that leave
	// the tree, both sorted by path.
	Written []TreeDeltaFile
	Removed []SnapshotFile
	// BaseTreeHash is the stored root tree hash the changes were worked out
	// against (the tree_hash record), TreeHash the root hash they lead to.
	BaseTreeHash string
	TreeHash     serverDomain.Hash
}

// ErrTreeDeltaBase means the stored tree is not known to be the manifest the
// delta was computed against: the synced-tree record names another commit or
// sparse set, the tree changed since the delta was planned, or the delta
// keeps a file the stored tree does not hold.
var ErrTreeDeltaBase = errors.New("stored tree is not the base of the delta")

// TreeDeltaMismatchError means applying the delta to the stored tree gave a
// root hash other than the expected one. Nothing was written.
type TreeDeltaMismatchError struct {
	Expected serverDomain.Hash
	Computed serverDomain.Hash
}

func (e *TreeDeltaMismatchError) Error() string {
	return fmt.Sprintf("local tree hash %s does not match the tree hash %s of the delta", e.Computed, e.Expected)
}
