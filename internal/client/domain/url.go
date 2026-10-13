package domain

import (
	"fmt"
	"net/url"
	"strings"
)

type NipaUrl struct {
	Url     string
	Host    string
	Org     string
	Project string
	Path    string
}

// ParseHost normalizes a bare host, URL or repository URL to its host part.
func ParseHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", NewUserError("host cannot be empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsedURL, err := url.Parse(raw)
	if err != nil || parsedURL.Host == "" {
		return "", NewUserError(fmt.Sprintf("invalid host %q", raw))
	}
	return parsedURL.Host, nil
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
		Org:     org,
		Project: project,
		Path:    repoPath,
	}
	return nipaUrl, nil
}
