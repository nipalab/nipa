package service

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type filesDaemon struct {
	*historyDaemon
	listReqs   []*daemonpb.ListFilesRequest
	entries    []*daemonpb.FileEntry
	listErr    error
	restoreReq *daemonpb.RestoreRevisionRequest
}

func (d *filesDaemon) ListFiles(_ context.Context, req *daemonpb.ListFilesRequest, _ ...grpc.CallOption) (*daemonpb.ListFilesResponse, error) {
	d.listReqs = append(d.listReqs, req)
	if d.listErr != nil {
		return nil, d.listErr
	}
	return &daemonpb.ListFilesResponse{Dir: req.GetDir(), Entries: d.entries}, nil
}

func (d *filesDaemon) RestoreRevision(_ context.Context, req *daemonpb.RestoreRevisionRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.restoreReq = req
	return d.opStream()
}

func newFiles(t *testing.T) (*Service, *filesDaemon) {
	t.Helper()
	ops := &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}
	d := &filesDaemon{historyDaemon: &historyDaemon{opsDaemon: ops}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_FilesMapsEntries(t *testing.T) {
	svc, d := newFiles(t)
	d.entries = []*daemonpb.FileEntry{
		{Name: "Audio", Path: "Assets/Audio", IsDir: true, Tracked: true, ChildrenChanged: true},
		{Name: "hero.png", Path: "Assets/hero.png", SizeBytes: 42, IsBinary: true, Kind: "image", Status: "modified", Staged: true, Tracked: true, Hash: "ab", MtimeNs: 7},
	}
	out, err := svc.Files(ctx, "/work/game", "Assets")
	require.NoError(t, err)
	assert.Equal(t, &FileListing{Dir: "Assets", Entries: []FileEntry{
		{Name: "Audio", Path: "Assets/Audio", IsDir: true, Tracked: true, ChildrenChanged: true},
		{Name: "hero.png", Path: "Assets/hero.png", Size: 42, Binary: true, Kind: "image", Status: "modified", Staged: true, Tracked: true, Hash: "ab", Mtime: 7},
	}}, out)
	assert.Equal(t, "Assets", d.listReqs[0].GetDir())

	d.listErr = status.Error(codes.InvalidArgument, "bad dir")
	_, err = svc.Files(ctx, "/work/game", "..")
	require.EqualError(t, err, "bad dir")
}

func TestService_FileInfoJoinsLastCommit(t *testing.T) {
	svc, d := newFiles(t)
	d.entries = []*daemonpb.FileEntry{{Name: "hero.png", Path: "Assets/hero.png", Kind: "image", Tracked: true}}
	d.log = []*pb.CommitLogEntry{{CommitId: "c7", AuthorName: "Mina", Message: "Repaint"}}

	info, err := svc.FileInfo(ctx, "/work/game", "/Assets/hero.png")
	require.NoError(t, err)
	assert.Equal(t, "Assets", d.listReqs[0].GetDir())
	assert.Equal(t, "hero.png", d.listReqs[0].GetName())
	assert.Equal(t, "Assets/hero.png", info.Entry.Path)
	require.NotNil(t, info.LastCommit)
	assert.Equal(t, "c7", info.LastCommit.ID)
	assert.Equal(t, "Assets/hero.png", d.logReqs[0].GetPath())

	d.log = nil
	info, err = svc.FileInfo(ctx, "/work/game", "top.txt")
	require.NoError(t, err)
	assert.Equal(t, "", d.listReqs[1].GetDir())
	assert.Nil(t, info.LastCommit)

	d.err = status.Error(codes.Unavailable, "server down")
	d.entries = []*daemonpb.FileEntry{{Name: "top.txt", Path: "top.txt"}}
	info, err = svc.FileInfo(ctx, "/work/game", "top.txt")
	require.NoError(t, err, "an unreachable server leaves only the history empty")
	assert.Nil(t, info.LastCommit)
	assert.NotEmpty(t, info.LastCommitError)
	d.err = nil

	_, err = svc.FileInfo(ctx, "/work/game", "/")
	require.Error(t, err)
	d.entries = nil
	_, err = svc.FileInfo(ctx, "/work/game", "gone.txt")
	require.ErrorContains(t, err, "does not exist")
	d.listErr = status.Error(codes.NotFound, "gone.txt does not exist")
	_, err = svc.FileInfo(ctx, "/work/game", "gone.txt")
	require.EqualError(t, err, "gone.txt does not exist")
}

func TestService_FileViewShowsPlainContent(t *testing.T) {
	svc, d := newFiles(t)
	d.blob = []*daemonpb.BlobEvent{
		{Event: &daemonpb.BlobEvent_Info{Info: &daemonpb.BlobInfo{Path: "a.txt", Kind: "text", SizeBytes: 4}}},
		{Event: &daemonpb.BlobEvent_Data{Data: []byte("a\nb\n")}},
	}
	view, err := svc.FileView(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	assert.Equal(t, "", view.Status)
	assert.False(t, view.Untracked)
	assert.Zero(t, view.Additions)
	require.Len(t, view.Hunks, 1)
	for _, l := range view.Hunks[0].Lines {
		assert.Equal(t, "context", l.Kind)
	}
	assert.Equal(t, int64(2), view.Hunks[0].Lines[1].New)

	d.blob = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 404, Message: "missing"}}}}
	view, err = svc.FileView(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	assert.Nil(t, view)

	d.blobErr = errors.New("broken")
	_, err = svc.FileView(ctx, "/work/game", "a.txt")
	require.Error(t, err)
}

