package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// newTreeClone saves files (slash paths) as the synced tree with real raw
// chunks and writes them to the working copy.
func newTreeClone(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(clientDomain.Config{Url: "https://nipa.example.com/default/default", Branch: "main"}))
	tree := &serverDomain.TreeNode{Hash: serverDomain.Hash{0x01}, Name: "root"}
	dirs := map[string]*serverDomain.TreeNode{"": tree}
	var ensure func(dir string) *serverDomain.TreeNode
	ensure = func(dir string) *serverDomain.TreeNode {
		if n, ok := dirs[dir]; ok {
			return n
		}
		parent, name := "", dir
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			parent, name = dir[:i], dir[i+1:]
		}
		n := &serverDomain.TreeNode{Hash: serverDomain.Hash{0x02, byte(len(dirs))}, Name: name}
		dirs[dir] = n
		p := ensure(parent)
		p.TreeChildren = append(p.TreeChildren, n)
		return n
	}
	for rel, content := range files {
		data := []byte(content)
		_, chunks, err := chunker.EncodeBytes(data, rel, chunker.IsBinary(data), chunker.EncodingRaw)
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, 0, len(chunks))
		wrapped := make([]serverDomain.Chunk, 0, len(chunks))
		for _, c := range chunks {
			hashes = append(hashes, c.Hash)
			wrapped = append(wrapped, serverDomain.Chunk{Hash: c.Hash, SizeBytes: c.SizeBytes})
		}
		dir, name := "", rel
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			dir, name = rel[:i], rel[i+1:]
		}
		ensure(dir).FileChildren = append(ensure(dir).FileChildren, &serverDomain.File{
			Name: name, Hash: chunker.FileHash(hashes), Mode: 2, SizeBytes: int64(len(data)),
			IsBinary: chunker.IsBinary(data), Encoding: chunker.EncodingRaw, Chunks: wrapped,
		})
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, data, 0o644))
	}
	require.NoError(t, lr.SaveTree(tree))
	require.NoError(t, lr.Close())
	return root
}

func listFiles(t *testing.T, srv *Server, root, dir string) map[string]*daemonpb.FileEntry {
	t.Helper()
	res, err := srv.ListFiles(context.Background(), &daemonpb.ListFilesRequest{Root: root, Dir: dir})
	require.NoError(t, err)
	out := map[string]*daemonpb.FileEntry{}
	for _, e := range res.GetEntries() {
		out[e.GetName()] = e
	}
	return out
}

