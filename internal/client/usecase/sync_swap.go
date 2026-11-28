package usecase

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

// swapPlan frees the paths a sync writes from what stands in their way on
// disk: a tracked file where a written file needs a folder (a file that
// became a directory), and a folder where a file is written (a directory
// that became a file, or a folder left empty). Only tracked files the sync
// removes anyway, unchanged, and empty folders are ever deleted here;
// anything else in the way refuses the sync before the working copy is
// touched (planSwaps).
type swapPlan struct {
	// blocking are removals that sit where a written file needs a folder.
	// They go before the writes are prepared (removeBlocking), because a
	// written file's temp file is created in its folder.
	blocking []clientDomain.SnapshotFile
	// inFolders are the removals inside the folders written files replace,
	// folders those folders and every folder below them; clearFolders
	// removes both right before the writes are swapped in.
	inFolders []clientDomain.SnapshotFile
	folders   []string
	// taken are the removals handled here, which removeLeftFiles skips.
	taken     map[string]struct{}
	statCache map[string]clientDomain.StatEntry
	folderSet map[string]struct{}
}

// swapBlocker is content in the way of a swap that the sync will not delete.
type swapBlocker struct {
	path   string
	reason string
}

// planSwaps finds what must be freed for the writes of a sync and refuses it,
// whether forced or not, when that would delete anything but tracked files the
// sync removes and that hold no local change, or empty folders: an untracked
// or ignored file inside a folder that becomes a file, an untracked file
// where a folder is needed, a locally changed tracked file in either place,
// or a link. A forced sync overwrites a locally changed file at a path it
// writes, but it never deletes a changed file the target drops (it keeps it,
// see guardedRemoveCached), and a swap cannot keep it, so a swap is refused
// either way. It costs one Lstat per written file and per distinct folder
// above one, and a walk of each folder that must become a file.
//
// planSwaps serves the paths that write from the synced commit (update/sync
// and restore of the synced snapshot), so a tracked file in the way is
// labelled as a damaged target (markKeptTracked).
func planSwaps(ctx context.Context, root string, lr any, writes []materializedFile, removals []clientDomain.SnapshotFile) (*swapPlan, error) {
	return planSwapsWith(ctx, root, lr, writes, removals, true)
}

// planSwapsWith is planSwaps; markKept tells whether a refusal labels the
// tracked files in the way as kept by a damaged target (markKeptTracked).
// RevisionRestore passes false: it writes a path from another commit than the
// synced one, so a tracked file above it says nothing about either commit.
// The three-way writes (merge, revert, shelve apply; applyThreeWay) pass
// false too: they write a merge result, not a commit, and merge.ThreeWay has
// no file/folder clash, so two sound sides (ours adds the file a, theirs
// a/b) can put a tracked file in the way.
func planSwapsWith(ctx context.Context, root string, lr any, writes []materializedFile, removals []clientDomain.SnapshotFile, markKept bool) (*swapPlan, error) {
	plan := &swapPlan{taken: map[string]struct{}{}}
	if len(writes) == 0 {
		return plan, nil
	}
	removed := make(map[string]clientDomain.SnapshotFile, len(removals))
	for _, f := range removals {
		removed[f.Path] = f
	}
	var blockers []swapBlocker
	take := func(f clientDomain.SnapshotFile, into *[]clientDomain.SnapshotFile) {
		if _, done := plan.taken[f.Path]; done {
			return
		}
		plan.taken[f.Path] = struct{}{}
		*into = append(*into, f)
	}
	// seen holds the folders above written files already looked at.
	seen := map[string]bool{}
	for _, w := range writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The folders above the file, from the top: the first one that is
		// not a real folder is in the way, and nothing exists below it.
		var dirs []string
		for d := parentDir(w.Path); d != ""; d = parentDir(d) {
			dirs = append(dirs, d)
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			d := dirs[i]
			if seen[d] {
				continue
			}
			seen[d] = true
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d)))
			if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
				// Missing, or below a file already found in the way.
				break
			}
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				continue
			}
			if f, ok := removed[d]; ok && info.Mode().IsRegular() {
				take(f, &plan.blocking)
			} else {
				blockers = append(blockers, swapBlocker{d, "untracked or kept file where a folder is needed"})
			}
			break
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(w.Path)))
		if err != nil || !info.IsDir() {
			// A missing file is written, a file or link is replaced as before.
			continue
		}
		found, err := scanSwapFolder(root, w.Path, removed)
		if err != nil {
			return nil, err
		}
		blockers = append(blockers, found.blockers...)
		for _, f := range found.files {
			take(f, &plan.inFolders)
		}
		plan.folders = append(plan.folders, found.folders...)
	}

	// The tracked files to delete must hold no local change.
	check := append(append([]clientDomain.SnapshotFile(nil), plan.blocking...), plan.inFolders...)
	if len(check) > 0 {
		paths := make([]string, 0, len(check))
		for _, f := range check {
			paths = append(paths, f.Path)
		}
		statCache, err := statEntriesFor(lr, paths)
		if err != nil {
			return nil, err
		}
		plan.statCache = statCache
		var mu sync.Mutex
		if err := runPool(ctx, check, func(f clientDomain.SnapshotFile) error {
			if trackedFileChanged(root, f, statCache) {
				mu.Lock()
				blockers = append(blockers, swapBlocker{f.Path, "locally changed"})
				mu.Unlock()
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if len(blockers) > 0 {
		if markKept {
			blockers = markKeptTracked(lr, blockers)
		}
		return nil, swapConflictError(blockers)
	}
	// Folders go deepest first, so each is empty when it is removed.
	sort.Slice(plan.folders, func(i, j int) bool {
		di, dj := strings.Count(plan.folders[i], "/"), strings.Count(plan.folders[j], "/")
		if di != dj {
			return di > dj
		}
		return plan.folders[i] < plan.folders[j]
	})
	return plan, nil
}

type swapFolderScan struct {
	files    []clientDomain.SnapshotFile
	folders  []string
	blockers []swapBlocker
}

// scanSwapFolder lists what is inside the working folder dir, which a written
// file replaces: the tracked files the sync removes, every folder (dir
// included), and whatever else is there as blockers. Links are never
// followed.
func scanSwapFolder(root, dir string, removed map[string]clientDomain.SnapshotFile) (swapFolderScan, error) {
	var out swapFolderScan
	base := filepath.Join(root, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			out.folders = append(out.folders, rel)
		case d.Type().IsRegular():
			if f, ok := removed[rel]; ok {
				out.files = append(out.files, f)
			} else {
				out.blockers = append(out.blockers, swapBlocker{rel, "untracked or kept file in a folder that becomes a file"})
			}
		default:
			out.blockers = append(out.blockers, swapBlocker{rel, "a link or special file, in a folder that becomes a file"})
		}
		return nil
	})
	return out, err
}

// trackedFileChanged reports whether the working file of a tracked path
// holds anything but its synced content, the way guardedRemoveCached decides
// to keep a file: a missing file holds nothing, a folder or link in its
// place and an unreadable file count as changed.
func trackedFileChanged(root string, f clientDomain.SnapshotFile, statCache map[string]clientDomain.StatEntry) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil || !info.Mode().IsRegular() {
		return true
	}
	if entry, ok := statCache[f.Path]; ok && statMatches(entry, info) {
		return entry.Hash != f.Hash
	}
	hash, err := hashWorkingPath(root, f.Path, f.Encoding)
	return err != nil || hash != f.Hash
}

