package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubUpdateClient struct {
	connectHost      string
	connectErr       error
	org, project     string
	branch, treePath string
	manifest         *serverDomain.TreeNode
	manifestErr      error
	downloadHashes   []serverDomain.Hash
	downloadData     map[serverDomain.Hash][]byte
	downloadErr      error
}

func (s *stubUpdateClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubUpdateClient) GetTreeNodeManifest(_ context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error) {
	s.org, s.project, s.branch, s.treePath = org, project, branch, path
	return s.manifest, s.manifestErr
}

func (s *stubUpdateClient) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, onChunk ...func(h serverDomain.Hash, data []byte)) (map[serverDomain.Hash][]byte, error) {
	s.downloadHashes = hashes
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	for _, h := range hashes {
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](h, s.downloadData[h])
		}
	}
	return s.downloadData, nil
}

func newTestUpdate(t *testing.T, local updateLocalRepo, client updateClient) *Update {
	t.Helper()
	auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
	return NewUpdate(auth, client, local)
}

func readRepoFile(t *testing.T, root, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	require.NoError(t, err)
	return data
}

func TestUpdate_Run_MaterializesChangedFile(t *testing.T) {
	root := t.TempDir()
	fileHash, chunks, err := chunkFile([]byte("new content"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      2, // FILE_MODE_READ_WRITE server representation
				SizeBytes: int64(len("new content")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: map[serverDomain.Hash][]byte{chunks[0].Hash: []byte("new content")},
	}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))

	require.Equal(t, root, local.initTarget)
	require.Equal(t, "example.com", client.connectHost)
	require.Equal(t, "org", client.org)
	require.Equal(t, "project", client.project)
	require.Equal(t, "main", client.branch)
	require.Equal(t, "", client.treePath, "update works on the whole tree, never a subpath")

	require.Equal(t, []serverDomain.Hash{chunks[0].Hash}, client.downloadHashes)
	require.Equal(t, []byte("new content"), local.storedChunks[chunks[0].Hash])

	got := readRepoFile(t, root, "a.txt")
	require.Equal(t, "new content", string(got))
	fi, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode(), "the server mode enum must be mapped back to real file permissions")
	require.NotNil(t, local.tree, "snapshot must be refreshed from the server tree")
}

func TestUpdate_Run_NestedDirectory(t *testing.T) {
	root := t.TempDir()
	_, chunks, err := chunkFile([]byte("nested"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		missingChunks: nil,
		storedChunks:  map[serverDomain.Hash][]byte{chunks[0].Hash: []byte("nested")},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			TreeChildren: []*serverDomain.TreeNode{{
				Name: "sub",
				FileChildren: []*serverDomain.File{{
					Name:      "inner.txt",
					Mode:      0o644,
					SizeBytes: int64(len("nested")),
					Chunks:    chunks,
				}},
			}},
		},
	}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	require.Equal(t, "nested", string(readRepoFile(t, root, "sub/inner.txt")))
}

func TestUpdate_Run_SkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	fileHash, chunks, err := chunkFile([]byte("same"))
	require.NoError(t, err)
	writeRepoFile(t, root, "a.txt", "same")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path: "a.txt",
			Hash: fileHash,
			Mode: 0o644,
		}}},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len("same")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
	}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	require.Nil(t, client.downloadHashes, "unchanged files must not trigger a download")
	require.Equal(t, "same", string(readRepoFile(t, root, "a.txt")))
	require.NotNil(t, local.tree)
}

func TestUpdate_Run_RestoresMissingWorkingFile(t *testing.T) {
	root := t.TempDir()
	fileHash, chunks, err := chunkFile([]byte("same"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig:   &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:     &domain.Snapshot{TreeHash: "h", Files: []domain.SnapshotFile{{Path: "a.txt", Hash: fileHash, Mode: 0o644}}},
		storedChunks: map[serverDomain.Hash][]byte{chunks[0].Hash: []byte("same")},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len("same")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
	}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	require.Equal(t, "same", string(readRepoFile(t, root, "a.txt")), "a missing working copy file must be restored")
	require.NotNil(t, local.tree)
}

func TestUpdate_Run_RemovesDeletedFile(t *testing.T) {
	root := t.TempDir()
	fileHash, _, err := chunkFile([]byte("stale"))
	require.NoError(t, err)
	writeRepoFile(t, root, "stale.txt", "stale")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path: "stale.txt",
			Hash: fileHash,
			Mode: 0o644,
		}}},
	}
	client := &stubUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	_, err = os.Stat(filepath.Join(root, "stale.txt"))
	require.Error(t, err, "file removed on the server must be removed from the working copy")
	require.NotNil(t, local.tree)
}

