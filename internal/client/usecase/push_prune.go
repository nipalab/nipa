package usecase

import (
	"log/slog"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

// maxTargetedPruneChunks bounds the cached chunks a prune after a push looks
// up one by one. Each lookup is one filtered scan of the files table (about
// a twentieth of the full sweep on a 100,000-file clone), so past this many
// the full sweep, which reads the table once, is the cheaper way.
const maxTargetedPruneChunks = 16

// pushCacheEffect is what a successful push can have left unreferenced in
// the object cache.
type pushCacheEffect struct {
	// applied is true when the stored tree was brought to the pushed commit
	// by the local apply: only the rows of paths changed, so only chunks can
	// have lost their last reference.
	applied bool
	// paths are the pushed files, whose rows the push rewrote.
	paths []string
	// chunks are the chunks of the previous rows of the pushed and removed
	// files, and of the rows the push wrote (the push caches some of those
	// for rows that end up not keeping them).
	chunks []domain.Hash
}

func newPushCacheEffect(applied bool, base map[string]clientDomain.SnapshotFile, files []*domain.PushFile, removed []string) *pushCacheEffect {
	effect := &pushCacheEffect{applied: applied}
	if !applied {
		return effect
	}
	seen := map[domain.Hash]bool{}
	add := func(hashes []domain.Hash) {
		for _, h := range hashes {
			if !seen[h] {
				seen[h] = true
				effect.chunks = append(effect.chunks, h)
			}
		}
	}
	for _, f := range files {
		effect.paths = append(effect.paths, f.Path)
		add(base[f.Path].Chunks)
		add(f.ChunkHashes)
	}
	for _, path := range removed {
		add(base[path].Chunks)
	}
	return effect
}

// targetedPruneRepo is a local repository that can prune a known set of
// chunks without listing the object cache or loading the snapshot.
type targetedPruneRepo interface {
	cacheLocalRepo
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	SnapshotFiles(paths []string) (map[string]clientDomain.SnapshotFile, error)
	FilesWithChunk(hash domain.Hash) ([]clientDomain.SnapshotFile, error)
	RemoveObjects(hashes []domain.Hash) (int, int64, error)
}

// pruneAfterPush prunes the object cache after a successful push. A push that
// was applied to the stored tree locally changed only the rows of the paths
// it sent, so the objects it can have orphaned are known (effect.chunks) and
// only those are checked and deleted. Every other push — one that concluded
// a merge or revert (nil effect), saved a fetched manifest, or left more
// cached chunks to look up than maxTargetedPruneChunks — and every
// repository without the by-path reads gets the full sweep, as before.
func pruneAfterPush(lr any, effect *pushCacheEffect, err error) {
	if err != nil {
		return
	}
	if repo, ok := lr.(targetedPruneRepo); ok && effect != nil && effect.applied {
		_, handled, pruneErr := pruneChunks(repo, effect.chunks, effect.paths)
		if pruneErr != nil {
			slog.Warn("local cache prune failed", "error", pruneErr)
			return
		}
		if handled {
			return
		}
	}
	pruneAfterSuccess(lr, nil)
}

// pruneChunks deletes those of the candidate objects that no snapshot file
// keeps, by the rule of the full sweep (pruneCache): an object stays while a
// tracked file that is cached in the pristine store (cachedInPristine) lists
// it as a chunk. It never deletes an object outside candidates, and runs
// under the same guard as the full sweep. changed names the paths whose rows
// were just rewritten; they are read first because they settle the chunks
// the push itself stored. Each cached candidate they do not keep costs one
// FilesWithChunk scan; handled is false when there are more of those than
// maxTargetedPruneChunks and the caller should run the full sweep instead.
func pruneChunks(lr targetedPruneRepo, candidates []domain.Hash, changed []string) (result PruneResult, handled bool, err error) {
	cached, err := cachedChunks(lr, candidates)
	if err != nil {
		return PruneResult{}, false, err
	}
	if len(cached) == 0 {
		// Nothing the push touched is in the cache: it orphaned nothing.
		return PruneResult{}, true, nil
	}
	release, skipped, err := pruneGuard(lr)
	if err != nil {
		return PruneResult{}, false, err
	}
	if skipped != nil {
		return *skipped, true, nil
	}
	defer release()

	pins, err := lr.ListPins()
	if err != nil {
		return PruneResult{}, false, err
	}
	rows, err := lr.SnapshotFiles(changed)
	if err != nil {
		return PruneResult{}, false, err
	}
	keep := map[domain.Hash]bool{}
	for _, f := range rows {
		if cachedInPristine(f, pins) {
			for _, h := range f.Chunks {
				keep[h] = true
			}
		}
	}
	unsettled := cached[:0]
	for _, h := range cached {
		if !keep[h] {
			unsettled = append(unsettled, h)
		}
	}
	if len(unsettled) > maxTargetedPruneChunks {
		return PruneResult{}, false, nil
	}
	var orphans []domain.Hash
	for _, h := range unsettled {
		users, err := lr.FilesWithChunk(h)
		if err != nil {
			return PruneResult{}, false, err
		}
		if !anyCachedInPristine(users, pins) {
			orphans = append(orphans, h)
		}
	}
	files, freed, err := lr.RemoveObjects(orphans)
	return PruneResult{Objects: files, Bytes: freed}, true, err
}

func anyCachedInPristine(files []clientDomain.SnapshotFile, pins []string) bool {
	for _, f := range files {
		if cachedInPristine(f, pins) {
			return true
		}
	}
	return false
}

// cachedChunks returns the candidates that have an object in the cache.
func cachedChunks(lr targetedPruneRepo, candidates []domain.Hash) ([]domain.Hash, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	missing, err := lr.MissingChunks(candidates)
	if err != nil {
		return nil, err
	}
	absent := make(map[domain.Hash]bool, len(missing))
	for _, h := range missing {
		absent[h] = true
	}
	seen := make(map[domain.Hash]bool, len(candidates))
	var cached []domain.Hash
	for _, h := range candidates {
		if absent[h] || seen[h] {
			continue
		}
		seen[h] = true
		cached = append(cached, h)
	}
	return cached, nil
}
