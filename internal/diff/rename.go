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

	var pairs []renamePair
	for _, di := range deleted {
		for _, ai := range added {
			score := similarity(changes[di].Old, changes[ai].New, load)
			if score >= threshold {
				pairs = append(pairs, renamePair{oldIdx: di, newIdx: ai, score: score})
			}
		}
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

func similarity(old, new Entry, load func(Entry) ([]byte, bool)) int {
	if old.Hash == new.Hash {
		return 100
	}
	if load != nil && !old.IsBinary && !new.IsBinary {
		oldContent, okOld := load(old)
		newContent, okNew := load(new)
		if okOld && okNew {
			return lineSimilarity(oldContent, newContent)
		}
	}
	return chunkSimilarity(old, new)
}

func lineSimilarity(a, b []byte) int {
	fa, fb := lineFingerprint(a), lineFingerprint(b)
	common, totalA, totalB := 0, 0, 0
	for h, n := range fa {
		totalA += n
		if m := fb[h]; m > 0 {
			common += min(n, m)
		}
	}
	for _, n := range fb {
		totalB += n
	}
	denom := max(totalA, totalB)
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
