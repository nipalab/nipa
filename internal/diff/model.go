package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// Status classifies one changed path.
type Status int

const (
	Added Status = iota + 1
	Modified
	Deleted
)

func (s Status) String() string {
	switch s {
	case Added:
		return "A"
	case Modified:
		return "M"
	case Deleted:
		return "D"
	default:
		return "?"
	}
}

// Entry is one file in a compared tree state.
type Entry struct {
	Path        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Hash        serverDomain.Hash
	ChunkHashes []serverDomain.Hash
	ChunkSizes  []int64
}

// Change is one path that differs between the old and new entry maps.
type Change struct {
	Path   string
	Status Status
	Old    Entry
	New    Entry
}

// Compare returns the changed paths, sorted by path. Files are equal when
// both content hash and mode match.
func Compare(oldMap, newMap map[string]Entry) []Change {
	var changes []Change
	for path, oldEntry := range oldMap {
		newEntry, ok := newMap[path]
		if !ok {
			changes = append(changes, Change{Path: path, Status: Deleted, Old: oldEntry})
			continue
		}
		if oldEntry.Hash != newEntry.Hash || oldEntry.Mode != newEntry.Mode {
			changes = append(changes, Change{Path: path, Status: Modified, Old: oldEntry, New: newEntry})
		}
	}
	for path, newEntry := range newMap {
		if _, ok := oldMap[path]; !ok {
			changes = append(changes, Change{Path: path, Status: Added, New: newEntry})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// FromTree flattens a recursive server tree manifest into path-keyed entries.
func FromTree(root *serverDomain.TreeNode) map[string]Entry {
	out := make(map[string]Entry)
	flattenTree(root, "", out)
	return out
}

func flattenTree(node *serverDomain.TreeNode, prefix string, out map[string]Entry) {
	if node == nil {
		return
	}
	for _, f := range node.FileChildren {
		if f == nil {
			continue
		}
		path := strings.TrimPrefix(prefix+"/"+f.Name, "/")
		hashes := make([]serverDomain.Hash, len(f.Chunks))
		sizes := make([]int64, len(f.Chunks))
		for i, c := range f.Chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		out[path] = Entry{
			Path:        path,
			Mode:        f.Mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Hash:        chunker.FileHash(hashes),
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
	}
	for _, child := range node.TreeChildren {
		if child == nil {
			continue
		}
		flattenTree(child, prefix+"/"+child.Name, out)
	}
}

// LoadContent reassembles a file from its cached chunks.
func LoadContent(loadChunk func(serverDomain.Hash) ([]byte, error), e Entry) ([]byte, error) {
	var out []byte
	for _, h := range e.ChunkHashes {
		data, err := loadChunk(h)
		if err != nil {
			return nil, fmt.Errorf("load chunk %s for %s: %w", h, e.Path, err)
		}
		out = append(out, data...)
	}
	return out, nil
}
