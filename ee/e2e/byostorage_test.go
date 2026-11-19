package e2e

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/byostorage"
	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
)

func minioAdmin(t *testing.T) (*minio.Client, string) {
	t.Helper()
	endpoint := testS3Endpoint
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	admin, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(e2eMinioUser, e2eMinioPass, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	require.NoError(t, err)
	return admin, endpoint
}

func objectKeys(t *testing.T, admin *minio.Client, bucket, prefix string) []string {
	t.Helper()
	var keys []string
	for object := range admin.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		require.NoError(t, object.Err)
		keys = append(keys, object.Key)
	}
	return keys
}

func TestEnterpriseBYOStorage(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	nipaStore := newS3ChunkStore(t)
	admin, endpoint := minioAdmin(t)
	suffix := randomSuffix(t)
	byoBucket := "byo-" + suffix
	require.Eventually(t, func() bool {
		return admin.MakeBucket(ctx, byoBucket, minio.MakeBucketOptions{}) == nil
	}, 30*time.Second, 250*time.Millisecond)

	node := mustSnowNode(t, 0)
	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	svc, err := byostorage.New(postgres.NewBYOStorageRepository(dbConn), ledger, dbtx.NewTransactor(dbConn), node, byostorage.Config{
		Key:          []byte(strings.Repeat("e2e-key-", 4)),
		AllowPrivate: true,
	})
	require.NoError(t, err)
	host, uc := startEnterpriseServerWith(t, dbConn, nipaStore, node, serverOptions{
		ledger:         ledger,
		chunkStores:    svc.Resolver(nipaStore),
		projectStorage: svc,
	})
	svc.WithAudit(uc.Audit).WithOrgs(uc.Org)

	admin1 := domain.ContextWithClaim(ctx, domain.Claims{UserID: 1, IsSuperAdmin: true})
	org, err := uc.Org.Create(admin1, "BYO "+suffix, "byo-"+suffix)
	require.NoError(t, err)

	server, err := svc.Create(admin1, byostorage.ServerInput{
		Name:            "minio",
		Endpoint:        endpoint,
		Bucket:          byoBucket,
		Prefix:          "tenant",
		AccessKeyID:     e2eMinioUser,
		SecretAccessKey: e2eMinioPass,
	})
	require.NoError(t, err)
	require.Empty(t, objectKeys(t, admin, byoBucket, ""), "the probe object is removed")

	_, err = svc.Create(admin1, byostorage.ServerInput{
		Name: "missing bucket", Endpoint: endpoint, Bucket: "no-such-" + suffix,
		AccessKeyID: e2eMinioUser, SecretAccessKey: e2eMinioPass,
	})
	var failure *byostorage.ProbeFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "bucket", failure.Check)

	before, err := uc.Project.Create(admin1, org.ID, "Before", "", "before", nil)
	require.NoError(t, err)
	require.NoError(t, svc.SetOrgDefault(admin1, org.ID, &server.ID))
	byoProject, err := uc.Project.Create(admin1, org.ID, "Own", "", "own", nil)
	require.NoError(t, err)

	assigned, err := svc.ProjectServer(ctx, byoProject.ID)
	require.NoError(t, err)
	require.Equal(t, server.ID, assigned.ID)
	none, err := svc.ProjectServer(ctx, before.ID)
	require.NoError(t, err)
	require.Nil(t, none, "a project created before the default keeps Nipa storage")
	require.Equal(t, 409, domainCode(svc.Delete(admin1, server.ID)))

	repo, pusher := connectClient(t, host)
	push := func(slug, content string) {
		url := "http://" + host + "/" + org.Slug + "/" + slug
		target := filepath.Join(t.TempDir(), slug)
		require.NoError(t, repo.Clone(ctx, url, host, org.Slug, slug, "", nil, target))
		writeFile(t, target, "asset.txt", content)
		stagePath(t, target, "asset.txt")
		require.NoError(t, pusher.Run(ctx, target, "add asset"))

		checkout := filepath.Join(t.TempDir(), slug+"-checkout")
		require.NoError(t, repo.Clone(ctx, url, host, org.Slug, slug, "", nil, checkout))
		require.Equal(t, content, readFile(t, checkout, "asset.txt"))
	}
	push("own", "stored in the customer's bucket "+suffix+"\n")

	keys := objectKeys(t, admin, byoBucket, "tenant/")
	require.NotEmpty(t, keys, "chunks land in the customer's bucket under its prefix")
	for _, key := range keys {
		hash, err := domain.ParseHashHex(strings.ReplaceAll(strings.TrimPrefix(key, "tenant/"), "/", ""))
		require.NoError(t, err)
		inNipa, err := nipaStore.Exists(ctx, hash)
		require.NoError(t, err)
		require.False(t, inNipa, "own-storage content never reaches the Nipa bucket")
	}

	push("before", "stored by nipa "+suffix+"\n")
	require.Len(t, objectKeys(t, admin, byoBucket, "tenant/"), len(keys), "the older project still writes to the Nipa bucket")

	usage, err := ledger.OrgStorage(ctx, org.ID)
	require.NoError(t, err)
	byProject := map[string]storageusage.ProjectUsage{}
	for _, project := range usage.Projects {
		byProject[project.Slug] = project
	}
	require.True(t, byProject["own"].External)
	require.Zero(t, byProject["own"].UsedBytes)
	require.False(t, byProject["before"].External)
	require.Positive(t, byProject["before"].UsedBytes)
	require.Equal(t, byProject["before"].UsedBytes, usage.UsedBytes, "own-storage bytes are not billed")

	require.NoError(t, ledger.RecountOrg(ctx, org.ID))
	recounted, err := ledger.OrgStorage(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, usage.UsedBytes, recounted.UsedBytes)
}

func domainCode(err error) int {
	var e *domain.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
