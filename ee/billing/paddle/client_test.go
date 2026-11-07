package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const testAPIKey = "pdl_sdbx_apikey_secret"

var testPrices = Prices{SeatMonthly: "pri_seat_m", SeatYearly: "pri_seat_y", StorageMonthly: "pri_st_m", StorageYearly: "pri_st_y"}

type recorded struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

type fakePaddle struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	handle   func(w http.ResponseWriter, r recorded)
}

func newFake(t *testing.T, handle func(w http.ResponseWriter, r recorded)) (*Client, *fakePaddle) {
	t.Helper()
	fake := &fakePaddle{t: t, handle: handle}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+testAPIKey, r.Header.Get("Authorization"))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rec := recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		if len(raw) > 0 {
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.NoError(t, json.Unmarshal(raw, &rec.Body))
		}
		fake.mu.Lock()
		fake.requests = append(fake.requests, rec)
		fake.mu.Unlock()
		handle(w, rec)
	}))
	t.Cleanup(server.Close)
	return New(Config{APIKey: testAPIKey, WebhookSecret: "whsec", Prices: testPrices, BaseURL: server.URL + "/"}), fake
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func paddleError(code, detail string) string {
	return `{"error":{"type":"request_error","code":"` + code + `","detail":"` + detail + `"},"meta":{"request_id":"r"}}`
}

const subscriptionJSON = `{
	"id":"sub_1","status":"active","customer_id":"ctm_1","updated_at":"2026-11-07T12:00:00Z",
	"custom_data":{"user_id":"2s"},
	"items":[{"quantity":4,"price":{"id":"pri_seat_y"}},{"quantity":2,"price":{"id":"pri_st_y"}}],
	"current_billing_period":{"starts_at":"2026-11-07T12:00:00Z","ends_at":"2027-11-07T12:00:00Z"},
	"scheduled_change":{"action":"cancel","effective_at":"2027-11-07T12:00:00Z"}
}`

func errorCode(err error) int {
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		return domErr.Code
	}
	return 0
}

func TestNew_BaseURLByEnvironment(t *testing.T) {
	require.Equal(t, SandboxBaseURL, New(Config{Environment: "sandbox"}).baseURL)
	require.Equal(t, ProductionBaseURL, New(Config{Environment: "Production"}).baseURL)
	require.Equal(t, SandboxBaseURL, New(Config{}).baseURL)
	client := New(Config{})
	require.NotZero(t, client.http.Timeout, "the HTTP client must time out")
	require.Equal(t, ProviderName, client.Name())
}

func TestEnsureCustomer_Creates(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusCreated, `{"data":{"id":"ctm_new"}}`)
	})
	id, err := client.EnsureCustomer(context.Background(), snow.ID(100), "a@example.com", "Alice")
	require.NoError(t, err)
	require.Equal(t, "ctm_new", id)
	require.Equal(t, "/customers", fake.requests[0].Path)
	require.Equal(t, http.MethodPost, fake.requests[0].Method)
	require.Equal(t, map[string]any{
		"email": "a@example.com", "name": "Alice",
		"custom_data": map[string]any{"user_id": snow.ID(100).Base36()},
	}, fake.requests[0].Body)
}

func TestEnsureCustomer_AdoptsExistingFromConflictDetail(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusConflict, paddleError("customer_already_exists", "Customer email conflicts with existing customer ID ctm_01ghbk4xjn4qdsmstcwzgcgg35"))
	})
	id, err := client.EnsureCustomer(context.Background(), snow.ID(1), "a@example.com", "")
	require.NoError(t, err)
	require.Equal(t, "ctm_01ghbk4xjn4qdsmstcwzgcgg35", id)
	require.Len(t, fake.requests, 1)
	_, hasName := fake.requests[0].Body["name"]
	require.False(t, hasName)
}

func TestEnsureCustomer_AdoptsExistingByLookup(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, r recorded) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusConflict, paddleError("customer_already_exists", "conflict"))
			return
		}
		writeJSON(w, http.StatusOK, `{"data":[{"id":"ctm_found"}],"meta":{}}`)
	})
	id, err := client.EnsureCustomer(context.Background(), snow.ID(1), "a+b@example.com", "A")
	require.NoError(t, err)
	require.Equal(t, "ctm_found", id)
	require.Equal(t, "email=a%2Bb%40example.com", fake.requests[1].Query)
}

