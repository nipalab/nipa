package domain

import (
	"net"
	"net/netip"
	"strings"
)

// IsCleartextRemote reports whether address is dialed without TLS (no https://
// scheme) to a host other than loopback (localhost, 127.0.0.0/8, ::1).
func IsCleartextRemote(address string) bool {
	address = strings.TrimSpace(address)
	if address == "" || strings.HasPrefix(strings.ToLower(address), "https://") {
		return false
	}
	host := HostForAddress(address)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Unmap().IsLoopback() {
		return false
	}
	return true
}
