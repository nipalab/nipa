package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds one delivery attempt.
	DefaultTimeout = 10 * time.Second
	// UserAgent identifies nipad to webhook receivers.
	UserAgent = "Nipa/1.0"

	SignatureHeader = "X-Nipa-Signature"
	EventHeader     = "X-Nipa-Event"
	DeliveryHeader  = "X-Nipa-Delivery"
	HookHeader      = "X-Nipa-Hook-Id"

	maxResponseSnippet = 512
	maxDrainBytes      = 64 << 10
)

type ClientConfig struct {
	// Timeout per attempt; defaults to DefaultTimeout.
	Timeout time.Duration
	// EgressAllowlist restricts deliveries to these hosts ("host" or
	// "host:port"). Empty allows every host.
	EgressAllowlist []string
}

type Client struct {
	secure    *http.Client
	insecure  *http.Client
	allowlist []string
}

// NewClient builds a delivery client. Redirects are never followed: a hook
// expects the POST itself to be answered, and following a redirect could leak
// the signed payload to a host outside the egress allowlist.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	insecureTransport := http.DefaultTransport.(*http.Transport).Clone()
	insecureTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	return &Client{
		secure: &http.Client{
			Timeout:       timeout,
			Transport:     http.DefaultTransport,
			CheckRedirect: noRedirects,
		},
		insecure: &http.Client{
			Timeout:       timeout,
			Transport:     insecureTransport,
			CheckRedirect: noRedirects,
		},
		allowlist: cfg.EgressAllowlist,
	}
}

type PostRequest struct {
	URL         string
	Secret      string
	InsecureTLS bool
	Event       string
	DeliveryID  string
	HookID      string
	Payload     []byte
}

type Result struct {
	StatusCode int
	Duration   time.Duration
	Err        error
}

// Post delivers one payload. Any non-2xx response is a failure; a failed
// attempt never retries here, the caller schedules it.
func (c *Client) Post(ctx context.Context, req PostRequest) Result {
	start := time.Now()
	fail := func(err error) Result {
		return Result{Duration: time.Since(start), Err: err}
	}

	if err := c.checkEgress(req.URL); err != nil {
		return fail(err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Payload))
	if err != nil {
		return fail(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", UserAgent)
	httpReq.Header.Set(EventHeader, req.Event)
	httpReq.Header.Set(DeliveryHeader, req.DeliveryID)
	httpReq.Header.Set(HookHeader, req.HookID)
	if req.Secret != "" {
		httpReq.Header.Set(SignatureHeader, Sign(req.Secret, req.Payload))
	}

	client := c.secure
	if req.InsecureTLS {
		client = c.insecure
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Result{
			StatusCode: resp.StatusCode,
			Duration:   time.Since(start),
			Err:        fmt.Errorf("unexpected response status %s%s", resp.Status, responseSnippet(resp.Body)),
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	return Result{StatusCode: resp.StatusCode, Duration: time.Since(start)}
}

func (c *Client) checkEgress(rawURL string) error {
	if len(c.allowlist) == 0 {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	for _, entry := range c.allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.EqualFold(entry, parsed.Host) || strings.EqualFold(entry, parsed.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%w: webhook host %q is not in the egress allowlist", errEgressBlocked, parsed.Host)
}

func responseSnippet(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseSnippet))
	if err != nil {
		return ""
	}
	snippet := strings.TrimSpace(string(data))
	if snippet == "" {
		return ""
	}
	return ": " + snippet
}

// IsEgressBlocked reports whether err came from the egress allowlist check.
// Blocked deliveries are permanent failures, not worth retrying.
func IsEgressBlocked(err error) bool {
	return errors.Is(err, errEgressBlocked)
}

var errEgressBlocked = errors.New("egress blocked")
