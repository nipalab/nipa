package usecase

import (
	"context"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/chunker"
	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type diffLocalRepo interface {
	Init(target string) error
	LoadConfig() (*clientDomain.Config, error)
	Snapshot() (*clientDomain.Snapshot, error)
	ListStaged() ([]string, error)
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunk(hash serverDomain.Hash, data []byte) error
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

type Diff struct {
	localRepo diffLocalRepo
}

func NewDiff(localRepo diffLocalRepo) *Diff {
	return &Diff{localRepo: localRepo}
}

// DiffFile is one changed file with both contents loaded for rendering.
type DiffFile struct {
	Change clientDiff.Change
	// Old is the base content (nil for added files).
	Old []byte
	// New is the changed content (nil for deleted files).
	New []byte
	// OldUnavailable marks base content missing from the local cache
	// (e.g. subdirectory clones); the change is still reported.
	OldUnavailable bool
}

type DiffResult struct {
	Base  string
	Head  string
	Files []DiffFile
}

func (d *Diff) Run(ctx context.Context, root string, revs []string) (*DiffResult, error) {
	if len(revs) > 0 {
		return nil, clientDomain.NewUserError("revision diffs are not supported yet")
	}
	if err := d.localRepo.Init(root); err != nil {
		return nil, err
	}
	cfg, err := d.localRepo.LoadConfig()
	if err != nil {
		return nil, err
	}
	return d.workingDiff(root, cfg)
}

func (d *Diff) workingDiff(root string, cfg *clientDomain.Config) (*DiffResult, error) {
	snapshot, err := d.localRepo.Snapshot()
	if err != nil {
		return nil, err
	}
	staged, err := d.localRepo.ListStaged()
	if err != nil {
		return nil, err
	}
	stagedSet := make(map[string]bool, len(staged))
	for _, p := range staged {
		stagedSet[p] = true
	}
	oldMap := snapshotEntries(snapshot)

	paths, err := walkWorkingFiles(root)
	if err != nil {
		return nil, err
	}
	newMap := make(map[string]clientDiff.Entry, len(paths))
	contents := make(map[string][]byte, len(paths))
	for _, p := range paths {
		if _, tracked := oldMap[p]; !tracked && !stagedSet[p] {
			continue
		}
		fp := filepath.Join(root, filepath.FromSlash(p))
		data, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		hash, chunks, err := chunkFile(data)
		if err != nil {
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		hashes := make([]serverDomain.Hash, len(chunks))
		sizes := make([]int64, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		newMap[p] = clientDiff.Entry{
			Path:        p,
			Mode:        serverModeFromPerm(info.Mode()),
			SizeBytes:   int64(len(data)),
			IsBinary:    chunker.IsBinary(data),
			Hash:        hash,
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
		contents[p] = data
	}

	changes := clientDiff.Compare(oldMap, newMap)
	files := make([]DiffFile, 0, len(changes))
	for _, c := range changes {
		f := DiffFile{Change: c}
		if c.Status == clientDiff.Added || c.Status == clientDiff.Modified {
			f.New = contents[c.Path]
		}
		if c.Status == clientDiff.Deleted || c.Status == clientDiff.Modified {
			old, err := clientDiff.LoadContent(d.localRepo.LoadChunk, c.Old)
			if err != nil {
				f.OldUnavailable = true
			} else {
				f.Old = old
			}
		}
		files = append(files, f)
	}
	return &DiffResult{
		Base:  cfg.Branch + " (last synced)",
		Head:  "working tree",
		Files: files,
	}, nil
}

func snapshotEntries(snapshot *clientDomain.Snapshot) map[string]clientDiff.Entry {
	out := make(map[string]clientDiff.Entry, len(snapshot.Files))
	for _, f := range snapshot.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 2
		}
		hashes := make([]serverDomain.Hash, len(f.Chunks))
		sizes := make([]int64, len(f.Chunks))
		for i, c := range f.Chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		out[f.Path] = clientDiff.Entry{
			Path:        f.Path,
			Mode:        mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Hash:        f.Hash,
			ChunkHashes: hashes,
			ChunkSizes:  sizes,
		}
	}
	return out
}

func serverModeFromPerm(perm os.FileMode) int {
	if perm&0o111 != 0 {
		return 3
	}
	if perm&0o222 == 0 {
		return 1
	}
	return 2
}
