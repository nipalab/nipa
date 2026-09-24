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

type Status struct {
	Staged    []string
	Modified  []string
	Untracked []string
	Missing   []string
	Conflicts []string
}
