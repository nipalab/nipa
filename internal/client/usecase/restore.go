package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// Restore discards unpushed changes to the given files or directories by
// re-materializing them from the last synced tree. Paths with staged changes
// are refused, since the staged content is not stored separately.
func (w *WorkingCopy) Restore(ctx context.Context, targets []string) (err error) {
	// Only a restore that put content into the object cache has something to
	// prune; the others skip the full-snapshot read a prune costs.
	cached := false
	defer func() {
		if cached {
			pruneAfterSuccess(w.localRepo, err)
		}
	}()
	unlock, err := lockShared(w.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	staged, err := w.listStagedSet()
	if err != nil {
		return err
	}
	tracked, paths, err := w.restoreSelection(targets)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if staged[p] {
			return fmt.Errorf("cannot restore %q with staged changes; push or reset them first", p)
		}
	}

	cached, err = w.restoreSnapshotCached(ctx, paths, tracked)
	return err
}

type snapshotFileGetter interface {
	SnapshotFile(p string) (domain.SnapshotFile, bool, error)
}

type statEntryGetter interface {
	StatEntries(paths []string) (map[string]domain.StatEntry, error)
}

// restoreSelection resolves the targets to tracked files. When every target
// names one tracked file, only those rows are read; a directory (or anything
// else) needs the whole snapshot to expand.
func (w *WorkingCopy) restoreSelection(targets []string) (map[string]domain.SnapshotFile, []string, error) {
	if tracked, ok, err := w.exactTargets(targets); err != nil {
		return nil, nil, err
	} else if !ok {
		snapshot, err := w.localRepo.Snapshot()
		if err != nil {
			return nil, nil, err
		}
		tracked = make(map[string]domain.SnapshotFile, len(snapshot.Files))
		for _, f := range snapshot.Files {
			tracked[f.Path] = f
		}
		paths, err := restoreTargets(targets, tracked)
		return tracked, paths, err
	} else {
		paths := make([]string, 0, len(tracked))
		for p := range tracked {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return tracked, paths, nil
	}
}

// exactTargets looks every target up as a single tracked file. It reports
// false when the repository cannot do that or a target is not one file, so
// the caller falls back to the full snapshot (and its error messages).
func (w *WorkingCopy) exactTargets(targets []string) (map[string]domain.SnapshotFile, bool, error) {
	getter, ok := w.localRepo.(snapshotFileGetter)
	if !ok || len(targets) == 0 {
		return nil, false, nil
	}
	tracked := make(map[string]domain.SnapshotFile, len(targets))
	for _, raw := range targets {
		target, err := normalizeTarget(raw)
		if err != nil || target == "" || isNipaPath(target) {
			return nil, false, nil
		}
		f, found, err := getter.SnapshotFile(target)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, nil
		}
		tracked[f.Path] = f
	}
	return tracked, true, nil
}

// restoreStatCache loads the fingerprints a restore of paths consults: the
// rows of those paths when the repository can read single rows and the
// restore is small, the whole cache otherwise.
func (w *WorkingCopy) restoreStatCache(paths []string) (map[string]domain.StatEntry, error) {
	if getter, ok := w.localRepo.(statEntryGetter); ok && len(paths) <= maxSingleRowStatReads {
		return getter.StatEntries(paths)
	}
	return w.localRepo.LoadStatCache()
}

// maxSingleRowStatReads is where one scan of the stat cache gets cheaper than
// a lookup per restored path.
var maxSingleRowStatReads = 512

// restoreSnapshot re-materializes the given tracked paths from the synced
// snapshot, skipping files that already match it. Raw files the object cache
// does not keep are rebuilt in place from the working file and the server
// (restoreDirect); everything else is materialized from the cache.
func (w *WorkingCopy) restoreSnapshot(ctx context.Context, paths []string, tracked map[string]domain.SnapshotFile) error {
	_, err := w.restoreSnapshotCached(ctx, paths, tracked)
	return err
}

// restoreSnapshotCached is restoreSnapshot that also reports whether content
// was added to the object cache on the way.
func (w *WorkingCopy) restoreSnapshotCached(ctx context.Context, paths []string, tracked map[string]domain.SnapshotFile) (cached bool, err error) {
	err = w.restoreFiles(ctx, paths, tracked, &cached)
	return cached, err
}

