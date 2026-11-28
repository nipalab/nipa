package usecase

import (
	"log/slog"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

// maxTargetedPruneChunks bounds the cached chunks a prune after a push looks
// up one by one with FilesWithChunk. Each lookup is one filtered scan of the
// files table (about a twentieth of the full sweep on a 100,000-file clone),
// so past this many a repository without FilesWithAnyChunk gets the full
// sweep, which reads the table once.
const maxTargetedPruneChunks = 16

// maxSingleChunkLookups is the number of unsettled chunks up to which the
// prune looks each one up (FilesWithChunk, ~14 ms per 100,000 files, an instr
// filter inside SQLite) rather than scanning the table once for all of them
// (FilesWithAnyChunk, ~27 ms per 100,000 files whatever their number).
const maxSingleChunkLookups = 1

// chunkSetScanner finds the files that list any chunk of a set in one scan of
// the files table, with memory that follows the matches.
type chunkSetScanner interface {
	FilesWithAnyChunk(hashes []domain.Hash) ([]clientDomain.SnapshotFile, error)
}

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
// a merge or revert (nil effect) or saved a fetched manifest — every
// repository without the by-path reads, and a repository without the set
// scan (FilesWithAnyChunk) left with more cached chunks to look up than
// maxTargetedPruneChunks, gets the full sweep, as before.
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
// the push itself stored. The cached candidates they do not keep are looked
// up by unkeptChunks: one scan of the files table for all of them, or one
// FilesWithChunk scan each for a few; handled is false when a repository
// without the set scan leaves more than maxTargetedPruneChunks and the caller
// should run the full sweep instead.
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
	orphans, handled, err := unkeptChunks(lr, unsettled, pins)
	if err != nil || !handled {
		return PruneResult{}, false, err
	}
	files, freed, err := lr.RemoveObjects(orphans)
	return PruneResult{Objects: files, Bytes: freed}, true, err
}

// unkeptChunks returns the chunks of hashes that no tracked file cached in
// the pristine store lists: a few are looked up one by one, more in one scan
// of the files table when the repository can (FilesWithAnyChunk). handled is
// false when there are more than maxTargetedPruneChunks to look up one by
// one, which is the full sweep's job.
func unkeptChunks(lr targetedPruneRepo, hashes []domain.Hash, pins []string) (orphans []domain.Hash, handled bool, err error) {
	if len(hashes) == 0 {
		return nil, true, nil
	}
	if scanner, ok := lr.(chunkSetScanner); ok && len(hashes) > maxSingleChunkLookups {
		users, err := scanner.FilesWithAnyChunk(hashes)
		if err != nil {
			return nil, false, err
		}
		wanted := make(map[domain.Hash]bool, len(hashes))
		for _, h := range hashes {
			wanted[h] = true
		}
		kept := map[domain.Hash]bool{}
		for _, f := range users {
			if !cachedInPristine(f, pins) {
				continue
			}
			for _, h := range f.Chunks {
				if wanted[h] {
					kept[h] = true
				}
			}
		}
		for _, h := range hashes {
			if !kept[h] {
				orphans = append(orphans, h)
			}
		}
		return orphans, true, nil
	}
	if len(hashes) > maxTargetedPruneChunks {
		return nil, false, nil
	}
	for _, h := range hashes {
		users, err := lr.FilesWithChunk(h)
		if err != nil {
			return nil, false, err
		}
		if !anyCachedInPristine(users, pins) {
			orphans = append(orphans, h)
		}
	}
	return orphans, true, nil
}

// pruneCandidates prunes, after a successful command, the cached objects the
// command can have orphaned (candidates) by the rule of the full sweep,
// without loading the snapshot or listing the cache, when the repository can
// scan the files table for a set of chunks (FilesWithAnyChunk). Any other
// repository, or a prune the targeted path cannot finish, gets the full
// sweep, which is what these commands ran before.
func pruneCandidates(lr any, candidates []domain.Hash, err error) {
	if err != nil {
		return
	}
	repo, ok := lr.(targetedPruneRepo)
	if _, scans := lr.(chunkSetScanner); ok && scans {
		_, handled, pruneErr := pruneChunks(repo, candidates, nil)
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
