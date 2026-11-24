package e2e

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/ee/billing/paddle"
	"github.com/nipalab/nipa/ee/byostorage"
	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

func ownStorageEvent(eventID, eventType, subID, status string, ownStorage bool, updated time.Time) []byte {
	items := `{"quantity":1,"price":{"id":"pri_seat_m"}}`
	if ownStorage {
		items += `,{"quantity":1,"price":{"id":"pri_own_m"}}`
	}
	return fmt.Appendf(nil, `{"event_id":%q,"event_type":%q,"occurred_at":%q,"notification_id":"ntf_own","data":{
		"id":%q,"status":%q,"customer_id":"ctm_e2e","updated_at":%q,
		"custom_data":{"user_id":"1"},"items":[%s],"current_billing_period":null,"scheduled_change":null}}`,
		eventID, eventType, updated.Format(time.RFC3339Nano), subID, status, updated.Format(time.RFC3339Nano), items)
}

func TestEnterpriseBYOStorageOwnStorageAddOn(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	nipaStore := newS3ChunkStore(t)
	admin, endpoint := minioAdmin(t)
	suffix := randomSuffix(t)
	bucket := "own-" + suffix
	require.Eventually(t, func() bool {
		return admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}) == nil
	}, 30*time.Second, 250*time.Millisecond)

	prices := billingPrices
	prices.OwnStorageMonthly, prices.OwnStorageYearly = "pri_own_m", "pri_own_y"
	provider := paddle.New(paddle.Config{APIKey: "pdl_sdbx_apikey_e2e", WebhookSecret: billingWebhookSecret, Prices: prices, BaseURL: "http://127.0.0.1:1"})
	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	billingRepo := postgres.NewBillingRepository(dbConn)
	billingSvc := billing.New(billingRepo, ledger, dbtx.NewTransactor(dbConn), billing.Config{
		FreeSeats: 1, OwnStorage: true,
	}).WithProvider(provider)
	require.NoError(t, billingRepo.InsertAccount(ctx, domain.BillingAccount{UserID: snow.ID(1), Provider: paddle.ProviderName, ProviderCustomerID: "ctm_e2e"}))

	node := mustSnowNode(t, 0)
	byo, err := byostorage.New(postgres.NewBYOStorageRepository(dbConn), ledger, dbtx.NewTransactor(dbConn), node, byostorage.Config{
		Key:          []byte(strings.Repeat("e2e-key-", 4)),
		AllowPrivate: true,
	})
	require.NoError(t, err)
	byo.WithEntitlement(billingSvc)
	host, uc := startEnterpriseServerWith(t, dbConn, nipaStore, node, serverOptions{
		ledger:         ledger,
		chunkStores:    byo.Resolver(nipaStore),
		projectStorage: byo,
	})
	byo.WithAudit(uc.Audit).WithOrgs(uc.Org)

	deliver := func(body []byte) {
		t.Helper()
		header := http.Header{}
		header.Set(paddle.SignatureHeader, paddle.Sign(billingWebhookSecret, time.Now(), body))
		event, err := provider.ParseWebhook(header, body)
		require.NoError(t, err)
		require.NoError(t, billingSvc.HandleEvent(ctx, event))
	}
	subID := "sub_own_" + suffix
	start := time.Now().UTC()
	t.Cleanup(func() {
		deliver(ownStorageEvent("evt_own_end_"+suffix, "subscription.canceled", subID, "canceled", false, start.Add(time.Hour)))
	})

	admin1 := domain.ContextWithClaim(ctx, domain.Claims{UserID: 1, IsSuperAdmin: true})
	input := byostorage.ServerInput{
		Name: "paid-" + suffix, Endpoint: endpoint, Bucket: bucket, Prefix: "paid",
		AccessKeyID: e2eMinioUser, SecretAccessKey: e2eMinioPass,
	}
	_, err = byo.Create(admin1, input)
	require.Equal(t, http.StatusPaymentRequired, domainCode(err), "no add-on, no storage server")

	deliver(ownStorageEvent("evt_own_on_"+suffix, "subscription.activated", subID, "active", true, start))
	summary, err := billingSvc.Summary(ctx, snow.ID(1))
	require.NoError(t, err)
	require.True(t, summary.OwnStorage)

	server, err := byo.Create(admin1, input)
	require.NoError(t, err)
	org, err := uc.Org.Create(admin1, "Own "+suffix, "own-"+suffix)
	require.NoError(t, err)
	require.NoError(t, byo.SetOrgDefault(admin1, org.ID, &server.ID))
	_, err = uc.Project.Create(admin1, org.ID, "Paid", "", "paid", nil)
	require.NoError(t, err)

	repo, pusher := connectClient(t, host)
	url := "http://" + host + "/" + org.Slug + "/paid"
	target := filepath.Join(t.TempDir(), "paid")
	require.NoError(t, repo.Clone(ctx, url, host, org.Slug, "paid", "", nil, target))
	writeFile(t, target, "asset.txt", "paid for "+suffix+"\n")
	stagePath(t, target, "asset.txt")
	require.NoError(t, pusher.Run(ctx, target, "add asset"))
	require.NotEmpty(t, objectKeys(t, admin, bucket, "paid/"))

	deliver(ownStorageEvent("evt_own_off_"+suffix, "subscription.updated", subID, "active", false, start.Add(time.Minute)))
	summary, err = billingSvc.Summary(ctx, snow.ID(1))
	require.NoError(t, err)
	require.False(t, summary.OwnStorage)

	writeFile(t, target, "more.txt", "after the add-on lapsed "+suffix+"\n")
	stagePath(t, target, "more.txt")
	err = pusher.Run(ctx, target, "add more")
	var clientErr *clientdomain.Error
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, http.StatusPaymentRequired, clientErr.Code, "push after the add-on lapsed: %v", err)

	checkout := filepath.Join(t.TempDir(), "paid-checkout")
	require.NoError(t, repo.Clone(ctx, url, host, org.Slug, "paid", "", nil, checkout))
	require.Equal(t, "paid for "+suffix+"\n", readFile(t, checkout, "asset.txt"), "reads stay open")

	_, err = byo.Create(admin1, input)
	require.Equal(t, http.StatusPaymentRequired, domainCode(err))
}
