// Package paddle is the Paddle Billing implementation of billing.Provider.
//
// Enterprise Edition: see ee/LICENSE.
package paddle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	ProviderName      = "paddle"
	SandboxBaseURL    = "https://sandbox-api.paddle.com"
	ProductionBaseURL = "https://api.paddle.com"

	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 4 << 20
	listPageSize     = 200
	maxListPages     = 50
)

var customerIDPattern = regexp.MustCompile(`ctm_[a-z0-9]+`)

// Prices are the configured Paddle price ids, one per item and interval.
type Prices struct {
	SeatMonthly    string
	SeatYearly     string
	StorageMonthly string
	StorageYearly  string
}

type Config struct {
	Environment   string
	APIKey        string
	WebhookSecret string
	Prices        Prices
	// BaseURL overrides the API host picked from Environment (tests).
	BaseURL    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// Client talks to the Paddle Billing REST API.
type Client struct {
	baseURL       string
	apiKey        string
	webhookSecret string
	prices        Prices
	http          *http.Client
	now           func() time.Time
}

var _ billing.Provider = (*Client)(nil)

func New(cfg Config) *Client {
	base := cfg.BaseURL
	if base == "" {
		base = SandboxBaseURL
		if strings.EqualFold(strings.TrimSpace(cfg.Environment), "production") {
			base = ProductionBaseURL
		}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL:       strings.TrimRight(base, "/"),
		apiKey:        cfg.APIKey,
		webhookSecret: cfg.WebhookSecret,
		prices:        cfg.Prices,
		http:          httpClient,
		now:           now,
	}
}

func (c *Client) Name() string { return ProviderName }

type apiError struct {
	Status int
	Type   string `json:"type"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e *apiError) Error() string {
	return fmt.Sprintf("paddle api error %d %s: %s", e.Status, e.Code, e.Detail)
}

// toDomain maps a Paddle failure to a domain error: conflicts and locked
// subscriptions are 409, everything else a 502 with the detail kept internal.
func toDomain(op string, err error) error {
	apiErr, ok := err.(*apiError)
	if ok && (apiErr.Status == http.StatusConflict || strings.HasPrefix(apiErr.Code, "subscription_locked") ||
		strings.HasPrefix(apiErr.Code, "subscription_update_when")) {
		return &domain.Error{
			Code:            http.StatusConflict,
			Message:         "the billing provider refused the change: " + apiErr.Detail,
			InternalMessage: op + ": " + apiErr.Error(),
			Cause:           err,
		}
	}
	return &domain.Error{
		Code:            http.StatusBadGateway,
		Message:         "billing provider request failed",
		InternalMessage: op + ": " + err.Error(),
		Cause:           err,
	}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var envelope struct {
			Error apiError `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		envelope.Error.Status = resp.StatusCode
		return &envelope.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode paddle response: %w", err)
	}
	return nil
}

type entityID struct {
	ID string `json:"id"`
}

type customData struct {
	UserID string `json:"user_id,omitempty"`
}

// EnsureCustomer creates the Paddle customer, or adopts the existing one when
// Paddle reports the email as taken.
func (c *Client) EnsureCustomer(ctx context.Context, userID snow.ID, email, name string) (string, error) {
	body := map[string]any{
		"email":       email,
		"custom_data": customData{UserID: userID.Base36()},
	}
	if strings.TrimSpace(name) != "" {
		body["name"] = name
	}
	var created struct {
		Data entityID `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/customers", nil, body, &created)
	if err == nil {
		return created.Data.ID, nil
	}
	apiErr, ok := err.(*apiError)
	if !ok || apiErr.Code != "customer_already_exists" {
		return "", toDomain("create customer", err)
	}
	if id := customerIDPattern.FindString(apiErr.Detail); id != "" {
		return id, nil
	}
	var listed struct {
		Data []entityID `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/customers", url.Values{"email": {email}}, nil, &listed); err != nil {
		return "", toDomain("find customer", err)
	}
	if len(listed.Data) == 0 {
		return "", toDomain("find customer", fmt.Errorf("customer for existing email not found"))
	}
	return listed.Data[0].ID, nil
}

type itemRequest struct {
	PriceID  string `json:"price_id"`
	Quantity int64  `json:"quantity"`
}

