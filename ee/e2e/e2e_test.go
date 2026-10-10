// Package e2e is the Nipa Enterprise Edition smoke suite: postgres and minio
// run in testcontainers, the server graph is built on the ee repositories and
// the S3 chunk store, and the real client performs a clone/push/tag round trip.
//
// Enterprise Edition: see ee/LICENSE.
package e2e

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/nodelease"
	"github.com/nipalab/nipa/ee/repository/postgres"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
	servergrpc "github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

const (
	e2eJWTSecret   = "ee-e2e-secret"
	e2eOrgSlug     = "default"
	e2eProjectSlug = "default"
	e2eSuperAdmin  = "nipa"
	e2eSuperPass   = "nipa"
	e2eBucket      = "nipa"
	e2eMinioUser   = "minioadmin"
	e2eMinioPass   = "minioadmin"
)

var (
	testPGDSN      string
	testS3Endpoint string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	var pgContainer *tcpostgres.PostgresContainer
	if dsn := os.Getenv("NIPA_TEST_POSTGRES_DSN"); dsn != "" {
		testPGDSN = dsn
	} else {
		container, err := tcpostgres.Run(ctx, "postgres:17",
			tcpostgres.WithDatabase("nipa"),
			tcpostgres.WithUsername("nipa"),
			tcpostgres.WithPassword("nipa"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			fmt.Fprintln(os.Stderr, "start postgres container:", err)
			os.Exit(1)
		}
		pgContainer = container
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			fmt.Fprintln(os.Stderr, "postgres connection string:", err)
			os.Exit(1)
		}
		testPGDSN = dsn
	}

	var minioContainer *tcminio.MinioContainer
	if endpoint := os.Getenv("NIPA_TEST_S3_ENDPOINT"); endpoint != "" {
		testS3Endpoint = endpoint
	} else {
		image := os.Getenv("NIPA_TEST_S3_IMAGE")
		if image == "" {
			image = "docker.io/pgsty/minio:latest"
		}
		container, err := tcminio.Run(ctx, image)
		if err != nil {
			fmt.Fprintln(os.Stderr, "start minio container:", err)
			if pgContainer != nil {
				_ = testcontainers.TerminateContainer(pgContainer)
			}
			os.Exit(1)
		}
		minioContainer = container
		endpoint, err := container.ConnectionString(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "minio connection string:", err)
			os.Exit(1)
		}
		testS3Endpoint = endpoint
	}

	code := m.Run()
	if pgContainer != nil {
		_ = testcontainers.TerminateContainer(pgContainer)
	}
	if minioContainer != nil {
		_ = testcontainers.TerminateContainer(minioContainer)
	}
	os.Exit(code)
}

type memoryStore struct {
	mu   sync.Mutex
	data map[string]*clientdomain.LoginResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: map[string]*clientdomain.LoginResult{}}
}

func (s *memoryStore) SaveToken(data *clientdomain.LoginResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[data.Host] = data
	return nil
}

