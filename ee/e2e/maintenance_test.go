package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func TestEnterpriseFrozenOnReadOnlyDatabase(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))
	chunks := newS3ChunkStore(t)

	host, uc := startEnterpriseServerWithUsecases(t, dbConn, chunks, mustSnowNode(t, 20), nil)
	repo, pusher := connectClient(t, host)
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, work))
	writeFile(t, work, "frozen.txt", "before the move\n")
	stagePath(t, work, "frozen.txt")
	require.NoError(t, pusher.Run(ctx, work, "add frozen.txt"))

	transport := clientgrpc.NewTransport()
	require.NoError(t, transport.Connect(host))
	login, err := transport.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)

	system := uc.Maintenance.WithSystemActor()
	_, err = system.SetServer(ctx, serverusecase.MaintenanceInput{Mode: "frozen", Message: "moving the database"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = system.EndServer(context.Background()) })

	separator := "?"
	if strings.Contains(testPGDSN, "?") {
		separator = "&"
	}
	readOnly, err := eedb.Open(testPGDSN + separator + "default_transaction_read_only=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = readOnly.Close() })
	frozenHost, frozenUc := startEnterpriseServerWithUsecases(t, readOnly, chunks, mustSnowNode(t, 21), nil)
	require.True(t, frozenUc.Maintenance.Service().Frozen(), "the guarded database reports frozen")

	store := newMemoryStore()
	login.Host = frozenHost
	require.NoError(t, store.SaveToken(login))
	frozenTransport := clientgrpc.NewTransport()
	client := clientgrpc.NewClient(frozenTransport, clientusecase.NewSession(store, frozenTransport, failPrompt{}))
	require.NoError(t, client.Connect(ctx, frozenHost))
	auth := clientusecase.NewAuth(client, store, failPrompt{})

	frozenRepo := clientusecase.NewRepo(auth, client, localrepo.NewLocalRepo())
	checkout := filepath.Join(t.TempDir(), "checkout")
	frozenURL := "http://" + frozenHost + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, frozenRepo.Clone(ctx, frozenURL, frozenHost, e2eOrgSlug, e2eProjectSlug, "", nil, checkout), "clone reads a guarded database")
	require.Equal(t, "before the move\n", readFile(t, checkout, "frozen.txt"))
	_, err = client.GetCommitLog(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil, 10)
	require.NoError(t, err)
	_, _, err = client.ListMergeRequests(ctx, e2eOrgSlug, e2eProjectSlug, clientdomain.ListMergeRequestOptions{})
	require.NoError(t, err)
	_, err = client.ListFileLocks(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	writeFile(t, checkout, "late.txt", "after the freeze\n")
	stagePath(t, checkout, "late.txt")
	err = clientusecase.NewPush(auth, client, localrepo.NewLocalRepo()).Run(ctx, checkout, "add late.txt")
	require.True(t, clientdomain.IsMaintenance(err), "a push is refused with 503, not a database error: %v", err)
	_, err = client.LoginWithUsernamePassword(ctx, frozenHost, e2eSuperAdmin, e2eSuperPass)
	require.True(t, clientdomain.IsMaintenance(err))
}
