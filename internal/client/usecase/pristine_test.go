package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubContentDownloader struct {
	data       map[serverDomain.Hash][]byte
	connectErr error
	host       string
	calls      int
	scope      clientDomain.ChunkScope
	hashes     []serverDomain.Hash
}

func (s *stubContentDownloader) Connect(_ context.Context, host string) error {
	s.host = host
	return s.connectErr
}

func (s *stubContentDownloader) DownloadChunks(_ context.Context, scope clientDomain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	s.calls++
	s.scope = scope
	s.hashes = append(s.hashes, hashes...)
	for _, h := range hashes {
		if data, ok := s.data[h]; ok {
			if err := onChunk(h, data); err != nil {
				return err
			}
		}
	}
	return nil
}

type pristineFixture struct {
	root    string
	lr      *localrepo.LocalRepo
	content map[string]string
	chunks  map[string][]serverDomain.Chunk
	stored  map[serverDomain.Hash][]byte
}

func newPristineFixture(t *testing.T, files map[string]string) *pristineFixture {
	t.Helper()
	fx := &pristineFixture{
		root:    t.TempDir(),
		lr:      localrepo.NewLocalRepo(),
		content: files,
		chunks:  map[string][]serverDomain.Chunk{},
		stored:  map[serverDomain.Hash][]byte{},
	}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	tree := &serverDomain.TreeNode{Name: "root", Hash: serverDomain.Hash{1}}
	for name, content := range files {
		fileHash, chunks, stored, encoding := testEncodedFile(t, name, content)
		fx.chunks[name] = chunks
		var data []*serverDomain.ChunkData
		for h, b := range stored {
			fx.stored[h] = b
			data = append(data, &serverDomain.ChunkData{Hash: h, Data: b})
		}
		require.NoError(t, fx.lr.StoreChunks(data))
		tree.FileChildren = append(tree.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary([]byte(content)), Encoding: encoding, Hash: fileHash, Chunks: chunks,
		})
		require.NoError(t, os.WriteFile(filepath.Join(fx.root, name), []byte(content), 0o644))
	}
	require.NoError(t, fx.lr.SaveTree(tree))
	require.NoError(t, fx.lr.SaveCommit("42", ""))
	return fx
}

func (fx *pristineFixture) dropObjects(t *testing.T) {
	t.Helper()
	dir := filepath.Join(fx.root, ".nipa", "objects")
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.MkdirAll(dir, 0o755))
}

func (fx *pristineFixture) requireCached(t *testing.T, name string) {
	t.Helper()
	for _, c := range fx.chunks[name] {
		got, err := fx.lr.LoadChunk(c.Hash)
		require.NoError(t, err)
		require.Equal(t, fx.stored[c.Hash], got)
	}
}

func (fx *pristineFixture) snapshotFile(t *testing.T, name string) clientDomain.SnapshotFile {
	t.Helper()
	snap, err := fx.lr.Snapshot()
	require.NoError(t, err)
	for _, f := range snap.Files {
		if f.Path == name {
			return f
		}
	}
	t.Fatalf("%s not in snapshot", name)
	return clientDomain.SnapshotFile{}
}

func (fx *pristineFixture) workingCopy(t *testing.T, dl ContentDownloader) *WorkingCopy {
	t.Helper()
	wc, err := NewWorkingCopy(fx.lr, fx.root)
	require.NoError(t, err)
	if dl != nil {
		wc.WithDownloader(nil, dl)
	}
	return wc
}

func requireExecBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode-only changes need executable bits")
	}
}

func TestRestore_RecapturesModeOnlyChangeOffline(t *testing.T) {
	requireExecBits(t)
	fx := newPristineFixture(t, map[string]string{"a.bin": "\x00binary payload\x01"})
	fx.dropObjects(t)
	require.NoError(t, os.Chmod(filepath.Join(fx.root, "a.bin"), 0o755))
	dl := &stubContentDownloader{}

	require.NoError(t, fx.workingCopy(t, dl).Restore(context.Background(), []string{"a.bin"}))
	require.Zero(t, dl.calls)
	fx.requireCached(t, "a.bin")
	info, err := os.Stat(filepath.Join(fx.root, "a.bin"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestRestore_DownloadsModifiedFile(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "original text\n"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "edited\n")
	dl := &stubContentDownloader{data: fx.stored}

	require.NoError(t, fx.workingCopy(t, dl).Restore(context.Background(), []string{"a.txt"}))
	require.Equal(t, 1, dl.calls)
	require.Equal(t, "http://example.com", dl.host)
	require.Equal(t, clientDomain.ChunkScope{Org: "org", Project: "project", CommitIDs: []string{"42"}, Paths: []string{"a.txt"}}, dl.scope)
	require.Equal(t, "original text\n", string(readRepoFile(t, fx.root, "a.txt")))
}

func TestRestore_NoDownloaderIsUserError(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "original text\n"})
	fx.dropObjects(t)
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a.txt")))

	err := fx.workingCopy(t, nil).Restore(context.Background(), []string{"a.txt"})
	var userErr *clientDomain.Error
	require.ErrorAs(t, err, &userErr)
	require.Equal(t, 400, userErr.Code)
	require.Contains(t, err.Error(), "content not cached locally; connect to the server")
}