func TestServer_ListFilesMergesStatus(t *testing.T) {
	root := newTreeClone(t, map[string]string{
		"README.md":                 "hello\n",
		"Assets/hero.png":           "\x89PNG\x00pixels",
		"Assets/gone.png":           "\x89PNG\x00gone",
		"Assets/edit.txt":           "before\n",
		"Assets/drop.txt":           "drop me\n",
		"Assets/Audio/theme.wav":    "RIFF\x00wave",
		"Assets/Audio/Sfx/hit.wav":  "RIFF\x00hit",
		"Source/Player/Movement.cs": "class Movement {}\n",
	})
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(root, "Assets/gone.png")))
	writeWorkingFile(t, root, "Assets/edit.txt", "after, longer\n")
	writeWorkingFile(t, root, "Assets/new.txt", "brand new\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Assets/Fresh/Deep"), 0o755))
	writeWorkingFile(t, root, "Assets/Fresh/Deep/a.png", "\x89PNG\x00a")
	writeWorkingFile(t, root, "staged.txt", "staged new\n")
	_, err = srv.Stage(context.Background(), &daemonpb.StageRequest{Root: root, Add: []string{"staged.txt"}})
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(root, "Assets/drop.txt")))
	_, err = srv.Stage(context.Background(), &daemonpb.StageRequest{Root: root, Add: []string{"Assets/drop.txt"}})
	require.NoError(t, err)

	top := listFiles(t, srv, root, "")
	require.Len(t, top, 4)
	require.True(t, top["Assets"].GetIsDir())
	require.True(t, top["Assets"].GetChildrenChanged())
	require.False(t, top["Source"].GetChildrenChanged())
	require.Equal(t, "", top["README.md"].GetStatus())
	require.Equal(t, "text", top["README.md"].GetKind())
	require.Equal(t, int64(6), top["README.md"].GetSizeBytes())
	require.NotEmpty(t, top["README.md"].GetHash())
	require.Zero(t, top["README.md"].GetMtimeNs())
	require.Equal(t, "added", top["staged.txt"].GetStatus())
	require.True(t, top["staged.txt"].GetStaged())
	require.False(t, top["staged.txt"].GetTracked())

	assets := listFiles(t, srv, root, "/Assets/")
	require.Equal(t, "", assets["hero.png"].GetStatus())
	require.Equal(t, "image", assets["hero.png"].GetKind())
	require.True(t, assets["hero.png"].GetIsBinary())
	require.Equal(t, "missing", assets["gone.png"].GetStatus())
	require.Equal(t, "modified", assets["edit.txt"].GetStatus())
	require.NotZero(t, assets["edit.txt"].GetMtimeNs())
	require.Equal(t, "deleted", assets["drop.txt"].GetStatus())
	require.True(t, assets["drop.txt"].GetStaged())
	require.Equal(t, "untracked", assets["new.txt"].GetStatus())
	require.Equal(t, int64(10), assets["new.txt"].GetSizeBytes())
	require.Equal(t, "Assets/new.txt", assets["new.txt"].GetPath())
	require.True(t, assets["Fresh"].GetIsDir())
	require.Equal(t, "untracked", assets["Fresh"].GetStatus())
	require.True(t, assets["Fresh"].GetChildrenChanged())
	require.True(t, assets["Audio"].GetIsDir())
	require.False(t, assets["Audio"].GetChildrenChanged())

	res, err := srv.ListFiles(context.Background(), &daemonpb.ListFilesRequest{Root: root, Dir: "Assets"})
	require.NoError(t, err)
	var names []string
	for _, e := range res.GetEntries() {
		names = append(names, e.GetName())
	}
	require.Equal(t, []string{"Audio", "Fresh", "drop.txt", "edit.txt", "gone.png", "hero.png", "new.txt"}, names)

	deep := listFiles(t, srv, root, "Assets/Fresh/Deep")
	require.Equal(t, "untracked", deep["a.png"].GetStatus())
	require.Equal(t, "image", deep["a.png"].GetKind())
	require.True(t, deep["a.png"].GetIsBinary())

	sfx := listFiles(t, srv, root, "Assets/Audio/Sfx")
	require.Equal(t, "audio", sfx["hit.wav"].GetKind())
}

func TestServer_ListFilesSingleEntryAndValidation(t *testing.T) {
	root := newTreeClone(t, map[string]string{"a/b.txt": "b\n"})
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	ctx := context.Background()

	res, err := srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: "a", Name: "b.txt"})
	require.NoError(t, err)
	require.Len(t, res.GetEntries(), 1)
	require.Equal(t, "a/b.txt", res.GetEntries()[0].GetPath())

	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: "a", Name: "nope"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: "a", Name: "x/y"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: "../x"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: ".nipa/objects"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: t.TempDir()})
	require.Error(t, err)

	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	defer unref()
	require.NoError(t, rp.localRepo.Close())
	_, err = srv.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root})
	require.Error(t, err)
}