func (s *memoryStore) LoadToken(host string) (*clientdomain.LoginResult, error) {
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

type testRegistry struct {
	auth         *serverusecase.Auth
	user         *serverusecase.User
	branch       *serverusecase.Branch
	tag          *serverusecase.Tag
	common       *serverusecase.Common
	push         *serverusecase.Push
	chunk        *serverusecase.Chunk
	permission   *serverusecase.Permission
	group        *serverusecase.Group
	mergeRequest *serverusecase.MergeRequest
	review       *serverusecase.MergeRequestReview
	check        *serverusecase.MergeRequestCheck
	fileLock     *serverusecase.FileLock
}

func (r *testRegistry) Auth() *serverusecase.Auth     { return r.auth }
func (r *testRegistry) User() *serverusecase.User     { return r.user }
func (r *testRegistry) Branch() *serverusecase.Branch { return r.branch }
func (r *testRegistry) Tag() *serverusecase.Tag       { return r.tag }
func (r *testRegistry) Common() *serverusecase.Common { return r.common }
func (r *testRegistry) Push() *serverusecase.Push     { return r.push }
func (r *testRegistry) Chunk() *serverusecase.Chunk   { return r.chunk }
func (r *testRegistry) Permission() *serverusecase.Permission {
	return r.permission
}
func (r *testRegistry) Group() *serverusecase.Group { return r.group }
func (r *testRegistry) MergeRequest() *serverusecase.MergeRequest {
	return r.mergeRequest
}
func (r *testRegistry) MergeRequestReview() *serverusecase.MergeRequestReview {
	return r.review
}
func (r *testRegistry) MergeRequestCheck() *serverusecase.MergeRequestCheck { return r.check }
func (r *testRegistry) FileLock() *serverusecase.FileLock                   { return r.fileLock }

func startEnterpriseServer(t *testing.T, dbConn *sql.DB, chunkStore storage.ChunkStore, node snow.Node) string {
	t.Helper()

	orgRepo := postgres.NewOrgRepository(dbConn)
	projectRepo := postgres.NewProjectRepository(dbConn)
	userRepo := postgres.NewUserRepository(dbConn)
	authRepo := postgres.NewAuthRepository(dbConn)
	branchRepo := postgres.NewBranchRepository(dbConn)
	pushRepo := postgres.NewPushRepository(dbConn)
	pbacRepo := postgres.NewPBACRepository(dbConn)

	passwordHasher := hasher.NewHasher(2)
	t.Cleanup(passwordHasher.Close)

	authUc := serverusecase.NewAuth(e2eJWTSecret, passwordHasher, userRepo, authRepo)
	groupRepo := postgres.NewGroupRepository(dbConn)
	orgUc := serverusecase.NewOrg(orgRepo, node)
	permissionUc := serverusecase.NewPermission(pbacRepo, userRepo, groupRepo, orgUc)
	commonUc := serverusecase.NewCommon(orgRepo, projectRepo)
	branchUc := serverusecase.NewBranchWithChunks(permissionUc, branchRepo, node, chunkStore)
	fileLockUc := serverusecase.NewFileLock(postgres.NewFileLockRepository(dbConn), branchRepo, permissionUc, node)
	branchUc = branchUc.WithFileLocks(fileLockUc)

	chunkUc := serverusecase.NewChunk(pushRepo, chunkStore, serverusecase.ChunkTransferConfig{
		SigningKey:  "ee-e2e-chunk-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 1000,
	})
	mrRepo := postgres.NewMergeRequestRepository(dbConn)
	reviewUc := serverusecase.NewMergeRequestReview(
		postgres.NewMergeRequestReviewRepository(dbConn), mrRepo, branchRepo, branchUc, permissionUc, userRepo, node,
	)
	checkUc := serverusecase.NewMergeRequestCheck(
		postgres.NewMergeRequestCheckRepository(dbConn), mrRepo, branchRepo, permissionUc, node,
	)
	reg := &testRegistry{
		auth:         authUc,
		user:         serverusecase.NewUser(node, userRepo, passwordHasher),
		branch:       branchUc,
		tag:          serverusecase.NewTag(permissionUc, postgres.NewTagRepository(dbConn), branchRepo, node),
		common:       commonUc,
		push:         serverusecase.NewPush(permissionUc, branchRepo, pushRepo, node).WithFileLocks(fileLockUc).WithReviews(reviewUc),
		chunk:        chunkUc,
		permission:   permissionUc,
		group:        serverusecase.NewGroup(groupRepo, node, permissionUc, orgUc),
		mergeRequest: serverusecase.NewMergeRequest(mrRepo, branchRepo, permissionUc, branchUc, node, dbtx.NewTransactor(dbConn)).WithFileLocks(fileLockUc).WithReview(reviewUc).WithChecks(checkUc),
		review:       reviewUc,
		check:        checkUc,
		fileLock:     fileLockUc,
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	interceptor := servergrpc.NewInterceptor(authUc)
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor.JWTUnary()),
		grpc.StreamInterceptor(interceptor.JWTStream()),
	)
	pb.RegisterNipaServiceServer(grpcServer, servergrpc.New(reg))

	chunkHandler := api.NewChunkTransferHandler(chunkUc)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				grpcServer.ServeHTTP(w, r)
				return
			}
			chunkHandler.ServeHTTP(w, r)
		}),
		Protocols: protocols,
	}
	go func() { _ = httpServer.Serve(lis) }()
	t.Cleanup(func() {
		_ = httpServer.Close()
		grpcServer.Stop()
	})

	return lis.Addr().String()
}