func (w *WorkingCopy) restoreFiles(ctx context.Context, paths []string, tracked map[string]domain.SnapshotFile, cached *bool) error {
	defer beginWorkingWrite(w.localRepo, w.root)()
	statCache, err := w.restoreStatCache(paths)
	if err != nil {
		return err
	}
	changed, layouts := w.changedFromSnapshot(paths, tracked, statCache)
	statEntries, cachedFiles, err := w.restoreDirect(ctx, changed, layouts)
	if err != nil {
		return restoreError(err)
	}
	fromCache := w.pristine()
	fromCache.statCache = statCache
	fromCache.stored = cached
	if err := fromCache.ensureSnapshotContent(ctx, cachedFiles); err != nil {
		return err
	}
	var jobs []materializeJob
	for _, f := range cachedFiles {
		jobs = append(jobs, materializeJob{file: snapshotMaterialized(f), prevMode: f.Mode})
	}
	materialized, err := materializeAll(ctx, w.root, jobs, w.localRepo.OpenChunk)
	if err != nil {
		return restoreError(err)
	}
	for path, entry := range materialized {
		statEntries[path] = entry
	}
	if len(statEntries) == 0 {
		return nil
	}
	return w.localRepo.SaveStatEntries(statEntries)
}

// restoreError names the file a failed materialization stopped at.
func restoreError(err error) error {
	var failed *materializeError
	if errors.As(err, &failed) {
		return fmt.Errorf("restore %s: %w", failed.path, failed.err)
	}
	return err
}

// workingLayout is the chunk layout of a working file as it was when hashed.
type workingLayout struct {
	chunks []serverDomain.Chunk
	info   os.FileInfo
}

// changedFromSnapshot returns the files whose working copy differs from the
// snapshot content or mode, so restoring the others would be a no-op. A fresh
// stat fingerprint is trusted like status does; the rest are hashed in
// parallel. Status uses the same mode comparison, so skipping cannot leave a
// file reported modified. The chunk layouts of the changed files that were
// hashed come back too: their unchanged chunks can be reused without reading
// the files again.
func (w *WorkingCopy) changedFromSnapshot(paths []string, tracked map[string]domain.SnapshotFile, statCache map[string]domain.StatEntry) ([]domain.SnapshotFile, map[string]workingLayout) {
	var changed []domain.SnapshotFile
	var hashJobs []statusHashJob
	for _, p := range paths {
		f := tracked[p]
		info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(p)))
		if err != nil || trackedModeFromPerm(info.Mode(), f.Mode) != normalizedMode(f.Mode) {
			changed = append(changed, f)
			continue
		}
		if entry, ok := statCache[p]; ok && entry.Hash == f.Hash && statMatches(entry, info) {
			continue
		}
		hashJobs = append(hashJobs, statusHashJob{path: p, encoding: f.Encoding, info: info})
	}
	layouts := map[string]workingLayout{}
	for i, res := range w.rehashLayout(hashJobs, true) {
		f := tracked[hashJobs[i].path]
		if res.err != nil || res.hash != f.Hash {
			changed = append(changed, f)
			if res.err == nil && res.chunks != nil {
				layouts[f.Path] = workingLayout{chunks: res.chunks, info: hashJobs[i].info}
			}
		}
	}
	return changed, layouts
}

// restoreTargets expands exact paths and directory prefixes to the tracked
// files they cover, matching git's "pathspec" semantics for restore.
func restoreTargets(targets []string, tracked map[string]domain.SnapshotFile) ([]string, error) {
	seen := make(map[string]bool, len(tracked))
	var paths []string
	for _, raw := range targets {
		if raw == "" {
			return nil, fmt.Errorf("a path is required")
		}
		target, err := normalizeTarget(raw)
		if err != nil {
			return nil, err
		}
		if isNipaPath(target) {
			return nil, fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		matched := false
		for p := range tracked {
			if target != "" && p != target && !strings.HasPrefix(p, target+"/") {
				continue
			}
			matched = true
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		if !matched {
			return nil, fmt.Errorf("path %q is not tracked", raw)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func snapshotMaterialized(f domain.SnapshotFile) materializedFile {
	return materializedFile{
		Path:        f.Path,
		Mode:        f.Mode,
		SizeBytes:   f.SizeBytes,
		Encoding:    f.Encoding,
		FileHash:    f.Hash,
		ChunkHashes: f.Chunks,
	}
}
