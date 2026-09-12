package e2e

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	servergrpc "github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

const (
	e2eJWTSecret       = "test-secret"
	e2eOrgSlug         = "default"
	e2eProjectSlug     = "default"
	e2eSuperAdminEmail = "supernipa"
	e2eSuperAdminPass  = "supernipa"
)

type testRegistry struct {
	auth   *serverusecase.Auth
	user   *serverusecase.User
	branch *serverusecase.Branch
	common *serverusecase.Common
	push   *serverusecase.Push
	chunk  *serverusecase.Chunk
}

func (r *testRegistry) Auth() *serverusecase.Auth     { return r.auth }
func (r *testRegistry) User() *serverusecase.User     { return r.user }
func (r *testRegistry) Branch() *serverusecase.Branch { return r.branch }
func (r *testRegistry) Common() *serverusecase.Common { return r.common }
func (r *testRegistry) Push() *serverusecase.Push     { return r.push }
func (r *testRegistry) Chunk() *serverusecase.Chunk   { return r.chunk }

type memoryStore struct {
	mu   sync.Mutex
	data map[string]*domain.LoginResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: map[string]*domain.LoginResult{}}
}

func (s *memoryStore) SaveToken(data *domain.LoginResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[data.Host] = data
	return nil
}

func (s *memoryStore) LoadToken(host string) (*domain.LoginResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.data[host]; ok {
		return data, nil
	}
	return nil, errors.New("no token stored for host " + host)
}

type failPrompt struct{}

func (failPrompt) PromptUsernameAndPassword() (string, string, error) {
	return "", "", errors.New("unexpected interactive prompt in e2e test")
}

func startTestServer(t *testing.T, dbConn *sql.DB) string {
	t.Helper()

	orgRepo := sqlite.NewOrgRepository(dbConn)
	projectRepo := sqlite.NewProjectRepository(dbConn)
	userRepo := sqlite.NewUserRepository(dbConn)
	authRepo := sqlite.NewAuthRepository(dbConn)
	branchRepo := sqlite.NewBranchRepository(dbConn)
	pushRepo := sqlite.NewPushRepository(dbConn)

	passwordHasher := hasher.NewHasher(2)
	t.Cleanup(passwordHasher.Close)

	node, err := snow.NewNode(0)
	require.NoError(t, err)

	authUc := serverusecase.NewAuth(e2eJWTSecret, passwordHasher, userRepo, authRepo)
	commonUc := serverusecase.NewCommon(orgRepo, projectRepo)
	branchUc := serverusecase.NewBranch(authUc, branchRepo)
	chunkStore, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = chunkStore.Close() })

	reg := &testRegistry{
		auth:   authUc,
		user:   serverusecase.NewUser(node),
		branch: branchUc,
		common: commonUc,
		push:   serverusecase.NewPush(authUc, branchRepo, pushRepo, node),
		chunk:  serverusecase.NewChunk(pushRepo, chunkStore),
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	interceptor := servergrpc.NewInterceptor(authUc)
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor.JWTUnary()),
		grpc.StreamInterceptor(interceptor.JWTStream()),
	)
	pb.RegisterNipaServiceServer(grpcServer, servergrpc.New(reg))
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	return lis.Addr().String()
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "nipa.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	require.NoError(t, db.MigrateUp(database, "sqlite3"))
	return database
}

func snapshotOf(t *testing.T, target string) *domain.Snapshot {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	snap, err := lr.Snapshot()
	require.NoError(t, err)
	return snap
}

func assertStagedEmpty(t *testing.T, target string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	paths, err := lr.ListStaged()
	require.NoError(t, err)
	require.Empty(t, paths)
}

func stagePath(t *testing.T, target, path string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	require.NoError(t, lr.StageAdd(path))
}

