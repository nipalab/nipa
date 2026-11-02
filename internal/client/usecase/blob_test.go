package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type blobResult struct {
	entry   diff.Entry
	content []byte
}

func readBlob(t *testing.T, d *Diff, root string, req BlobRequest) (blobResult, error) {
	t.Helper()
	var out blobResult
	err := d.ReadBlob(context.Background(), root, req, func(e diff.Entry, r io.Reader) error {
		out.entry = e
		var err error
		out.content, err = io.ReadAll(r)
		return err
	})
	return out, err
}

func requireCode(t *testing.T, err error, code int) {
	t.Helper()
	var domErr *clientDomain.Error
	require.True(t, errors.As(err, &domErr), "want domain error, got %v", err)
	require.Equal(t, code, domErr.Code)
}

func TestReadBlob_Working(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "src/a.txt", "hello\n")
	writeRepoFile(t, root, "art/a.png", "\x89PNG\x00\x01")
	d := NewDiff(nil, nil, newDiffStub())

	got, err := readBlob(t, d, root, BlobRequest{Path: "./src//a.txt"})
	require.NoError(t, err)
	require.Equal(t, "src/a.txt", got.entry.Path)
	require.EqualValues(t, 6, got.entry.SizeBytes)
	require.False(t, got.entry.IsBinary)
	require.Equal(t, "hello\n", string(got.content))

	got, err = readBlob(t, d, root, BlobRequest{Source: BlobWorking, Path: "art/a.png"})
	require.NoError(t, err)
	require.True(t, got.entry.IsBinary)

	_, err = readBlob(t, d, root, BlobRequest{Path: "missing.txt"})
	requireCode(t, err, 404)

	_, err = readBlob(t, d, root, BlobRequest{Path: "src"})
	requireCode(t, err, 400)
}

func TestReadBlob_WorkingRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unsupported")
	}
	writeRepoFile(t, root, "real.txt", "inside")
	require.NoError(t, os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "inner.txt")))
	d := NewDiff(nil, nil, newDiffStub())

	_, err := readBlob(t, d, root, BlobRequest{Path: "link.txt"})
	requireCode(t, err, 400)

	got, err := readBlob(t, d, root, BlobRequest{Path: "inner.txt"})
	require.NoError(t, err)
	require.Equal(t, "inside", string(got.content))

	_, err = readBlob(t, d, filepath.Join(root, "nope"), BlobRequest{Path: "real.txt"})
	require.Error(t, err)
}

func TestReadBlob_PathValidation(t *testing.T) {
	d := NewDiff(nil, nil, newDiffStub())
	for _, p := range []string{"", "  ", "/etc/passwd", "..", "../x", "a/../../x", ".", ".nipa", ".nipa/config"} {
		_, err := readBlob(t, d, t.TempDir(), BlobRequest{Path: p})
		requireCode(t, err, 400)
	}
}

func TestReadBlob_InitAndSourceErrors(t *testing.T) {
	repo := newDiffStub()
	repo.initErr = errors.New("no repo")
	_, err := readBlob(t, NewDiff(nil, nil, repo), t.TempDir(), BlobRequest{Path: "a"})
	require.EqualError(t, err, "no repo")

	_, err = readBlob(t, NewDiff(nil, nil, newDiffStub()), t.TempDir(), BlobRequest{Source: "nope", Path: "a"})
	requireCode(t, err, 400)
}