func TestReadBlob_SnapshotRecapturesOffline(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"notes.txt": "zstd compressed text\nline two\n"})
	require.Equal(t, chunker.EncodingZstd, fx.snapshotFile(t, "notes.txt").Encoding)
	fx.dropObjects(t)
	client := &stubDiffClient{}

	got, err := readBlob(t, NewDiff(nil, client, fx.lr), fx.root, BlobRequest{Path: "notes.txt", Source: BlobSnapshot})
	require.NoError(t, err)
	require.Equal(t, "zstd compressed text\nline two\n", string(got.content))
	require.Empty(t, client.downloaded)
	fx.requireCached(t, "notes.txt")
}

func TestReadBlob_SnapshotDownloadsModifiedFile(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "snapshot side\n"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "working side\n")
	client := &stubDiffClient{download: fx.stored}

	got, err := readBlob(t, NewDiff(diffAuth(t), client, fx.lr), fx.root, BlobRequest{Path: "a.txt", Source: BlobSnapshot})
	require.NoError(t, err)
	require.Equal(t, "snapshot side\n", string(got.content))
	require.NotEmpty(t, client.downloaded)
}

func TestReadBlob_SnapshotNoDownloader(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "snapshot side\n"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "working side\n")

	_, err := readBlob(t, NewDiff(nil, nil, fx.lr), fx.root, BlobRequest{Path: "a.txt", Source: BlobSnapshot})
	require.ErrorContains(t, err, "content not cached locally")
}

func workingDiffFile(t *testing.T, files []diff.FileDiff, path string) diff.FileDiff {
	t.Helper()
	for _, f := range files {
		if f.Change.Path == path {
			return f
		}
	}
	t.Fatalf("%s not in diff", path)
	return diff.FileDiff{}
}

func TestDiff_WorkingRecapturesModeOnlyChangeOffline(t *testing.T) {
	requireExecBits(t)
	fx := newPristineFixture(t, map[string]string{"run.sh": "echo hi\n"})
	fx.dropObjects(t)
	require.NoError(t, os.Chmod(filepath.Join(fx.root, "run.sh"), 0o755))
	client := &stubDiffClient{}

	files, err := NewDiff(nil, client, fx.lr).Run(context.Background(), fx.root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Empty(t, client.downloaded)
	fx.requireCached(t, "run.sh")
}

func TestDiff_WorkingDownloadsOldSide(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "old line\n"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "new line\n")
	client := &stubDiffClient{download: fx.stored}

	files, err := NewDiff(diffAuth(t), client, fx.lr).Run(context.Background(), fx.root, nil, DiffOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, client.downloaded)
	require.Equal(t, "old line\n", string(workingDiffFile(t, files, "a.txt").Old))
}

func TestDiff_WorkingNoDownloader(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "old line\n"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "new line\n")

	_, err := NewDiff(nil, nil, fx.lr).Run(context.Background(), fx.root, nil, DiffOptions{})
	require.ErrorContains(t, err, "content not cached locally")
}

func TestDiff_WorkingSkipsBinaryOldSide(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.bin": "\x00old\x01"})
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.bin", "\x00new\x02")

	files, err := NewDiff(nil, nil, fx.lr).Run(context.Background(), fx.root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)

	_, err = NewDiff(nil, nil, fx.lr).Run(context.Background(), fx.root, nil, DiffOptions{Binary: true})
	require.ErrorContains(t, err, "content not cached locally")
}

func TestEnsureSnapshotContent_StaleStatCacheFallsThroughToDownload(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "snapshot content\n"})
	f := fx.snapshotFile(t, "a.txt")
	path := filepath.Join(fx.root, "a.txt")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, fx.lr.SaveStatEntries(map[string]clientDomain.StatEntry{"a.txt": {
		SizeBytes: info.Size(), MtimeNS: info.ModTime().UnixNano(), Mode: 2, Hash: f.Hash, CachedAt: time.Now().UnixNano(),
	}}))
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "changed content!\n")
	later := info.ModTime().Add(time.Second)
	require.NoError(t, os.Chtimes(path, later, later))
	dl := &stubContentDownloader{data: fx.stored}

	p := pristine{root: fx.root, repo: fx.lr, client: dl}
	require.NoError(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{f}))
	require.Equal(t, 1, dl.calls)
	fx.requireCached(t, "a.txt")
}

func TestEnsureSnapshotContent_FreshMismatchSkipsRead(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "snapshot content\n"})
	f := fx.snapshotFile(t, "a.txt")
	writeFileAt(t, fx.root, "a.txt", "modified content\n")
	path := filepath.Join(fx.root, "a.txt")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, fx.lr.SaveStatEntries(map[string]clientDomain.StatEntry{"a.txt": {
		SizeBytes: info.Size(), MtimeNS: info.ModTime().UnixNano(), Mode: 2, Hash: serverDomain.Hash{9}, CachedAt: time.Now().Add(time.Second).UnixNano(),
	}}))
	fx.dropObjects(t)
	dl := &stubContentDownloader{data: fx.stored}

	p := pristine{root: fx.root, repo: fx.lr, client: dl}
	require.NoError(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{f}))
	require.Equal(t, 1, dl.calls)
	require.Equal(t, fx.chunks["a.txt"][0].Hash, dl.hashes[0])
}

