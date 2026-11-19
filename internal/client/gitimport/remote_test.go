package gitimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/gitimport/lfs"
)

func TestIsRemoteURL(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "git@host:path")
	require.NoError(t, os.MkdirAll(local, 0o755))
	cases := map[string]bool{
		"https://example.com/org/repo.git": true,
		"HTTP://example.com/repo":          true,
		"ssh://git@example.com/repo.git":   true,
		"git://example.com/repo":           true,
		"file:///srv/repo":                 true,
		"git@github.com:org/repo.git":      true,
		"git@github.com:/srv/repo.git":     true,
		"../game":                          false,
		"/srv/game":                        false,
		"host:path":                        false,
		local:                              false,
		`C:\work\game`:                     false,
	}
	for in, want := range cases {
		require.Equal(t, want, IsRemoteURL(in), in)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://ann:secret@example.com/org/repo.git": "https://example.com/org/repo.git",
		"https://token@example.com/repo":              "https://example.com/repo",
		"ssh://git:pw@example.com:22/repo":            "ssh://git@example.com:22/repo",
		"ssh://:pw@example.com/repo":                  "ssh://example.com/repo",
		"ssh://git@example.com/repo":                  "ssh://git@example.com/repo",
		"git@example.com:org/repo.git":                "git@example.com:org/repo.git",
		"file:///srv/repo":                            "file:///srv/repo",
		"https://example.com/repo":                    "https://example.com/repo",
		"https://ann:p%zz@example.com/repo":           "https://example.com/repo",
	}
	for in, want := range cases {
		require.Equal(t, want, RedactURL(in), in)
	}
}

func TestMirrorDir(t *testing.T) {
	a := MirrorDir("/cache", "https://ann:one@example.com/repo.git")
	b := MirrorDir("/cache", "https://bob:two@example.com/repo.git")
	require.Equal(t, a, b, "credentials do not change the cache key")
	require.Equal(t, "/cache", filepath.Dir(a))
	require.Len(t, filepath.Base(a), 64)
	require.NotEqual(t, a, MirrorDir("/cache", "https://example.com/other.git"))

	t.Setenv("XDG_CACHE_HOME", "/xdg")
	t.Setenv("HOME", "/home/x")
	root, err := DefaultCacheRoot()
	require.NoError(t, err)
	require.Equal(t, filepath.Join("nipa", "git-import"), filepath.Join(filepath.Base(filepath.Dir(root)), filepath.Base(root)))
}

func fileURL(dir string) string {
	return "file://" + filepath.ToSlash(dir)
}

func TestRun_FileRemoteMirrorsAndFetches(t *testing.T) {
	repo := newSampleRepo(t)
	cache := t.TempDir()
	srv := newFakeServer()
	opts := baseOptions(fileURL(repo.dir))
	var previewSource, previewMirror string
	opts.Confirm = func(s *Summary) error { previewSource, previewMirror = s.Source, s.Mirror; return nil }

	sum, err := NewImporter(srv).WithCacheRoot(cache).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, 4, sum.Imported)
	require.Equal(t, fileURL(repo.dir), sum.Source)
	require.Equal(t, fileURL(repo.dir), previewSource)
	require.Equal(t, MirrorDir(cache, opts.GitPath), previewMirror)
	require.Equal(t, MirrorDir(cache, opts.GitPath), sum.Mirror)
	require.False(t, sum.CacheRemoved)
	_, err = os.Stat(filepath.Join(sum.Mirror, StateDirName, StateFileName))
	require.NoError(t, err, "the state lives in the mirror")
	_, err = os.Stat(filepath.Join(repo.dir, ".git", StateDirName))
	require.True(t, os.IsNotExist(err), "the source repository is not written")

	next := repo.commit("Cid", map[string]string{"README.md": "hello again\n", "art/logo.png": "\x89PNG\x00\x01\x02", "docs/a.md": "feature\n", "new.txt": "new\n"}, "next\n", repo.merge)
	repo.branch("main", next)
	opts.Update = true
	sum, err = NewImporter(srv).WithCacheRoot(cache).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.Equal(t, 1, sum.Imported)
	require.Equal(t, RefUpdated, statuses(sum)["branch:main"])

	opts.DropCache = true
	sum, err = NewImporter(srv).WithCacheRoot(cache).Run(context.Background(), opts, nil)
	require.NoError(t, err)
	require.True(t, sum.CacheRemoved)
	_, err = os.Stat(sum.Mirror)
	require.True(t, os.IsNotExist(err))
}

