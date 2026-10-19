package domain

import (
	"fmt"
	"net/url"
	"strings"
)

type NipaUrl struct {
	Url     string
	Host    string
	Origin  string
	Org     string
	Project string
	Path    string
}

// Endpoint is a normalized server address: Address keeps the http/https scheme
// when the input carried one so the transport can pick TLS, while Host is the
// scheme-less host:port key sessions are stored under.
type Endpoint struct {
	Address string
	Host    string
}

// ParseHost normalizes a bare host, URL or repository URL to its host part.
// The scheme is irrelevant for a host, so a bare value is parsed as a
// scheme-relative reference.
func ParseHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", NewUserError("host cannot be empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	parsedURL, err := url.Parse(raw)
	if err != nil || parsedURL.Host == "" {
		return "", NewUserError(fmt.Sprintf("invalid host %q", raw))
	}
	return parsedURL.Host, nil
}

// HostForAddress returns the scheme-less host[:port] of a server address,
// which may be a bare host or carry an http/https scheme. It is the key tokens
// are stored under, while the scheme selects the transport security.
func HostForAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	host, err := ParseHost(address)
	if err != nil {
		return address
	}
	return host
}

// ParseEndpoint normalizes a bare host, URL or repository URL into a server
// endpoint. Only http and https carry a scheme through to the address; any
// other scheme is rejected.
func ParseEndpoint(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, NewUserError("host cannot be empty")
	}
	scheme := ""
	if idx := strings.Index(raw, "://"); idx >= 0 {
		scheme = strings.ToLower(raw[:idx])
		if scheme != "http" && scheme != "https" {
			return Endpoint{}, NewUserError("invalid URL scheme, expected http or https")
		}
	}
	host, err := ParseHost(raw)
	if err != nil {
		return Endpoint{}, err
	}
	address := host
	if scheme != "" {
		address = scheme + "://" + host
	}
	return Endpoint{Address: address, Host: host}, nil
}

func ParseNipaUrl(urlString string) (*NipaUrl, error) {
	allowedSchemes := map[string]struct{}{
		"http":  {},
		"https": {},
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return nil, NewUserError(fmt.Sprintf("url can not be parsed: %v", err))
	}

	if _, ok := allowedSchemes[parsedURL.Scheme]; !ok {
		return nil, NewUserError("invalid URL scheme, expected http or https")
	}

	pathSegments := strings.Split(strings.TrimPrefix(parsedURL.Path, "/"), "/")
	if len(pathSegments) < 2 {
		return nil, NewUserError("invalid URL path, expected /org/project/[path]")
	}

	org := pathSegments[0]
	project := pathSegments[1]
	repoPath := ""
	if len(pathSegments) > 2 {
		repoPath = strings.Join(pathSegments[2:], "/")
	}

	nipaUrl := &NipaUrl{
		Url:     urlString,
		Host:    parsedURL.Host,
		Origin:  parsedURL.Scheme + "://" + parsedURL.Host,
		Org:     org,
		Project: project,
		Path:    repoPath,
	}
	return nipaUrl, nil
}
