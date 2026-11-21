package localrepo

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func (l *LocalRepo) StageAdd(path string) error {
	return l.StageAddMany([]string{path})
}

func (l *LocalRepo) StageAddMany(paths []string) error {
	if l.db == nil {
		return errors.New("local repo not initialized")
	}
	if len(paths) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcLocalrepo.New(tx)
	for _, path := range paths {
		if err := q.StagedFileInsert(ctx, path); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	copy(paths, files)
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
	fanout, listed := l.objectFanout(len(hashes))
	seen := make(map[serverDomain.Hash]bool, len(hashes))
	var missing []serverDomain.Hash
	for _, h := range hashes {
		if seen[h] {
			continue
		}
		seen[h] = true
		if listed && !fanout[h[0]] {
			missing = append(missing, h)
			continue
		}
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

// minFanoutProbe is the number of hashes from which one listing of the
// object store's fan-out directories is cheaper than a stat per hash.
const minFanoutProbe = 16

// objectFanout lists which of the 256 fan-out directories of the object
// store exist, so MissingChunks answers for the others without touching the
// disk: on a fresh clone the store is empty and no hash is probed at all. It
// reports false when the listing is not worth it or failed, and every hash is
// then probed.
func (l *LocalRepo) objectFanout(hashes int) (present [256]bool, listed bool) {
	if hashes < minFanoutProbe {
		return present, false
	}
	entries, err := os.ReadDir(filepath.Join(l.target, ConfigDir, ObjectsDir))
	if err != nil {
		return present, errors.Is(err, fs.ErrNotExist)
	}
	for _, entry := range entries {
		if b, err := hex.DecodeString(entry.Name()); err == nil && len(b) == 1 {
			present[b[0]] = true
		}
	}
	return present, true
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
	rows, err := q.SnapshotFileList(ctx)
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
			Encoding:  r.Encoding,
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
