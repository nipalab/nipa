package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/ignore"
)

func TestService_GeneratedFoldersAndIgnore(t *testing.T) {
	root := t.TempDir()
	h := newHarness()
	h.store.repos = []string{root}
	var changed int
	h.svc.Events = func(name string, _ any) {
		if name == EventRepoChanged {
			changed++
		}
	}

	got, err := h.svc.GeneratedFolders(root, []string{"node_modules/a/i.js", "node_modules/b.js", "src/a.go"})
	require.NoError(t, err)
	require.Equal(t, []ignore.Generated{{Dir: "node_modules", Files: 2, Pattern: "node_modules/"}}, got)

	n, err := h.svc.IgnoreFolders(root, []string{"node_modules/"})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, changed)
	n, err = h.svc.IgnoreFolders(root, []string{"node_modules/"})
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, changed, "nothing new, no refresh")

	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0o755))
	_, err = h.svc.IgnoreFolders(filepath.Join(root, "dir"), []string{"x"})
	require.Error(t, err, "unknown clone")
	_, err = h.svc.GeneratedFolders("/elsewhere", nil)
	require.Error(t, err)
}

func TestService_IgnoreFolders_WriteError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".nipaignore"), 0o755))
	h := newHarness()
	h.store.repos = []string{root}
	_, err := h.svc.IgnoreFolders(root, []string{"x/"})
	require.Error(t, err)
}

func TestService_ImportGitignore(t *testing.T) {
	root := t.TempDir()
	h := newHarness()
	h.store.repos = []string{root}
	var changed int
	h.svc.Events = func(name string, _ any) {
		if name == EventRepoChanged {
			changed++
		}
	}

	ok, err := h.svc.CanImportGitignore(root)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = h.svc.ImportGitignore(root)
	require.Error(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n[Bb]in/\n"), 0o644))
	ok, err = h.svc.CanImportGitignore(root)
	require.NoError(t, err)
	require.True(t, ok)

	res, err := h.svc.ImportGitignore(root)
	require.NoError(t, err)
	require.Equal(t, 2, res.Added)
	require.Equal(t, []string{"[Bb]in/"}, res.Unsupported)
	require.Equal(t, 1, changed)

	ok, err = h.svc.CanImportGitignore(root)
	require.NoError(t, err)
	require.False(t, ok)

	_, err = h.svc.CanImportGitignore("/elsewhere")
	require.Error(t, err)
	_, err = h.svc.ImportGitignore("/elsewhere")
	require.Error(t, err)
}
