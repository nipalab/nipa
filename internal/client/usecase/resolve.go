package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/nipalab/nipa/internal/client/domain"
)

const maxMarkerScanBytes = 64 << 20

var conflictMarkers = [][]byte{[]byte("<<<<<<< ours\n"), []byte(">>>>>>> theirs\n")}

type ConflictRepo interface {
	LoadConfig() (*domain.Config, error)
	Snapshot() (*domain.Snapshot, error)
	LoadMergeState() (*domain.MergeState, error)
	SaveMergeState(state *domain.MergeState) error
	LoadRevertState() (*domain.RevertState, error)
	SaveRevertState(state *domain.RevertState) error
}

// Resolution says how one conflicted file ends up. Delete removes it,
// Content replaces it, and neither keeps the working file as it is. Text kept
// as is or written from Content must not carry conflict markers unless
// AllowMarkers is set (a version taken verbatim from a commit).
type Resolution struct {
	Delete       bool
	Content      io.Reader
	AllowMarkers bool
	Mode         int
}

type Conflicts struct {
	localRepo ConflictRepo
	wc        *WorkingCopy
	root      string
}

func NewConflicts(localRepo ConflictRepo, wc *WorkingCopy, root string) *Conflicts {
	return &Conflicts{localRepo: localRepo, wc: wc, root: root}
}

// List describes the pending merge or revert and its conflicted files; it
// returns nil when nothing is pending.
func (c *Conflicts) List() (*domain.ConflictSet, error) {
	cfg, err := c.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	merge, err := c.localRepo.LoadMergeState()
	if err != nil {
		return nil, err
	}
	if merge != nil {
		set := &domain.ConflictSet{
			Op:     "merge",
			Base:   domain.ConflictRevision{ID: merge.BaseCommitID, Label: "merge base"},
			Ours:   domain.ConflictRevision{ID: merge.TargetCommitID, Label: cfg.Branch},
			Theirs: domain.ConflictRevision{ID: merge.SourceCommitID, Label: merge.SourceBranch},
		}
		set.Files = conflictFiles(merge.Conflicts, merge.ConflictKinds, merge.ConflictBinary, merge.Resolved)
		return set, nil
	}
	revert, err := c.localRepo.LoadRevertState()
	if err != nil {
		return nil, err
	}
	if revert == nil {
		return c.shelfConflicts(cfg)
	}
	set := &domain.ConflictSet{
		Op:     "revert",
		Ours:   domain.ConflictRevision{ID: revert.CurrentCommitID, Label: cfg.Branch},
		Theirs: domain.ConflictRevision{ID: revert.TheirsCommitID, Label: "reverted"},
	}
	if len(revert.Targets) > 0 {
		target := revert.Targets[0]
		set.Base = domain.ConflictRevision{ID: target.ID, Label: revertLabel(target)}
		set.Theirs.Label = "before " + revertLabel(target)
	}
	set.Files = conflictFiles(revert.Conflicts, revert.ConflictKinds, revert.ConflictBinary, revert.Resolved)
	return set, nil
}

type shelfStateRepo interface {
	LoadShelfState() (*domain.ShelfState, error)
	SaveShelfState(state *domain.ShelfState) error
	ClearShelfState() error
	DropShelf(id string) error
}

// shelfConflicts describes a waiting shelf apply: ours is the synced
// snapshot, theirs the shelf (its id) and base the version it was shelved
// from.
func (c *Conflicts) shelfConflicts(cfg *domain.Config) (*domain.ConflictSet, error) {
	repo, ok := c.localRepo.(shelfStateRepo)
	if !ok {
		return nil, nil
	}
	state, err := repo.LoadShelfState()
	if err != nil || state == nil {
		return nil, err
	}
	set := &domain.ConflictSet{
		Op:     "shelf",
		Base:   domain.ConflictRevision{ID: state.BaseCommitID, Label: "shelved from"},
		Ours:   domain.ConflictRevision{ID: state.OursCommitID, Label: cfg.Branch},
		Theirs: domain.ConflictRevision{ID: state.ShelfID, Label: "shelf " + state.ShelfName},
	}
	set.Files = conflictFiles(state.Conflicts, state.ConflictKinds, state.ConflictBinary, state.Resolved)
	return set, nil
}

func revertLabel(ref domain.CommitRef) string {
	if s := firstLine(ref.Subject); s != "" {
		return s
	}
	return ref.ID
}

func conflictFiles(paths []string, kinds map[string]string, binary, resolved []string) []domain.ConflictFile {
	out := make([]domain.ConflictFile, 0, len(paths))
	for _, p := range paths {
		kind := kinds[p]
		if kind == "" {
			kind = domain.ConflictText
		}
		out = append(out, domain.ConflictFile{Path: p, Kind: kind, Binary: slices.Contains(binary, p), Resolved: slices.Contains(resolved, p)})
	}
	return out
}

