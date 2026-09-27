package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
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

	dialTimeout = 30 * time.Second
)

type ClientConfig struct {
	// Timeout per attempt; defaults to DefaultTimeout.
	Timeout time.Duration
	// EgressAllowlist limits deliveries to these destinations. Entries may be
	// hostnames (no port), IPs or CIDR ranges. Empty allows public addresses
	// only; loopback, link-local and private ranges require an explicit entry.
	EgressAllowlist []string
}

// Client posts signed payloads to webhook endpoints. Destinations are checked
// on the address actually dialed: an empty allowlist admits public addresses
// only, a configured allowlist admits only listed hosts or ranges. Resolving
// once and dialing the validated IP keeps DNS rebinding from slipping past the
// check.
type Client struct {
	secure   *http.Client
	insecure *http.Client
	egress   egressPolicy
	dialer   *net.Dialer
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// NewClient builds a delivery client. Redirects are never followed: a hook
// expects the POST itself to be answered, and following a redirect could leak
// the signed payload to a host outside the egress guard.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := &Client{
		egress:   parseEgressAllowlist(cfg.EgressAllowlist),
		dialer:   &net.Dialer{Timeout: dialTimeout, KeepAlive: dialTimeout},
		lookupIP: net.DefaultResolver.LookupIPAddr,
	}
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.secure = &http.Client{Timeout: timeout, Transport: client.transport(false), CheckRedirect: noRedirects}
	client.insecure = &http.Client{Timeout: timeout, Transport: client.transport(true), CheckRedirect: noRedirects}
	return client
}

func (c *Client) transport(insecure bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy would hide the destination the egress guard must validate.
	transport.Proxy = nil
	transport.DialContext = c.dialContext
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

// dialContext resolves the destination once, rejects addresses the egress
// policy forbids, and dials the vetted IP so the check and the connection
// cannot disagree.
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := c.lookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, resolved := range addrs {
		if !c.egress.allowsDial(host, resolved.IP) {
			lastErr = fmt.Errorf("%w: webhook host %q resolves to %s, which is not allowed",
				errEgressBlocked, host, resolved.IP)
			continue
		}
		conn, err := c.dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: webhook host %q has no addresses", errEgressBlocked, host)
	}
	return nil, lastErr
}

func (c *Client) checkEgress(rawURL string) error {
	if c.egress.empty() {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	host := parsed.Hostname()
	if c.egress.allowsHost(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && c.egress.allowsIP(ip) {
		return nil
	}
	if len(c.egress.nets) > 0 {
		// A hostname may resolve into a listed range; the dial guard decides.
		return nil
	}
	return fmt.Errorf("%w: webhook host %q is not in the egress allowlist", errEgressBlocked, parsed.Host)
}

type egressPolicy struct {
	hosts []string
	nets  []*net.IPNet
}

func parseEgressAllowlist(entries []string) egressPolicy {
	var policy egressPolicy
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, block, err := net.ParseCIDR(entry); err == nil {
			policy.nets = append(policy.nets, block)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			policy.nets = append(policy.nets, singleIPNet(ip))
			continue
		}
		policy.hosts = append(policy.hosts, strings.ToLower(strings.TrimSuffix(entry, ".")))
	}
	return policy
}

func singleIPNet(ip net.IP) *net.IPNet {
	bits := 128
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
}

func (p egressPolicy) empty() bool {
	return len(p.hosts) == 0 && len(p.nets) == 0
}

func (p egressPolicy) allowsHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range p.hosts {
		if entry == host {
			return true
		}
	}
	return false
}

func (p egressPolicy) allowsIP(ip net.IP) bool {
	for _, block := range p.nets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// allowsDial decides on the address the connection would use. An explicitly
// listed hostname is trusted; otherwise the resolved IP must be listed, or be
// public when no allowlist is configured.
func (p egressPolicy) allowsDial(host string, ip net.IP) bool {
	if p.allowsHost(host) || p.allowsIP(ip) {
		return true
	}
	return p.empty() && isPublicIP(ip)
}

func isPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
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
	return errors.Is(err, errEgressBlocked)
}

var errEgressBlocked = errors.New("egress blocked")
