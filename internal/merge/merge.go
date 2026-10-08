package merge

import (
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

type File struct {
	Path        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Encoding    string
	Hash        domain.Hash
	ChunkHashes []domain.Hash
	ChunkSizes  []int64
}

func Flatten(root *domain.TreeNode) map[string]File {
	out := make(map[string]File)
	flatten(root, "", out)
	return out
}

func flatten(node *domain.TreeNode, prefix string, out map[string]File) {
	if node == nil {
		return
	}
	for _, f := range node.FileChildren {
		path := strings.TrimPrefix(prefix+"/"+f.Name, "/")
		hashes := make([]domain.Hash, len(f.Chunks))
		sizes := make([]int64, len(f.Chunks))
		for i, c := range f.Chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		out[path] = File{
			Path:        path,
			Mode:        f.Mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Encoding:    f.Encoding,
			Hash:        chunker.FileHash(hashes),
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
	}
	for _, child := range node.TreeChildren {
		if child == nil {
			continue
		}
		flatten(child, prefix+"/"+child.Name, out)
	}
}

type Decision int

const (
	// KeepOurs keeps the current (target) version; it is already on disk.
	KeepOurs Decision = iota
	// KeepTheirs takes the source version; content must be fetched and written.
	KeepTheirs
	// TextMerge combines base/ours/theirs line-by-line; content is required.
	TextMerge
	// BinaryConflict: both sides changed a binary differently. Content is
	// unspecified; the conflict must be resolved by picking a version.
	BinaryConflict
	// AddAddConflict: both sides added different content at the same path.
	AddAddConflict
	// ModifyDeleteConflict: ours modified a file theirs deleted.
	ModifyDeleteConflict
	// DeleteModifyConflict: ours deleted a file theirs modified.
	DeleteModifyConflict
)

type Conflict struct {
	Path   string
	Kind   Decision
	Base   File
	Ours   File
	Theirs File
}

type Entry struct {
	Decision Decision
	Base     File
	Ours     File
	Theirs   File
}

type Result struct {
	Entries   map[string]Entry
	Deleted   []string
	Conflicts []Conflict
}

func ThreeWay(base, ours, theirs map[string]File) *Result {
	res := &Result{Entries: make(map[string]Entry)}

	paths := make(map[string]bool)
	for p := range base {
		paths[p] = true
	}
	for p := range ours {
		paths[p] = true
	}
	for p := range theirs {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	for _, p := range sorted {
		b, inB := base[p]
		o, inO := ours[p]
		t, inT := theirs[p]
		switch {
		case inB:
			switch {
			case inO && inT:
				switch {
				case sameFile(o, b) && sameFile(t, b):
					// Unchanged on both sides: not part of the merge result.
				case sameFile(o, t):
					// Both sides changed identically: keep it.
					res.entry(KeepOurs, p, b, o, t)
				case sameFile(o, b):
					res.entry(KeepTheirs, p, b, o, t)
				case sameFile(t, b):
					res.entry(KeepOurs, p, b, o, t)
				case o.IsBinary || t.IsBinary:
					res.conflict(BinaryConflict, p, b, o, t)
				default:
					res.entry(TextMerge, p, b, o, t)
				}
			case inO:
				// theirs deleted the file.
				if sameFile(o, b) {
					res.deleted(p)
				} else {
					res.conflict(ModifyDeleteConflict, p, b, o, t)
				}
			case inT:
				// ours deleted the file.
				if sameFile(t, b) {
					res.deleted(p)
				} else {
					res.conflict(DeleteModifyConflict, p, b, o, t)
				}
			default:
				// deleted on both sides: gone.
				res.deleted(p)
			}
		default:
			switch {
			case inO && inT:
				if sameFile(o, t) {
					res.entry(KeepOurs, p, b, o, t)
				} else {
					res.conflict(AddAddConflict, p, b, o, t)
				}
			case inO:
				res.entry(KeepOurs, p, b, o, t)
			case inT:
				res.entry(KeepTheirs, p, b, o, t)
			}
		}
	}
	return res
}

func (r *Result) entry(d Decision, path string, b, o, t File) {
	r.Entries[path] = Entry{Decision: d, Base: b, Ours: o, Theirs: t}
}

func (r *Result) deleted(path string) {
	r.Deleted = append(r.Deleted, path)
}

func (r *Result) conflict(kind Decision, path string, b, o, t File) {
	r.Entries[path] = Entry{Decision: kind, Base: b, Ours: o, Theirs: t}
	r.Conflicts = append(r.Conflicts, Conflict{Path: path, Kind: kind, Base: b, Ours: o, Theirs: t})
}

func sameFile(a, b File) bool {
	return a.Hash == b.Hash && a.Mode == b.Mode
}