func TestEnsureCustomer_Errors(t *testing.T) {
	client, _ := newFake(t, func(w http.ResponseWriter, r recorded) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusConflict, paddleError("customer_already_exists", "conflict"))
			return
		}
		writeJSON(w, http.StatusOK, `{"data":[]}`)
	})
	_, err := client.EnsureCustomer(context.Background(), snow.ID(1), "a@example.com", "A")
	require.Equal(t, http.StatusBadGateway, errorCode(err))

	client, _ = newFake(t, func(w http.ResponseWriter, r recorded) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusConflict, paddleError("customer_already_exists", "conflict"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, `oops`)
	})
	_, err = client.EnsureCustomer(context.Background(), snow.ID(1), "a@example.com", "A")
	require.Equal(t, http.StatusBadGateway, errorCode(err))

	client, _ = newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusBadRequest, paddleError("invalid_field", "email is invalid"))
	})
	_, err = client.EnsureCustomer(context.Background(), snow.ID(1), "bad", "A")
	require.Equal(t, http.StatusBadGateway, errorCode(err))
	require.NotContains(t, err.Error(), testAPIKey)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.NotContains(t, domErr.InternalMessage, testAPIKey)
	require.Contains(t, domErr.InternalMessage, "invalid_field")
}

func TestCreateCheckout(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusCreated, `{"data":{"id":"txn_1","status":"draft"}}`)
	})
	id, err := client.CreateCheckout(context.Background(), "ctm_1", snow.ID(5), billing.Items{Seats: 3, StoragePacks: 2, Interval: "month"})
	require.NoError(t, err)
	require.Equal(t, "txn_1", id)
	require.Equal(t, "/transactions", fake.requests[0].Path)
	require.Equal(t, map[string]any{
		"customer_id": "ctm_1",
		"custom_data": map[string]any{"user_id": "5"},
		"items": []any{
			map[string]any{"price_id": "pri_seat_m", "quantity": float64(3)},
			map[string]any{"price_id": "pri_st_m", "quantity": float64(2)},
		},
	}, fake.requests[0].Body)

	_, err = client.CreateCheckout(context.Background(), "ctm_1", snow.ID(5), billing.Items{Seats: 1, Interval: "yearly"})
	require.NoError(t, err)
	require.Equal(t, []any{map[string]any{"price_id": "pri_seat_y", "quantity": float64(1)}}, fake.requests[1].Body["items"],
		"no storage item without packs")

	_, err = client.CreateCheckout(context.Background(), "ctm_1", snow.ID(5), billing.Items{Seats: 0, Interval: "month"})
	require.Equal(t, http.StatusBadRequest, errorCode(err))
	require.Len(t, fake.requests, 2)

	failing, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusInternalServerError, paddleError("internal_error", "x"))
	})
	_, err = failing.CreateCheckout(context.Background(), "ctm_1", snow.ID(5), billing.Items{Seats: 1, Interval: "month"})
	require.Equal(t, http.StatusBadGateway, errorCode(err))
}

func TestUpdateSubscription(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusOK, `{"data":`+subscriptionJSON+`}`)
	})
	sub, err := client.UpdateSubscription(context.Background(), "sub_1", billing.Items{Seats: 4, StoragePacks: 2, Interval: "year"})
	require.NoError(t, err)
	require.Equal(t, http.MethodPatch, fake.requests[0].Method)
	require.Equal(t, "/subscriptions/sub_1", fake.requests[0].Path)
	require.Equal(t, "prorated_immediately", fake.requests[0].Body["proration_billing_mode"])
	require.Equal(t, "prevent_change", fake.requests[0].Body["on_payment_failure"])
	require.Len(t, fake.requests[0].Body["items"], 2)

	end := time.Date(2027, 11, 7, 12, 0, 0, 0, time.UTC)
	require.Equal(t, billing.Subscription{
		ProviderSubscriptionID: "sub_1", UserID: snow.ID(100), Plan: billing.PlanTeam, Status: "active",
		Interval: billing.IntervalYear, Seats: 4, StoragePacks: 2, CurrentPeriodEnd: &end, CancelAtPeriodEnd: true,
		ProviderUpdatedAt: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC),
	}, sub)

	_, err = client.UpdateSubscription(context.Background(), "sub_1", billing.Items{Seats: 1, Interval: "week"})
	require.Equal(t, http.StatusBadRequest, errorCode(err))
}

func TestUpdateSubscription_ConflictMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   int
	}{
		{http.StatusBadRequest, "subscription_locked_pending_changes", http.StatusConflict},
		{http.StatusBadRequest, "subscription_update_when_canceled", http.StatusConflict},
		{http.StatusConflict, "conflict", http.StatusConflict},
		{http.StatusBadRequest, "invalid_field", http.StatusBadGateway},
		{http.StatusNotFound, "not_found", http.StatusBadGateway},
	} {
		client, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
			writeJSON(w, tc.status, paddleError(tc.code, "Cannot update subscription, pending scheduled changes."))
		})
		_, err := client.UpdateSubscription(context.Background(), "sub_1", billing.Items{Seats: 1, Interval: "month"})
		require.Equal(t, tc.want, errorCode(err), tc.code)
	}
}