// damagedTargetReason marks a blocker that is a tracked file the sync does
// not remove: the target commit keeps it and also holds a file at, or a
// folder over, the same path. A sound commit cannot; a server without the
// tree check could store one. It holds only where the writes come from the
// commit the snapshot is synced to (update/sync, restore of the synced
// snapshot), never for RevisionRestore, which writes from an older commit,
// nor for the three-way writes (merge, revert, shelve apply), which write a
// merge result that two sound commits can make clash.
const damagedTargetReason = "tracked file the target commit keeps: that commit holds a path as both a file and a folder"

// markKeptTracked tells the blockers that are tracked files apart from
// untracked ones. It runs on the refusal only and reads the blockers' rows;
// planSwaps (update/sync, restore of the synced snapshot) runs it;
// planSwapsWith only when asked, which RevisionRestore and the three-way
// writes do not (see damagedTargetReason).
func markKeptTracked(lr any, blockers []swapBlocker) []swapBlocker {
	reader, ok := lr.(interface {
		SnapshotFiles(paths []string) (map[string]clientDomain.SnapshotFile, error)
	})
	if !ok {
		return blockers
	}
	var paths []string
	for _, b := range blockers {
		if strings.HasPrefix(b.reason, "untracked or kept file") {
			paths = append(paths, b.path)
		}
	}
	if len(paths) == 0 {
		return blockers
	}
	tracked, err := reader.SnapshotFiles(paths)
	if err != nil {
		return blockers
	}
	for i, b := range blockers {
		if _, kept := tracked[b.path]; kept && strings.HasPrefix(b.reason, "untracked or kept file") {
			blockers[i].reason = damagedTargetReason
		}
	}
	return blockers
}

// swapConflictError refuses a sync that would have to delete content to turn
// a file into a folder or a folder into a file.
func swapConflictError(blockers []swapBlocker) error {
	sort.Slice(blockers, func(i, j int) bool { return blockers[i].path < blockers[j].path })
	var b strings.Builder
	fmt.Fprintf(&b, "%d path(s) in the way of a file that becomes a folder or a folder that becomes a file:", len(blockers))
	for i, x := range blockers {
		if i == maxListedLocalChanges {
			fmt.Fprintf(&b, "\n  ... and %d more", len(blockers)-i)
			break
		}
		fmt.Fprintf(&b, "\n  %s (%s)", x.path, x.reason)
	}
	for _, x := range blockers {
		if x.reason == damagedTargetReason {
			b.WriteString("\nthe target commit is damaged: moving the tracked files it keeps does not help, a commit that deletes the file or the files below it repairs the branch")
			break
		}
	}
	// The CLI prints the message only, so it says what to do too.
	b.WriteString("\nnothing was changed; move them out of the way (push or shelve changes to tracked files first) and run again; --force does not delete them")
	return clientDomain.NewUserError(b.String()).
		WithHint("nothing was changed in the working copy; --force does not delete these", "move them out of the way (push or shelve changes to tracked files first), then run the command again")
}

