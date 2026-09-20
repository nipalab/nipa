package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
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

func TestEndToEnd_PBACAdminSurface(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)

	adminClient, _ := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)

	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("regular-pass")
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		45, "Managed", "managed@example.com", passwordHash,
	)
	require.NoError(t, err)
	userID := snow.ID(45).Base36()

	info, err := adminClient.GetMyPermissions(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Equal(t, uint64(serverDomain.PermissionAll), info.ProjectPermission)

	rule, err := adminClient.CreatePBACRule(ctx, e2eOrgSlug, e2eProjectSlug, userID, "", "assets",
		uint64(serverDomain.PermissionRead|serverDomain.PermissionWrite))
	require.NoError(t, err)
	require.NotZero(t, rule.ID)

	rules, err := adminClient.ListPBACRules(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, userID, rules[0].UserID)
	require.Equal(t, "assets", rules[0].PathPrefix)

	managedClient, _ := loginE2EClient(t, ctx, host, "managed@example.com", "regular-pass")
	managed, err := managedClient.GetMyPermissions(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Equal(t, uint64(serverDomain.PermissionRead|serverDomain.PermissionWrite), managed.ProjectPermission)
	require.Len(t, managed.Rules, 1)

	_, err = managedClient.ListPBACRules(ctx, e2eOrgSlug, e2eProjectSlug)
	require.Error(t, err)
	require.Equal(t, 403, clientErrorCode(t, err), "non-admins cannot list rules")

	entry, err := adminClient.SetProjectPathPermission(ctx, e2eOrgSlug, e2eProjectSlug, "", uint64(serverDomain.PermissionRead))
	require.NoError(t, err)
	require.Equal(t, uint64(serverDomain.PermissionRead), entry.Permission)
	defaults, err := adminClient.ListProjectPathPermissions(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Len(t, defaults, 1)
	require.NoError(t, adminClient.DeleteProjectPathPermission(ctx, e2eOrgSlug, e2eProjectSlug, ""))

	group, err := adminClient.CreateGroup(ctx, e2eOrgSlug, "artists", "2d team")
	require.NoError(t, err)
	require.NotEmpty(t, group.ID)
	groups, err := adminClient.ListGroups(ctx, e2eOrgSlug)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "artists", groups[0].Name)

	require.NoError(t, adminClient.AddGroupMember(ctx, e2eOrgSlug, group.ID, userID))
	groupRule, err := adminClient.CreatePBACRule(ctx, e2eOrgSlug, e2eProjectSlug, "", group.ID, "docs",
		uint64(serverDomain.PermissionRead))
	require.NoError(t, err)

	managed, err = managedClient.GetMyPermissions(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Len(t, managed.Rules, 2, "group rules must apply after membership changes")

	require.NoError(t, adminClient.RemoveGroupMember(ctx, e2eOrgSlug, group.ID, userID))
	managed, err = managedClient.GetMyPermissions(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Len(t, managed.Rules, 1, "removed members lose group rules immediately")

	require.NoError(t, adminClient.DeletePBACRule(ctx, e2eOrgSlug, e2eProjectSlug, rule.ID))
	require.NoError(t, adminClient.DeletePBACRule(ctx, e2eOrgSlug, e2eProjectSlug, groupRule.ID))
	rules, err = adminClient.ListPBACRules(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)
	require.Empty(t, rules)

	require.Error(t, adminClient.DeletePBACRule(ctx, e2eOrgSlug, e2eProjectSlug, rule.ID), "deleting a missing rule fails")
}

func TestEndToEnd_PBACWriteEnforcement(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)

	adminClient, adminStore := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	adminAuth := clientusecase.NewAuth(adminClient, adminStore, failPrompt{})
	repo := clientusecase.NewRepo(adminAuth, adminClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", target))

	writeFile(t, target, "assets/logo.png", "logo-v1")
	writeFile(t, target, "src/main.go", "package main")
	stagePath(t, target, "assets/logo.png")
	stagePath(t, target, "src/main.go")
	pusher := clientusecase.NewPush(adminAuth, adminClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, target, "seed pbac write files"))

	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("regular-pass")
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		44, "Writer", "writer@example.com", passwordHash,
	)
	require.NoError(t, err)

	projectID := snow.ID(1)
	writerID := snow.ID(44)
	pbacRepo := sqlite.NewPBACRepository(dbConn)
	_, err = pbacRepo.CreateRule(ctx, serverDomain.PBACRule{
		UserID: &writerID, OrgID: 1, ProjectID: &projectID,
		PathPrefix: "", Permission: serverDomain.PermissionRead,
	})
	require.NoError(t, err)
	_, err = pbacRepo.CreateRule(ctx, serverDomain.PBACRule{
		UserID: &writerID, OrgID: 1, ProjectID: &projectID,
		PathPrefix: "assets", Permission: serverDomain.PermissionRead | serverDomain.PermissionWrite,
	})
	require.NoError(t, err)

	writerClient, writerStore := loginE2EClient(t, ctx, host, "writer@example.com", "regular-pass")
	writerAuth := clientusecase.NewAuth(writerClient, writerStore, failPrompt{})
	writerRepo := clientusecase.NewRepo(writerAuth, writerClient, localrepo.NewLocalRepo())
	work := filepath.Join(t.TempDir(), "writer")
	require.NoError(t, writerRepo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", "", work))

	writeFile(t, work, "assets/logo.png", "logo-v2")
	stagePath(t, work, "assets/logo.png")
	writerPusher := clientusecase.NewPush(writerAuth, writerClient, localrepo.NewLocalRepo())
	require.NoError(t, writerPusher.Run(ctx, work, "update logo"))

	writeFile(t, work, "src/main.go", "package main // v2")
	stagePath(t, work, "src/main.go")
	err = writerPusher.Run(ctx, work, "update src")
	require.Error(t, err)
	require.Equal(t, 403, clientErrorCode(t, err), "write outside the granted prefix must be rejected")
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
	logo := manifest.TreeChildren[0].FileChildren[0]
	require.Equal(t, "logo.png", logo.Name)

	_, err = regularClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", []string{"src"})
	require.Error(t, err)
	require.Equal(t, 404, clientErrorCode(t, err), "hidden paths must look missing")

	head, err := regularClient.GetBranchByName(ctx, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.NotNil(t, head.CommitID)
	scope := clientDomain.ChunkScope{
		Org:       e2eOrgSlug,
		Project:   e2eProjectSlug,
		CommitIDs: []string{head.CommitID.Base36()},
	}

	require.NotEmpty(t, logo.Chunks)
	visibleDownloaded := 0
	err = regularClient.DownloadChunks(ctx, scope, []serverDomain.Hash{logo.Chunks[0].Hash}, func(_ serverDomain.Hash, _ []byte) error {
		visibleDownloaded++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, visibleDownloaded)

	hiddenChunks, err := chunker.ChunkAll([]byte("package main"))
	require.NoError(t, err)
	require.NotEmpty(t, hiddenChunks)
	err = regularClient.DownloadChunks(ctx, scope, []serverDomain.Hash{hiddenChunks[0].Hash}, func(_ serverDomain.Hash, _ []byte) error {
		return nil
	})
	require.Error(t, err)
	require.Equal(t, 404, clientErrorCode(t, err), "chunks outside the visible tree must not leak")

	noRuleClient, _ := loginE2EClient(t, ctx, host, "norule@example.com", "regular-pass")
	_, err = noRuleClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.Error(t, err)
	require.Equal(t, 403, clientErrorCode(t, err), "no rules means no access")
}