func newS3ChunkStore(t *testing.T) storage.ChunkStore {
	t.Helper()

	endpoint := testS3Endpoint
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	secure := strings.HasPrefix(endpoint, "https://")
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")

	ctx := context.Background()
	admin, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(e2eMinioUser, e2eMinioPass, ""),
		Secure: secure,
	})
	require.NoError(t, err)

	exists, err := admin.BucketExists(ctx, e2eBucket)
	require.NoError(t, err)
	if !exists {
		require.NoError(t, admin.MakeBucket(ctx, e2eBucket, minio.MakeBucketOptions{}))
	}

	store, err := s3store.New(ctx, s3store.Config{
		Endpoint:        endpoint,
		Bucket:          e2eBucket,
		AccessKeyID:     e2eMinioUser,
		SecretAccessKey: e2eMinioPass,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func writeFile(t *testing.T, target, path, content string) {
	t.Helper()
	fp := filepath.Join(target, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
}

func stagePath(t *testing.T, target, path string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	require.NoError(t, lr.StageAdd(path))
}

func readFile(t *testing.T, target, path string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
	require.NoError(t, err)
	return string(got)
}

func mustSnowNode(t *testing.T, nodeID int64) snow.Node {
	t.Helper()
	node, err := snow.NewNode(nodeID)
	require.NoError(t, err)
	return node
}

func connectClient(t *testing.T, host string) (*clientusecase.Repo, *clientusecase.Push) {
	t.Helper()

	ctx := context.Background()
	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	return clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()),
		clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
}

func TestEnterpriseClonePushTag(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	host := startEnterpriseServer(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NotEmpty(t, loginResult.AccessToken)
	require.NoError(t, store.SaveToken(loginResult))

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	const content = "enterprise e2e through s3\n"
	writeFile(t, target, "hello.txt", content)
	stagePath(t, target, "hello.txt")

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "add hello"))

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, content, readFile(t, checkout, "hello.txt"))

	tagUc := clientusecase.NewTag(auth, grpcClient, localrepo.NewLocalRepo())
	created, err := tagUc.Create(ctx, target, "v0.1.0", "first enterprise release", clientusecase.TagTarget{Branch: "main"})
	require.NoError(t, err)
	require.Equal(t, "v0.1.0", created.Name)

	tags, err := tagUc.List(ctx, target)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	require.Equal(t, "v0.1.0", tags[0].Name)
	require.NotEmpty(t, tags[0].CommitID)
}

func TestEnterpriseTwoInstanceNodeLease(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	_, err = dbConn.ExecContext(ctx, "DELETE FROM snowflake_node_leases")
	require.NoError(t, err)

	newManager := func(holder string) *nodelease.Manager {
		t.Helper()
		manager, err := nodelease.New(nodelease.Config{DB: dbConn, Holder: holder, TTL: 30 * time.Second})
		require.NoError(t, err)
		t.Cleanup(func() { manager.Release(context.Background()) })
		return manager
	}

	mgrA := newManager("instance-a")
	nodeA, err := mgrA.Acquire(ctx)
	require.NoError(t, err)

	mgrB := newManager("instance-b")
	nodeB, err := mgrB.Acquire(ctx)
	require.NoError(t, err)

	mgrC := newManager("instance-c")
	_, err = mgrC.Acquire(ctx)
	require.NoError(t, err)

	require.NotEqual(t, mgrA.NodeID(), mgrB.NodeID())
	require.NotEqual(t, mgrA.NodeID(), mgrC.NodeID())
	require.NotEqual(t, mgrB.NodeID(), mgrC.NodeID())

	var leased int
	require.NoError(t, dbConn.QueryRowContext(ctx, "SELECT count(*) FROM snowflake_node_leases").Scan(&leased))
	require.Equal(t, 3, leased)

	chunkStore := newS3ChunkStore(t)
	hostA := startEnterpriseServer(t, dbConn, chunkStore, nodeA)
	hostB := startEnterpriseServer(t, dbConn, chunkStore, nodeB)

	repoA, pushA := connectClient(t, hostA)
	repoB, pushB := connectClient(t, hostB)

	urlA := "http://" + hostA + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	urlB := "http://" + hostB + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	targetA := filepath.Join(t.TempDir(), "work-a")
	require.NoError(t, repoA.Clone(ctx, urlA, hostA, e2eOrgSlug, e2eProjectSlug, "", nil, targetA))
	writeFile(t, targetA, "instance-a.txt", "written through instance a\n")
	stagePath(t, targetA, "instance-a.txt")
	require.NoError(t, pushA.Run(ctx, targetA, "add instance-a.txt"))

	targetB := filepath.Join(t.TempDir(), "work-b")
	require.NoError(t, repoB.Clone(ctx, urlB, hostB, e2eOrgSlug, e2eProjectSlug, "", nil, targetB))
	require.Equal(t, "written through instance a\n", readFile(t, targetB, "instance-a.txt"))
	writeFile(t, targetB, "instance-b.txt", "written through instance b\n")
	stagePath(t, targetB, "instance-b.txt")
	require.NoError(t, pushB.Run(ctx, targetB, "add instance-b.txt"))

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repoA.Clone(ctx, urlA, hostA, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, "written through instance b\n", readFile(t, checkout, "instance-b.txt"))

	mgrA.Release(ctx)
	mgrB.Release(ctx)
	mgrC.Release(ctx)

	require.NoError(t, dbConn.QueryRowContext(ctx, "SELECT count(*) FROM snowflake_node_leases").Scan(&leased))
	require.Zero(t, leased)

	reacquired, err := newManager("instance-d").Acquire(ctx)
	require.NoError(t, err)
	require.NotNil(t, reacquired)
}