func TestUpdateSubscription_UnmappableResponse(t *testing.T) {
	client, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusOK, `{"data":{"id":"sub_1","status":"active","updated_at":"2026-11-07T12:00:00Z","items":[{"quantity":1,"price":{"id":"pri_other"}}]}}`)
	})
	_, err := client.UpdateSubscription(context.Background(), "sub_1", billing.Items{Seats: 1, Interval: "month"})
	require.Equal(t, http.StatusBadGateway, errorCode(err))
}

func TestGetSubscription(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, r recorded) {
		if strings.HasSuffix(r.Path, "missing") {
			writeJSON(w, http.StatusNotFound, paddleError("not_found", "nope"))
			return
		}
		writeJSON(w, http.StatusOK, `{"data":`+subscriptionJSON+`}`)
	})
	sub, err := client.GetSubscription(context.Background(), "sub_1")
	require.NoError(t, err)
	require.Equal(t, "sub_1", sub.ProviderSubscriptionID)
	require.Equal(t, http.MethodGet, fake.requests[0].Method)

	_, err = client.GetSubscription(context.Background(), "missing")
	require.Equal(t, http.StatusBadGateway, errorCode(err))
}

func TestListSubscriptions_FollowsPagination(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, r recorded) {
		if !strings.Contains(r.Query, "after=") {
			writeJSON(w, http.StatusOK, `{"data":[`+subscriptionJSON+`],"meta":{"pagination":{"has_more":true}}}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"data":[`+strings.Replace(subscriptionJSON, "sub_1", "sub_2", 1)+`],"meta":{"pagination":{"has_more":false}}}`)
	})
	subs, err := client.ListSubscriptions(context.Background(), "ctm_1")
	require.NoError(t, err)
	require.Len(t, subs, 2)
	require.Equal(t, "sub_2", subs[1].ProviderSubscriptionID)
	require.Equal(t, "customer_id=ctm_1&per_page=200", fake.requests[0].Query)
	require.Equal(t, "after=sub_1&customer_id=ctm_1&per_page=200", fake.requests[1].Query)
}

func TestListSubscriptions_Errors(t *testing.T) {
	client, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusUnauthorized, paddleError("authentication_malformed", "bad key"))
	})
	_, err := client.ListSubscriptions(context.Background(), "ctm_1")
	require.Equal(t, http.StatusBadGateway, errorCode(err))

	client, _ = newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusOK, `{"data":[{"id":"sub_x","items":[]}]}`)
	})
	_, err = client.ListSubscriptions(context.Background(), "ctm_1")
	require.Equal(t, http.StatusBadGateway, errorCode(err))

	client, _ = newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusOK, `not json`)
	})
	_, err = client.ListSubscriptions(context.Background(), "ctm_1")
	require.Equal(t, http.StatusBadGateway, errorCode(err))
}

func TestPortalURL(t *testing.T) {
	client, fake := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusCreated, `{"data":{"urls":{"general":{"overview":"https://portal.example/overview"},"subscriptions":[]}}}`)
	})
	link, err := client.PortalURL(context.Background(), "ctm_1", "sub_1")
	require.NoError(t, err)
	require.Equal(t, "https://portal.example/overview", link)
	require.Equal(t, "/customers/ctm_1/portal-sessions", fake.requests[0].Path)
	require.Equal(t, []any{"sub_1"}, fake.requests[0].Body["subscription_ids"])

	_, err = client.PortalURL(context.Background(), "ctm_1", "")
	require.NoError(t, err)
	require.NotContains(t, fake.requests[1].Body, "subscription_ids")

	empty, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusCreated, `{"data":{"urls":{}}}`)
	})
	_, err = empty.PortalURL(context.Background(), "ctm_1", "")
	require.Equal(t, http.StatusBadGateway, errorCode(err))

	failing, _ := newFake(t, func(w http.ResponseWriter, _ recorded) {
		writeJSON(w, http.StatusNotFound, paddleError("not_found", "x"))
	})
	_, err = failing.PortalURL(context.Background(), "ctm_1", "")
	require.Equal(t, http.StatusBadGateway, errorCode(err))
}

func TestDo_TransportError(t *testing.T) {
	client := New(Config{APIKey: testAPIKey, BaseURL: "http://127.0.0.1:1", Prices: testPrices})
	_, err := client.GetSubscription(context.Background(), "sub_1")
	require.Equal(t, http.StatusBadGateway, errorCode(err))
	require.NotContains(t, err.Error(), testAPIKey)
}
