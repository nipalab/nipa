package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
)

func TestDispatcher_Metrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	store := newFakeStore()
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	metrics := obs.NewMetrics()
	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig()).WithMetrics(metrics)
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)
	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryDelivered
	})
	dispatcher.markFailed(domain.WebhookDelivery{ID: created.ID, WebhookID: hook.ID}, 1, nil, "boom")

	rr := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	require.Contains(t, body, `nipa_webhook_deliveries_total{result="delivered"} 1`)
	require.Contains(t, body, `nipa_webhook_deliveries_total{result="failed"} 1`)
	require.Contains(t, body, "nipa_webhook_queue_depth")
	require.Contains(t, body, "nipa_webhook_in_flight")
}
