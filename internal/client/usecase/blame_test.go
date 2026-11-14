package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubBlameClient struct {
	*stubDiffClient
	logs        map[string][]*serverDomain.CommitLogEntry
	trees       map[string]*serverDomain.TreeNode
	logErr      error
	manifestErr error
	logRevs     []string
	dirs        []string
	reads       []string
}

func (s *stubBlameClient) GetPathCommitLog(_ context.Context, _, _, rev, _ string, limit int) ([]*serverDomain.CommitLogEntry, error) {
	s.logRevs = append(s.logRevs, rev)
	if limit != BlameMaxCommits {
		return nil, fmt.Errorf("unexpected limit %d", limit)
	}
	return s.logs[rev], s.logErr
}

func (s *stubBlameClient) GetCommitDirManifest(_ context.Context, _, _, commitID, dir string) (*serverDomain.TreeNode, error) {
	s.reads = append(s.reads, commitID)
	s.dirs = append(s.dirs, dir)
	if s.manifestErr != nil {
		return nil, s.manifestErr
	}
	tree, ok := s.trees[commitID]
	if !ok {
		return nil, &clientDomain.Error{Code: 404, Message: "path not found"}
	}
	return tree, nil
}

// blameTree builds a commit tree holding files (slash paths) and records
// their encoded chunks in stored.
func blameTree(t *testing.T, files map[string]string, stored map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
	t.Helper()
	root := &serverDomain.TreeNode{Name: "root"}
	for p, content := range files {
		node := root
		parts := strings.Split(p, "/")
		for _, dir := range parts[:len(parts)-1] {
			var next *serverDomain.TreeNode
			for _, child := range node.TreeChildren {
				if child.Name == dir {
					next = child
				}
			}
			if next == nil {
				next = &serverDomain.TreeNode{Name: dir}
				node.TreeChildren = append(node.TreeChildren, next)
			}
			node = next
		}
		name := parts[len(parts)-1]
		fileHash, chunks, data, encoding := testEncodedFile(t, name, content)
		for h, b := range data {
			stored[h] = b
		}
		node.FileChildren = append(node.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary([]byte(content)), Encoding: encoding, Hash: fileHash, Chunks: chunks,
		})
	}
	return root
}

type blameFixture struct {
	root   string
	lr     *localrepo.LocalRepo
	client *stubBlameClient
	stored map[serverDomain.Hash][]byte
}

func newBlameFixture(t *testing.T) *blameFixture {
	t.Helper()
	fx := &blameFixture{root: t.TempDir(), lr: localrepo.NewLocalRepo(), stored: map[serverDomain.Hash][]byte{}}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	fx.client = &stubBlameClient{
		stubDiffClient: &stubDiffClient{download: fx.stored, branches: map[string]*serverDomain.Branch{}, tags: map[string]*clientDomain.Tag{}},
		logs:           map[string][]*serverDomain.CommitLogEntry{},
		trees:          map[string]*serverDomain.TreeNode{},
	}
	return fx
}

func (fx *blameFixture) commit(t *testing.T, id int64, parent *int64, author string, files map[string]string) *serverDomain.CommitLogEntry {
	t.Helper()
	cid := snow.ID(id)
	fx.client.trees[cid.Base36()] = blameTree(t, files, fx.stored)
	entry := &serverDomain.CommitLogEntry{
		Commit:      serverDomain.Commit{ID: cid, Hash: serverDomain.Hash{byte(id)}, Message: fmt.Sprintf("commit %d", id), CreatedAt: time.Unix(id, 0).UTC()},
		AuthorName:  author,
		AuthorEmail: strings.ToLower(author) + "@example.com",
	}
	if parent != nil {
		p := snow.ID(*parent)
		entry.Parent1ID = &p
	}
	return entry
}

func (fx *blameFixture) pin(t *testing.T, id int64) {
	t.Helper()
	require.NoError(t, fx.lr.SaveCommit(snow.ID(id).Base36(), ""))
}

func (fx *blameFixture) run(t *testing.T, path, rev string) (*BlameResult, error) {
	t.Helper()
	return NewBlame(diffAuth(t), fx.client, fx.lr).Run(context.Background(), fx.root, path, rev, nil)
}

