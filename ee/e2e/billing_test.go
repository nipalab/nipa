package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/ee/billing/paddle"
	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	billingWebhookSecret = "pdl_ntfset_e2e"
	billingFreeQuota     = int64(64)
	billingSeatBytes     = int64(1 << 20)
	billingPackBytes     = int64(2 << 20)
)

var billingPrices = paddle.Prices{SeatMonthly: "pri_seat_m", SeatYearly: "pri_seat_y", StorageMonthly: "pri_st_m", StorageYearly: "pri_st_y"}

type fakePaddleAPI struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakePaddleAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/customers":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"ctm_e2e"}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/transactions":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"txn_e2e","status":"draft"}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"request_error","code":"not_found","detail":"not found"}}`)
	}
}

type restClient struct {
	t     *testing.T
	base  string
	token string
}

func (c *restClient) do(method, path string, body any, header http.Header) (int, map[string]any) {
	c.t.Helper()
	var raw []byte
	switch v := body.(type) {
	case nil:
	case []byte:
		raw = v
	default:
		var err error
		raw, err = json.Marshal(v)
		require.NoError(c.t, err)
	}
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(raw))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for key, values := range header {
		req.Header[key] = values
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return hex.EncodeToString(raw)
}

func subscriptionEvent(eventID, eventType, subID, status string, seats, packs int64, updated time.Time) []byte {
	period := `null`
	if status == "active" {
		period = fmt.Sprintf(`{"starts_at":%q,"ends_at":%q}`, updated.Format(time.RFC3339), updated.AddDate(0, 1, 0).Format(time.RFC3339))
	}
	return fmt.Appendf(nil, `{"event_id":%q,"event_type":%q,"occurred_at":%q,"notification_id":"ntf_e2e","data":{
		"id":%q,"status":%q,"customer_id":"ctm_e2e","updated_at":%q,
		"custom_data":{"user_id":"1"},
		"items":[{"quantity":%d,"price":{"id":"pri_seat_m"}},{"quantity":%d,"price":{"id":"pri_st_m"}}],
		"current_billing_period":%s,"scheduled_change":null}}`,
		eventID, eventType, updated.Format(time.RFC3339Nano), subID, status, updated.Format(time.RFC3339Nano), seats, packs, period)
}

func signedHeader(secret string, body []byte) http.Header {
	header := http.Header{}
	header.Set(paddle.SignatureHeader, paddle.Sign(secret, time.Now(), body))
	return header
}

func TestEnterpriseBillingPaddleSmoke(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	fakeAPI := &fakePaddleAPI{}
	fakeServer := httptest.NewServer(fakeAPI)
	t.Cleanup(fakeServer.Close)

	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	repo := postgres.NewBillingRepository(dbConn)
	provider := paddle.New(paddle.Config{APIKey: "pdl_sdbx_apikey_e2e", WebhookSecret: billingWebhookSecret, Prices: billingPrices, BaseURL: fakeServer.URL})
	service := billing.New(repo, ledger, dbtx.NewTransactor(dbConn), billing.Config{
		FreeSeats: 1, FreeQuotaBytes: billingFreeQuota, SeatIncludedBytes: billingSeatBytes, StoragePackBytes: billingPackBytes,
	}).WithProvider(provider)

	host, _ := startEnterpriseServerWith(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), serverOptions{
		ledger:   ledger,
		seatGate: service,
		rest: func(reg *serverapp.Registry) http.Handler {
			return api.NewAPI(reg,
				api.WithRoutes(func(ws *restful.WebService) {
					storageusage.RegisterRoutes(ws, storageusage.RoutesConfig{Ledger: ledger, Billing: service.Summary})
					billing.RegisterRoutes(ws, service)
				}),
				api.WithWebService(billing.WebhookService(service)),
			).SetupRoute()
		},
	})
	rest := &restClient{t: t, base: "http://" + host}

	status, login := rest.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": e2eSuperAdmin, "password": e2eSuperPass}, nil)
	require.Equal(t, http.StatusOK, status, login)
	rest.token = login["access_token"].(string)

	seatsUsed, err := repo.CountSeats(ctx, snow.ID(1))
	require.NoError(t, err)
	paidSeats := seatsUsed + 1

	status, body := rest.do(http.MethodPost, "/api/v1/billing/checkout", map[string]any{"seats": paidSeats, "interval": "month"}, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "txn_e2e", body["transaction_id"])
	account, err := repo.Account(ctx, snow.ID(1))
	require.NoError(t, err)
	require.Equal(t, "ctm_e2e", account.ProviderCustomerID)

	webhook := billing.WebhookPath(provider)
	require.Equal(t, "/api/webhooks/paddle", webhook)
	suffix := randomSuffix(t)
	subID := "sub_" + suffix
	activatedAt := time.Now().UTC()
	activated := subscriptionEvent("evt_act_"+suffix, "subscription.activated", subID, "active", paidSeats, 1, activatedAt)

	status, _ = rest.do(http.MethodPost, webhook, activated, signedHeader("wrong-secret", activated))
	require.Equal(t, http.StatusUnauthorized, status, "a bad signature is refused")
	_, err = repo.Event(ctx, "evt_act_"+suffix)
	require.Error(t, err, "nothing is stored for an unverified request")

	status, body = rest.do(http.MethodPost, webhook, activated, signedHeader(billingWebhookSecret, activated))
	require.Equal(t, http.StatusOK, status, body)

	status, me := rest.do(http.MethodGet, "/api/v1/billing/me", nil, nil)
	require.Equal(t, http.StatusOK, status, me)
	require.Equal(t, "team", me["plan"])
	require.Equal(t, "active", me["status"])
	require.Equal(t, "month", me["interval"])
	require.Equal(t, float64(paidSeats), me["seat_limit"])
	require.Equal(t, float64(1), me["storage_packs"])
	require.Equal(t, float64(paidSeats*billingSeatBytes+billingPackBytes), me["quota_bytes"])

	newUser := func(name string) snow.ID {
		t.Helper()
		id := snow.ID(time.Now().UnixNano() & 0x7fffffffffff)
		_, err := dbConn.ExecContext(ctx, `INSERT INTO users (id, name, email, password) VALUES ($1, $2, $3, 'x')`,
			id.Int64(), name, fmt.Sprintf("%s-%s@example.com", name, suffix))
		require.NoError(t, err)
		return id
	}
	paidMember := newUser("paid-member")
	status, body = rest.do(http.MethodPost, "/api/v1/orgs/default/members", map[string]string{"user_id": paidMember.Base36(), "role": "member"}, nil)
	require.Less(t, status, 300, "a paid seat admits a member: %v", body)

	canceledAt := activatedAt.Add(time.Second)
	canceled := subscriptionEvent("evt_can_"+suffix, "subscription.canceled", subID, "canceled", paidSeats, 1, canceledAt)
	status, body = rest.do(http.MethodPost, webhook, canceled, signedHeader(billingWebhookSecret, canceled))
	require.Equal(t, http.StatusOK, status, body)

	status, body = rest.do(http.MethodPost, webhook, activated, signedHeader(billingWebhookSecret, activated))
	require.Equal(t, http.StatusOK, status, body)

	status, me = rest.do(http.MethodGet, "/api/v1/billing/me", nil, nil)
	require.Equal(t, http.StatusOK, status, me)
	require.Equal(t, "free", me["plan"], "the replayed activation did not undo the cancel")
	require.Equal(t, float64(1), me["seat_limit"])
	require.Equal(t, float64(billingFreeQuota), me["quota_bytes"])

	repoClient, pusher := connectClient(t, host)
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repoClient.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	raw := make([]byte, 4096)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	writeFile(t, target, "billing-"+suffix+".txt", hex.EncodeToString(raw))
	stagePath(t, target, "billing-"+suffix+".txt")
	err = pusher.Run(ctx, target, "over the free quota")
	var clientErr *clientdomain.Error
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, http.StatusPaymentRequired, clientErr.Code, "push over the free quota: %v", err)

	outsider := newUser("outsider")
	status, body = rest.do(http.MethodPost, "/api/v1/orgs/default/members", map[string]string{"user_id": outsider.Base36(), "role": "member"}, nil)
	require.Equal(t, http.StatusPaymentRequired, status, "org member over the free seats: %v", body)

	status, group := rest.do(http.MethodPost, "/api/v1/orgs/default/groups", map[string]string{"name": "billing-" + suffix}, nil)
	require.Less(t, status, 300, group)
	status, body = rest.do(http.MethodPost, "/api/v1/orgs/default/groups/"+group["id"].(string)+"/members",
		map[string]string{"user_id": outsider.Base36()}, nil)
	require.Equal(t, http.StatusPaymentRequired, status, "group member over the free seats: %v", body)

	status, body = rest.do(http.MethodPost, "/api/v1/orgs/default/projects/default/permissions/rules",
		map[string]any{"user_id": outsider.Base36(), "path_prefix": "/", "permission": 1}, nil)
	require.Equal(t, http.StatusPaymentRequired, status, "user rule over the free seats: %v", body)

	status, body = rest.do(http.MethodPost, "/api/v1/orgs/default/members", map[string]string{"user_id": paidMember.Base36(), "role": "member"}, nil)
	require.Less(t, status, 300, "an existing seat is never refused: %v", body)

	fakeAPI.mu.Lock()
	defer fakeAPI.mu.Unlock()
	require.Equal(t, []string{"POST /customers", "POST /transactions"}, fakeAPI.calls, "webhooks never call Paddle")
}