func writeFile(t *testing.T, target, path, content string) {
	t.Helper()
	fp := filepath.Join(target, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
}

func assertFileContent(t *testing.T, target, path, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}

func TestEndToEnd_CloneAddPushFetch(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport)
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})

	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NotEmpty(t, loginResult.AccessToken)
	require.NotEmpty(t, loginResult.RefreshToken)
	require.NoError(t, store.SaveToken(loginResult))

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	snap0 := snapshotOf(t, target)
	require.Empty(t, snap0.TreeHash, "cloning an empty branch must produce an empty tree")
	require.Empty(t, snap0.Files)

	const contentA = "hello e2e with the real stack\n"
	writeFile(t, target, "a.txt", contentA)
	stagePath(t, target, "a.txt")

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "add a.txt"))

	assertStagedEmpty(t, target)
	snap1 := snapshotOf(t, target)
	require.Len(t, snap1.Files, 1)
	require.Equal(t, "a.txt", snap1.Files[0].Path)
	require.False(t, snap1.Files[0].IsBinary)
	require.EqualValues(t, len(contentA), snap1.Files[0].SizeBytes)
	require.NotEmpty(t, snap1.Files[0].Chunks)
	require.NotEmpty(t, snap1.TreeHash, "after push the local tree hash must track the server head")

	hashes := make([]serverDomain.Hash, 0, len(snap1.Files[0].Chunks))
	for _, c := range snap1.Files[0].Chunks {
		hashes = append(hashes, c.Hash)
	}
	downloaded, err := grpcClient.DownloadChunks(ctx, hashes)
	require.NoError(t, err)
	require.Len(t, downloaded, len(hashes))
	var blob []byte
	for h, data := range downloaded {
		require.Equal(t, h, chunker.Sum(data), "downloaded chunk must match its hash")
		blob = data
	}
	require.Equal(t, contentA, string(blob))

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", checkout))
	snapCheckout := snapshotOf(t, checkout)
	require.Len(t, snapCheckout.Files, 1)
	require.Equal(t, "a.txt", snapCheckout.Files[0].Path)
	require.EqualValues(t, len(contentA), snapCheckout.Files[0].SizeBytes)
	assertFileContent(t, checkout, "a.txt", contentA)

	const contentB = "second file content\n"
	writeFile(t, target, "a.txt", contentA+"extra line\n")
	writeFile(t, target, "b.txt", contentB)
	stagePath(t, target, "a.txt")
	stagePath(t, target, "b.txt")
	require.NoError(t, pusher.Run(ctx, target, "update a, add b"))

	materialized := filepath.Join(t.TempDir(), "materialized")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", materialized))
	updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, updater.Run(ctx, materialized), "update must materialize the working copy from the server tree")
	assertFileContent(t, materialized, "a.txt", contentA+"extra line\n")
	assertFileContent(t, materialized, "b.txt", contentB)
	require.Len(t, snapshotOf(t, materialized).Files, 2, "update must refresh the local snapshot")

	require.NoError(t, os.Remove(filepath.Join(target, "a.txt")))
	stagePath(t, target, "a.txt")
	require.NoError(t, pusher.Run(ctx, target, "remove a"))

	require.NoError(t, updater.Run(ctx, materialized), "update must apply server removals")
	require.FileExists(t, filepath.Join(materialized, "b.txt"))
	require.NoFileExists(t, filepath.Join(materialized, "a.txt"), "a file removed on the server must leave the working copy")
	assertFileContent(t, materialized, "b.txt", contentB)

	checkoutAfter := filepath.Join(t.TempDir(), "checkout-after")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", checkoutAfter))
	snapAfter := snapshotOf(t, checkoutAfter)
	require.Len(t, snapAfter.Files, 1)
	require.Equal(t, "b.txt", snapAfter.Files[0].Path)
	require.EqualValues(t, len(contentB), snapAfter.Files[0].SizeBytes)
	assertFileContent(t, checkoutAfter, "b.txt", contentB)
	require.NoFileExists(t, filepath.Join(checkoutAfter, "a.txt"), "a fresh clone must only materialize files that still exist on the server")
}
