package usecase

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type diffLocalRepo interface {
	Init(target string) error
	Snapshot() (*clientDomain.Snapshot, error)
	ListStaged() ([]string, error)
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

// DiffOptions controls which paths and states are compared.
type DiffOptions struct {
	// Staged only compares paths staged for the next push.
	Staged bool
	// Paths limits the diff to these repo-relative paths (prefix match).
	Paths []string
	// Filter limits changes to the given statuses; nil keeps all.
	Filter map[diff.Status]bool
	// Reverse swaps the old and new sides.
	Reverse bool
}

type Diff struct {
	localRepo diffLocalRepo
}

func NewDiff(localRepo diffLocalRepo) *Diff {
	return &Diff{localRepo: localRepo}
}

// Run compares the working tree against the last synced snapshot.
func (d *Diff) Run(ctx context.Context, root string, opts DiffOptions) ([]diff.FileDiff, error) {
	if err := d.localRepo.Init(root); err != nil {
		return nil, err
	}
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

	oldMap := snapshotEntries(snapshot, opts, stagedSet)
	newMap, contents, err := d.scanWorking(root, oldMap, stagedSet, opts)
	if err != nil {
		return nil, err
	}

	changes := filterStatus(diff.Compare(oldMap, newMap), opts.Filter)
	files := make([]diff.FileDiff, 0, len(changes))
	for _, c := range changes {
		f := diff.FileDiff{Change: c}
		switch c.Status {
		case diff.Added:
			f.New = contents[c.Path]
		case diff.Deleted:
			content, ok := d.loadOld(c.Old)
			f.Old, f.OldUnavailable = content, !ok
		case diff.Modified:
			content, ok := d.loadOld(c.Old)
			f.Old, f.OldUnavailable = content, !ok
			f.New = contents[c.Path]
		}
		files = append(files, f)
	}
	if opts.Reverse {
		reverseFiles(files)
	}
	return files, nil
}

func (d *Diff) loadOld(e diff.Entry) ([]byte, bool) {
	content, err := diff.LoadContent(d.localRepo.LoadChunk, e)
	if err != nil {
		return nil, false
	}
	return content, true
}

func (d *Diff) scanWorking(root string, oldMap map[string]diff.Entry, staged map[string]bool, opts DiffOptions) (map[string]diff.Entry, map[string][]byte, error) {
	paths, err := walkWorkingFiles(root)
	if err != nil {
		return nil, nil, err
	}
	newMap := make(map[string]diff.Entry, len(paths))
	contents := make(map[string][]byte, len(paths))
	for _, p := range paths {
		if !pathMatches(p, opts.Paths) {
			continue
		}
		if opts.Staged && !staged[p] {
			continue
		}
		if _, tracked := oldMap[p]; !tracked && !staged[p] {
			continue
		}
		fp := filepath.Join(root, filepath.FromSlash(p))
		data, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		hash, chunks, err := chunkFile(data)
		if err != nil {
			continue
		}
		hashes := make([]serverDomain.Hash, len(chunks))
		sizes := make([]int64, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			sizes[i] = c.SizeBytes
		}
		newMap[p] = diff.Entry{
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
	return newMap, contents, nil
}

func snapshotEntries(snapshot *clientDomain.Snapshot, opts DiffOptions, staged map[string]bool) map[string]diff.Entry {
	out := make(map[string]diff.Entry, len(snapshot.Files))
	for _, f := range snapshot.Files {
		if opts.Staged && !staged[f.Path] {
			continue
		}
		if !pathMatches(f.Path, opts.Paths) {
			continue
		}
		mode := f.Mode
		if mode == 0 {
			mode = 2
		}
		out[f.Path] = diff.Entry{
			Path:        f.Path,
			Mode:        mode,
			SizeBytes:   f.SizeBytes,
			IsBinary:    f.IsBinary,
			Hash:        f.Hash,
			ChunkHashes: f.Chunks,
		}
	}
	return out
}

func pathMatches(path string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		f = strings.Trim(strings.TrimPrefix(filepath.ToSlash(f), "./"), "/")
		if f == "" || path == f || strings.HasPrefix(path, f+"/") {
			return true
		}
	}
	return false
}

func filterStatus(changes []diff.Change, filter map[diff.Status]bool) []diff.Change {
	if filter == nil {
		return changes
	}
	out := make([]diff.Change, 0, len(changes))
	for _, c := range changes {
		if filter[c.Status] {
			out = append(out, c)
		}
	}
	return out
}

func reverseFiles(files []diff.FileDiff) {
	for i := range files {
		f := &files[i]
		f.Change.Old, f.Change.New = f.Change.New, f.Change.Old
		f.Old, f.New = f.New, f.Old
		f.OldUnavailable, f.NewUnavailable = f.NewUnavailable, f.OldUnavailable
		switch f.Change.Status {
		case diff.Added:
			f.Change.Status = diff.Deleted
		case diff.Deleted:
			f.Change.Status = diff.Added
		}
	}
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