func TestReadBlob_Snapshot(t *testing.T) {
	repo := newDiffStub()
	big := strings.Repeat("line of text that repeats\n", 20000)
	withSnapshotFile(t, repo, "a.txt", big)
	d := NewDiff(nil, nil, repo)

	got, err := readBlob(t, d, t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.NoError(t, err)
	require.Greater(t, len(got.entry.ChunkHashes), 1)
	require.Equal(t, big, string(got.content))

	_, err = readBlob(t, d, t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "b.txt"})
	requireCode(t, err, 404)

	repo.snapshotErr = errors.New("db gone")
	_, err = readBlob(t, d, t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.EqualError(t, err, "db gone")
}

func TestReadBlob_SnapshotDecodesZstd(t *testing.T) {
	repo := newDiffStub()
	content := strings.Repeat("compressible ", 1000)
	encoding, chunks, err := chunker.EncodeBytes([]byte(content), "a.txt", false, chunker.EncodingZstd)
	require.NoError(t, err)
	require.Equal(t, chunker.EncodingZstd, encoding)
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		repo.chunks[c.Hash] = c.Data
	}
	repo.snapshot.Files = append(repo.snapshot.Files, clientDomain.SnapshotFile{
		Path: "a.txt", Encoding: encoding, Chunks: hashes, SizeBytes: int64(len(content)),
	})

	got, err := readBlob(t, NewDiff(nil, nil, repo), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.NoError(t, err)
	require.Equal(t, content, string(got.content))
	require.Equal(t, 2, got.entry.Mode)
}

func TestReadBlob_SnapshotChunkErrors(t *testing.T) {
	repo := newDiffStub()
	withSnapshotFile(t, repo, "a.txt", "hello\n")
	repo.loadErr = errors.New("object missing")
	_, err := readBlob(t, NewDiff(nil, nil, repo), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.ErrorContains(t, err, "object missing")

	bad := newDiffStub()
	h := serverDomain.Hash{9}
	bad.chunks[h] = []byte("not zstd")
	bad.snapshot.Files = append(bad.snapshot.Files, clientDomain.SnapshotFile{Path: "z.txt", Encoding: chunker.EncodingZstd, Chunks: []serverDomain.Hash{h}})
	_, err = readBlob(t, NewDiff(nil, nil, bad), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "z.txt"})
	require.ErrorContains(t, err, "decode chunk")
}

func TestReadBlob_CallbackErrorPropagates(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "x")
	err := NewDiff(nil, nil, newDiffStub()).ReadBlob(context.Background(), root, BlobRequest{Path: "a.txt"}, func(diff.Entry, io.Reader) error {
		return errors.New("viewer closed")
	})
	require.EqualError(t, err, "viewer closed")
}

func revisionBlobFixture(t *testing.T, content string) (*stubDiffRepo, *stubDiffClient) {
	t.Helper()
	commitID := snow.ID(7).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{"main": {Name: "main", CommitID: ptrID(snow.ID(7))}},
		commits:  map[string]*clientDomain.CommitDetail{commitID: {ID: commitID, Tree: fileTree(t, "hero.png", content)}},
		download: contentChunks(t, content),
	}
	return repo, client
}

func TestReadBlob_RevisionDownloadsMissingChunks(t *testing.T) {
	content := "\x89PNG revision bytes"
	repo, client := revisionBlobFixture(t, content)

	got, err := readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "hero.png"})
	require.NoError(t, err)
	require.Equal(t, content, string(got.content))
	require.NotEmpty(t, client.downloaded)
	require.Equal(t, "http://example.com", client.connectHost)

	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "nope.png"})
	requireCode(t, err, 404)

	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "ghost", Path: "hero.png"})
	requireCode(t, err, 400)
}

func TestReadBlob_RevisionErrors(t *testing.T) {
	req := BlobRequest{Source: BlobRevision, Revision: "main", Path: "hero.png"}

	repo, client := revisionBlobFixture(t, "data")
	repo.configErr = errors.New("no config")
	_, err := readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), req)
	require.EqualError(t, err, "no config")

	repo, client = revisionBlobFixture(t, "data")
	repo.config = &clientDomain.Config{Url: "::bad"}
	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), req)
	require.Error(t, err)

	repo, client = revisionBlobFixture(t, "data")
	client.connectErr = errors.New("offline")
	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), req)
	require.EqualError(t, err, "offline")

	repo, client = revisionBlobFixture(t, "data")
	repo.missingErr = errors.New("stat failed")
	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), req)
	require.EqualError(t, err, "stat failed")

	repo, client = revisionBlobFixture(t, "data")
	client.downloadErr = errors.New("download failed")
	_, err = readBlob(t, NewDiff(diffAuth(t), client, repo), t.TempDir(), req)
	require.ErrorContains(t, err, "download failed")
}

func TestBlobReaderSmallBuffer(t *testing.T) {
	repo := newDiffStub()
	withSnapshotFile(t, repo, "a.txt", "abcdef")
	entry := snapshotEntries(repo.snapshot, DiffOptions{}, nil)["a.txt"]
	r := &blobReader{load: repo.LoadChunk, entry: entry}
	var out bytes.Buffer
	buf := make([]byte, 2)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.Equal(t, "abcdef", out.String())
}

func TestReadBlob_WorkingRefusesMetadataAlias(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, ".nipa/config", "{}")
	if err := os.Symlink(filepath.Join(root, ".nipa", "config"), filepath.Join(root, "evil")); err != nil {
		t.Skip("symlinks unsupported")
	}
	d := NewDiff(nil, nil, newDiffStub())

	_, err := readBlob(t, d, root, BlobRequest{Path: "evil"})
	requireCode(t, err, 400)

	_, err = readBlob(t, d, root, BlobRequest{Path: ".NIPA/config"})
	requireCode(t, err, 400)
}
