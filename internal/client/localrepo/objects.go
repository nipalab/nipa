package localrepo

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/fsperm"
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

// StoreChunkFrom writes one chunk object from a stream, hashing it on the way
// so the content never has to be held in memory: the object only appears
// under its hash when the bytes read match it. It returns the bytes read.
func (l *LocalRepo) StoreChunkFrom(hash serverDomain.Hash, r io.Reader) (int64, error) {
	if l.db == nil {
		return 0, errors.New("local repo not initialized")
	}
	p := l.objectPath(hash)
	if _, err := os.Stat(p); err == nil {
		return io.Copy(io.Discard, r)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), tempFilePrefix+"*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	hasher := chunker.NewHasher()
	buf := make([]byte, 128<<10)
	n, err := io.CopyBuffer(io.MultiWriter(tmp, hasher), r, buf)
	if err == nil {
		err = tmp.Chmod(fsperm.Mask(0o644))
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return n, err
	}
	if hasher.Sum() != hash {
		return n, fmt.Errorf("chunk hash mismatch for %s", hash)
	}
	return n, os.Rename(tmpName, p)
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
			candidate, temp := pruneCandidate(dir.Name(), entry.Name(), keep)
			if !candidate {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return files, freed, err
			}
			if temp && !info.ModTime().Before(cutoff) {
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

func pruneCandidate(prefix, name string, keep map[serverDomain.Hash]struct{}) (candidate, temp bool) {
	if strings.HasPrefix(name, tempFilePrefix) {
		return true, true
	}
	hash, err := serverDomain.ParseHashHex(prefix + name)
	if err != nil {
		return false, false
	}
	_, ok := keep[hash]
	return !ok, false
}