func TestEnsureSnapshotContent_NothingMissing(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "cached\n"})
	p := pristine{root: fx.root, repo: fx.lr}
	require.NoError(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{fx.snapshotFile(t, "a.txt")}))
	require.NoError(t, p.ensureSnapshotContent(context.Background(), nil))
}

type failingPristineRepo struct {
	*localrepo.LocalRepo
	missingErr, statErr, storeErr, configErr, commitErr error
}

func (r *failingPristineRepo) MissingChunks(h []serverDomain.Hash) ([]serverDomain.Hash, error) {
	if r.missingErr != nil {
		return nil, r.missingErr
	}
	return r.LocalRepo.MissingChunks(h)
}

func (r *failingPristineRepo) LoadStatCache() (map[string]clientDomain.StatEntry, error) {
	if r.statErr != nil {
		return nil, r.statErr
	}
	return r.LocalRepo.LoadStatCache()
}

func (r *failingPristineRepo) StoreChunks(c []*serverDomain.ChunkData) error {
	if r.storeErr != nil {
		return r.storeErr
	}
	return r.LocalRepo.StoreChunks(c)
}

func (r *failingPristineRepo) LoadConfig() (*clientDomain.Config, error) {
	if r.configErr != nil {
		return nil, r.configErr
	}
	return r.LocalRepo.LoadConfig()
}

func (r *failingPristineRepo) LoadCommit() (*clientDomain.LocalCommit, error) {
	if r.commitErr != nil {
		return nil, r.commitErr
	}
	return r.LocalRepo.LoadCommit()
}

func TestEnsureSnapshotContent_Errors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]struct {
		repo   func(lr *localrepo.LocalRepo) *failingPristineRepo
		dl     *stubContentDownloader
		modify bool
	}{
		"missing": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, missingErr: boom}
		}},
		"stat": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, statErr: boom}
		}},
		"store": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, storeErr: boom}
		}},
		"config": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, configErr: boom}
		}, dl: &stubContentDownloader{}, modify: true},
		"commit": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, commitErr: boom}
		}, dl: &stubContentDownloader{}, modify: true},
		"connect": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo { return &failingPristineRepo{LocalRepo: lr} }, dl: &stubContentDownloader{connectErr: boom}, modify: true},
		"download": {repo: func(lr *localrepo.LocalRepo) *failingPristineRepo {
			return &failingPristineRepo{LocalRepo: lr, storeErr: boom}
		}, dl: &stubContentDownloader{}, modify: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newPristineFixture(t, map[string]string{"a.txt": "content\n"})
			f := fx.snapshotFile(t, "a.txt")
			fx.dropObjects(t)
			if tc.modify {
				writeFileAt(t, fx.root, "a.txt", "other\n")
			}
			if name == "download" {
				tc.dl.data = fx.stored
			}
			p := pristine{root: fx.root, repo: tc.repo(fx.lr)}
			if tc.dl != nil {
				p.client = tc.dl
			}
			require.ErrorIs(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{f}), boom)
		})
	}
}

func TestEnsureSnapshotContent_LoginRequired(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "content\n"})
	f := fx.snapshotFile(t, "a.txt")
	fx.dropObjects(t)
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a.txt")))
	auth := NewAuth(nil, &stubSecureStorage{loadErr: errors.New("no token")}, &stubUserInput{err: errors.New("no terminal")})

	p := pristine{root: fx.root, repo: fx.lr, auth: auth, client: &stubContentDownloader{}}
	require.Error(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{f}))
}

func TestEnsureSnapshotContent_SkipsNonRegularPath(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "content\n"})
	f := fx.snapshotFile(t, "a.txt")
	fx.dropObjects(t)
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a.txt")))
	require.NoError(t, os.Mkdir(filepath.Join(fx.root, "a.txt"), 0o755))
	dl := &stubContentDownloader{data: fx.stored}

	p := pristine{root: fx.root, repo: fx.lr, client: dl}
	require.NoError(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{f}))
	require.Equal(t, 1, dl.calls)
}

func writeFileAt(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestEnsureSnapshotContent_SharedChunkRecapturedByLaterFile(t *testing.T) {
	fx := newPristineFixture(t, map[string]string{"a.txt": "same content\n", "b.txt": "same content\n"})
	a, b := fx.snapshotFile(t, "a.txt"), fx.snapshotFile(t, "b.txt")
	require.Equal(t, a.Chunks, b.Chunks)
	fx.dropObjects(t)
	writeFileAt(t, fx.root, "a.txt", "edited\n")

	p := pristine{root: fx.root, repo: fx.lr}
	require.NoError(t, p.ensureSnapshotContent(context.Background(), []clientDomain.SnapshotFile{a, b}))
	fx.requireCached(t, "a.txt")
}
