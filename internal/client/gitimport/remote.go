package gitimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

const mirrorCacheDir = "nipa/git-import"

var (
	remotePrefixes = []string{"https://", "http://", "ssh://", "git://", "file://"}
	scpURL         = regexp.MustCompile(`^([^@/\\:\s]+)@([^@/\\:\s]+):(.+)$`)
	userinfoRe     = regexp.MustCompile(`://[^/@]*@`)
)

func IsRemoteURL(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range remotePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	if _, err := os.Stat(s); err == nil {
		return false
	}
	return scpURL.MatchString(s)
}

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if scpURL.MatchString(raw) {
			return raw
		}
		return userinfoRe.ReplaceAllString(raw, "://")
	}
	if u.User == nil {
		return u.String()
	}
	if u.Scheme == "ssh" && u.User.Username() != "" {
		u.User = url.User(u.User.Username())
	} else {
		u.User = nil
	}
	return u.String()
}

func MirrorDir(root, raw string) string {
	sum := sha256.Sum256([]byte(RedactURL(raw)))
	return filepath.Join(root, hex.EncodeToString(sum[:]))
}

func DefaultCacheRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", domain.NewUserError("cannot find the user cache folder for remote git mirrors: " + err.Error())
	}
	return filepath.Join(dir, filepath.FromSlash(mirrorCacheDir)), nil
}

type remoteSource struct {
	raw      string
	display  string
	cloneURL string
	dir      string
	http     bool
	credURL  *url.URL
	auth     transport.AuthMethod
	urlAuth  bool
	secrets  []string
}

func newRemoteSource(root, raw string) (*remoteSource, error) {
	rs := &remoteSource{raw: raw, display: RedactURL(raw), dir: MirrorDir(root, raw)}
	if m := scpURL.FindStringSubmatch(raw); m != nil && !strings.Contains(raw, "://") {
		rs.cloneURL = raw
		rs.auth = sshAuth(m[1])
		return rs, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme != "file" {
		return nil, domain.NewUserError(fmt.Sprintf("invalid git url %q", rs.display))
	}
	pass, hasPass := "", false
	if u.User != nil {
		pass, hasPass = u.User.Password()
		if hasPass && pass != "" {
			rs.secrets = append(rs.secrets, pass)
		}
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		rs.http = true
		rs.credURL = lfs.StripUserinfo(u)
		if c, ok := lfs.FromURL(u); ok {
			rs.auth, rs.urlAuth = &githttp.BasicAuth{Username: c.Username, Password: c.Password}, true
			rs.secrets = append(rs.secrets, c.Username)
		}
	case "ssh":
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		if hasPass {
			rs.auth = &gitssh.Password{User: user, Password: pass}
		} else {
			rs.auth = sshAuth(user)
		}
	case "file":
		installFileTransport()
	}
	rs.cloneURL = RedactURL(raw)
	if strings.ToLower(u.Scheme) == "ssh" && u.User != nil {
		rs.cloneURL = (&url.URL{Scheme: u.Scheme, User: url.User(u.User.Username()), Host: u.Host, Path: u.Path}).String()
	}
	return rs, nil
}

func sshAuth(user string) transport.AuthMethod {
	if user == "" {
		user = "git"
	}
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		if a, err := gitssh.NewSSHAgentAuth(user); err == nil {
			return a
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		if a, err := gitssh.NewPublicKeysFromFile(user, filepath.Join(home, ".ssh", name), ""); err == nil {
			return a
		}
	}
	return nil
}

func (im *Importer) mirror(ctx context.Context, raw string) (*remoteSource, error) {
	root := im.cacheRoot
	if root == "" {
		var err error
		if root, err = DefaultCacheRoot(); err != nil {
			return nil, err
		}
	}
	rs, err := newRemoteSource(root, raw)
	if err != nil {
		return nil, err
	}
	if repo, err := git.PlainOpen(rs.dir); err == nil {
		err = im.withAuth(ctx, rs, func(auth transport.AuthMethod) error {
			err := repo.FetchContext(ctx, &git.FetchOptions{RemoteName: git.DefaultRemoteName, Auth: auth, Force: true, Prune: true})
			if errors.Is(err, git.NoErrAlreadyUpToDate) {
				return nil
			}
			return err
		})
		return rs, err
	}
	if err := os.RemoveAll(rs.dir); err != nil {
		return nil, fmt.Errorf("prepare git mirror cache: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create git mirror cache: %w", err)
	}
	tmp, err := os.MkdirTemp(root, filepath.Base(rs.dir)+".tmp-")
	if err != nil {
		return nil, fmt.Errorf("create git mirror cache: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	err = im.withAuth(ctx, rs, func(auth transport.AuthMethod) error {
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
		_, err := git.PlainCloneContext(ctx, tmp, true, &git.CloneOptions{URL: rs.cloneURL, Auth: auth, Mirror: true})
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, rs.dir); err != nil {
		return nil, fmt.Errorf("store git mirror: %w", err)
	}
	return rs, nil
}

func (im *Importer) withAuth(ctx context.Context, rs *remoteSource, fn func(transport.AuthMethod) error) error {
	err := fn(rs.auth)
	if err == nil {
		return nil
	}
	if isAuthError(err) && rs.http && !rs.urlAuth && im.credentials != nil && ctx.Err() == nil {
		creds, ferr := im.credentials.Fill(ctx, rs.credURL)
		if ferr == nil {
			rs.secrets = append(rs.secrets, creds.Password, creds.Credential)
			var auth transport.AuthMethod = &githttp.BasicAuth{Username: creds.Username, Password: creds.Password}
			if creds.AuthType != "" && strings.EqualFold(creds.AuthType, "bearer") {
				auth = &githttp.TokenAuth{Token: creds.Credential}
			}
			if err = fn(auth); err == nil {
				rs.auth = auth
				_ = im.credentials.Approve(ctx, rs.credURL, creds)
				return nil
			}
			if isAuthError(err) {
				_ = im.credentials.Reject(ctx, rs.credURL, creds)
			}
		}
	}
	return rs.error(err)
}

func isAuthError(err error) bool {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) || errors.Is(err, transport.ErrInvalidAuthMethod) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "no supported methods remain")
}

func (rs *remoteSource) error(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case isAuthError(err):
		return domain.NewUserError(fmt.Sprintf("authentication failed for %s: %s", rs.display, rs.scrub(err.Error()))).
			WithHint("the git server refused the credentials", "put the credentials in the URL or a git credential helper, or load your SSH key into ssh-agent")
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return domain.NewUserError(fmt.Sprintf("git repository %s not found", rs.display)).
			WithHint("the server has no repository at this URL, or hides it from these credentials", "check the URL and that your credentials can read it")
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return domain.NewUserError(fmt.Sprintf("git repository %s is empty", rs.display))
	}
	return fmt.Errorf("mirror %s: %s", rs.display, rs.scrub(err.Error()))
}

func (rs *remoteSource) scrub(msg string) string {
	if rs.raw != rs.display {
		msg = strings.ReplaceAll(msg, rs.raw, rs.display)
	}
	for _, s := range rs.secrets {
		if len(s) >= 4 {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return msg
}

var fileTransport sync.Once

func installFileTransport() {
	fileTransport.Do(func() {
		client.InstallProtocol("file", server.NewServer(fileLoader{}))
	})
}

type fileLoader struct{}

func (fileLoader) Load(ep *transport.Endpoint) (storer.Storer, error) {
	path := ep.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	repo, err := git.PlainOpen(filepath.FromSlash(path))
	if err != nil {
		return nil, transport.ErrRepositoryNotFound
	}
	return repo.Storer, nil
}
