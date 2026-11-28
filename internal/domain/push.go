package domain

import "github.com/nipalab/nipa/internal/snow"

type PushFile struct {
	Path        string
	Mode        int
	SizeBytes   int64
	IsBinary    bool
	Encoding    string
	FileHash    Hash
	ChunkHashes []Hash
}

type ChunkData struct {
	Hash    Hash
	Data    []byte
	RawSize int64
}

type PushResult struct {
	CommitID   snow.ID
	CommitHash Hash
	TreeHash   Hash
	// View describes the pusher's full, recursive, unsparse manifest view.
	View PushView
}

// PushView is the view token of the pusher's full, recursive, unsparse
// manifest (what GetTreeManifest returns as view for no paths) and what it
// says about the commit a push created. It lets a client that applies the
// push to its stored tree record that tree as a delta base without fetching
// a manifest.
type PushView struct {
	// Token is empty when the server sent none (an older server).
	Token []byte
	// Unrestricted reports that the view hides no path of any commit: a tree
	// with the commit's root hash is that view of the commit.
	Unrestricted bool
	// HidesPushed reports that the pusher cannot read a path the push wrote,
	// so the pushed tree holds a file the view hides.
	HidesPushed bool
}
