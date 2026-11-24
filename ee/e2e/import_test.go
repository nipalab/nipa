package e2e

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/chunker"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func eeImportFile(t *testing.T, c *clientgrpc.Client, path, content string) *serverDomain.PushFile {
	t.Helper()
	encoding, encoded, err := chunker.EncodeBytes([]byte(content), path, false, "")
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, 0, len(encoded))
	chunks := make([]*serverDomain.ChunkData, 0, len(encoded))
	for _, e := range encoded {
		hashes = append(hashes, e.Hash)
		chunks = append(chunks, &serverDomain.ChunkData{Hash: e.Hash, Data: e.Data, RawSize: e.RawSize})
	}
	_, _, err = c.UploadChunks(context.Background(), clientdomain.ChunkScope{Org: e2eOrgSlug, Project: e2eProjectSlug}, chunks)
	require.NoError(t, err)
	return &serverDomain.PushFile{Path: path, Mode: 0o644, SizeBytes: int64(len(content)), Encoding: encoding, FileHash: treehash.FileHash(hashes), ChunkHashes: hashes}
}

func eeStreamImport(t *testing.T, c *clientgrpc.Client, commits []*clientgrpc.ImportCommit) []*clientgrpc.ImportedCommit {
	t.Helper()
	session, err := c.OpenImport(context.Background(), e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	defer session.Close()
	for _, commit := range commits {
		require.NoError(t, session.Send(commit))
	}
	require.NoError(t, session.CloseSend())
	var out []*clientgrpc.ImportedCommit
	for {
		res, err := session.Recv()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, res)
	}
}

func TestEnterpriseGitImport(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))
	host := startEnterpriseServer(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), nil)

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))

	origin := func(n int) string { return fmt.Sprintf("git:ee-e2e:%040x", n) }
	authored := time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC)
	commits := []*clientgrpc.ImportCommit{
		{Origin: origin(1), Message: "root", AuthorName: "Ann", AuthorEmail: "ann@example.com", AuthoredAt: authored, Files: []*serverDomain.PushFile{eeImportFile(t, grpcClient, "a.txt", "root\n")}},
		{Origin: origin(2), ParentOrigins: []string{origin(1)}, Message: "child", AuthoredAt: authored, Files: []*serverDomain.PushFile{eeImportFile(t, grpcClient, "a.txt", "child\n")}},
		{Origin: origin(3), ParentOrigins: []string{origin(2), origin(1)}, Message: "merge", AuthoredAt: authored, Files: []*serverDomain.PushFile{eeImportFile(t, grpcClient, "m.txt", "merged\n")}},
	}
	imported := eeStreamImport(t, grpcClient, commits)
	require.Len(t, imported, 3)

	results, err := grpcClient.ImportSetRefs(ctx, e2eOrgSlug, e2eProjectSlug, clientgrpc.ImportRefs{
		Branches: []clientgrpc.ImportRef{{Name: "imported", Origin: origin(3)}, {Name: "imported-root", Origin: origin(1)}},
		Tags:     []clientgrpc.ImportRef{{Name: "imported-v1", Origin: origin(2)}},
	})
	require.NoError(t, err)
	for _, r := range results {
		require.Equal(t, serverusecase.ImportRefCreated, r.Status, r.Name)
	}

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "imported")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "imported", nil, target))
	require.Equal(t, "child\n", readFile(t, target, "a.txt"))
	require.Equal(t, "merged\n", readFile(t, target, "m.txt"))

	again := eeStreamImport(t, grpcClient, commits)
	for i := range again {
		require.Equal(t, imported[i].CommitID, again[i].CommitID)
	}

	root, err := postgres.NewBranchRepository(dbConn).GetCommit(ctx, imported[0].CommitID)
	require.NoError(t, err)
	require.Equal(t, "Ann", root.AuthorName)
	require.Equal(t, origin(1), root.Origin)
	require.True(t, authored.Equal(root.CreatedAt), root.CreatedAt.String())
}

func TestEnterpriseGitImportQuotaEstimate(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))
	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	usage, err := ledger.OrgStorage(ctx, snow.ID(1))
	require.NoError(t, err)
	require.NoError(t, ledger.SetComputedQuota(ctx, snow.ID(1), usage.UsedBytes+4096))
	host := startEnterpriseServer(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), ledger)

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	grpcClient := clientgrpc.NewClient(transport, clientusecase.NewSession(store, transport, failPrompt{}))
	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))

	checked, err := grpcClient.ImportEstimate(ctx, e2eOrgSlug, e2eProjectSlug, 1024)
	require.NoError(t, err)
	require.True(t, checked)

	_, err = grpcClient.ImportEstimate(ctx, e2eOrgSlug, e2eProjectSlug, 1<<20)
	var clientErr *clientdomain.Error
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, 402, clientErr.Code)
	require.Contains(t, clientErr.Message, "storage quota exceeded")

	require.NoError(t, ledger.SetComputedQuota(ctx, snow.ID(1), 0))
	checked, err = grpcClient.ImportEstimate(ctx, e2eOrgSlug, e2eProjectSlug, 1<<20)
	require.NoError(t, err)
	require.True(t, checked, "an unlimited quota still answers through the ledger")
}
