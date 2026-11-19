package gitimport

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-git/go-git/v5/storage/filesystem"
	_ "modernc.org/sqlite"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/gitimport"
)

const (
	StateDirName  = "nipa-import"
	StateFileName = "state.db"
)

const stateSchema = `
CREATE TABLE IF NOT EXISTS blobs (
    git_sha TEXT NOT NULL,
    profile TEXT NOT NULL,
    file_hash BLOB NOT NULL,
    size INTEGER NOT NULL,
    is_binary INTEGER NOT NULL,
    encoding TEXT NOT NULL,
    chunks BLOB NOT NULL,
    chunk_sizes BLOB NOT NULL,
    uploaded INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (git_sha, profile)
);
CREATE TABLE IF NOT EXISTS lfs (
    oid TEXT PRIMARY KEY,
    git_sha TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

const metaTargetKey = "target"

type StateTarget struct {
	Host      string `json:"host"`
	Org       string `json:"org"`
	Project   string `json:"project"`
	SourceKey string `json:"source_key"`
}

func (t StateTarget) String() string {
	return fmt.Sprintf("%s/%s/%s (source key %q)", t.Host, t.Org, t.Project, t.SourceKey)
}

type State struct {
	db      *sql.DB
	mu      sync.Mutex
	pending map[gitimport.BlobKey]*gitimport.Blob
}

func GitDir(src *gitimport.Source) (string, error) {
	storage, ok := src.Repository().Storer.(*filesystem.Storage)
	if !ok {
		return "", errors.New("the git repository is not stored on disk")
	}
	return storage.Filesystem().Root(), nil
}

func OpenState(dir string, reset bool) (*State, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create import state folder: %w", err)
	}
	path := filepath.Join(dir, StateFileName)
	if reset {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("reset import state: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open import state: %w", err)
	}
	if _, err := db.Exec(stateSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open import state %s: %w", path, err)
	}
	return &State{db: db, pending: map[gitimport.BlobKey]*gitimport.Blob{}}, nil
}

func (s *State) Close() error {
	return s.db.Close()
}

func (s *State) CheckTarget(target StateTarget) error {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaTargetKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		data, err := json.Marshal(target)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, metaTargetKey, string(data))
		return err
	}
	if err != nil {
		return err
	}
	var stored StateTarget
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return fmt.Errorf("read import state target: %w", err)
	}
	if stored != target {
		return domain.NewUserError(fmt.Sprintf("the import state of this git repository belongs to %s, not %s; rerun with --reset-state to start over", stored, target)).
			WithHint("the state remembers which content was uploaded to that project", "rerun with --reset-state to start over for the new target")
	}
	return nil
}

func (s *State) Get(key gitimport.BlobKey) (*gitimport.Blob, bool, error) {
	s.mu.Lock()
	b, ok := s.pending[key]
	s.mu.Unlock()
	if ok {
		return b, true, nil
	}
	var fileHash, chunks, sizes []byte
	blob := &gitimport.Blob{}
	err := s.db.QueryRow(`SELECT file_hash, size, is_binary, encoding, chunks, chunk_sizes FROM blobs WHERE git_sha = ? AND profile = ? AND uploaded = 1`, key.SHA, key.Profile).
		Scan(&fileHash, &blob.Size, &blob.IsBinary, &blob.Encoding, &chunks, &sizes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(fileHash) != len(serverDomain.Hash{}) || len(chunks)%len(serverDomain.Hash{}) != 0 {
		return nil, false, nil
	}
	copy(blob.FileHash[:], fileHash)
	blob.Chunks = decodeHashes(chunks)
	blob.ChunkSizes, err = decodeSizes(sizes)
	if err != nil || len(blob.ChunkSizes) != len(blob.Chunks) {
		return nil, false, nil
	}
	return blob, true, nil
}

func (s *State) Put(key gitimport.BlobKey, blob *gitimport.Blob) error {
	s.mu.Lock()
	s.pending[key] = blob
	s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO blobs (git_sha, profile, file_hash, size, is_binary, encoding, chunks, chunk_sizes, uploaded)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
ON CONFLICT (git_sha, profile) DO UPDATE SET file_hash = excluded.file_hash, size = excluded.size, is_binary = excluded.is_binary,
    encoding = excluded.encoding, chunks = excluded.chunks, chunk_sizes = excluded.chunk_sizes, uploaded = 0`,
		key.SHA, key.Profile, blob.FileHash[:], blob.Size, blob.IsBinary, blob.Encoding, encodeHashes(blob.Chunks), encodeSizes(blob.ChunkSizes))
	return err
}

func (s *State) PendingKeys() []gitimport.BlobKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]gitimport.BlobKey, 0, len(s.pending))
	for k := range s.pending {
		keys = append(keys, k)
	}
	return keys
}

func (s *State) MarkUploaded(keys []gitimport.BlobKey) error {
	if len(keys) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := tx.Exec(`UPDATE blobs SET uploaded = 1 WHERE git_sha = ? AND profile = ?`, k.SHA, k.Profile); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	for _, k := range keys {
		delete(s.pending, k)
	}
	s.mu.Unlock()
	return nil
}

func encodeHashes(hashes []serverDomain.Hash) []byte {
	out := make([]byte, 0, len(hashes)*len(serverDomain.Hash{}))
	for _, h := range hashes {
		out = append(out, h[:]...)
	}
	return out
}

func decodeHashes(raw []byte) []serverDomain.Hash {
	size := len(serverDomain.Hash{})
	out := make([]serverDomain.Hash, 0, len(raw)/size)
	for i := 0; i+size <= len(raw); i += size {
		var h serverDomain.Hash
		copy(h[:], raw[i:i+size])
		out = append(out, h)
	}
	return out
}

func encodeSizes(sizes []int64) []byte {
	out := make([]byte, 0, len(sizes)*3)
	for _, n := range sizes {
		out = binary.AppendUvarint(out, uint64(n))
	}
	return out
}

func decodeSizes(raw []byte) ([]int64, error) {
	var out []int64
	for len(raw) > 0 {
		n, read := binary.Uvarint(raw)
		if read <= 0 {
			return nil, errors.New("corrupt chunk sizes")
		}
		out = append(out, int64(n))
		raw = raw[read:]
	}
	return out, nil
}
