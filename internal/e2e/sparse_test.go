package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func assertNoFile(t *testing.T, target, path string) {
	t.Helper()

	_, err := os.Stat(filepath.Join(target, filepath.FromSlash(path)))
	require.True(t, os.IsNotExist(err), "%s must not exist in a sparse checkout", path)
}

func TestEndToEnd_RestrictedUserSparsePush(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)

	adminClient, adminStore := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	adminAuth := clientusecase.NewAuth(adminClient, adminStore, failPrompt{})
	repo := clientusecase.NewRepo(adminAuth, adminClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, seed))
	writeFile(t, seed, "assets/logo.png", "logo-v1")
	writeFile(t, seed, "src/main.go", "package main")
	stagePath(t, seed, "assets/logo.png")
	stagePath(t, seed, "src/main.go")
	require.NoError(t, clientusecase.NewPush(adminAuth, adminClient, localrepo.NewLocalRepo()).Run(ctx, seed, "seed"))

	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("regular-pass")
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES (?, ?, ?, ?, 0)`,
		46, "Sparse Writer", "sparse@example.com", passwordHash,
	)
	require.NoError(t, err)
	projectID := snow.ID(1)
	writerID := snow.ID(46)
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

	writerClient, writerStore := loginE2EClient(t, ctx, host, "sparse@example.com", "regular-pass")
	writerAuth := clientusecase.NewAuth(writerClient, writerStore, failPrompt{})
	writerRepo := clientusecase.NewRepo(writerAuth, writerClient, localrepo.NewLocalRepo())
	work := filepath.Join(t.TempDir(), "writer")
	require.NoError(t, writerRepo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", []string{"assets"}, work))
	assertFileContent(t, work, "assets/logo.png", "logo-v1")
	assertNoFile(t, work, "src/main.go")

	writeFile(t, work, "assets/logo.png", "logo-v2")
	stagePath(t, work, "assets/logo.png")
	require.NoError(t, clientusecase.NewPush(writerAuth, writerClient, localrepo.NewLocalRepo()).Run(ctx, work, "sparse write"))

	full := filepath.Join(t.TempDir(), "full")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, full))
	assertFileContent(t, full, "assets/logo.png", "logo-v2")
	assertFileContent(t, full, "src/main.go", "package main")
}

func TestEndToEnd_SparseCheckout(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	adminClient, adminStore := loginE2EClient(t, ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	adminAuth := clientusecase.NewAuth(adminClient, adminStore, failPrompt{})
	repo := clientusecase.NewRepo(adminAuth, adminClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, seed))
	writeFile(t, seed, "assets/logo.png", "logo-v1")
	writeFile(t, seed, "src/main.go", "package main")
	writeFile(t, seed, "docs/readme.md", "docs-v1")
	stagePath(t, seed, "assets/logo.png")
	stagePath(t, seed, "src/main.go")
	stagePath(t, seed, "docs/readme.md")
	pusher := clientusecase.NewPush(adminAuth, adminClient, localrepo.NewLocalRepo())
	require.NoError(t, pusher.Run(ctx, seed, "seed sparse files"))

	work := filepath.Join(t.TempDir(), "sparse")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", []string{"assets"}, work))
	assertFileContent(t, work, "assets/logo.png", "logo-v1")
	assertNoFile(t, work, "src/main.go")
	assertNoFile(t, work, "docs/readme.md")

	writeFile(t, work, "assets/logo.png", "logo-v2")
	stagePath(t, work, "assets/logo.png")
	require.NoError(t, pusher.Run(ctx, work, "update logo"), "sparse clones must be able to push")

	full := filepath.Join(t.TempDir(), "full")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, full))
	assertFileContent(t, full, "assets/logo.png", "logo-v2")
	assertFileContent(t, full, "src/main.go", "package main")
	assertFileContent(t, full, "docs/readme.md", "docs-v1")

	updater := clientusecase.NewUpdate(adminAuth, adminClient, localrepo.NewLocalRepo())
	require.NoError(t, updater.SetSparse(work, []string{"assets", "docs"}))
	require.NoError(t, updater.Run(ctx, work))
	assertFileContent(t, work, "docs/readme.md", "docs-v1")

	require.NoError(t, updater.SetSparse(work, []string{"docs"}))
	require.NoError(t, updater.Run(ctx, work))
	assertNoFile(t, work, "assets/logo.png")
	assertFileContent(t, work, "docs/readme.md", "docs-v1")

	require.NoError(t, updater.SetSparse(work, nil))
	require.NoError(t, updater.Run(ctx, work))
	assertFileContent(t, work, "src/main.go", "package main")
	assertFileContent(t, work, "assets/logo.png", "logo-v2")
	assertFileContent(t, work, "docs/readme.md", "docs-v1")
}