func TestRun_FileRemoteUsesSourceLFSStore(t *testing.T) {
	repo := newLFSRepo(t)
	storeLFS(t, filepath.Join(repo.dir, ".git"), repo.hero)
	storeLFS(t, filepath.Join(repo.dir, ".git"), repo.map1)
	srv := newFakeServer()
	sum, err := NewImporter(srv).WithCacheRoot(t.TempDir()).Run(context.Background(), baseOptions(fileURL(repo.dir)), nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), sum.LFSRead)
	require.Equal(t, int64(len(repo.map1)), fileOf(srv, repo.second, "maps/one.map").size)
}

func TestRun_RemoteErrors(t *testing.T) {
	ctx := context.Background()
	srv := newFakeServer()
	cache := t.TempDir()

	_, err := NewImporter(srv).WithCacheRoot(cache).Run(ctx, baseOptions(fileURL(t.TempDir())), nil)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Message, "not found")

	empty := t.TempDir()
	_, err = git.PlainInit(empty, false)
	require.NoError(t, err)
	_, err = NewImporter(srv).WithCacheRoot(cache).Run(ctx, baseOptions(fileURL(empty)), nil)
	require.Error(t, err)
	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	require.Empty(t, entries, "a failed clone leaves no mirror behind")

	_, err = NewImporter(srv).WithCacheRoot(cache).Run(ctx, baseOptions("https://exa mple.com/x"), nil)
	require.ErrorContains(t, err, "invalid git url")

	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o644))
	_, err = NewImporter(srv).WithCacheRoot(filepath.Join(blocker, "sub")).Run(ctx, baseOptions(fileURL(newSampleRepo(t).dir)), nil)
	require.ErrorContains(t, err, "git mirror cache")
}

func TestRun_RemoteAuthErrorHasHint(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	git401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auths = append(auths, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer git401.Close()
	helper := &fakeCredentials{creds: lfs.Credentials{Username: "ann", Password: "helper-secret"}}
	srv := newFakeServer()

	_, err := NewImporter(srv).WithCacheRoot(t.TempDir()).WithCredentials(helper).Run(context.Background(), baseOptions(git401.URL+"/org/repo.git"), nil)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Message, "authentication failed for "+git401.URL+"/org/repo.git")
	require.Contains(t, de.Action, "credential helper")
	require.Equal(t, 1, helper.filled)
	require.Equal(t, 1, helper.rejected)
	require.Zero(t, helper.approved)
	require.NotContains(t, err.Error(), "helper-secret")

	u, _ := url.Parse(git401.URL + "/org/repo.git")
	u.User = url.UserPassword("ann", "url-secret")
	helper = &fakeCredentials{}
	_, err = NewImporter(srv).WithCacheRoot(t.TempDir()).WithCredentials(helper).Run(context.Background(), baseOptions(u.String()), nil)
	require.ErrorAs(t, err, &de)
	require.NotContains(t, de.Error(), "url-secret")
	require.Zero(t, helper.filled, "url credentials are not replaced by the helper")
	mu.Lock()
	require.Contains(t, auths[len(auths)-1], "Basic ")
	mu.Unlock()

	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	_, err = NewImporter(srv).WithCacheRoot(t.TempDir()).WithCredentials(nil).Run(context.Background(), baseOptions(notFound.URL+"/missing.git"), nil)
	require.ErrorAs(t, err, &de)
	require.Contains(t, de.Message, "not found")
}