// removeBlocking deletes the tracked files that sit where a written file
// needs a folder. planSwaps found them unchanged; one changed since is kept
// and fails the sync.
func (s *swapPlan) removeBlocking(ctx context.Context, root string) error {
	return s.removeTracked(ctx, root, s.blocking)
}

// clearFolders deletes the folders written files replace: the tracked files
// in them, then the folders, deepest first. A read-only folder is made
// writable first, since it goes anyway.
func (s *swapPlan) clearFolders(ctx context.Context, root string) error {
	if len(s.folders) == 0 {
		return nil
	}
	wt := acquireWorkTree(root)
	defer wt.release()
	for _, dir := range s.folders {
		if err := checkRemoveInsideRoot(root, dir); err != nil {
			return err
		}
		name := filepath.FromSlash(dir)
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if info.IsDir() && info.Mode().Perm()&0o300 != 0o300 {
			if err := wt.chmod(name, info.Mode().Perm()|0o700); err != nil {
				return err
			}
		}
	}
	if err := s.removeTracked(ctx, root, s.inFolders); err != nil {
		return err
	}
	for _, dir := range s.folders {
		if err := wt.remove(filepath.FromSlash(dir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	return nil
}

func (s *swapPlan) removeTracked(ctx context.Context, root string, files []clientDomain.SnapshotFile) error {
	return runPool(ctx, files, func(f clientDomain.SnapshotFile) error {
		removed, err := guardedRemoveCached(root, f, s.statCache)
		if err != nil {
			return fmt.Errorf("remove %s: %w", f.Path, err)
		}
		if !removed {
			return swapConflictError([]swapBlocker{{f.Path, "changed while the sync ran"}})
		}
		return nil
	})
}

// left returns the removals the swap plan did not take.
func (s *swapPlan) left(removals []clientDomain.SnapshotFile) []clientDomain.SnapshotFile {
	if len(s.taken) == 0 && len(s.folders) == 0 {
		return removals
	}
	out := make([]clientDomain.SnapshotFile, 0, len(removals))
	for _, f := range removals {
		if !s.gone(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// gone reports whether the plan took care of removing the tracked path p:
// removed here, or below a folder that becomes a file (such a path is gone
// from disk, since the folder held nothing else).
func (s *swapPlan) gone(p string) bool {
	if _, done := s.taken[p]; done {
		return true
	}
	if len(s.folders) == 0 {
		return false
	}
	if s.folderSet == nil {
		s.folderSet = make(map[string]struct{}, len(s.folders))
		for _, d := range s.folders {
			s.folderSet[d] = struct{}{}
		}
	}
	for d := parentDir(p); d != ""; d = parentDir(d) {
		if _, cleared := s.folderSet[d]; cleared {
			return true
		}
	}
	return false
}

// notDirMessage is the message os.Root gives when it opens a file as a
// folder; that error value is not exported and wraps no errno.
const notDirMessage = "not a directory"

// isNotDir reports an error that says a folder on the way to a path is a
// file, which means the path itself cannot exist.
func isNotDir(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return true
	}
	var pe *fs.PathError
	return errors.As(err, &pe) && pe.Err != nil && pe.Err.Error() == notDirMessage
}

// pruneEmptiedFolders removes the folders that removing paths left empty,
// deepest first and up to (never including) the root. A folder that still
// holds anything, or is a link, stays.
func pruneEmptiedFolders(root string, paths []string) {
	seen := map[string]struct{}{}
	var dirs []string
	for _, p := range paths {
		for d := parentDir(p); d != ""; d = parentDir(d) {
			if _, ok := seen[d]; ok {
				break
			}
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		return
	}
	sort.Slice(dirs, func(i, j int) bool {
		di, dj := strings.Count(dirs[i], "/"), strings.Count(dirs[j], "/")
		if di != dj {
			return di > dj
		}
		return dirs[i] < dirs[j]
	})
	wt := acquireWorkTree(root)
	defer wt.release()
	for _, d := range dirs {
		if checkRemoveInsideRoot(root, d) != nil {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil || !info.IsDir() {
			continue
		}
		// Remove fails on a folder that is not empty; that is the check.
		_ = wt.remove(filepath.FromSlash(d))
	}
}

// isMissing reports an error that means a working path does not exist:
// nothing there, or a file where a folder on the way to it should be.
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || isNotDir(err)
}

// statTracked stats the working file of a tracked path. A folder there (the
// file became a directory) reads as missing, like a file the tracked path
// cannot be below.
func statTracked(root, p string) (os.FileInfo, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	if err == nil && info.IsDir() {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return info, err
}
