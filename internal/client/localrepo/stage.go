package localrepo

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"strings"

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
	seen := make(map[serverDomain.Hash]bool, len(hashes))
	var missing []serverDomain.Hash
	for _, h := range hashes {
		if seen[h] {
			continue
		}
		seen[h] = true
		_, err := os.Stat(l.objectPath(h))
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, h)
		default:
			return nil, err
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

	files := make([]domain.SnapshotFile, 0, len(rows))
	for _, r := range rows {
		chunks, err := decodeChunkHashes(r.Chunks)
		if err != nil {
			return nil, err
		}
		files = append(files, domain.SnapshotFile{
			Path:      stripRoot(r.Path),
			Hash:      hashFromBytes(r.Hash),
			Mode:      int(r.Mode),
			IsBinary:  r.IsBinary,
			SizeBytes: r.SizeBytes,
			Chunks:    chunks,
		})
	}
	return &domain.Snapshot{TreeHash: treeHash, Files: files}, nil
}

func stripRoot(path string) string {
	return strings.TrimPrefix(path, "/")
}

func hashFromBytes(b []byte) serverDomain.Hash {
	var h serverDomain.Hash
	copy(h[:], b)
	return h
}
