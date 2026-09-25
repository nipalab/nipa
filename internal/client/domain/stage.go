package domain

import (
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type SnapshotFile struct {
	Path      string
	Hash      serverDomain.Hash
	Mode      int
	IsBinary  bool
	Encoding  string
	SizeBytes int64
	Chunks    []serverDomain.Hash
}

type Snapshot struct {
	TreeHash string
	Files    []SnapshotFile
}

// StatEntry is a validated working-copy fingerprint: the stat values observed
// when Hash was computed. A later stat match means the file still has Hash.
type StatEntry struct {
	SizeBytes int64
	MtimeNS   int64
	Mode      int
	Hash      serverDomain.Hash
	CachedAt  int64
}

type Status struct {
	Staged    []string
	Deleted   []string
	Modified  []string
	Untracked []string
	Missing   []string
	Conflicts []string
}
