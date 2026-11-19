package lfs

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
)

type Settings struct {
	Endpoint string
	Stores   []*LocalStore
}

func Discover(repo *git.Repository, remoteURL string) Settings {
	cfg := gitConfig(repo)
	lfsCfg := lfsConfig(repo)
	get := func(section, subsection, key string) string {
		for _, c := range []*format.Config{cfg, lfsCfg} {
			if c == nil {
				continue
			}
			var v string
			if subsection == "" {
				v = c.Section(section).Option(key)
			} else {
				v = c.Section(section).Subsection(subsection).Option(key)
			}
			if v != "" {
				return v
			}
		}
		return ""
	}
	remote := defaultRemote(cfg)
	if v := get("lfs", "", "url"); v != "" {
		return settingsFor(v)
	}
	if remote != "" {
		if v := get("remote", remote, "lfsurl"); v != "" {
			return settingsFor(v)
		}
	}
	if remoteURL == "" && remote != "" && cfg != nil {
		remoteURL = cfg.Section("remote").Subsection(remote).Option("url")
	}
	if remoteURL == "" {
		return Settings{}
	}
	return settingsFor(DeriveEndpoint(remoteURL))
}

func settingsFor(endpoint string) Settings {
	if endpoint == "" {
		return Settings{}
	}
	if dir, ok := LocalPath(endpoint); ok {
		return Settings{Stores: LocalStores(dir)}
	}
	return Settings{Endpoint: endpoint}
}

func gitConfig(repo *git.Repository) *format.Config {
	cfg, err := repo.Config()
	if err != nil || cfg.Raw == nil {
		return nil
	}
	return cfg.Raw
}

func defaultRemote(cfg *format.Config) string {
	if cfg == nil {
		return ""
	}
	remotes := cfg.Section("remote").Subsections
	for _, r := range remotes {
		if r.Name == "origin" {
			return r.Name
		}
	}
	if len(remotes) > 0 {
		return remotes[0].Name
	}
	return ""
}

func lfsConfig(repo *git.Repository) *format.Config {
	var data []byte
	if wt, err := repo.Worktree(); err == nil {
		f, err := wt.Filesystem.Open(".lfsconfig")
		if err == nil {
			data, _ = io.ReadAll(io.LimitReader(f, 64<<10))
			_ = f.Close()
		}
	} else if head, err := repo.Head(); err == nil {
		data = headFile(repo, head.Hash(), ".lfsconfig")
	}
	if len(data) == 0 {
		return nil
	}
	cfg := format.New()
	if err := format.NewDecoder(strings.NewReader(string(data))).Decode(cfg); err != nil {
		return nil
	}
	return cfg
}

func headFile(repo *git.Repository, h plumbing.Hash, name string) []byte {
	commit, err := repo.CommitObject(h)
	if err != nil {
		return nil
	}
	f, err := commit.File(name)
	if err != nil || f.Size > 64<<10 {
		return nil
	}
	content, err := f.Contents()
	if err != nil {
		return nil
	}
	return []byte(content)
}

var scpLike = regexp.MustCompile(`^(?:([^@/:]+)@)?([^@/:]+):(.+)$`)

func DeriveEndpoint(remote string) string {
	if _, ok := LocalPath(remote); ok {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" || u.Host == "" {
		m := scpLike.FindStringSubmatch(remote)
		if m == nil {
			return ""
		}
		u = &url.URL{Scheme: "https", Host: m[2], Path: "/" + strings.TrimPrefix(m[3], "/")}
	} else {
		switch u.Scheme {
		case "http", "https":
		case "ssh", "git", "git+ssh", "ssh+git":
			u = &url.URL{Scheme: "https", Host: u.Hostname(), Path: u.Path}
		default:
			return ""
		}
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, ".git") {
		u.Path += ".git"
	}
	u.Path += "/info/lfs"
	return u.String()
}

func LocalPath(raw string) (string, bool) {
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		return filepath.FromSlash(u.Path), true
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		return raw, true
	}
	return "", false
}

func LocalStores(path string) []*LocalStore {
	var out []*LocalStore
	for _, dir := range []string{filepath.Join(path, ".git"), path} {
		if info, err := os.Stat(filepath.Join(dir, "lfs", "objects")); err == nil && info.IsDir() {
			out = append(out, NewLocalStore(dir))
		}
	}
	return out
}
