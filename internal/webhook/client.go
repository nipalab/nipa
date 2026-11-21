package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/egress"
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
	// EgressAllowlist adds private destinations to the always-allowed public
	// addresses. Entries may be hostnames (no port), IPs or CIDR ranges.
	EgressAllowlist []string
}

// Client posts signed payloads to webhook endpoints through the egress guard.
type Client struct {
	secure   *http.Client
	insecure *http.Client
	guard    *egress.Guard
}

// NewClient builds a delivery client. Redirects are never followed: a hook
// expects the POST itself to be answered, and following a redirect could leak
// the signed payload to a host outside the egress guard.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := &Client{guard: egress.New(egress.Config{Allowlist: cfg.EgressAllowlist, Label: "webhook"})}
	client.secure = &http.Client{Timeout: timeout, Transport: client.transport(false), CheckRedirect: egress.NoRedirects}
	client.insecure = &http.Client{Timeout: timeout, Transport: client.transport(true), CheckRedirect: egress.NoRedirects}
	return client
}

func (c *Client) transport(insecure bool) *http.Transport {
	transport := c.guard.Transport()
	if insecure {
		// insecure_tls is an explicit per-webhook admin opt-in for receivers
		// with self-signed certificates (parity with Perforce TeamHub).
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //NOSONAR
	}
	return transport
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
	defer func() { _ = resp.Body.Close() }()

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

// IsEgressBlocked reports whether err came from the egress guard. Blocked
// deliveries are permanent failures, not worth retrying.
func IsEgressBlocked(err error) bool {
	return egress.IsBlocked(err)
}
