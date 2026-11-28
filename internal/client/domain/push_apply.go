package domain

import (
	"errors"
	"fmt"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// PushApply describes a push the server accepted, for applying it to the
// stored tree without fetching the new head's manifest.
type PushApply struct {
	// Files and Removed are exactly what the push sent.
	Files   []*serverDomain.PushFile
	Removed []string
	// BaseCommitID is the commit the server applied the push to ("" for the
	// first push to an empty branch); CommitID is the commit it created.
	BaseCommitID string
	CommitID     string
	// Sparse is the checkout's sparse set. Only a full checkout can be applied
	// locally, so anything but an empty set is refused.
	Sparse []string
	// TreeHash is the new head's root tree hash, from the push response.
	TreeHash serverDomain.Hash
	// View is what the push response says about the pusher's full manifest
	// view; it decides the view token the applied tree is recorded with
	// (localrepo.ApplyPush).
	View serverDomain.PushView
}

// ErrPushApplyBase means the stored tree is not known to be the full tree of
// the commit the push was based on, so the push cannot be applied to it.
var ErrPushApplyBase = errors.New("stored tree is not the base of the push")

// PushApplyMismatchError means applying the push to the stored tree gave a
// root hash other than the server's. Nothing was written.
type PushApplyMismatchError struct {
	Expected serverDomain.Hash
	Computed serverDomain.Hash
}

func (e *PushApplyMismatchError) Error() string {
	return fmt.Sprintf("local tree hash %s does not match the pushed tree hash %s", e.Computed, e.Expected)
}

// PushApplyClashError means applying the push to the stored tree would leave
// a path as a file and as a folder at once. Nothing was written.
type PushApplyClashError struct {
	Path string
}

func (e *PushApplyClashError) Error() string {
	return fmt.Sprintf("the pushed tree holds %q as a file and as a folder", e.Path)
}