type fakeCredentials struct {
	creds                      lfs.Credentials
	err                        error
	filled, approved, rejected int
}

func (f *fakeCredentials) Fill(context.Context, *url.URL) (lfs.Credentials, error) {
	f.filled++
	return f.creds, f.err
}

func (f *fakeCredentials) Approve(context.Context, *url.URL, lfs.Credentials) error {
	f.approved++
	return nil
}

func (f *fakeCredentials) Reject(context.Context, *url.URL, lfs.Credentials) error {
	f.rejected++
	return nil
}

func TestWithAuthUsesHelperCredentials(t *testing.T) {
	ctx := context.Background()
	rs, err := newRemoteSource(t.TempDir(), "https://example.com/org/repo.git")
	require.NoError(t, err)
	helper := &fakeCredentials{creds: lfs.Credentials{Username: "ann", Password: "pw"}}
	im := NewImporter(newFakeServer()).WithCredentials(helper)
	var seen []transport.AuthMethod
	err = im.withAuth(ctx, rs, func(a transport.AuthMethod) error {
		seen = append(seen, a)
		if a == nil {
			return transport.ErrAuthenticationRequired
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, seen, 2)
	require.Equal(t, &githttp.BasicAuth{Username: "ann", Password: "pw"}, seen[1])
	require.Equal(t, 1, helper.approved)
	require.Equal(t, seen[1], rs.auth)

	rs, _ = newRemoteSource(t.TempDir(), "https://example.com/org/repo.git")
	helper = &fakeCredentials{creds: lfs.Credentials{AuthType: "Bearer", Credential: "token"}}
	err = im.WithCredentials(helper).withAuth(ctx, rs, func(a transport.AuthMethod) error {
		if a == nil {
			return transport.ErrAuthorizationFailed
		}
		return errors.New("boom token here")
	})
	require.ErrorContains(t, err, "boom *** here")
	require.Zero(t, helper.rejected)

	err = im.withAuth(ctx, rs, func(transport.AuthMethod) error { return context.Canceled })
	require.ErrorIs(t, err, context.Canceled)
}

func TestNewRemoteSource(t *testing.T) {
	rs, err := newRemoteSource("/c", "git@example.com:org/repo.git")
	require.NoError(t, err)
	require.Equal(t, "git@example.com:org/repo.git", rs.cloneURL)
	require.False(t, rs.http)

	rs, err = newRemoteSource("/c", "ssh://deploy:pw@example.com:2222/org/repo.git")
	require.NoError(t, err)
	require.Equal(t, "ssh://deploy@example.com:2222/org/repo.git", rs.cloneURL)
	require.Equal(t, "ssh://deploy@example.com:2222/org/repo.git", rs.display)
	require.Contains(t, rs.scrub("dial ssh://deploy:pw-long@x"), "ssh://deploy:pw-long@x")
	require.NotNil(t, rs.auth)

	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("HOME", t.TempDir())
	rs, err = newRemoteSource("/c", "ssh://example.com/org/repo.git")
	require.NoError(t, err)
	require.Nil(t, rs.auth)

	rs, err = newRemoteSource("/c", "https://ann:longsecret@example.com/r.git")
	require.NoError(t, err)
	require.True(t, rs.urlAuth)
	require.Equal(t, "https://example.com/r.git", rs.cloneURL)
	require.Equal(t, "failed at https://example.com/r.git with ***", rs.scrub("failed at https://ann:longsecret@example.com/r.git with longsecret"))

	require.True(t, isAuthError(errors.New("ssh: handshake failed: ssh: unable to authenticate")))
	require.False(t, isAuthError(errors.New("other")))
	require.Contains(t, rs.error(errors.New("plain failure")).Error(), "mirror https://example.com/r.git")
}
