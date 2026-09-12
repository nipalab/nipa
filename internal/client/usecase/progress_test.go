package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type progressStart struct {
	objects int
	bytes   int64
}

type progressCount struct {
	objects int
	bytes   int64
}

type stubProgress struct {
	starts    []progressStart
	counts    []progressCount
	endCalled bool
}

func (s *stubProgress) DownloadStart(objects int, bytes int64) {
	s.starts = append(s.starts, progressStart{objects: objects, bytes: bytes})
}

func (s *stubProgress) DownloadProgress(objects int, bytes int64) {
	s.counts = append(s.counts, progressCount{objects: objects, bytes: bytes})
}

func (s *stubProgress) DownloadEnd() {
	s.endCalled = true
}

func (s *stubProgress) UploadStart(objects int, bytes int64) {
	s.starts = append(s.starts, progressStart{objects: objects, bytes: bytes})
}

func (s *stubProgress) UploadProgress(objects int, bytes int64) {
	s.counts = append(s.counts, progressCount{objects: objects, bytes: bytes})
}

func (s *stubProgress) UploadEnd() {
	s.endCalled = true
}

func TestRepo_Clone_ReportsDownloadProgress(t *testing.T) {
	content := "real file bytes"
	chunked, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	chunkHash := chunked[0].Hash
	fileHash := chunker.FileHash([]serverDomain.Hash{chunkHash})

	manifest := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name:      "hello.txt",
			Mode:      2,
			SizeBytes: int64(len(content)),
			Hash:      fileHash,
			Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: int64(len(content))}},
		}},
	}
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	prog := &stubProgress{}
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      manifest,
		download:      map[serverDomain.Hash][]byte{chunkHash: []byte(content)},
	}, &stubLocalRepo{missingChunks: []serverDomain.Hash{chunkHash}})

	require.NoError(t, repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", t.TempDir(), prog))

	require.Equal(t, []progressStart{{objects: 1, bytes: int64(len(content))}}, prog.starts)
	require.Equal(t, []progressCount{{objects: 1, bytes: int64(len(content))}}, prog.counts)
	require.True(t, prog.endCalled, "DownloadEnd must fire after a non-empty download")
}

func TestRepo_Clone_NoProgressWhenEverythingIsLocal(t *testing.T) {
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	prog := &stubProgress{}
	repo := NewRepo(auth, &stubRepoInterface{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		manifest:      &serverDomain.TreeNode{},
	}, &stubLocalRepo{})

	require.NoError(t, repo.Clone(context.Background(), "http://example.com/org/project", "example.com", "org", "project", "main", "", t.TempDir(), prog))

	require.Empty(t, prog.starts, "an empty tree must not report any download work")
	require.False(t, prog.endCalled)
}

func TestUpdate_Run_ReportsDownloadProgress(t *testing.T) {
	content := "update bytes"
	fileHash, chunks, err := chunkFile([]byte(content))
	require.NoError(t, err)

	prog := &stubProgress{}
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
				Mode:      2,
				SizeBytes: int64(len(content)),
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: map[serverDomain.Hash][]byte{chunks[0].Hash: []byte(content)},
	}
	updater := newTestUpdate(t, local, client)

	require.NoError(t, updater.Run(context.Background(), t.TempDir(), prog))

	require.Equal(t, []progressStart{{objects: 1, bytes: int64(len(content))}}, prog.starts)
	require.Equal(t, []progressCount{{objects: 1, bytes: int64(len(content))}}, prog.counts)
	require.True(t, prog.endCalled)
}