func int64p(v int64) *int64 { return &v }

func blameSummary(res *BlameResult) []string {
	out := make([]string, len(res.Lines))
	for i, l := range res.Lines {
		switch {
		case l.Uncommitted:
			out[i] = l.Text + "@new"
		case l.CommitID == "":
			out[i] = l.Text + "@?"
		default:
			id, _ := snow.ParseBase36(l.CommitID)
			out[i] = fmt.Sprintf("%s@%d", l.Text, id)
		}
	}
	return out
}

// history: 1 adds a,b,c; 2 changes b and adds d; 3 changes c.
func threeCommits(t *testing.T, fx *blameFixture, path string) {
	t.Helper()
	c1 := fx.commit(t, 1, nil, "Ana", map[string]string{path: "a\nb\nc\n"})
	c2 := fx.commit(t, 2, int64p(1), "Ben", map[string]string{path: "a\nB\nc\nd\n"})
	c3 := fx.commit(t, 3, int64p(2), "Cy", map[string]string{path: "a\nB\nC\nd\n"})
	fx.client.logs[snow.ID(3).Base36()] = []*serverDomain.CommitLogEntry{c3, c2, c1}
	fx.client.logs[snow.ID(2).Base36()] = []*serverDomain.CommitLogEntry{c2, c1}
	fx.client.branches["main"] = &serverDomain.Branch{Name: "main", CommitID: ptrID(snow.ID(3))}
}

func TestBlame_WorkingCopy(t *testing.T) {
	fx := newBlameFixture(t)
	threeCommits(t, fx, "src/a.txt")
	fx.pin(t, 3)
	writeRepoFile(t, fx.root, "src/a.txt", "a\r\nB\r\nC\r\nd\r\nnew\r\n")

	var calls [][2]int
	res, err := NewBlame(diffAuth(t), fx.client, fx.lr).Run(context.Background(), fx.root, "src/a.txt", "", func(done, total int) {
		calls = append(calls, [2]int{done, total})
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a@1", "B@2", "C@3", "d@2", "new@new"}, blameSummary(res))
	require.Empty(t, res.Revision)
	require.False(t, res.Truncated)
	require.Equal(t, "src/a.txt", res.Path)
	require.Len(t, res.Commits, 3)
	require.Equal(t, snow.ID(3).Base36(), res.Commits[0].ID)
	require.Equal(t, "Cy", res.Commits[0].AuthorName)
	require.Equal(t, "cy@example.com", res.Commits[0].AuthorEmail)
	require.Equal(t, "commit 3", res.Commits[0].Message)
	require.NotEmpty(t, res.Commits[0].Hash)
	require.Equal(t, []string{snow.ID(3).Base36()}, fx.client.logRevs)
	require.Equal(t, []string{"src", "src", "src"}, fx.client.dirs)
	require.Equal(t, [2]int{3, 3}, calls[len(calls)-1])
	require.Equal(t, [2]int{1, 4}, calls[0])
}

func TestBlame_RevisionTokens(t *testing.T) {
	fx := newBlameFixture(t)
	threeCommits(t, fx, "a.txt")
	fx.client.tags["v1"] = &clientDomain.Tag{Name: "v1", CommitID: snow.ID(2).Base36()}
	fx.pin(t, 3)

	want2 := []string{"a@1", "B@2", "c@1", "d@2"}
	want3 := []string{"a@1", "B@2", "C@3", "d@2"}
	for rev, want := range map[string][]string{"v1": want2, snow.ID(2).Base36(): want2, "main": want3, "HEAD": want3, "@": want3} {
		res, err := fx.run(t, "a.txt", rev)
		require.NoError(t, err, rev)
		require.Equal(t, want, blameSummary(res), rev)
		require.NotEmpty(t, res.Revision)
	}
	require.Equal(t, []string{""}, fx.client.dirs[:1])

	_, err := fx.run(t, "a.txt", "no-pe")
	require.ErrorContains(t, err, `revision "no-pe" not found`)
}

func TestBlame_StopsOnceEveryLineIsAttributed(t *testing.T) {
	fx := newBlameFixture(t)
	c1 := fx.commit(t, 1, nil, "Ana", map[string]string{"a.txt": "old\n"})
	c2 := fx.commit(t, 2, int64p(1), "Ben", map[string]string{"a.txt": "new\n"})
	c3 := fx.commit(t, 3, int64p(2), "Cy", map[string]string{"a.txt": "new\nmore\n"})
	fx.client.logs[snow.ID(3).Base36()] = []*serverDomain.CommitLogEntry{c3, c2, c1}

	res, err := fx.run(t, "a.txt", snow.ID(3).Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"new@2", "more@3"}, blameSummary(res))
	require.Equal(t, []string{snow.ID(3).Base36(), snow.ID(2).Base36(), snow.ID(1).Base36()}, fx.client.reads)

	fx.client.reads = nil
	fx.client.logs[snow.ID(2).Base36()] = []*serverDomain.CommitLogEntry{c2, c1}
	res, err = fx.run(t, "a.txt", snow.ID(2).Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"new@2"}, blameSummary(res))
	require.Len(t, res.Commits, 1)
	require.Len(t, fx.client.reads, 2)
}

