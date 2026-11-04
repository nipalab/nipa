package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type identityConn struct {
	fakeProxyConn
	user string
	err  error
}

func (c *identityConn) UserID(context.Context) (string, error) { return c.user, c.err }

type lockService struct {
	pb.NipaServiceClient
	locks     []*pb.FileLockDetail
	listErr   error
	branch    *pb.Branch
	branchErr error
	perm      uint64
	permErr   error
	asked     []string
}

func (s *lockService) ListFileLocks(_ context.Context, req *pb.ListFileLocksRequest, _ ...grpc.CallOption) (*pb.ListFileLocksResponse, error) {
	s.asked = append(s.asked, "locks:"+req.GetContext().GetProject())
	return &pb.ListFileLocksResponse{Locks: s.locks}, s.listErr
}

func (s *lockService) GetBranchByName(_ context.Context, req *pb.GetBranchByNameRequest, _ ...grpc.CallOption) (*pb.GetBranchByNameResponse, error) {
	s.asked = append(s.asked, "branch:"+req.GetName())
	if s.branchErr != nil {
		return nil, s.branchErr
	}
	return &pb.GetBranchByNameResponse{Branch: s.branch}, nil
}

func (s *lockService) GetMyPermissions(context.Context, *pb.GetMyPermissionsRequest, ...grpc.CallOption) (*pb.GetMyPermissionsResponse, error) {
	return &pb.GetMyPermissionsResponse{ProjectPermission: s.perm}, s.permErr
}

func lockClone(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(clientDomain.Config{Url: "https://nipa.example.com/studio/game", Branch: branch}))
	file := func(name string, binary bool, h byte) *serverDomain.File {
		return &serverDomain.File{Name: name, Hash: serverDomain.Hash{h}, Mode: 0o644, SizeBytes: 3, IsBinary: binary, Chunks: []serverDomain.Chunk{{Hash: serverDomain.Hash{h + 1}, SizeBytes: 3}}}
	}
	require.NoError(t, lr.SaveTree(&serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{
			file("hero.psd", true, 0x10),
			file("tex.png", true, 0x20),
			file("items.bin", true, 0x30),
			file("notes.txt", false, 0x40),
			file("gone.wav", true, 0x50),
		},
		TreeChildren: []*serverDomain.TreeNode{{
			Hash:         serverDomain.Hash{0x02},
			Name:         "props",
			FileChildren: []*serverDomain.File{file("crate.fbx", true, 0x70)},
		}},
	}))
	require.NoError(t, lr.Close())
	for _, rel := range []string{"hero.psd", "tex.png", "items.bin", "notes.txt", "new.fbx", "readme.md", "props/crate.fbx"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755))
		writeWorkingFile(t, root, rel, "changed "+rel)
	}
	return root
}

func lockServer(t *testing.T, branch string, svc *lockService, user string) (*Server, string, *identityConn) {
	t.Helper()
	root := lockClone(t, branch)
	conn := &identityConn{fakeProxyConn: fakeProxyConn{client: svc}, user: user}
	srv := newOpServer(t, RepoOps{Proxy: conn})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root, conn
}

func states(res *daemonpb.LockStatusResponse) map[string]string {
	out := map[string]string{}
	for _, f := range res.GetFiles() {
		out[f.GetPath()] = f.GetState()
	}
	return out
}

