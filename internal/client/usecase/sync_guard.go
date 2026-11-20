package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

// overwriteCandidate is a working file a sync is about to replace: the version
// the sync writes and, when the path is tracked, the version last synced.
type overwriteCandidate struct {
	target  materializedFile
	base    clientDomain.SnapshotFile
	tracked bool
}

type statCacheLoader interface {
	LoadStatCache() (map[string]clientDomain.StatEntry, error)
}

// maxListedLocalChanges bounds the paths a refused sync names.
const maxListedLocalChanges = 20

// localChangeConflicts returns the candidates whose working file holds content
// the sync would destroy: an edit of a tracked file, or an untracked file in
// the way. A file that already equals what the sync writes loses nothing, and
// a local permission change alone is not treated as content to keep.
// Only the files the sync replaces are looked at, and a fresh stat fingerprint
// answers without reading the file, so the cost follows the size of the
// update, not of the repository.
func localChangeConflicts(ctx context.Context, root string, lr any, candidates []overwriteCandidate) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	var statCache map[string]clientDomain.StatEntry
	if loader, ok := lr.(statCacheLoader); ok {
		cache, err := loader.LoadStatCache()
		if err != nil {
			return nil, err
		}
		statCache = cache
	}

	var (
		mu        sync.Mutex
		conflicts []string
	)
	err := runPool(ctx, candidates, func(c overwriteCandidate) error {
		if localChangeLost(root, c, statCache) {
			mu.Lock()
			conflicts = append(conflicts, c.target.Path)
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func localChangeLost(root string, c overwriteCandidate, statCache map[string]clientDomain.StatEntry) bool {
	path := c.target.Path
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || !info.Mode().IsRegular() {
		// Nothing readable to lose; materialization reports what it cannot replace.
		return false
	}
	if !c.tracked {
		hash, err := hashWorkingPath(root, path, c.target.Encoding)
		return err != nil || hash != c.target.FileHash
	}
	hash, known := domain.Hash{}, false
	if entry, ok := statCache[path]; ok && statMatches(entry, info) {
		hash, known = entry.Hash, true
	}
	if !known {
		hash, err = hashWorkingPath(root, path, c.base.Encoding)
		if err != nil {
			return true
		}
	}
	if hash == c.base.Hash {
		return false
	}
	return storedEncoding(c.base.Encoding) != storedEncoding(c.target.Encoding) || hash != c.target.FileHash
}

// hashWorkingPath hashes a working file the way a tracked file with the given
// stored encoding is hashed.
func hashWorkingPath(root, path, encoding string) (domain.Hash, error) {
	hash, _, err := hashWorkingLayout(root, path, encoding)
	return hash, err
}

// hashWorkingLayout is hashWorkingPath that also returns the file's chunks
// (hash and stored size, in file order).
func hashWorkingLayout(root, path, encoding string) (domain.Hash, []domain.Chunk, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return domain.Hash{}, nil, err
	}
	defer func() { _ = f.Close() }()
	isBinary, err := chunker.ProbeBinary(f)
	if err != nil {
		return domain.Hash{}, nil, err
	}
	hash, chunks, _, err := chunkReader(f, path, isBinary, storedEncoding(encoding))
	return hash, chunks, err
}

// localChangesError refuses a sync that would overwrite local changes.
func localChangesError(paths []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "local changes to %d file(s) would be overwritten:", len(paths))
	for i, p := range paths {
		if i == maxListedLocalChanges {
			fmt.Fprintf(&b, "\n  ... and %d more", len(paths)-i)
			break
		}
		b.WriteString("\n  " + p)
	}
	return clientDomain.NewUserError(b.String()).
		WithHint("nothing was changed in the working copy", "push or shelve the changes first, or run again with --force to discard them")
}
