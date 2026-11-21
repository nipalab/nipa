package localrepo

import (
	"crypto/rand"
	"encoding/hex"
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

// openObjectDir opens the fan-out folder of hash inside the object cache as an
// os.Root, creating it when missing, and refuses a link anywhere on the way
// (.nipa, .nipa/objects or the fan-out folder itself). It returns the root and
// the object's file name inside it.
func (l *LocalRepo) openObjectDir(hash serverDomain.Hash) (*os.Root, string, error) {
	hex := hash.String()
	root, err := ensureRealDir(l.target, filepath.Join(ConfigDir, ObjectsDir, hex[:2]))
	if err != nil {
		return nil, "", err
	}
	return root, hex[2:], nil
}

// createRootTemp creates a new temp file with a random name inside root.
func createRootTemp(root *os.Root) (*os.File, string, error) {
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := tempFilePrefix + hex.EncodeToString(b[:])
		f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, name, err
	}
	return nil, "", errors.New("could not create a temp file")
}

func (l *LocalRepo) putObject(hash serverDomain.Hash, data []byte) error {
	dir, name, err := l.openObjectDir(hash)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Stat(name); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, tmpName, err := createRootTemp(dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(fsperm.Mask(0o644)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return dir.Rename(tmpName, name)
}

// StoreChunkFrom writes one chunk object from a stream, hashing it on the way
// so the content never has to be held in memory: the object only appears
// under its hash when the bytes read match it. It returns the bytes read.
func (l *LocalRepo) StoreChunkFrom(hash serverDomain.Hash, r io.Reader) (int64, error) {
	if l.db == nil {
		return 0, errors.New("local repo not initialized")
	}
	dir, name, err := l.openObjectDir(hash)
	if err != nil {
		return 0, err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Stat(name); err == nil {
		return io.Copy(io.Discard, r)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	tmp, tmpName, err := createRootTemp(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = dir.Remove(tmpName) }()

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
	return n, dir.Rename(tmpName, name)
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
// files left behind by interrupted writes. It lists and deletes through the
// objects folder opened as an os.Root (openRealDir), so a linked objects or
// fan-out folder never leads it to delete anything outside the cache.
func (l *LocalRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	if l.db == nil {
		return 0, 0, errors.New("local repo not initialized")
	}
	root, err := openRealDir(l.target, filepath.Join(ConfigDir, ObjectsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = root.Close() }()
	objects := root.FS()
	dirs, err := fs.ReadDir(objects, ".")
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
		entries, err := fs.ReadDir(objects, dir.Name())
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
			if err := root.Remove(filepath.Join(dir.Name(), entry.Name())); err != nil {
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