func TestServer_LockStatusOnDefaultBranch(t *testing.T) {
	svc := &lockService{
		branch: &pb.Branch{Name: "main", IsDefault: true},
		perm:   1 | 2 | uint64(serverDomain.PermissionAdmin),
		locks: []*pb.FileLockDetail{
			{Path: "hero.psd", Global: true, HeldBy: "me", HeldByName: "Me"},
			{Path: "props", Global: true, HeldBy: "bob", HeldByName: "Bob"},
			{Path: "tex.png", Branch: "feature", HeldBy: "me"},
			{Path: "new.fbx", Global: true, HeldBy: "bob", HeldByName: "Bob"},
		},
	}
	srv, root, _ := lockServer(t, "main", svc, "me")
	res, err := srv.LockStatus(context.Background(), &daemonpb.LockStatusRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, "me", res.GetUserId())
	require.Equal(t, "main", res.GetBranch())
	require.True(t, res.GetDefaultBranch())
	require.True(t, res.GetCanReleaseAny())
	require.Len(t, res.GetLocks(), 4)
	require.Equal(t, map[string]string{
		"gone.wav":        "needed",
		"hero.psd":        "mine",
		"items.bin":       "needed",
		"new.fbx":         "theirs",
		"props/crate.fbx": "theirs",
		"tex.png":         "needed",
	}, states(res))
	for _, f := range res.GetFiles() {
		if f.GetPath() == "props/crate.fbx" {
			require.Equal(t, "props", f.GetLockPath())
			require.Equal(t, "Bob", f.GetHeldByName())
			require.Equal(t, "bob", f.GetHeldBy())
			require.True(t, f.GetTracked())
		}
		if f.GetPath() == "new.fbx" {
			require.False(t, f.GetTracked())
		}
	}
	require.Equal(t, []string{"locks:game", "branch:main"}, svc.asked)
}

func TestServer_LockStatusOnFeatureBranch(t *testing.T) {
	svc := &lockService{
		branch: &pb.Branch{Name: "feature"},
		perm:   3,
		locks: []*pb.FileLockDetail{
			{Path: "hero.psd", Global: true, HeldBy: "bob"},
			{Path: "tex.png", Branch: "feature", HeldBy: "me"},
			{Path: "items.bin", Branch: "other", HeldBy: "bob"},
		},
	}
	srv, root, _ := lockServer(t, "feature", svc, "me")
	res, err := srv.LockStatus(context.Background(), &daemonpb.LockStatusRequest{Root: root})
	require.NoError(t, err)
	require.False(t, res.GetDefaultBranch())
	require.False(t, res.GetCanReleaseAny())
	got := states(res)
	require.Equal(t, "needed", got["hero.psd"])
	require.Equal(t, "mine", got["tex.png"])
	require.Equal(t, "needed", got["items.bin"])
	require.Equal(t, "free", got["new.fbx"])

	svc.branchErr = status.Error(codes.NotFound, "branch not found")
	res, err = srv.LockStatus(context.Background(), &daemonpb.LockStatusRequest{Root: root})
	require.NoError(t, err)
	require.False(t, res.GetDefaultBranch())
}

func TestServer_LockStatusErrors(t *testing.T) {
	ctx := context.Background()
	svc := &lockService{branch: &pb.Branch{Name: "main", IsDefault: true}}
	srv, root, conn := lockServer(t, "main", svc, "me")

	svc.listErr = status.Error(codes.Unavailable, "server down")
	_, err := srv.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root})
	require.Equal(t, codes.Unavailable, status.Code(err))
	svc.listErr = nil

	svc.branchErr = status.Error(codes.PermissionDenied, "no")
	_, err = srv.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	svc.branchErr = nil

	svc.permErr = status.Error(codes.Internal, "perm")
	_, err = srv.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root})
	require.Equal(t, codes.Internal, status.Code(err))
	svc.permErr = nil

	conn.err = errors.New("no token")
	_, err = srv.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root})
	require.Error(t, err)

	_, err = srv.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: t.TempDir()})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	plain, root2 := watchedProxyServer(t, &fakeProxyConn{client: svc})
	_, err = plain.LockStatus(ctx, &daemonpb.LockStatusRequest{Root: root2})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestServer_LockStatusWithoutBranch(t *testing.T) {
	svc := &lockService{locks: []*pb.FileLockDetail{{Path: "hero.psd", Global: true, HeldBy: "me"}}}
	srv, root, _ := lockServer(t, "", svc, "me")
	res, err := srv.LockStatus(context.Background(), &daemonpb.LockStatusRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, []string{"locks:game"}, svc.asked)
	require.Equal(t, "needed", states(res)["hero.psd"])
}
