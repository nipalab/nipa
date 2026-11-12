package localrepo

import (
	"context"
	"database/sql"
	"errors"
	"path"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// ListDir returns one level of the snapshot: the names of the sub-directories
// and the files directly inside dir ("" is the root). Paths are working-copy
// relative, like Snapshot's.
func (l *LocalRepo) ListDir(dir string) ([]string, []domain.SnapshotFile, error) {
	if l.db == nil {
		return nil, nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	stored := storedDir(dir)
	nodes, err := q.TreeNodeChildren(ctx, stored)
	if err != nil {
		return nil, nil, err
	}
	dirs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		dirs = append(dirs, path.Base(n))
	}
	rows, err := q.FileListByTree(ctx, stored)
	if err != nil {
		return nil, nil, err
	}
	files := make([]domain.SnapshotFile, 0, len(rows))
	for _, r := range rows {
		f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, f)
	}
	return dirs, files, nil
}

// SnapshotFile returns one tracked file; ok is false when p is not tracked.
func (l *LocalRepo) SnapshotFile(p string) (domain.SnapshotFile, bool, error) {
	if l.db == nil {
		return domain.SnapshotFile{}, false, errors.New("local repo not initialized")
	}
	if p == "" {
		return domain.SnapshotFile{}, false, nil
	}
	r, err := sqlcLocalrepo.New(l.db).FileGet(context.Background(), storedDir(p))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SnapshotFile{}, false, nil
	}
	if err != nil {
		return domain.SnapshotFile{}, false, err
	}
	f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
	return f, err == nil, err
}

func storedDir(dir string) string {
	if dir == "" {
		return ""
	}
	return "/" + dir
}

func snapshotFile(p string, hash []byte, size, mode int64, binary bool, encoding string, chunks []byte) (domain.SnapshotFile, error) {
	hashes, err := decodeChunkHashes(chunks)
	if err != nil {
		return domain.SnapshotFile{}, err
	}
	return domain.SnapshotFile{
		Path:      stripRoot(p),
		Hash:      hashFromBytes(hash),
		Mode:      int(mode),
		IsBinary:  binary,
		Encoding:  encoding,
		SizeBytes: size,
		Chunks:    hashes,
	}, nil
}
