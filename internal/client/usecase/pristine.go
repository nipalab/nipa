package usecase

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type ContentDownloader interface {
	Connect(ctx context.Context, host string) error
	chunkDownloader
}

type pristineRepo interface {
	LoadConfig() (*clientDomain.Config, error)
	LoadCommit() (*clientDomain.LocalCommit, error)
	LoadStatCache() (map[string]clientDomain.StatEntry, error)
	MissingChunks(hashes []domain.Hash) ([]domain.Hash, error)
	StoreChunks(chunks []*domain.ChunkData) error
}

// pristine makes sure the cached content of snapshot files is present, first
// by re-chunking unchanged working files, then by downloading what is left.
type pristine struct {
	root   string
	repo   pristineRepo
	auth   *Auth
	client ContentDownloader
}

func (p pristine) ensureSnapshotContent(ctx context.Context, files []clientDomain.SnapshotFile) error {
	missing, err := p.missingSet(files)
	if err != nil || len(missing) == 0 {
		return err
	}
	statCache, err := p.repo.LoadStatCache()
	if err != nil {
		return err
	}
	for _, f := range files {
		if !needsAny(f, missing) {
			continue
		}
		if err := p.recapture(f, statCache, missing); err != nil {
			return err
		}
	}
	var pending []clientDomain.SnapshotFile
	for _, f := range files {
		if needsAny(f, missing) {
			pending = append(pending, f)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	return p.download(ctx, pending, missing)
}

func (p pristine) missingSet(files []clientDomain.SnapshotFile) (map[domain.Hash]struct{}, error) {
	seen := make(map[domain.Hash]struct{})
	var hashes []domain.Hash
	for _, f := range files {
		for _, h := range f.Chunks {
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				hashes = append(hashes, h)
			}
		}
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	missing, err := p.repo.MissingChunks(hashes)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.Hash]struct{}, len(missing))
	for _, h := range missing {
		out[h] = struct{}{}
	}
	return out, nil
}

func needsAny(f clientDomain.SnapshotFile, missing map[domain.Hash]struct{}) bool {
	for _, h := range f.Chunks {
		if _, ok := missing[h]; ok {
			return true
		}
	}
	return false
}

// recapture re-chunks the working file and stores every chunk the cache is
// missing. Chunks are content addressed, so a stored chunk is always correct
// even when the rest of the file changed.
func (p pristine) recapture(f clientDomain.SnapshotFile, statCache map[string]clientDomain.StatEntry, missing map[domain.Hash]struct{}) error {
	abs := filepath.Join(p.root, filepath.FromSlash(f.Path))
	file, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return err
	}
	if entry, ok := statCache[f.Path]; ok && statMatches(entry, info) && entry.Hash != f.Hash {
		return nil
	}
	isBinary, err := chunker.ProbeBinary(file)
	if err != nil {
		return err
	}
	_, err = chunker.Encode(file, f.Path, isBinary, storedEncoding(f.Encoding), func(c chunker.EncodedChunk) error {
		if _, ok := missing[c.Hash]; !ok {
			return nil
		}
		if err := p.repo.StoreChunks([]*domain.ChunkData{{Hash: c.Hash, Data: c.Data}}); err != nil {
			return err
		}
		delete(missing, c.Hash)
		return nil
	})
	return err
}

func (p pristine) download(ctx context.Context, files []clientDomain.SnapshotFile, missing map[domain.Hash]struct{}) error {
	if p.client == nil {
		return clientDomain.NewUserError("content not cached locally; connect to the server")
	}
	cfg, err := p.repo.LoadConfig()
	if err != nil {
		return err
	}
	nu, err := clientDomain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return err
	}
	if err := p.client.Connect(ctx, nu.Origin); err != nil {
		return err
	}
	if p.auth != nil {
		if err := p.auth.MakeSureLoggedIn(ctx, nu.Host); err != nil {
			return err
		}
	}
	var commitID string
	if pin, err := p.repo.LoadCommit(); err != nil {
		return err
	} else if pin != nil {
		commitID = pin.CommitID
	}
	paths := make([]string, 0, len(files))
	var estimated int64
	for _, f := range files {
		paths = append(paths, f.Path)
		estimated += f.SizeBytes
	}
	hashes := make([]domain.Hash, 0, len(missing))
	for h := range missing {
		hashes = append(hashes, h)
	}
	scope := clientDomain.ChunkScope{Org: nu.Org, Project: nu.Project, CommitIDs: commitIDs(commitID), Paths: paths}
	return downloadMissing(ctx, p.client, p.repo, scope, hashes, estimated)
}
