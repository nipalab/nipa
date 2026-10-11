package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

// Restore discards unpushed changes to the given files or directories by
// re-materializing them from the last synced tree. Paths with staged changes
// are refused, since the staged content is not stored separately.
func (w *WorkingCopy) Restore(_ context.Context, targets []string) error {
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

	statEntries := make(map[string]domain.StatEntry)
	for _, p := range paths {
		f := tracked[p]
		if current, err := w.hashFile(p, f.Encoding); err == nil && current == f.Hash {
			continue
		}
		if err := materializeFile(w.root, snapshotMaterialized(f), w.localRepo.OpenChunk); err != nil {
			return fmt.Errorf("restore %s: %w", p, err)
		}
		if entry, err := statEntryFor(w.root, p, f.Hash); err == nil {
			statEntries[p] = entry
		}
	}
	if len(statEntries) == 0 {
		return nil
	}
	return w.localRepo.SaveStatEntries(statEntries)
}

// restoreTargets expands exact paths and directory prefixes to the tracked
// files they cover, matching git's "pathspec" semantics for restore.
func restoreTargets(targets []string, tracked map[string]domain.SnapshotFile) ([]string, error) {
	seen := make(map[string]bool, len(tracked))
	var paths []string
	for _, t := range targets {
		if err := validateRelPath(t); err != nil {
			return nil, err
		}
		if isNipaPath(t) {
			return nil, fmt.Errorf("cannot operate on path inside %q", nipaDir)
		}
		if t == "" {
			return nil, fmt.Errorf("a path is required")
		}
		matched := false
		for p := range tracked {
			if p != t && !strings.HasPrefix(p, t+"/") {
				continue
			}
			matched = true
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		if !matched {
			return nil, fmt.Errorf("path %q is not tracked", t)
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
