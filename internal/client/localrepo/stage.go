package localrepo

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func (l *LocalRepo) StageAdd(path string) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	return q.StagedFileInsert(ctx, path)
}

func (l *LocalRepo) StageRemove(paths []string) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcLocalrepo.New(tx)
	for _, path := range paths {
		if err := q.StagedFileDelete(ctx, path); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *LocalRepo) ListStaged() ([]string, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	files, err := q.StagedFileList(ctx)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(files))
	for i, path := range files {
		paths[i] = path
	}
	return paths, nil
}

func (l *LocalRepo) ClearStaged() error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	return q.StagedFileDeleteAll(ctx)
}

func (l *LocalRepo) MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	seen := make(map[string]bool, len(hashes))
	var missing []serverDomain.Hash
	for _, h := range hashes {
		key := hex.EncodeToString(h[:])
		if seen[key] {
			continue
		}
		seen[key] = true
		exists, err := q.ChunkExists(ctx, h.Bytes())
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, h)
		}
	}
	return missing, nil
}

func (l *LocalRepo) Snapshot() (*domain.Snapshot, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	treeHash, err := q.MetaGet(ctx, "tree_hash")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		treeHash = ""
	}
	rows, err := q.SnapshotFileList(ctx, treeHash)
	if err != nil {
		return nil, err
	}
	chunkRows, err := q.SnapshotFileChunkList(ctx, treeHash)
	if err != nil {
		return nil, err
	}

	files := make([]domain.SnapshotFile, 0, len(rows))
	ci := 0
	for _, r := range rows {
		path := stripRoot(r.Path)
		sf := domain.SnapshotFile{
			Path:      path,
			Mode:      int(r.Mode),
			IsBinary:  r.IsBinary,
			SizeBytes: r.SizeBytes,
		}
		for ci < len(chunkRows) && stripRoot(chunkRows[ci].FilePath) == path {
			sf.Chunks = append(sf.Chunks, serverDomain.Chunk{
				Hash:      hashFromBytes(chunkRows[ci].Hash),
				SizeBytes: chunkRows[ci].SizeBytes,
			})
			ci++
		}
		sf.Hash = fileHashOf(sf.Chunks)
		files = append(files, sf)
	}
	return &domain.Snapshot{TreeHash: treeHash, Files: files}, nil
}

func stripRoot(path string) string {
	return strings.TrimPrefix(path, "/")
}

func fileHashOf(chunks []serverDomain.Chunk) serverDomain.Hash {
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	return chunker.FileHash(hashes)
}

func hashFromBytes(b []byte) serverDomain.Hash {
	var h serverDomain.Hash
	copy(h[:], b)
	return h
}
