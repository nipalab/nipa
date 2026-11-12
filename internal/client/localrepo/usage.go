package localrepo

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nipalab/nipa/internal/client/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// DiskUsage sums the snapshot file sizes in SQL and stats the object cache
// in one pass over its fan-out directories.
func (l *LocalRepo) DiskUsage() (*domain.DiskUsage, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	q := sqlcLocalrepo.New(l.db)
	row, err := q.SnapshotUsage(context.Background())
	if err != nil {
		return nil, err
	}
	pins, err := q.PinnedPathList(context.Background())
	if err != nil {
		return nil, err
	}
	objects, bytes, err := l.objectStats()
	if err != nil {
		return nil, err
	}
	return &domain.DiskUsage{
		WorkingFiles: row.Files,
		WorkingBytes: row.SizeBytes,
		CacheObjects: objects,
		CacheBytes:   bytes,
		Pins:         pins,
	}, nil
}

func (l *LocalRepo) objectStats() (int64, int64, error) {
	root := filepath.Join(l.target, ConfigDir, ObjectsDir)
	dirs, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var objects, bytes int64
	for _, dir := range dirs {
		if !dir.IsDir() || len(dir.Name()) != 2 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, dir.Name()))
		if err != nil {
			return 0, 0, err
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			bytes += info.Size()
			if candidate, temp := pruneCandidate(dir.Name(), entry.Name(), nil); candidate && !temp {
				objects++
			}
		}
	}
	return objects, bytes, nil
}
