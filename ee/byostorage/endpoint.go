package byostorage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/nipalab/nipa/internal/domain"
)

// ErrAddressNotAllowed refuses a connection to a private, loopback,
// link-local or otherwise non-public address.
var ErrAddressNotAllowed = errors.New("storage server address is not a public address")

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`)
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9._\-/]*$`)
)

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// CheckAddress refuses addresses a customer server must never resolve to:
// loopback, private, link-local (cloud metadata included), CGNAT,
// multicast, unspecified and documentation or translation ranges.
func CheckAddress(addr netip.Addr) error {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsUnspecified() || addr.IsMulticast() {
		return ErrAddressNotAllowed
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return ErrAddressNotAllowed
		}
	}
	return nil
}

func dialControl(allowPrivate bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return ErrAddressNotAllowed
		}
		return CheckAddress(addr)
	}
}

// NewTransport returns the transport every request to a customer server goes
// through: no proxy, and the dialer checks the resolved address at connect
// time, so DNS rebinding cannot reach an internal address.
func NewTransport(allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   dialControl(allowPrivate),
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	transport.MaxIdleConnsPerHost = 16
	return transport
}

// NormalizeEndpoint validates a customer endpoint and returns it as
// scheme://host[:port]. Only https is accepted unless allowPrivate is set;
// credentials, paths, queries and fragments are refused, and a literal IP
// must be public.
func NormalizeEndpoint(raw string, allowPrivate bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", domain.NewErrorUser("endpoint is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" {
		return "", domain.NewErrorUser("endpoint must be a URL such as https://s3.example.com")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch {
	case scheme == "https":
	case scheme == "http" && allowPrivate:
	default:
		return "", domain.NewErrorUser("endpoint must use https")
	}
	if parsed.User != nil {
		return "", domain.NewErrorUser("endpoint must not contain credentials")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", domain.NewErrorUser("endpoint must not contain a path, query or fragment")
	}
	host := strings.ToLower(parsed.Host)
	if !allowPrivate {
		addr, err := netip.ParseAddr(parsed.Hostname())
		if (err == nil && CheckAddress(addr) != nil) || (err != nil && isLocalName(parsed.Hostname())) {
			return "", domain.NewErrorUser("endpoint must be a public address")
		}
	}
	return scheme + "://" + host, nil
}

func isLocalName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".")
}

func validateBucket(bucket string) error {
	if !bucketPattern.MatchString(bucket) || strings.Contains(bucket, "..") {
		return domain.NewErrorUser("bucket must be 3-63 lowercase letters, digits, dots or hyphens")
	}
	return nil
}

func normalizePrefix(prefix string) (string, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if len(prefix) > 200 || !prefixPattern.MatchString(prefix) {
		return "", domain.NewErrorUser("prefix may only contain letters, digits, '.', '_', '-' and '/' (at most 200)")
	}
	for _, segment := range strings.Split(prefix, "/") {
		if prefix != "" && (segment == "" || segment == "." || segment == "..") {
			return "", domain.NewErrorUser(fmt.Sprintf("prefix has an invalid segment %q", segment))
		}
	}
	return prefix, nil
}
