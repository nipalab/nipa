package ratelimit

import (
	"net"
	"net/netip"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of CIDRs or IPs.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, err
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func trusted(addr netip.Addr, proxies []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, p := range proxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func hostAddr(hostport string) (netip.Addr, bool) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// ClientIP returns the caller address. X-Forwarded-For is honoured only when the
// direct peer is a trusted proxy; the right-most untrusted hop wins.
func ClientIP(remoteAddr, xff string, proxies []netip.Prefix) string {
	peer, ok := hostAddr(remoteAddr)
	if !ok {
		return remoteAddr
	}
	if xff == "" || !trusted(peer, proxies) {
		return peer.String()
	}
	hops := strings.Split(xff, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := hostAddr(hops[i])
		if !ok {
			return peer.String()
		}
		if !trusted(hop, proxies) {
			return hop.String()
		}
	}
	return peer.String()
}
