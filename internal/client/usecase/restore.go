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
)

// Restore discards unpushed changes to the given files or directories by
// re-materializing them from the last synced tree. Paths with staged changes
// are refused, since the staged content is not stored separately.
func (w *WorkingCopy) Restore(ctx context.Context, targets []string) error {
	snapshot, err := w.localRepo.Snapshot()
	if err != nil {
		return err
	}
	staged, err := w.listStagedSet()
	if err != nil {
		return err
	}

	tracked := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		tracked[f.Path] = f
	}
	paths, err := restoreTargets(targets, tracked)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if staged[p] {
			return fmt.Errorf("cannot restore %q with staged changes; push or reset them first", p)
		}
	}

	statCache, err := w.localRepo.LoadStatCache()
	if err != nil {
		return err
	}
	var jobs []materializeJob
	for _, f := range w.changedFromSnapshot(paths, tracked, statCache) {
		jobs = append(jobs, materializeJob{file: snapshotMaterialized(f), prevMode: f.Mode})
	}
	statEntries, err := materializeAll(ctx, w.root, jobs, w.localRepo.OpenChunk)
	var failed *materializeError
	if errors.As(err, &failed) {
		return fmt.Errorf("restore %s: %w", failed.path, failed.err)
	}
	if err != nil {
		return err
	}
	if len(statEntries) == 0 {
		return nil
	}
	return w.localRepo.SaveStatEntries(statEntries)
}

// changedFromSnapshot returns the files whose working copy differs from the
// snapshot content or mode, so restoring the others would be a no-op. A fresh
// stat fingerprint is trusted like status does; the rest are hashed in
// parallel. Status uses the same mode comparison, so skipping cannot leave a
// file reported modified.
func (w *WorkingCopy) changedFromSnapshot(paths []string, tracked map[string]domain.SnapshotFile, statCache map[string]domain.StatEntry) []domain.SnapshotFile {
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
	for i, res := range w.rehash(hashJobs) {
		f := tracked[hashJobs[i].path]
		if res.err != nil || res.hash != f.Hash {
			changed = append(changed, f)
		}
	}
	return changed
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