func TestBlame_TruncatedHistory(t *testing.T) {
	fx := newBlameFixture(t)
	fx.commit(t, 1, nil, "Ana", map[string]string{"a.txt": "a\n"})
	c2 := fx.commit(t, 2, int64p(1), "Ben", map[string]string{"a.txt": "a\nb\n"})
	fx.client.logs[snow.ID(2).Base36()] = []*serverDomain.CommitLogEntry{c2}

	res, err := fx.run(t, "a.txt", snow.ID(2).Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"a@?", "b@2"}, blameSummary(res))
	require.True(t, res.Truncated)
}

func TestBlame_EmptyLog(t *testing.T) {
	fx := newBlameFixture(t)
	fx.commit(t, 1, nil, "Ana", map[string]string{"a.txt": "a\n"})
	fx.pin(t, 1)

	res, err := fx.run(t, "a.txt", "HEAD")
	require.NoError(t, err)
	require.Equal(t, []string{"a@?"}, blameSummary(res))
	require.True(t, res.Truncated)
	require.Empty(t, res.Commits)

	writeRepoFile(t, fx.root, "a.txt", "a\nb\n")
	res, err = fx.run(t, "a.txt", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a@?", "b@new"}, blameSummary(res))

	writeRepoFile(t, fx.root, "fresh.txt", "x\n")
	res, err = fx.run(t, "fresh.txt", "")
	require.NoError(t, err)
	require.Equal(t, []string{"x@new"}, blameSummary(res))
	require.False(t, res.Truncated)

	_, err = fx.run(t, "fresh.txt", "HEAD")
	require.True(t, errors.Is(err, ErrFileMissing))
}

func TestBlame_DeletedAtRevision(t *testing.T) {
	fx := newBlameFixture(t)
	c1 := fx.commit(t, 1, nil, "Ana", map[string]string{"a.txt": "a\n"})
	c2 := fx.commit(t, 2, int64p(1), "Ben", map[string]string{"b.txt": "b\n"})
	fx.client.logs[snow.ID(2).Base36()] = []*serverDomain.CommitLogEntry{c2, c1}

	_, err := fx.run(t, "a.txt", snow.ID(2).Base36())
	require.True(t, errors.Is(err, ErrFileMissing))

	fx.pin(t, 2)
	writeRepoFile(t, fx.root, "a.txt", "a\n")
	res, err := fx.run(t, "a.txt", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a@new"}, blameSummary(res))
}

func TestBlame_NoCommits(t *testing.T) {
	fx := newBlameFixture(t)
	fx.client.branches["main"] = &serverDomain.Branch{Name: "main"}
	writeRepoFile(t, fx.root, "a.txt", "a\nb\n")

	res, err := fx.run(t, "a.txt", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a@new", "b@new"}, blameSummary(res))

	_, err = fx.run(t, "a.txt", "main")
	require.ErrorContains(t, err, "no commits yet")

	writeRepoFile(t, fx.root, "empty.txt", "")
	res, err = fx.run(t, "empty.txt", "")
	require.NoError(t, err)
	require.Empty(t, res.Lines)
}

func TestBlame_OlderBinaryOrLargeVersionEndsHistory(t *testing.T) {
	fx := newBlameFixture(t)
	c1 := fx.commit(t, 1, nil, "Ana", map[string]string{"a.txt": "bin\x00ary"})
	c2 := fx.commit(t, 2, int64p(1), "Ben", map[string]string{"a.txt": "a\n"})
	fx.client.logs[snow.ID(2).Base36()] = []*serverDomain.CommitLogEntry{c2, c1}

	res, err := fx.run(t, "a.txt", snow.ID(2).Base36())
	require.NoError(t, err)
	require.Equal(t, []string{"a@?"}, blameSummary(res))
	require.True(t, res.Truncated)

	fx.pin(t, 2)
	writeRepoFile(t, fx.root, "a.txt", "a\n")
	fx.client.trees[snow.ID(2).Base36()].FileChildren[0].SizeBytes = BlameMaxBytes + 1
	res, err = fx.run(t, "a.txt", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a@?"}, blameSummary(res))
	require.True(t, res.Truncated)

	_, err = fx.run(t, "a.txt", snow.ID(2).Base36())
	require.ErrorContains(t, err, "larger than 2 MiB")
	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	_, err = fx.run(t, "a.txt", snow.ID(1).Base36())
	require.Error(t, err)
}

func TestBlame_RejectsUnsupportedFiles(t *testing.T) {
	fx := newBlameFixture(t)
	fx.pin(t, 1)
	writeRepoFile(t, fx.root, "img.bin", "\x00\x01\x02")
	_, err := fx.run(t, "img.bin", "")
	require.ErrorContains(t, err, "not a text file")

	writeRepoFile(t, fx.root, "big.txt", strings.Repeat("x\n", BlameMaxBytes/2+1))
	_, err = fx.run(t, "big.txt", "")
	require.ErrorContains(t, err, "larger than 2 MiB")

	_, err = fx.run(t, "missing.txt", "")
	require.True(t, errors.Is(err, ErrFileMissing))

	_, err = fx.run(t, "../x", "")
	require.Error(t, err)

	_, err = NewBlame(diffAuth(t), fx.client, fx.lr).Run(context.Background(), t.TempDir(), "a.txt", "", nil)
	require.Error(t, err)
}

func TestBlame_ClientErrors(t *testing.T) {
	fx := newBlameFixture(t)
	threeCommits(t, fx, "a.txt")
	fx.pin(t, 3)
	writeRepoFile(t, fx.root, "a.txt", "a\n")
	boom := errors.New("boom")

	fx.client.connectErr = boom
	_, err := fx.run(t, "a.txt", "")
	require.ErrorIs(t, err, boom)
	fx.client.connectErr = nil

	fx.client.logErr = boom
	_, err = fx.run(t, "a.txt", "")
	require.ErrorIs(t, err, boom)
	fx.client.logErr = nil

	fx.client.manifestErr = boom
	_, err = fx.run(t, "a.txt", "")
	require.ErrorIs(t, err, boom)
	fx.client.manifestErr = nil

	fx.client.downloadErr = boom
	_, err = fx.run(t, "a.txt", snow.ID(3).Base36())
	require.ErrorIs(t, err, boom)
	fx.client.downloadErr = nil

	fx.client.tagErr = boom
	_, err = fx.run(t, "a.txt", "nope")
	require.ErrorIs(t, err, boom)
}

func TestBlame_OlderReadErrorFails(t *testing.T) {
	fx := newBlameFixture(t)
	threeCommits(t, fx, "a.txt")
	delete(fx.stored, fx.client.trees[snow.ID(1).Base36()].FileChildren[0].Chunks[0].Hash)
	_, err := fx.run(t, "a.txt", snow.ID(3).Base36())
	require.Error(t, err)
}

func TestSplitBlameLines(t *testing.T) {
	require.Equal(t, []string{}, splitBlameLines(nil))
	require.Equal(t, []string{"a", "", "b"}, splitBlameLines([]byte("a\r\n\nb")))
	require.Equal(t, "", parentDir("a.txt"))
	require.Equal(t, "x/y", parentDir("x/y/a.txt"))
}
