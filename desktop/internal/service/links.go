package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

const EventNavigate = "app:navigate"

// LinkScheme is the custom URL scheme the app registers.
const LinkScheme = "nipa"

// Navigation tells the frontend what to show. Root is empty when no opened
// clone matches the link; Server, Org and Project then let it offer a clone.
type Navigation struct {
	Root    string `json:"root"`
	Tab     string `json:"tab"`
	Number  int64  `json:"number"`
	Commit  string `json:"commit"`
	Server  string `json:"server"`
	Org     string `json:"org"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Action  string `json:"action"`
	// Branch is the lock scope a "lock" action targets; empty is mainline.
	Branch string `json:"branch"`
}

var errBadLink = errors.New("not a Nipa link; expected nipa://<server>/<org>/<project>[/merge-requests/<n>|/commits/<id>|/file/<path>|/files|/history|/branches|/locks|/changes]")

var linkTabs = map[string]string{
	"":               "changes",
	"changes":        "changes",
	"history":        "history",
	"commits":        "history",
	"branches":       "branches",
	"tags":           "branches",
	"locks":          "locks",
	"merge-requests": "mrs",
	"settings":       "details",
	"files":          "files",
	"file":           "files",
}

// ParseLink reads nipa://<host[:port]>/<org>/<project>[/<section>[/<id>]],
// where nipa://.../file/<path> opens the Files tab at a file.
// A server query parameter overrides the address offered for cloning.
func ParseLink(raw string) (Navigation, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, LinkScheme) || u.Host == "" {
		return Navigation{}, errBadLink
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Navigation{}, errBadLink
	}
	if len(parts) > 3 && parts[2] == "file" {
		return Navigation{Tab: "files", Server: linkServer(u), Org: parts[0], Project: parts[1], Path: strings.Join(parts[3:], "/")}, nil
	}
	if len(parts) > 4 || len(parts) == 3 && parts[2] == "file" {
		return Navigation{}, errBadLink
	}
	section := ""
	if len(parts) > 2 {
		section = parts[2]
	}
	tab, ok := linkTabs[section]
	if !ok {
		return Navigation{}, errBadLink
	}
	nav := Navigation{Tab: tab, Server: linkServer(u), Org: parts[0], Project: parts[1]}
	if len(parts) == 4 {
		switch section {
		case "merge-requests":
			n, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil || n <= 0 {
				return Navigation{}, errBadLink
			}
			nav.Number = n
		case "commits":
			nav.Commit = parts[3]
		default:
			return Navigation{}, errBadLink
		}
	}
	return nav, nil
}

func linkServer(u *url.URL) string {
	if s := strings.TrimSpace(u.Query().Get("server")); s != "" {
		if ep, err := clientDomain.ParseEndpoint(s); err == nil {
			return ep.Address
		}
	}
	host := u.Hostname()
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return "http://" + u.Host // NOSONAR loopback only
	}
	return "https://" + u.Host
}

// OpenLink resolves a nipa:// link against the opened clones and navigates
// there.
func (s *Service) OpenLink(ctx context.Context, raw string) (Navigation, error) {
	nav, err := ParseLink(raw)
	if err != nil {
		return Navigation{}, err
	}
	host := clientDomain.HostForAddress(nav.Server)
	repos, err := s.Repos(ctx)
	if err != nil {
		return Navigation{}, err
	}
	for _, r := range repos {
		u, err := clientDomain.ParseNipaUrl(r.URL)
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Host, host) && u.Org == nav.Org && u.Project == nav.Project {
			nav.Root = r.Root
			break
		}
	}
	s.Navigate(nav)
	return nav, nil
}

// Navigate tells the frontend to show nav; it is also kept until the
// frontend takes it, for links that started the app.
func (s *Service) Navigate(nav Navigation) {
	s.mu.Lock()
	s.pending = &nav
	s.mu.Unlock()
	s.emit(EventNavigate, nav)
}

// TakeNavigation returns and clears the last navigation the frontend has not
// handled yet.
func (s *Service) TakeNavigation() *Navigation {
	s.mu.Lock()
	defer s.mu.Unlock()
	nav := s.pending
	s.pending = nil
	return nav
}

// LinkArg returns the first nipa:// argument, as passed by the OS when a link
// opens the app.
func LinkArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(strings.ToLower(a), LinkScheme+"://") {
			return a
		}
	}
	return ""
}
