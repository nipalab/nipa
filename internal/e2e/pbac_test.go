package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func loginE2EClient(t *testing.T, ctx context.Context, host, email, password string) (*clientgrpc.Client, *memoryStore) {
	t.Helper()

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	client := clientgrpc.NewClient(transport, session)
	require.NoError(t, client.Connect(ctx, host))

	result, err := client.LoginWithUsernamePassword(ctx, host, email, password)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(result))
	return client, store
}

func clientErrorCode(t *testing.T, err error) int {
	t.Helper()

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	return domErr.Code
}

func TestEndToEnd_PBACManifestFiltering(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)

	adminClient, adminStore := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	adminAuth := clientusecase.NewAuth(adminClient, adminStore, failPrompt{})

	repo := clientusecase.NewRepo(adminAuth, adminClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	writeFile(t, target, "assets/logo.png", "logo-bytes")
	writeFile(t, target, "src/main.go", "package main")
	stagePath(t, target, "assets/logo.png")
	stagePath(t, target, "src/main.go")
	pusher := clientusecase.NewPush(adminAuth, adminClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "seed pbac files"))

	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("regular-pass")
	require.NoError(t, err)

	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		42, "Regular User", "regular@example.com", passwordHash,
	)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		43, "No Rule User", "norule@example.com", passwordHash,
	)
	require.NoError(t, err)

	projectID := snow.ID(1)
	regularID := snow.ID(42)
	_, err = sqlite.NewPBACRepository(dbConn).CreateRule(ctx, serverDomain.PBACRule{
		UserID:     &regularID,
		OrgID:      1,
		ProjectID:  &projectID,
		PathPrefix: "assets",
		Permission: serverDomain.PermissionRead,
	})
	require.NoError(t, err)

	regularClient, _ := loginE2EClient(t, ctx, host, "regular@example.com", "regular-pass")

	branches, err := regularClient.ListBranches(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.NotEmpty(t, branches, "a user with a rule can list branches")

	manifest, err := regularClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.Empty(t, manifest.FileChildren)
	require.Len(t, manifest.TreeChildren, 1, "src must be pruned")
	require.Equal(t, "assets", manifest.TreeChildren[0].Name)
	require.Len(t, manifest.TreeChildren[0].FileChildren, 1)
	require.Equal(t, "logo.png", manifest.TreeChildren[0].FileChildren[0].Name)

	_, err = regularClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", []string{"src"})
	require.Error(t, err)
	require.Equal(t, 404, clientErrorCode(t, err), "hidden paths must look missing")

	noRuleClient, _ := loginE2EClient(t, ctx, host, "norule@example.com", "regular-pass")
	_, err = noRuleClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.Error(t, err)
	require.Equal(t, 403, clientErrorCode(t, err), "no rules means no access")
}