func TestService_RestoreRevisionRunsOp(t *testing.T) {
	svc, d := newFiles(t)
	d.op = []*daemonpb.OpEvent{
		{Event: &daemonpb.OpEvent_Started{Started: &daemonpb.OpStarted{Phase: "restore"}}},
		{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Restore{Restore: &daemonpb.RestoreRevisionResult{Path: "a.txt", CommitId: "c3"}}}}},
	}
	out, err := svc.RestoreRevision(ctx, "/work/game", "a.txt", "c3")
	require.NoError(t, err)
	assert.Equal(t, "c3", out.CommitID)
	assert.Equal(t, "a.txt", d.restoreReq.GetPath())
	assert.Equal(t, "c3", d.restoreReq.GetCommitId())

	_, err = svc.RestoreRevision(ctx, "/work/game", "a.txt", "")
	require.Error(t, err)

	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Failure{Failure: &daemonpb.OpFailure{Code: 404, Message: "a.txt does not exist in c3"}}}}
	_, err = svc.RestoreRevision(ctx, "/work/game", "a.txt", "c3")
	require.EqualError(t, err, "a.txt does not exist in c3")
}

func TestService_RevealFile(t *testing.T) {
	svc, _ := newFiles(t)
	var revealed []string
	svc.RevealFunc = func(p string) error {
		revealed = append(revealed, p)
		return nil
	}
	root := filepath.Join(t.TempDir(), "game")
	require.NoError(t, svc.RevealFile(ctx, root, "Assets/hero.png"))
	require.NoError(t, svc.RevealFile(ctx, root, ""))
	assert.Equal(t, []string{filepath.Join(root, "Assets", "hero.png"), root}, revealed)
	require.Error(t, svc.RevealFile(ctx, root, "../escape"))
	require.Error(t, svc.RevealFile(ctx, root, ".nipa/config"))
}

func TestRevealCommands(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hero.png")
	mac := revealCommands("darwin", file)
	require.Len(t, mac, 1)
	assert.Equal(t, []string{"open", "-R", file}, mac[0].Args)
	win := revealCommands("windows", file)
	assert.Equal(t, []string{"explorer", "/select," + file}, win[0].Args)
	linux := revealCommands("linux", file)
	require.Len(t, linux, 2)
	assert.Equal(t, "dbus-send", linux[0].Args[0])
	assert.Contains(t, strings.Join(linux[0].Args, " "), "array:string:file://")
	assert.Equal(t, []string{"xdg-open", dir}, linux[1].Args)
	assert.Equal(t, []string{"xdg-open", dir}, revealCommands("linux", dir)[1].Args, "a folder opens itself")
}

func TestRunReveal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses true/false")
	}
	require.NoError(t, runReveal([]*exec.Cmd{exec.Command("true"), exec.Command("does-not-exist-nipa")}))
	require.NoError(t, runReveal([]*exec.Cmd{exec.Command("false"), exec.Command("true")}))
	require.Error(t, runReveal([]*exec.Cmd{exec.Command("false"), exec.Command("does-not-exist-nipa")}))
}

func TestService_FileLink(t *testing.T) {
	svc := New(nil, &memStore{}, nil)
	for repoURL, want := range map[string]string{
		"https://nipa.example.com/studio/game": "nipa://nipa.example.com/studio/game/file/Assets/Hero/hero%20v2.png",
		"http://localhost:6811/studio/game":    "nipa://localhost:6811/studio/game/file/Assets/Hero/hero%20v2.png",
		"http://vcs.lan/studio/game":           "nipa://vcs.lan/studio/game/file/Assets/Hero/hero%20v2.png?server=http%3A%2F%2Fvcs.lan",
	} {
		link, err := svc.FileLink(repoURL, "/Assets/Hero/hero v2.png")
		require.NoError(t, err)
		assert.Equal(t, want, link)
		nav, err := ParseLink(link)
		require.NoError(t, err)
		assert.Equal(t, "Assets/Hero/hero v2.png", nav.Path)
		assert.Equal(t, "files", nav.Tab)
	}
	_, err := svc.FileLink("nipa.example.com", "a")
	require.Error(t, err)
}
