package localrepo

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// pathBatch is the number of paths bound to one by-path query, far below
// SQLite's bound-parameter limit.
const pathBatch = 500

// dirRange returns the bounds of the stored paths below dir ("" is the root):
// every path that starts with "<dir>/" sorts from that prefix up to, but not
// including, the prefix with its final slash replaced by the next byte. A
// range on the primary key needs no LIKE, so wildcard characters in dir are
// plain bytes and a sibling such as "<dir>b" is never matched.
func dirRange(dir string) (lower, upper string) {
	stored := storedDir(dir)
	return stored + "/", stored + "0"
}

// SnapshotTreeHash returns the root tree hash of the stored snapshot, what
// Snapshot().TreeHash holds, without reading any file row.
func (l *LocalRepo) SnapshotTreeHash() (string, error) {
	if l.db == nil {
		return "", errors.New("local repo not initialized")
	}
	treeHash, err := sqlcLocalrepo.New(l.db).MetaGet(context.Background(), "tree_hash")
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return treeHash, err
}

// SnapshotFiles returns the tracked files among the given working-copy
// relative paths, keyed by path, with indexed reads only: a caller that
// touches a few files of a large clone never loads the whole snapshot. Paths
// that are not tracked are absent from the result.
func (l *LocalRepo) SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	ctx := context.Background()
	q := sqlcLocalrepo.New(l.db)
	files := make(map[string]domain.SnapshotFile, len(paths))
	stored := make([]string, 0, min(len(paths), pathBatch))
	for start := 0; start < len(paths); start += pathBatch {
		stored = stored[:0]
		for _, p := range paths[start:min(start+pathBatch, len(paths))] {
			if p != "" {
				stored = append(stored, storedDir(p))
			}
		}
		if len(stored) == 0 {
			continue
		}
		rows, err := q.FileListByPaths(ctx, stored)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
			if err != nil {
				return nil, err
			}
			files[f.Path] = f
		}
	}
	return files, nil
}

// SnapshotFilesUnder returns the tracked files below the directory dir, at
// any depth, sorted by path; "" is the whole snapshot. It is one range scan
// of the files primary key, so its cost follows the size of the directory.
func (l *LocalRepo) SnapshotFilesUnder(dir string) ([]domain.SnapshotFile, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	lower, upper := dirRange(dir)
	rows, err := sqlcLocalrepo.New(l.db).FileListInRange(context.Background(), sqlcLocalrepo.FileListInRangeParams{Lower: lower, Upper: upper})
	if err != nil {
		return nil, err
	}
	files := make([]domain.SnapshotFile, 0, len(rows))
	for _, r := range rows {
		f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// TracksUnder reports whether any tracked file lies below the directory dir.
func (l *LocalRepo) TracksUnder(dir string) (bool, error) {
	if l.db == nil {
		return false, errors.New("local repo not initialized")
	}
	lower, upper := dirRange(dir)
	_, err := sqlcLocalrepo.New(l.db).FileFirstInRange(context.Background(), sqlcLocalrepo.FileFirstInRangeParams{Lower: lower, Upper: upper})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// FilesWithChunk returns the tracked files whose chunk list holds hash. There
// is no chunk index: SQLite filters the files table on the chunk blob, so the
// call is one table scan that returns only the matching rows.
func (l *LocalRepo) FilesWithChunk(hash serverDomain.Hash) ([]domain.SnapshotFile, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	rows, err := sqlcLocalrepo.New(l.db).FileListByChunk(context.Background(), hash.Bytes())
	if err != nil {
		return nil, err
	}
	var files []domain.SnapshotFile
	for _, r := range rows {
		f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
		if err != nil {
			return nil, err
		}
		// The blob match can straddle two neighbouring hashes.
		for _, h := range f.Chunks {
			if h == hash {
				files = append(files, f)
				break
			}
		}
	}
	return files, nil
}

// RemoveObjects deletes the given cached objects and returns how many it
// removed and the bytes freed. Hashes without an object are skipped. Like
// PruneObjects it acts through the objects folder opened as an os.Root and
// never follows a linked fan-out folder.
func (l *LocalRepo) RemoveObjects(hashes []serverDomain.Hash) (int, int64, error) {
	if l.db == nil {
		return 0, 0, errors.New("local repo not initialized")
	}
	if len(hashes) == 0 {
		return 0, 0, nil
	}
	root, err := openRealDir(l.target, filepath.Join(ConfigDir, ObjectsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = root.Close() }()
	var (
		files int
		freed int64
	)
	for _, h := range hashes {
		name := h.String()
		dir, err := root.Lstat(name[:2])
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !dir.IsDir()) {
			continue
		}
		if err != nil {
			return files, freed, err
		}
		object := filepath.Join(name[:2], name[2:])
		info, err := root.Lstat(object)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && info.IsDir()) {
			continue
		}
		if err != nil {
			return files, freed, err
		}
		if err := root.Remove(object); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return files, freed, err
		}
		files++
		freed += info.Size()
	}
	return files, freed, nil
}
