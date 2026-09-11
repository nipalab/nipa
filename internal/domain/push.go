package domain

import "github.com/nipalab/nipa/internal/snow"

type PushFile struct {
	Path        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	FileHash    Hash
	ChunkHashes []Hash
}

type ChunkData struct {
	Hash Hash
	Data []byte
}

type PushResult struct {
	CommitID   snow.ID
	CommitHash Hash
	TreeHash   Hash
}