func TestUpdate_Run_KeepsLocallyModifiedFile(t *testing.T) {
	root := t.TempDir()
	fileHash, _, err := chunkFile([]byte("committed"))
	require.NoError(t, err)
	writeRepoFile(t, root, "stale.txt", "local edits")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path: "stale.txt",
			Hash: fileHash,
			Mode: 0o644,
		}}},
	}
	client := &stubUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	require.Equal(t, "local edits", string(readRepoFile(t, root, "stale.txt")), "a file with local edits must be left alone")
	require.NotNil(t, local.tree)
}

func TestUpdate_Run_RemovesMissingBaseFile(t *testing.T) {
	root := t.TempDir()
	fileHash, _, err := chunkFile([]byte("committed"))
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path: "gone.txt",
			Hash: fileHash,
			Mode: 0o644,
		}}},
	}
	client := &stubUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
}

func TestUpdate_Run_EmptyRemote(t *testing.T) {
	root := t.TempDir()
	fileHash, _, err := chunkFile([]byte("stale"))
	require.NoError(t, err)
	writeRepoFile(t, root, "stale.txt", "stale")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path: "stale.txt",
			Hash: fileHash,
			Mode: 0o644,
		}}},
	}
	client := &stubUpdateClient{manifest: nil}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), root))
	require.Nil(t, local.tree, "an empty remote tree must clear the local snapshot")
	_, err = os.Stat(filepath.Join(root, "stale.txt"))
	require.Error(t, err)
}

func TestUpdate_Run_Error_SubdirClone(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project/src", Branch: "main"},
	}
	updater := newTestUpdate(t, local, &stubUpdateClient{})

	err := updater.Run(context.Background(), t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "subdirectory clone")
}

func TestUpdate_Run_Error_LoadConfigFails(t *testing.T) {
	wantErr := errors.New("config missing")
	updater := newTestUpdate(t, &stubLocalRepo{configLoadErr: wantErr}, &stubUpdateClient{})

	err := updater.Run(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestUpdate_Run_Error_ManifestFails(t *testing.T) {
	wantErr := errors.New("manifest failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
	}
	client := &stubUpdateClient{manifestErr: wantErr}
	updater := newTestUpdate(t, local, client)

	err := updater.Run(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestUpdate_Run_Error_DownloadFails(t *testing.T) {
	wantErr := errors.New("download failed")
	_, chunks, err := chunkFile([]byte("data"))
	require.NoError(t, err)
	fileHash := chunker.FileHash([]serverDomain.Hash{chunks[0].Hash})

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len("data")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadErr: wantErr,
	}
	updater := newTestUpdate(t, local, client)

	err = updater.Run(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestUpdate_Run_Error_ChunkMissingFromResponse(t *testing.T) {
	_, chunks, err := chunkFile([]byte("data"))
	require.NoError(t, err)
	fileHash := chunker.FileHash([]serverDomain.Hash{chunks[0].Hash})

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len("data")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: map[serverDomain.Hash][]byte{},
	}
	updater := newTestUpdate(t, local, client)

	err = updater.Run(context.Background(), t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not return chunk")
}

func TestUpdate_Run_Error_ChunkHashMismatch(t *testing.T) {
	_, chunks, err := chunkFile([]byte("data"))
	require.NoError(t, err)
	fileHash := chunker.FileHash([]serverDomain.Hash{chunks[0].Hash})

	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len("data")),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: map[serverDomain.Hash][]byte{chunks[0].Hash: []byte("corrupted")},
	}
	updater := newTestUpdate(t, local, client)

	err = updater.Run(context.Background(), t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash mismatch")
}

func TestUpdate_Run_Error_SaveTreeFails(t *testing.T) {
	wantErr := errors.New("save tree failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		treeErr:    wantErr,
	}
	client := &stubUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	updater := newTestUpdate(t, local, client)

	err := updater.Run(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}

func TestUpdate_Run_Error_LoginRequired(t *testing.T) {
	wantErr := errors.New("login failed")
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	input := &stubUserInput{username: "apin", password: "secret"}
	executor := &stubLoginExecutor{usernameErr: wantErr}
	auth := NewAuth(executor, storage, input)
	updater := NewUpdate(auth, &stubUpdateClient{}, &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
	})

	err := updater.Run(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
}
