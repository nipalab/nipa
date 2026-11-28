package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestStatBlob_Working(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "art/a.png", "\x89PNG\x00\x01")
	mtime := time.Unix(1700000000, 123456789)
	require.NoError(t, os.Chtimes(filepath.Join(root, "art", "a.png"), mtime, mtime))
	repo := newDiffStub()
	repo.initErr = errors.New("must not open the clone")
	d := NewDiff(nil, nil, repo)

	st, err := d.StatBlob(context.Background(), root, BlobRequest{Path: "./art//a.png"})
	require.NoError(t, err)
	require.True(t, st.Working())
	require.Equal(t, "art/a.png", st.Entry.Path)
	require.EqualValues(t, 6, st.Entry.SizeBytes)
	require.Equal(t, serverDomain.Hash{}, st.Entry.Hash)
	require.Equal(t, mtime.UnixNano(), st.ModTimeNS)
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(realRoot, "art", "a.png"), st.AbsPath)

	_, err = d.StatBlob(context.Background(), root, BlobRequest{Source: BlobWorking, Path: "missing.png"})
	require.ErrorIs(t, err, ErrFileMissing)
	_, err = d.StatBlob(context.Background(), root, BlobRequest{Path: "art"})
	requireCode(t, err, 400)
	_, err = d.StatBlob(context.Background(), root, BlobRequest{Path: "../x"})
	requireCode(t, err, 400)
}

func TestStatBlob_WorkingRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	if err := os.Symlink(outside, filepath.Join(root, "link.png")); err != nil {
		t.Skip("symlinks unsupported")
	}
	_, err := NewDiff(nil, nil, newDiffStub()).StatBlob(context.Background(), root, BlobRequest{Path: "link.png"})
	requireCode(t, err, 400)
}

func TestStatBlob_Snapshot(t *testing.T) {
	repo := newDiffStub()
	withSnapshotFile(t, repo, "a.txt", "hello\n")
	d := NewDiff(nil, nil, repo)

	st, err := d.StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.NoError(t, err)
	require.False(t, st.Working())
	require.EqualValues(t, 6, st.Entry.SizeBytes)
	require.Equal(t, repo.snapshot.Files[0].Hash, st.Entry.Hash)

	_, err = d.StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "b.txt"})
	require.ErrorIs(t, err, ErrFileMissing)

	repo.snapshotErr = errors.New("db gone")
	_, err = d.StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a.txt"})
	require.EqualError(t, err, "db gone")
}

func TestStatBlob_RevisionDownloadsNothing(t *testing.T) {
	content := "\x89PNG revision bytes"
	repo, client := revisionBlobFixture(t, content)
	d := NewDiff(diffAuth(t), client, repo)

	st, err := d.StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "hero.png"})
	require.NoError(t, err)
	require.NotEqual(t, serverDomain.Hash{}, st.Entry.Hash)
	require.EqualValues(t, len(content), st.Entry.SizeBytes)
	require.Empty(t, client.downloaded)

	read, err := readBlob(t, d, t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "hero.png"})
	require.NoError(t, err)
	require.Equal(t, read.entry.Hash, st.Entry.Hash)

	_, err = d.StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "nope.png"})
	require.ErrorIs(t, err, ErrFileMissing)
}

func TestStatBlob_Errors(t *testing.T) {
	repo := newDiffStub()
	repo.initErr = errors.New("no repo")
	_, err := NewDiff(nil, nil, repo).StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobSnapshot, Path: "a"})
	require.EqualError(t, err, "no repo")

	_, err = NewDiff(nil, nil, newDiffStub()).StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: "nope", Path: "a"})
	requireCode(t, err, 400)

	repo, client := revisionBlobFixture(t, "data")
	repo.configErr = errors.New("no config")
	_, err = NewDiff(diffAuth(t), client, repo).StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobRevision, Revision: "main", Path: "hero.png"})
	require.EqualError(t, err, "no config")

	_, err = NewDiff(nil, nil, &stubLocalRepo{}).StatBlob(context.Background(), t.TempDir(), BlobRequest{Source: BlobShelf, Revision: "x", Path: "a"})
	require.ErrorContains(t, err, "not supported")
}

func TestStatBlob_Shelf(t *testing.T) {
	ctx := context.Background()
	commit := snow.ID(7).Base36()
	fx := newShelfFixture(t, map[string]string{"hero.png": "\x00v1"})
	require.NoError(t, fx.lr.SaveCommit(commit, ""))
	writeRepoFile(t, fx.root, "hero.png", "\x00v22")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)

	revisionChunks := map[serverDomain.Hash][]byte{}
	client := &stubDiffClient{
		commits:  map[string]*clientDomain.CommitDetail{commit: {ID: commit, Tree: encodedTree(t, map[string]string{"hero.png": "\x00v1"}, revisionChunks)}},
		download: revisionChunks,
	}
	d := NewDiff(diffAuth(t), client, fx.lr)

	st, err := d.StatBlob(ctx, fx.root, BlobRequest{Source: BlobShelf, Revision: shelf.ID, Path: "hero.png"})
	require.NoError(t, err)
	require.EqualValues(t, 4, st.Entry.SizeBytes)

	base, err := d.StatBlob(ctx, fx.root, BlobRequest{Source: BlobShelfBase, Revision: shelf.ID, Path: "hero.png"})
	require.NoError(t, err)
	require.EqualValues(t, 3, base.Entry.SizeBytes)
	require.Empty(t, client.downloaded)

	_, err = d.StatBlob(ctx, fx.root, BlobRequest{Source: BlobShelf, Revision: "nope", Path: "hero.png"})
	require.Error(t, err)
}
