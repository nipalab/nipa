package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestUpdate_Run_RecordsStatFingerprint(t *testing.T) {
	root := t.TempDir()
	fileHash, chunks, stored, fileEncoding := testEncodedFile(t, "a.txt", "new content")
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
				SizeBytes: int64(len("new content")),
				Encoding:  fileEncoding,
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: stored,
	}

	require.NoError(t, newTestUpdate(t, local, client).Run(context.Background(), root))

	entry, ok := local.savedStats["a.txt"]
	require.True(t, ok, "materialized files must be fingerprinted")
	require.Equal(t, fileHash, entry.Hash)
	require.Equal(t, int64(len("new content")), entry.SizeBytes)
	require.Equal(t, 2, entry.Mode)

	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.True(t, statMatches(entry, info), "the fingerprint must satisfy the status fast path")
}

func TestUpdate_Run_FingerprintMakesStatusStatOnly(t *testing.T) {
	root := t.TempDir()
	fileHash, chunks, stored, fileEncoding := testEncodedFile(t, "a.txt", "new content")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path:      "a.txt",
			Hash:      fileHash,
			Mode:      2,
			SizeBytes: int64(len("new content")),
			Encoding:  fileEncoding,
		}}},
		missingChunks: []serverDomain.Hash{chunks[0].Hash},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      2,
				SizeBytes: int64(len("new content")),
				Encoding:  fileEncoding,
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: stored,
	}
	require.NoError(t, newTestUpdate(t, local, client).Run(context.Background(), root))

	wc := newWorkingCopy(t, local, root)
	calls := countHashes(wc)
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified)
	require.Equal(t, 0, calls.Load(), "a file materialized by update must not be rehashed by the next status")
}

func TestUpdate_Run_DoesNotFingerprintUnverifiedFiles(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "dirty local edit")
	fileHash, chunks, _, fileEncoding := testEncodedFile(t, "a.txt", "base content")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
			Path:      "a.txt",
			Hash:      fileHash,
			Mode:      2,
			SizeBytes: int64(len("base content")),
			Encoding:  fileEncoding,
		}}},
	}
	client := &stubUpdateClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      2,
				SizeBytes: int64(len("base content")),
				Encoding:  fileEncoding,
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
	}

	require.NoError(t, newTestUpdate(t, local, client).Run(context.Background(), root))

	require.NotContains(t, local.savedStats, "a.txt",
		"a skipped file's content is not verified, so it must not be fingerprinted")

	wc := newWorkingCopy(t, local, root)
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, st.Modified, "the dirty file must be detected by hashing")
}

func TestPush_Run_RecordsStatFingerprint(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	fileHash, _, _, err := chunkFile("file.txt", []byte("hello world"), "")
	require.NoError(t, err)

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
	}
	client := &stubPushClient{
		pushResult: &serverDomain.PushResult{CommitID: 1, CommitHash: fileHash, TreeHash: fileHash},
		manifest:   &serverDomain.TreeNode{Name: "root"},
	}

	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "add a.txt"))

	entry, ok := local.savedStats["a.txt"]
	require.True(t, ok, "pushed files must be fingerprinted so the next status can trust them")
	require.Equal(t, fileHash, entry.Hash)

	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.True(t, statMatches(entry, info))
}

func TestApplyThreeWay_KeepTheirsRecordsStatFingerprint(t *testing.T) {
	root := t.TempDir()
	content := "theirs content\n"
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)

	local := &stubLocalRepo{storedChunks: map[serverDomain.Hash][]byte{}}
	hashes := make([]serverDomain.Hash, len(chunks))
	sizes := make([]int64, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		sizes[i] = int64(len(c.Data))
		local.storedChunks[c.Hash] = c.Data
	}
	theirs := merge.File{
		Path:        "a.txt",
		Mode:        2,
		SizeBytes:   int64(len(content)),
		Encoding:    chunker.EncodingRaw,
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}
	res := &merge.Result{Entries: map[string]merge.Entry{
		"a.txt": {Decision: merge.KeepTheirs, Theirs: theirs},
	}}

	applied, err := applyThreeWay(context.Background(), &stubMergeClient{}, local, root,
		map[string]merge.File{}, nil, res, domain.ChunkScope{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, applied.Staged)

	entry, ok := local.savedStats["a.txt"]
	require.True(t, ok, "merge materialization must be fingerprinted")
	require.Equal(t, theirs.Hash, entry.Hash)

	info, err := os.Stat(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.True(t, statMatches(entry, info))
}
