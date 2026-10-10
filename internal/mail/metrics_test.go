package mail

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
)

func TestDispatcher_Metrics(t *testing.T) {
	body, err := EncodeMessage(Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)

	metrics := obs.NewMetrics()
	sender := &fakeSender{}
	dispatcher := NewDispatcher(stubStore{}, sender, DispatcherConfig{}).WithMetrics(metrics)

	dispatcher.deliver(domain.EmailDelivery{ID: 1, Body: body})

	sender.mu.Lock()
	sender.failures = 1
	sender.mu.Unlock()
	dispatcher.deliver(domain.EmailDelivery{ID: 2, Body: body})

	dispatcher.markFailed(domain.EmailDelivery{ID: 3}, errors.New("boom"))

	rr := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := rr.Body.String()
	require.Contains(t, text, `nipa_mail_deliveries_total{result="delivered"} 1`)
	require.Contains(t, text, `nipa_mail_deliveries_total{result="retry"} 1`)
	require.Contains(t, text, `nipa_mail_deliveries_total{result="failed"} 1`)
	require.Contains(t, text, "nipa_mail_queue_depth")
}
