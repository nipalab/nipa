package domain

import "time"

const (
	ShelfModified = "modified"
	ShelfAdded    = "added"
	ShelfDeleted  = "deleted"
)

// ShelfFile is one stored file version: hashes are hex, chunks are the
// stored (encoded) chunk hashes in order.
type ShelfFile struct {
	Hash      string   `json:"hash"`
	Mode      int      `json:"mode"`
	SizeBytes int64    `json:"size_bytes"`
	IsBinary  bool     `json:"is_binary,omitempty"`
	Encoding  string   `json:"encoding"`
	Chunks    []string `json:"chunks,omitempty"`
}

// ShelfEntry is one shelved path. File is the shelved content (nil for a
// deletion) and Base the synced version it was changed from (nil for a new
// file). Untracked marks a new file that was not staged.
type ShelfEntry struct {
	Path      string     `json:"path"`
	Status    string     `json:"status"`
	File      *ShelfFile `json:"file,omitempty"`
	Base      *ShelfFile `json:"base,omitempty"`
	Staged    bool       `json:"staged,omitempty"`
	Untracked bool       `json:"untracked,omitempty"`
	Locked    bool       `json:"locked,omitempty"`
	// BaseStored says the base chunks were copied into the shelf (text), so
	// a three-way merge never needs the server.
	BaseStored bool `json:"base_stored,omitempty"`
}

type Shelf struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	CreatedAt    time.Time    `json:"created_at"`
	Branch       string       `json:"branch"`
	Head         *HeadRef     `json:"head,omitempty"`
	BaseCommitID string       `json:"base_commit_id,omitempty"`
	BaseTreeHash string       `json:"base_tree_hash,omitempty"`
	Entries      []ShelfEntry `json:"entries"`
	SizeBytes    int64        `json:"size_bytes,omitempty"`
}

func (s *Shelf) Entry(path string) (ShelfEntry, bool) {
	for _, e := range s.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return ShelfEntry{}, false
}

// ShelfState is a shelf apply waiting on conflicts. Applied lists every path
// the apply wrote, so an abort can put the synced versions back.
type ShelfState struct {
	ShelfID        string            `json:"shelf_id"`
	ShelfName      string            `json:"shelf_name"`
	BaseCommitID   string            `json:"base_commit_id,omitempty"`
	OursCommitID   string            `json:"ours_commit_id,omitempty"`
	Keep           bool              `json:"keep,omitempty"`
	Applied        []string          `json:"applied,omitempty"`
	Conflicts      []string          `json:"conflicts,omitempty"`
	ConflictKinds  map[string]string `json:"conflict_kinds,omitempty"`
	ConflictBinary []string          `json:"conflict_binary,omitempty"`
	Resolved       []string          `json:"resolved,omitempty"`
}

// ShelfRevision is the ConflictRevision id of a shelf's side.
func ShelfRevision(id string) string {
	return "shelf:" + id
}
