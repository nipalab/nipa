package lfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo":            "https://github.com/org/repo.git/info/lfs",
		"https://ann:pw@github.com/org/repo.git": "https://ann:pw@github.com/org/repo.git/info/lfs",
		"https://host/org/repo/?x=1#frag":        "https://host/org/repo.git/info/lfs",
		"git@github.com:org/repo.git":            "https://github.com/org/repo.git/info/lfs",
		"github.com:org/repo":                    "https://github.com/org/repo.git/info/lfs",
		"ssh://git@host:2222/org/repo.git":       "https://host/org/repo.git/info/lfs",
		"git://host/org/repo":                    "https://host/org/repo.git/info/lfs",
		"file:///srv/repo":                       "file:///srv/repo",
		"/srv/repo":                              "/srv/repo",
		"ftp://host/repo":                        "",
		"not a url":                              "",
	}
	for in, want := range cases {
		assert.Equal(t, want, DeriveEndpoint(in), in)
	}
	p, ok := LocalPath("file:///srv/repo")
	assert.True(t, ok)
	assert.Equal(t, filepath.FromSlash("/srv/repo"), p)
	_, ok = LocalPath("file://%zz")
	assert.False(t, ok)
	_, ok = LocalPath("https://x/y")
	assert.False(t, ok)
	p, ok = LocalPath("../rel")
	assert.True(t, ok)
	assert.Equal(t, "../rel", p)
}

func setConfig(t *testing.T, repo *git.Repository, edit func(*config.Config)) {
	t.Helper()
	cfg, err := repo.Config()
	require.NoError(t, err)
	edit(cfg)
	require.NoError(t, repo.SetConfig(cfg))
}

func addRemote(name, url string) func(*config.Config) {
	return func(cfg *config.Config) {
		cfg.Remotes[name] = &config.RemoteConfig{Name: name, URLs: []string{url}, Fetch: []config.RefSpec{"+refs/heads/*:refs/remotes/" + config.RefSpec(name) + "/*"}}
	}
}

func TestDiscoverFromGitConfig(t *testing.T) {
	repo, err := git.PlainInit(t.TempDir(), false)
	require.NoError(t, err)
	assert.Equal(t, Settings{}, Discover(repo, ""))

	setConfig(t, repo, addRemote("upstream", "git@example.com:org/up.git"))
	assert.Equal(t, "https://example.com/org/up.git/info/lfs", Discover(repo, "").Endpoint)
	setConfig(t, repo, addRemote("origin", "https://example.com/org/game"))
	assert.Equal(t, "https://example.com/org/game.git/info/lfs", Discover(repo, "").Endpoint)
	assert.Equal(t, "https://other.example.com/x.git/info/lfs", Discover(repo, "https://other.example.com/x").Endpoint)

	setConfig(t, repo, func(cfg *config.Config) {
		cfg.Raw.Section("remote").Subsection("origin").SetOption("lfsurl", "https://lfs.example.com/remote")
	})
	assert.Equal(t, "https://lfs.example.com/remote", Discover(repo, "").Endpoint)
	setConfig(t, repo, func(cfg *config.Config) {
		cfg.Raw.Section("lfs").SetOption("url", "https://lfs.example.com/global")
	})
	assert.Equal(t, "https://lfs.example.com/global", Discover(repo, "").Endpoint)
}

func TestDiscoverFromLFSConfig(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[lfs]\n\turl = https://lfs.example.com/from-file\n"), 0o644))
	setConfig(t, repo, addRemote("origin", "https://example.com/org/game"))
	assert.Equal(t, "https://lfs.example.com/from-file", Discover(repo, "").Endpoint)

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[remote \"origin\"]\n\tlfsurl = https://lfs.example.com/remote-file\n"), 0o644))
	assert.Equal(t, "https://lfs.example.com/remote-file", Discover(repo, "").Endpoint)

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[broken"), 0o644))
	assert.Equal(t, "https://example.com/org/game.git/info/lfs", Discover(repo, "").Endpoint)
}

func TestDiscoverBareReadsLFSConfigFromHead(t *testing.T) {
	repo, err := git.PlainInit(t.TempDir(), true)
	require.NoError(t, err)
	store := func(o object.Object) plumbing.Hash {
		obj := repo.Storer.NewEncodedObject()
		require.NoError(t, o.Encode(obj))
		h, err := repo.Storer.SetEncodedObject(obj)
		require.NoError(t, err)
		return h
	}
	blob := repo.Storer.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, _ := blob.Writer()
	_, _ = w.Write([]byte("[lfs]\n\turl = https://lfs.example.com/bare\n"))
	_ = w.Close()
	bh, err := repo.Storer.SetEncodedObject(blob)
	require.NoError(t, err)
	tree := store(&object.Tree{Entries: []object.TreeEntry{{Name: ".lfsconfig", Mode: 0o100644, Hash: bh}}})
	sig := object.Signature{Name: "a", Email: "a@x", When: time.Unix(0, 0)}
	commit := store(&object.Commit{Author: sig, Committer: sig, Message: "m", TreeHash: tree})
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/main", commit)))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/main")))
	assert.Equal(t, "https://lfs.example.com/bare", Discover(repo, "").Endpoint)
	assert.Nil(t, headFile(repo, plumbing.ZeroHash, ".lfsconfig"))
	assert.Nil(t, headFile(repo, commit, "missing"))
}

func TestDiscoverLocalRemoteUsesItsStore(t *testing.T) {
	src := t.TempDir()
	_, err := git.PlainInit(src, false)
	require.NoError(t, err)
	writeObject(t, filepath.Join(src, ".git"), "object")

	repo, err := git.PlainInit(t.TempDir(), true)
	require.NoError(t, err)
	s := Discover(repo, "file://"+filepath.ToSlash(src))
	assert.Empty(t, s.Endpoint)
	require.Len(t, s.Stores, 1)
	assert.Equal(t, filepath.Join(src, ".git", "lfs", "objects"), s.Stores[0].Dir)

	setConfig(t, repo, addRemote("origin", src))
	assert.Len(t, Discover(repo, "").Stores, 1)
	assert.Empty(t, LocalStores(t.TempDir()))
}