func (c *Client) itemList(items billing.Items) ([]itemRequest, error) {
	items, err := items.Validate()
	if err != nil {
		return nil, err
	}
	seat, storage := c.prices.SeatMonthly, c.prices.StorageMonthly
	if items.Interval == billing.IntervalYear {
		seat, storage = c.prices.SeatYearly, c.prices.StorageYearly
	}
	list := []itemRequest{{PriceID: seat, Quantity: items.Seats}}
	if items.StoragePacks > 0 {
		list = append(list, itemRequest{PriceID: storage, Quantity: items.StoragePacks})
	}
	return list, nil
}

// CreateCheckout creates a transaction the browser opens with Paddle.js.
func (c *Client) CreateCheckout(ctx context.Context, customerID string, userID snow.ID, items billing.Items) (string, error) {
	list, err := c.itemList(items)
	if err != nil {
		return "", err
	}
	var created struct {
		Data entityID `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/transactions", nil, map[string]any{
		"items":       list,
		"customer_id": customerID,
		"custom_data": customData{UserID: userID.Base36()},
	}, &created); err != nil {
		return "", toDomain("create transaction", err)
	}
	return created.Data.ID, nil
}

// UpdateSubscription replaces the item list, prorated immediately; a failed
// payment leaves the subscription unchanged.
func (c *Client) UpdateSubscription(ctx context.Context, subscriptionID string, items billing.Items) (billing.Subscription, error) {
	list, err := c.itemList(items)
	if err != nil {
		return billing.Subscription{}, err
	}
	var updated struct {
		Data subscription `json:"data"`
	}
	if err := c.do(ctx, http.MethodPatch, "/subscriptions/"+url.PathEscape(subscriptionID), nil, map[string]any{
		"items":                  list,
		"proration_billing_mode": "prorated_immediately",
		"on_payment_failure":     "prevent_change",
	}, &updated); err != nil {
		return billing.Subscription{}, toDomain("update subscription", err)
	}
	return c.apiSubscription(updated.Data)
}

func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (billing.Subscription, error) {
	var got struct {
		Data subscription `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(subscriptionID), nil, nil, &got); err != nil {
		return billing.Subscription{}, toDomain("get subscription", err)
	}
	return c.apiSubscription(got.Data)
}

// apiSubscription maps a subscription the API returned; a shape the server
// cannot map is the provider's fault, not the caller's.
func (c *Client) apiSubscription(raw subscription) (billing.Subscription, error) {
	sub, err := c.mapSubscription(raw)
	if err != nil {
		return billing.Subscription{}, toDomain("map subscription", err)
	}
	return sub, nil
}

// ListSubscriptions returns every subscription of a customer, following the
// cursor pagination.
func (c *Client) ListSubscriptions(ctx context.Context, customerID string) ([]billing.Subscription, error) {
	var subs []billing.Subscription
	after := ""
	for range maxListPages {
		query := url.Values{"customer_id": {customerID}, "per_page": {fmt.Sprint(listPageSize)}}
		if after != "" {
			query.Set("after", after)
		}
		var page struct {
			Data []subscription `json:"data"`
			Meta struct {
				Pagination struct {
					HasMore bool `json:"has_more"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := c.do(ctx, http.MethodGet, "/subscriptions", query, nil, &page); err != nil {
			return nil, toDomain("list subscriptions", err)
		}
		for _, raw := range page.Data {
			sub, err := c.apiSubscription(raw)
			if err != nil {
				return nil, err
			}
			subs = append(subs, sub)
		}
		if !page.Meta.Pagination.HasMore || len(page.Data) == 0 {
			return subs, nil
		}
		after = page.Data[len(page.Data)-1].ID
	}
	return subs, nil
}

// PortalURL creates a customer portal session and returns its overview link.
func (c *Client) PortalURL(ctx context.Context, customerID, subscriptionID string) (string, error) {
	body := map[string]any{}
	if subscriptionID != "" {
		body["subscription_ids"] = []string{subscriptionID}
	}
	var session struct {
		Data struct {
			URLs struct {
				General struct {
					Overview string `json:"overview"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerID)+"/portal-sessions", nil, body, &session); err != nil {
		return "", toDomain("create portal session", err)
	}
	if session.Data.URLs.General.Overview == "" {
		return "", toDomain("create portal session", fmt.Errorf("no portal url in response"))
	}
	return session.Data.URLs.General.Overview, nil
}
