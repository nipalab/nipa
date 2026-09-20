package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubDiffRepo struct {
	snapshot    *domain.Snapshot
	staged      []string
	chunks      map[serverDomain.Hash][]byte
	config      *domain.Config
	commit      *domain.LocalCommit
	missing     []serverDomain.Hash
	initErr     error
	configErr   error
	snapshotErr error
	stagedErr   error
	loadErr     error
	commitErr   error
	missingErr  error
	storeErr    error
	initTarget  string
}

func newDiffStub() *stubDiffRepo {
	return &stubDiffRepo{
		snapshot: &domain.Snapshot{},
		chunks:   make(map[serverDomain.Hash][]byte),
	}
}

func (s *stubDiffRepo) Init(target string) error {
	s.initTarget = target
	return s.initErr
}

func (s *stubDiffRepo) LoadConfig() (*domain.Config, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	if s.config != nil {
		return s.config, nil
	}
	return &domain.Config{Url: "http://example.com/org/project", Branch: "main"}, nil
}

func (s *stubDiffRepo) LoadCommit() (*domain.LocalCommit, error) {
	if s.commitErr != nil {
		return nil, s.commitErr
	}
	if s.commit != nil {
		return s.commit, nil
	}
	return &domain.LocalCommit{}, nil
}

func (s *stubDiffRepo) Snapshot() (*domain.Snapshot, error) {
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	return s.snapshot, nil
}

func (s *stubDiffRepo) ListStaged() ([]string, error) {
	return s.staged, s.stagedErr
}

func (s *stubDiffRepo) MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error) {
	if s.missingErr != nil {
		return nil, s.missingErr
	}
	if s.missing != nil {
		return s.missing, nil
	}
	return hashes, nil
}

func (s *stubDiffRepo) StoreChunks(chunks []*serverDomain.ChunkData) error {
	if s.storeErr != nil {
		return s.storeErr
	}
	for _, c := range chunks {
		s.chunks[c.Hash] = c.Data
	}
	return nil
}

func (s *stubDiffRepo) OpenChunk(hash serverDomain.Hash) (io.ReadCloser, error) {
	data, ok := s.chunks[hash]
	if !ok {
		return nil, errors.New("chunk not found in cache")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *stubDiffRepo) LoadChunk(hash serverDomain.Hash) ([]byte, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	data, ok := s.chunks[hash]
	if !ok {
		return nil, errors.New("chunk not found in cache")
	}
	return data, nil
}

func withSnapshotFile(t *testing.T, s *stubDiffRepo, path, content string) {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		s.chunks[c.Hash] = c.Data
	}
	s.snapshot.Files = append(s.snapshot.Files, domain.SnapshotFile{
		Path:      path,
		Hash:      chunker.FileHash(hashes),
		Mode:      2,
		IsBinary:  chunker.IsBinary([]byte(content)),
		SizeBytes: int64(len(content)),
		Chunks:    hashes,
	})
}

func TestDiff_Modified(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "old\n")
	writeRepoFile(t, root, "a.txt", "new\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []byte("old\n"), files[0].Old)
	require.Equal(t, []byte("new\n"), files[0].New)
	require.False(t, files[0].OldUnavailable)
	require.Equal(t, 2, files[0].Change.New.Mode)
}

func TestDiff_UnchangedIsSkipped(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "same\n")
	writeRepoFile(t, root, "a.txt", "same\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestDiff_AddedStaged(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	stub.staged = []string{"new.txt"}
	writeRepoFile(t, root, "new.txt", "hello\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Added, files[0].Change.Status)
	require.Equal(t, []byte("hello\n"), files[0].New)
}

func TestDiff_UntrackedIsIgnored(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	writeRepoFile(t, root, "loose.txt", "hello\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestDiff_Deleted(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "gone.txt", "bye\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Deleted, files[0].Change.Status)
	require.Equal(t, []byte("bye\n"), files[0].Old)
	require.False(t, files[0].OldUnavailable)
}

func TestDiff_ModeChange(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "run.sh", "echo\n")
	writeRepoFile(t, root, "run.sh", "echo\n")
	require.NoError(t, os.Chmod(root+"/run.sh", 0o755))

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, 2, files[0].Change.Old.Mode)
	require.Equal(t, 3, files[0].Change.New.Mode)
}

func TestDiff_PathFilter(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "a\n")
	withSnapshotFile(t, stub, "dir/b.txt", "b\n")
	writeRepoFile(t, root, "a.txt", "A\n")
	writeRepoFile(t, root, "dir/b.txt", "B\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Paths: []string{"dir"}})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "dir/b.txt", files[0].Change.Path)
}

func TestDiff_StagedFilter(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	stub.staged = []string{"a.txt"}
	withSnapshotFile(t, stub, "a.txt", "a\n")
	withSnapshotFile(t, stub, "b.txt", "b\n")
	writeRepoFile(t, root, "a.txt", "A\n")
	writeRepoFile(t, root, "b.txt", "B\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Staged: true})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "a.txt", files[0].Change.Path)
}

func TestDiff_StatusFilter(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	stub.staged = []string{"new.txt"}
	withSnapshotFile(t, stub, "a.txt", "a\n")
	writeRepoFile(t, root, "a.txt", "A\n")
	writeRepoFile(t, root, "new.txt", "n\n")

	onlyAdded := map[diff.Status]bool{diff.Added: true}
	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Filter: onlyAdded})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Added, files[0].Change.Status)

	onlyDeleted := map[diff.Status]bool{diff.Deleted: true}
	files, err = NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Filter: onlyDeleted})
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestDiff_Reverse(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	stub.staged = []string{"new.txt"}
	writeRepoFile(t, root, "new.txt", "n\n")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{Reverse: true})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Deleted, files[0].Change.Status)
	require.Equal(t, []byte("n\n"), files[0].Old)
}

func TestDiff_OldContentUnavailable(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "a.txt", "old\n")
	writeRepoFile(t, root, "a.txt", "new\n")
	stub.loadErr = errors.New("missing")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].OldUnavailable)
}

func TestDiff_Binary(t *testing.T) {
	root := t.TempDir()
	stub := newDiffStub()
	withSnapshotFile(t, stub, "img.bin", "old\x00data")
	writeRepoFile(t, root, "img.bin", "new\x00data")

	files, err := NewDiff(nil, nil, stub).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].Change.New.IsBinary)
	require.True(t, files[0].Change.Old.IsBinary)
}

func TestDiff_EmptyRepo(t *testing.T) {
	files, err := NewDiff(nil, nil, newDiffStub()).Run(context.Background(), t.TempDir(), nil, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestDiff_InitError(t *testing.T) {
	stub := newDiffStub()
	stub.initErr = errors.New("nope")
	_, err := NewDiff(nil, nil, stub).Run(context.Background(), t.TempDir(), nil, DiffOptions{})
	require.Error(t, err)
}

func TestDiff_SnapshotError(t *testing.T) {
	stub := newDiffStub()
	stub.snapshotErr = errors.New("nope")
	_, err := NewDiff(nil, nil, stub).Run(context.Background(), t.TempDir(), nil, DiffOptions{})
	require.Error(t, err)
}

func TestDiff_ListStagedError(t *testing.T) {
	stub := newDiffStub()
	stub.stagedErr = errors.New("nope")
	_, err := NewDiff(nil, nil, stub).Run(context.Background(), t.TempDir(), nil, DiffOptions{})
	require.Error(t, err)
}