// Resolve settles one conflicted file: it writes or removes the working file,
// stages the outcome and records the path as resolved.
func (c *Conflicts) Resolve(ctx context.Context, path string, res Resolution) error {
	p, err := cleanBlobPath(path)
	if err != nil {
		return err
	}
	set, err := c.List()
	if err != nil {
		return err
	}
	if set == nil {
		return domain.NewUserError("no merge or revert is waiting")
	}
	idx := slices.IndexFunc(set.Files, func(f domain.ConflictFile) bool { return f.Path == p })
	if idx < 0 {
		return domain.NewUserError(fmt.Sprintf("%s has no conflict to resolve", p))
	}
	file := set.Files[idx]
	abs := filepath.Join(c.root, filepath.FromSlash(p))

	switch {
	case res.Delete:
		if err := checkRemoveInsideRoot(c.root, p); err != nil {
			return err
		}
		wt := acquireWorkTree(c.root)
		err := wt.remove(filepath.FromSlash(p))
		wt.release()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	case res.Content != nil:
		if err := checkWriteInsideRoot(c.root, p); err != nil {
			return err
		}
		if err := c.write(res.Content, !file.Binary && !res.AllowMarkers, p, res.Mode); err != nil {
			return err
		}
	case !file.Binary:
		if err := checkWorkingMarkers(abs, p); err != nil {
			return err
		}
	}
	if set.Op != "shelf" {
		if err := c.stage(ctx, p, abs); err != nil {
			return err
		}
	}
	return c.markResolved(p)
}

// write replaces p's working file with content through a temp file in its
// folder, inside the work tree.
func (c *Conflicts) write(content io.Reader, checkMarkers bool, p string, mode int) error {
	if checkMarkers {
		data, err := io.ReadAll(content)
		if err != nil {
			return err
		}
		if hasMarkers(data) {
			return markerError(p)
		}
		content = bytes.NewReader(data)
	}
	wt := acquireWorkTree(c.root)
	defer wt.release()
	name := filepath.FromSlash(p)
	dir, done, err := wt.dirOf("write", name, true)
	if err != nil {
		return err
	}
	defer done()
	leaf := filepath.Base(name)
	perm := os.FileMode(0o644)
	if mode != 0 {
		perm = materializedMode(mode)
	} else if info, err := dir.Lstat(leaf); err == nil && info.Mode().IsRegular() {
		perm = info.Mode().Perm()
	}
	tmp, tmpName, err := createTempIn(dir, ".nipa-resolve-")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmpName) }()
	_, err = io.Copy(tmp, content)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceIn(dir, tmpName, leaf)
}

func checkWorkingMarkers(abs, p string) error {
	info, err := os.Stat(abs)
	if err != nil || info.Size() > maxMarkerScanBytes {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if hasMarkers(data) {
		return markerError(p)
	}
	return nil
}

func hasMarkers(data []byte) bool {
	for _, m := range conflictMarkers {
		if bytes.HasPrefix(data, m) || bytes.Contains(data, append([]byte{'\n'}, m...)) {
			return true
		}
	}
	return false
}

func markerError(p string) error {
	return domain.NewUserError(fmt.Sprintf("%s still has conflict markers; pick a side for every conflict first", p))
}

func (c *Conflicts) stage(ctx context.Context, p, abs string) error {
	if _, err := os.Lstat(abs); err == nil {
		return c.wc.Add(ctx, []string{p}, AddOptions{Force: true})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	snapshot, err := c.localRepo.Snapshot()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(snapshot.Files, func(f domain.SnapshotFile) bool { return f.Path == p }) {
		return c.wc.Add(ctx, []string{p}, AddOptions{Force: true})
	}
	return c.wc.Remove(ctx, []string{p})
}

func (c *Conflicts) markResolved(p string) error {
	merge, err := c.localRepo.LoadMergeState()
	if err != nil {
		return err
	}
	if merge != nil {
		if !slices.Contains(merge.Resolved, p) {
			merge.Resolved = append(merge.Resolved, p)
		}
		return c.localRepo.SaveMergeState(merge)
	}
	revert, err := c.localRepo.LoadRevertState()
	if err != nil {
		return err
	}
	if revert == nil {
		return c.markShelfResolved(p)
	}
	if !slices.Contains(revert.Resolved, p) {
		revert.Resolved = append(revert.Resolved, p)
	}
	return c.localRepo.SaveRevertState(revert)
}

// markShelfResolved records p; once every conflict is settled the apply is
// finished and the shelf dropped unless it was applied with keep.
func (c *Conflicts) markShelfResolved(p string) error {
	repo, ok := c.localRepo.(shelfStateRepo)
	if !ok {
		return nil
	}
	state, err := repo.LoadShelfState()
	if err != nil || state == nil {
		return err
	}
	if !slices.Contains(state.Resolved, p) {
		state.Resolved = append(state.Resolved, p)
	}
	for _, conflict := range state.Conflicts {
		if !slices.Contains(state.Resolved, conflict) {
			return repo.SaveShelfState(state)
		}
	}
	if err := repo.ClearShelfState(); err != nil {
		return err
	}
	if state.Keep {
		return nil
	}
	return dropFinishedShelf(repo, state.ShelfID)
}

func dropFinishedShelf(repo interface{ DropShelf(id string) error }, id string) error {
	if err := repo.DropShelf(id); err != nil && !isNotFoundError(err) {
		return err
	}
	return nil
}

// ConflictSides names where each version of a conflicted file is read from:
// commits for a merge or revert; for a shelf apply the shelf's base, the
// synced snapshot and the shelf. A nil side has no version.
func ConflictSides(set *domain.ConflictSet, p string) (base, ours, theirs *BlobRequest) {
	if set.Op == "shelf" {
		return &BlobRequest{Source: BlobShelfBase, Revision: set.Theirs.ID, Path: p},
			&BlobRequest{Source: BlobSnapshot, Path: p},
			&BlobRequest{Source: BlobShelf, Revision: set.Theirs.ID, Path: p}
	}
	rev := func(id string) *BlobRequest {
		if id == "" {
			return nil
		}
		return &BlobRequest{Source: BlobRevision, Revision: id, Path: p}
	}
	return rev(set.Base.ID), rev(set.Ours.ID), rev(set.Theirs.ID)
}