func TestServer_ListFilesReusesCachedStatus(t *testing.T) {
	root := newTreeClone(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	srv := newOpServer(t, RepoOps{})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = srv.Status(ctx, &daemonpb.StatusRequest{Root: root})
	require.NoError(t, err)
	rp.rememberStatus(rp.changes.generation(), &clientDomain.Status{Branch: "main", Modified: []string{"b.txt"}})
	require.Equal(t, "modified", listFiles(t, srv, root, "")["b.txt"].GetStatus(), "the cached status is used as is")

	rp.changes.publish([]string{"b.txt"})
	require.Equal(t, "", listFiles(t, srv, root, "")["b.txt"].GetStatus(), "a change notice invalidates it")

	rp.rememberStatus(rp.changes.generation(), &clientDomain.Status{Modified: []string{"a.txt"}})
	rp.statusMu.Lock()
	rp.statusAt = time.Now().Add(-2 * statusTTL)
	rp.statusMu.Unlock()
	require.Equal(t, "", listFiles(t, srv, root, "")["a.txt"].GetStatus(), "an old status expires")

	rp.rememberStatus(rp.changes.generation(), &clientDomain.Status{Conflicts: []string{"a.txt", "b.txt"}, Resolved: []string{"b.txt"}, Staged: []string{"b.txt"}})
	entries := listFiles(t, srv, root, "")
	require.Equal(t, "conflict", entries["a.txt"].GetStatus())
	require.Equal(t, "modified", entries["b.txt"].GetStatus())
	require.True(t, entries["b.txt"].GetStaged())
}

func TestServer_ListFilesLargeDirectory(t *testing.T) {
	files := map[string]string{"Other/x.txt": "x\n", "top.txt": "t\n"}
	for i := range 10000 {
		files[fmt.Sprintf("Big/f%05d.txt", i)] = fmt.Sprintf("line %d\n", i)
	}
	root := newTreeClone(t, files)
	srv := newOpServer(t, RepoOps{})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)
	rp.rememberStatus(rp.changes.generation(), &clientDomain.Status{Modified: []string{"Big/f00042.txt"}})

	start := time.Now()
	big := listFiles(t, srv, root, "Big")
	require.Len(t, big, 10000)
	require.Equal(t, "modified", big["f00042.txt"].GetStatus())
	require.Less(t, time.Since(start), 5*time.Second)

	top := listFiles(t, srv, root, "")
	require.Len(t, top, 3)
	require.True(t, top["Big"].GetChildrenChanged())
	require.False(t, top["Other"].GetChildrenChanged())
}

type stubRestorer struct {
	err   error
	calls []string
}

func (s *stubRestorer) Run(_ context.Context, root, path, commitID string, progress ...usecase.DownloadProgress) error {
	s.calls = append(s.calls, path+"@"+commitID)
	if len(progress) > 0 {
		progress[0].DownloadStart(1, 10)
		progress[0].DownloadEnd()
	}
	return s.err
}

func TestServer_RestoreRevisionStreams(t *testing.T) {
	root := newTestClone(t)
	restorer := &stubRestorer{}
	srv := newOpServer(t, RepoOps{Restore: restorer})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)
	gen := rp.changes.generation()

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.RestoreRevision(&daemonpb.RestoreRevisionRequest{Root: root, Path: "tracked.txt", CommitId: "abc"}, stream))
	events := stream.events()
	require.Equal(t, "restore", events[0].GetStarted().GetPhase())
	require.NotEmpty(t, progressEvents(events))
	result := terminalEvent(t, events).GetResult().GetRestore()
	require.Equal(t, "tracked.txt", result.GetPath())
	require.Equal(t, "abc", result.GetCommitId())
	require.Equal(t, []string{"tracked.txt@abc"}, restorer.calls)
	require.Greater(t, rp.changes.generation(), gen, "a finished restore publishes a change")

	restorer.err = clientDomain.NewUserError("revision \"x\" not found")
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.RestoreRevision(&daemonpb.RestoreRevisionRequest{Root: root, Path: "tracked.txt", CommitId: "x"}, stream))
	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 400, last.GetFailure().GetCode())
	require.Contains(t, last.GetFailure().GetMessage(), "not found")
}

func TestServer_RestoreRevisionWithoutRunner(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.RestoreRevision(&daemonpb.RestoreRevisionRequest{Root: root, Path: "tracked.txt", CommitId: "abc"}, stream))
	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 409, last.GetFailure().GetCode())

	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.RestoreRevision(&daemonpb.RestoreRevisionRequest{Root: t.TempDir()}, stream))
	require.NotNil(t, stream.events()[0].GetFailure())
}
