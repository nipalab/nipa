package diff

import (
	"hash/fnv"
	"sort"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// DefaultRenameThreshold is the minimum similarity percentage for a detected
// rename, matching git's default.
const DefaultRenameThreshold = 50

// DetectRenames rewrites Deleted/Added changes into Renamed changes. Content
// is loaded lazily through load for line-based similarity; unavailable content
// falls back to chunk overlap, and equal file hashes always score 100.
func DetectRenames(changes []Change, load func(Entry) ([]byte, bool)) []Change {
	return detectRenames(changes, load, DefaultRenameThreshold)
}

type renamePair struct {
	oldIdx, newIdx, score int
}

func detectRenames(changes []Change, load func(Entry) ([]byte, bool), threshold int) []Change {
	var deleted, added []int
	for i, c := range changes {
		switch c.Status {
		case Deleted:
			deleted = append(deleted, i)
		case Added:
			added = append(added, i)
		}
	}
	if len(deleted) == 0 || len(added) == 0 {
		return changes
	}

	pairs := exactRenamePairs(changes, deleted, added)
	if len(deleted)*len(added) <= maxRenameComparisons {
		pairs = append(pairs, similarRenamePairs(changes, deleted, added, load, threshold)...)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if changes[pairs[i].oldIdx].Path != changes[pairs[j].oldIdx].Path {
			return changes[pairs[i].oldIdx].Path < changes[pairs[j].oldIdx].Path
		}
		return changes[pairs[i].newIdx].Path < changes[pairs[j].newIdx].Path
	})

	usedOld := make(map[int]bool)
	usedNew := make(map[int]bool)
	renamed := make(map[int]Change)
	consumed := make(map[int]bool)
	for _, p := range pairs {
		if usedOld[p.oldIdx] || usedNew[p.newIdx] {
			continue
		}
		usedOld[p.oldIdx] = true
		usedNew[p.newIdx] = true
		renamed[p.oldIdx] = Change{
			Path:       changes[p.newIdx].Path,
			Status:     Renamed,
			Old:        changes[p.oldIdx].Old,
			New:        changes[p.newIdx].New,
			Similarity: p.score,
		}
		consumed[p.newIdx] = true
	}

	out := make([]Change, 0, len(changes))
	for i, c := range changes {
		if consumed[i] {
			continue
		}
		if rc, ok := renamed[i]; ok {
			out = append(out, rc)
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// maxRenameComparisons bounds the content comparisons of one rename pass, like
// git's diff.renameLimit: past it only exact (equal hash) renames are found.
const maxRenameComparisons = 50_000

func exactRenamePairs(changes []Change, deleted, added []int) []renamePair {
	byHash := make(map[serverDomain.Hash][]int, len(deleted))
	for _, di := range deleted {
		h := changes[di].Old.Hash
		byHash[h] = append(byHash[h], di)
	}
	var pairs []renamePair
	for _, ai := range added {
		for _, di := range byHash[changes[ai].New.Hash] {
			pairs = append(pairs, renamePair{oldIdx: di, newIdx: ai, score: 100})
		}
	}
	return pairs
}

// renameSide caches one file's line fingerprint so every candidate pair reuses
// it instead of loading and hashing the content again.
type renameSide struct {
	ok    bool
	lines int
	fp    map[uint64]int
}

func similarRenamePairs(changes []Change, deleted, added []int, load func(Entry) ([]byte, bool), threshold int) []renamePair {
	sides := make(map[int]*renameSide, len(deleted)+len(added))
	side := func(idx int, e Entry) *renameSide {
		if s, ok := sides[idx]; ok {
			return s
		}
		s := &renameSide{}
		if load != nil && !e.IsBinary {
			if content, ok := load(e); ok {
				s.ok = true
				s.fp = lineFingerprint(content)
				for _, n := range s.fp {
					s.lines += n
				}
			}
		}
		sides[idx] = s
		return s
	}

	var pairs []renamePair
	for _, di := range deleted {
		old := changes[di].Old
		for _, ai := range added {
			cur := changes[ai].New
			if old.Hash == cur.Hash {
				continue
			}
			var score int
			if !old.IsBinary && !cur.IsBinary && load != nil {
				a, b := side(di, old), side(ai, cur)
				if a.ok && b.ok {
					if !canReach(a.lines, b.lines, threshold) {
						continue
					}
					score = fingerprintSimilarity(a, b)
				} else {
					score = chunkSimilarity(old, cur)
				}
			} else {
				score = chunkSimilarity(old, cur)
			}
			if score >= threshold {
				pairs = append(pairs, renamePair{oldIdx: di, newIdx: ai, score: score})
			}
		}
	}
	return pairs
}

// canReach reports whether two files of a and b lines can still score
// threshold: the shared lines are at most the smaller count, and the score
// divides by the larger one.
func canReach(a, b, threshold int) bool {
	hi := max(a, b)
	if hi == 0 {
		return threshold <= 0
	}
	return min(a, b)*100/hi >= threshold
}

func fingerprintSimilarity(a, b *renameSide) int {
	small, large := a.fp, b.fp
	if len(small) > len(large) {
		small, large = large, small
	}
	common := 0
	for h, n := range small {
		if m := large[h]; m > 0 {
			common += min(n, m)
		}
	}
	denom := max(a.lines, b.lines)
	if denom == 0 {
		return 0
	}
	return common * 100 / denom
}

func lineFingerprint(data []byte) map[uint64]int {
	out := make(map[uint64]int)
	for _, line := range splitLines(data) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(line))
		out[h.Sum64()]++
	}
	return out
}

func chunkSimilarity(old, new Entry) int {
	if len(old.ChunkHashes) == 0 || len(new.ChunkHashes) == 0 {
		return 0
	}
	seen := make(map[serverDomain.Hash]bool, len(old.ChunkHashes))
	for _, h := range old.ChunkHashes {
		seen[h] = true
	}
	common := 0
	for _, h := range new.ChunkHashes {
		if seen[h] {
			common++
		}
	}
	return common * 100 / max(len(old.ChunkHashes), len(new.ChunkHashes))
}
