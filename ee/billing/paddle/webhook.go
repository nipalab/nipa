package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/internal/domain"
)

const (
	SignatureHeader    = "Paddle-Signature"
	signatureTolerance = 5 * time.Minute
)

// Sign builds a Paddle-Signature header value for body at ts.
func Sign(secret string, ts time.Time, body []byte) string {
	stamp := strconv.FormatInt(ts.Unix(), 10)
	return "ts=" + stamp + ";h1=" + signature(secret, stamp, body)
}

func signature(secret, stamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp))
	mac.Write([]byte(":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a Paddle-Signature header: HMAC-SHA256 over
// "ts:body", any of several h1 values (secret rotation), with a 5 minute
// tolerance on ts.
func (c *Client) VerifySignature(header string, body []byte) error {
	var stamp string
	var candidates []string
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "ts":
			stamp = value
		case "h1":
			candidates = append(candidates, value)
		}
	}
	if stamp == "" || len(candidates) == 0 || c.webhookSecret == "" {
		return billing.ErrInvalidSignature
	}
	unix, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return billing.ErrInvalidSignature
	}
	age := c.now().Sub(time.Unix(unix, 0))
	if age > signatureTolerance || age < -signatureTolerance {
		return billing.ErrInvalidSignature
	}
	expected := []byte(signature(c.webhookSecret, stamp, body))
	for _, candidate := range candidates {
		if hmac.Equal(expected, []byte(strings.ToLower(candidate))) {
			return nil
		}
	}
	return billing.ErrInvalidSignature
}

// ParseWebhook verifies the signature of the raw body, then decodes it.
func (c *Client) ParseWebhook(header http.Header, body []byte) (billing.Event, error) {
	if err := c.VerifySignature(header.Get(SignatureHeader), body); err != nil {
		return billing.Event{}, err
	}
	return c.DecodeEvent(body)
}

type eventEnvelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// DecodeEvent reads the envelope of a stored or verified event.
func (c *Client) DecodeEvent(payload []byte) (billing.Event, error) {
	var envelope eventEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return billing.Event{}, domain.NewErrorUser("invalid paddle event")
	}
	if envelope.EventID == "" || envelope.EventType == "" || envelope.OccurredAt.IsZero() {
		return billing.Event{}, domain.NewErrorUser("paddle event without event_id, event_type or occurred_at")
	}
	return billing.Event{
		ID:         envelope.EventID,
		Type:       envelope.EventType,
		OccurredAt: envelope.OccurredAt,
		Payload:    payload,
	}, nil
}

// EventSubscription maps the subscription carried by a subscription event.
func (c *Client) EventSubscription(event billing.Event) (billing.EventSubscription, error) {
	var envelope struct {
		Data subscription `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		return billing.EventSubscription{}, domain.NewErrorUser("invalid paddle subscription event")
	}
	sub, err := c.mapSubscription(envelope.Data)
	if err != nil {
		return billing.EventSubscription{}, err
	}
	return billing.EventSubscription{Subscription: sub, CustomerID: envelope.Data.CustomerID}, nil
}
