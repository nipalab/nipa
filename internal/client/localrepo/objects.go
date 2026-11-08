package localrepo

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func (l *LocalRepo) objectPath(hash serverDomain.Hash) string {
	hex := hash.String()
	return filepath.Join(l.target, ConfigDir, ObjectsDir, hex[:2], hex[2:])
}

func (l *LocalRepo) putObject(hash serverDomain.Hash, data []byte) error {
	p := l.objectPath(hash)
	if _, err := os.Stat(p); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

func (l *LocalRepo) OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	f, err := os.Open(l.objectPath(hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("chunk not found in cache")
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

const tempFilePrefix = ".tmp-"

var staleTempAge = time.Hour

// PruneObjects deletes cached objects whose hash is not in keep, plus temp
// files left behind by interrupted writes.
func (l *LocalRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	if l.db == nil {
		return 0, 0, errors.New("local repo not initialized")
	}
	root := filepath.Join(l.target, ConfigDir, ObjectsDir)
	dirs, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var (
		files int
		freed int64
	)
	cutoff := time.Now().Add(-staleTempAge)
	for _, dir := range dirs {
		if !dir.IsDir() || len(dir.Name()) != 2 {
			continue
		}
		dirPath := filepath.Join(root, dir.Name())
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return files, freed, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return files, freed, err
			}
			if !prunable(dir.Name(), entry.Name(), info, keep, cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(dirPath, entry.Name())); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return files, freed, err
			}
			files++
			freed += info.Size()
		}
	}
	return files, freed, nil
}

func prunable(prefix, name string, info fs.FileInfo, keep map[serverDomain.Hash]struct{}, cutoff time.Time) bool {
	if strings.HasPrefix(name, tempFilePrefix) {
		return info.ModTime().Before(cutoff)
	}
	hash, err := serverDomain.ParseHashHex(prefix + name)
	if err != nil {
		return false
	}
	_, ok := keep[hash]
	return !ok
}
