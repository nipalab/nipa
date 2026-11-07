package paddle

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/billing"
)

var webhookNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func webhookClient(secret string) *Client {
	return New(Config{WebhookSecret: secret, Prices: testPrices, Now: func() time.Time { return webhookNow }})
}

const eventBody = `{"event_id":"evt_1","event_type":"subscription.activated","occurred_at":"2026-11-07T11:59:00Z","notification_id":"ntf_1","data":` + subscriptionJSON + `}`

func TestVerifySignature(t *testing.T) {
	client := webhookClient("whsec")
	body := []byte(eventBody)
	valid := Sign("whsec", webhookNow, body)

	require.NoError(t, client.VerifySignature(valid, body))
	require.NoError(t, client.VerifySignature(Sign("whsec", webhookNow.Add(-4*time.Minute), body), body), "inside the tolerance")
	require.NoError(t, client.VerifySignature(strings.Replace(valid, ";h1=", ";h1=deadbeef;h1=", 1), body),
		"any h1 may match during secret rotation")

	for name, header := range map[string]string{
		"empty":        "",
		"no h1":        "ts=1",
		"no ts":        "h1=abc",
		"bad ts":       "ts=abc;h1=abc",
		"garbage":      "nonsense",
		"expired":      Sign("whsec", webhookNow.Add(-6*time.Minute), body),
		"future":       Sign("whsec", webhookNow.Add(6*time.Minute), body),
		"other secret": Sign("other", webhookNow, body),
	} {
		require.ErrorIs(t, client.VerifySignature(header, body), billing.ErrInvalidSignature, name)
	}
	require.ErrorIs(t, client.VerifySignature(valid, []byte(eventBody+" ")), billing.ErrInvalidSignature, "tampered body")
	require.ErrorIs(t, webhookClient("").VerifySignature(Sign("", webhookNow, body), body), billing.ErrInvalidSignature,
		"an unset secret never verifies")
}

func TestParseWebhook(t *testing.T) {
	client := webhookClient("whsec")
	body := []byte(eventBody)
	header := http.Header{}
	header.Set(SignatureHeader, Sign("whsec", webhookNow, body))

	event, err := client.ParseWebhook(header, body)
	require.NoError(t, err)
	require.Equal(t, "evt_1", event.ID)
	require.Equal(t, "subscription.activated", event.Type)
	require.Equal(t, time.Date(2026, 11, 7, 11, 59, 0, 0, time.UTC), event.OccurredAt)
	require.Equal(t, body, event.Payload)

	_, err = client.ParseWebhook(http.Header{}, body)
	require.ErrorIs(t, err, billing.ErrInvalidSignature)

	bad := []byte(`{"event_id":"evt_1"}`)
	header.Set(SignatureHeader, Sign("whsec", webhookNow, bad))
	_, err = client.ParseWebhook(header, bad)
	require.Equal(t, http.StatusBadRequest, errorCode(err))

	_, err = client.DecodeEvent([]byte(`nope`))
	require.Equal(t, http.StatusBadRequest, errorCode(err))
}

func TestEventSubscription(t *testing.T) {
	client := webhookClient("whsec")
	got, err := client.EventSubscription(billing.Event{Payload: []byte(eventBody)})
	require.NoError(t, err)
	require.Equal(t, "ctm_1", got.CustomerID)
	require.Equal(t, "sub_1", got.Subscription.ProviderSubscriptionID)
	require.Equal(t, int64(4), got.Subscription.Seats)
	require.Equal(t, int64(2), got.Subscription.StoragePacks)
	require.True(t, got.Subscription.CancelAtPeriodEnd)

	_, err = client.EventSubscription(billing.Event{Payload: []byte(`nope`)})
	require.Equal(t, http.StatusBadRequest, errorCode(err))
}

func TestMapSubscription(t *testing.T) {
	client := webhookClient("")
	base := func() subscription {
		var s subscription
		s.ID = "sub_1"
		s.Status = "past_due"
		s.UpdatedAt = webhookNow
		return s
	}
	item := func(price string, quantity int64) struct {
		Quantity int64 `json:"quantity"`
		Price    struct {
			ID string `json:"id"`
		} `json:"price"`
	} {
		var it struct {
			Quantity int64 `json:"quantity"`
			Price    struct {
				ID string `json:"id"`
			} `json:"price"`
		}
		it.Quantity = quantity
		it.Price.ID = price
		return it
	}

	raw := base()
	raw.Items = append(raw.Items, item("pri_seat_m", 2))
	sub, err := client.mapSubscription(raw)
	require.NoError(t, err)
	require.Equal(t, billing.IntervalMonth, sub.Interval)
	require.Equal(t, int64(2), sub.Seats)
	require.Zero(t, sub.StoragePacks, "no storage item means no packs")
	require.Zero(t, sub.UserID)
	require.Nil(t, sub.CurrentPeriodEnd, "canceled and paused subscriptions have no billing period")
	require.False(t, sub.CancelAtPeriodEnd)
	require.Equal(t, "past_due", sub.Status)

	for name, mutate := range map[string]func(*subscription){
		"missing id":     func(s *subscription) { s.ID = "" },
		"unknown price":  func(s *subscription) { s.Items = append(s.Items, item("pri_other", 1)) },
		"mixed interval": func(s *subscription) { s.Items = append(s.Items, item("pri_st_y", 1)) },
		"no seat item":   func(s *subscription) { s.Items = s.Items[:0]; s.Items = append(s.Items, item("pri_st_m", 1)) },
		"bad user id":    func(s *subscription) { s.CustomData.UserID = "not base36!" },
		"empty price id": func(s *subscription) { s.Items = append(s.Items, item("", 1)) },
	} {
		raw := base()
		raw.Items = append(raw.Items, item("pri_seat_m", 2))
		mutate(&raw)
		_, err := client.mapSubscription(raw)
		require.Equal(t, http.StatusBadRequest, errorCode(err), name)
	}
}
