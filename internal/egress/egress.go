// Package egress guards outbound HTTP requests whose destination is user
// input (webhooks, issue trackers). Destinations are checked on the address
// actually dialed: an empty allowlist admits public addresses only, a
// configured allowlist admits only listed hosts or ranges. Resolving once and
// dialing the validated IP keeps DNS rebinding from slipping past the check.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultDialTimeout = 30 * time.Second

// ErrBlocked marks a destination the guard refused. Blocked requests are
// permanent failures, not worth retrying.
var ErrBlocked = errors.New("egress blocked")

// IsBlocked reports whether err came from the guard.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrBlocked)
}

type Config struct {
	// Allowlist entries may be hostnames (no port), IPs or CIDR ranges. Empty
	// allows public addresses only; loopback, link-local and private ranges
	// require an explicit entry.
	Allowlist []string
	// Label names the destination in errors ("webhook host ..."); defaults to
	// "destination".
	Label string
	// DialTimeout defaults to 30 s.
	DialTimeout time.Duration
	// Resolver replaces net.DefaultResolver.LookupIPAddr.
	Resolver func(ctx context.Context, host string) ([]net.IPAddr, error)
}

type Guard struct {
	policy   Policy
	label    string
	dialer   *net.Dialer
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)
}

func New(cfg Config) *Guard {
	timeout := cfg.DialTimeout
	if timeout <= 0 {
		timeout = defaultDialTimeout
	}
	label := cfg.Label
	if label == "" {
		label = "destination"
	}
	lookup := cfg.Resolver
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	return &Guard{
		policy:   ParseAllowlist(cfg.Allowlist),
		label:    label,
		dialer:   &net.Dialer{Timeout: timeout, KeepAlive: timeout},
		lookupIP: lookup,
	}
}

// Transport clones the default transport without a proxy (a proxy would hide
// the destination) and dials through the guard.
func (g *Guard) Transport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = g.DialContext
	return transport
}

// NoRedirects is an http.Client CheckRedirect that hands the redirect response
// back to the caller instead of following it past the guard.
func NoRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// DialContext resolves the destination once, rejects addresses the policy
// forbids, and dials the vetted IP so the check and the connection cannot
// disagree.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := g.lookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	var blockedErr, dialErr error
	for _, resolved := range addrs {
		if !g.policy.AllowsDial(host, resolved.IP) {
			blockedErr = fmt.Errorf("%w: %s host %q resolves to %s, which is not allowed",
				ErrBlocked, g.label, host, resolved.IP)
			continue
		}
		conn, err := g.dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if err != nil {
			dialErr = err
			continue
		}
		return conn, nil
	}
	switch {
	case dialErr != nil:
		return nil, dialErr
	case blockedErr != nil:
		return nil, blockedErr
	}
	return nil, fmt.Errorf("%w: %s host %q has no addresses", ErrBlocked, g.label, host)
}

// CheckURL refuses early a URL whose host can never pass the allowlist. It
// admits everything when no allowlist is set, since the dial guard decides.
func (g *Guard) CheckURL(rawURL string) error {
	if g.policy.Empty() {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid %s url: %w", g.label, err)
	}
	host := parsed.Hostname()
	if g.policy.AllowsHost(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && g.policy.AllowsIP(ip) {
		return nil
	}
	if len(g.policy.nets) > 0 {
		return nil
	}
	return fmt.Errorf("%w: %s host %q is not in the egress allowlist", ErrBlocked, g.label, parsed.Host)
}

type Policy struct {
	hosts []string
	nets  []*net.IPNet
}

func ParseAllowlist(entries []string) Policy {
	var policy Policy
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
		policy.hosts = append(policy.hosts, normalizeHost(entry))
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

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func (p Policy) Empty() bool {
	return len(p.hosts) == 0 && len(p.nets) == 0
}

func (p Policy) AllowsHost(host string) bool {
	host = normalizeHost(host)
	for _, entry := range p.hosts {
		if entry == host {
			return true
		}
	}
	return false
}

func (p Policy) AllowsIP(ip net.IP) bool {
	for _, block := range p.nets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// AllowsDial decides on the address the connection would use. An explicitly
// listed hostname is trusted; otherwise the resolved IP must be listed, or be
// public when no allowlist is configured.
func (p Policy) AllowsDial(host string, ip net.IP) bool {
	if p.AllowsHost(host) || p.AllowsIP(ip) {
		return true
	}
	return p.Empty() && IsPublicIP(ip)
}

func IsPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
}
