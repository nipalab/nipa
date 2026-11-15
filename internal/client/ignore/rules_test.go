package ignore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func TestDetectGenerated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "game", "ProjectSettings"), 0o755))
	writeFile(t, root, "unreal/Shooter.uproject", "{}")

	got := DetectGenerated(root, []string{
		"web/node_modules/a/index.js",
		"web/node_modules/a/node_modules/b/index.js",
		"web/node_modules/c.js",
		"web/.next/cache/x",
		"game/Library/ArtifactDB",
		"game/Assets/Library/notes.txt",
		"Library/books.txt",
		"unreal/Intermediate/Build/x.obj",
		"other/Intermediate/x",
		"src/main.go",
		"node_modules",
	})
	require.Equal(t, []Generated{
		{Dir: "web/node_modules", Files: 3, Pattern: "node_modules/"},
		{Dir: "game/Library", Files: 1, Pattern: "/game/Library/"},
		{Dir: "unreal/Intermediate", Files: 1, Pattern: "/unreal/Intermediate/"},
		{Dir: "web/.next", Files: 1, Pattern: ".next/"},
	}, got)
	require.Empty(t, DetectGenerated(root, nil))
}

func TestDetectGenerated_UnityAtRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ProjectSettings"), 0o755))
	got := DetectGenerated(root, []string{"Library/a", "Temp/b", "Assets/c.png"})
	require.Equal(t, []Generated{
		{Dir: "Library", Files: 1, Pattern: "/Library/"},
		{Dir: "Temp", Files: 1, Pattern: "/Temp/"},
	}, got)
}

func TestAppendRules(t *testing.T) {
	root := t.TempDir()
	n, err := AppendRules(root, []string{"node_modules/", " ", "node_modules/"})
	require.NoError(t, err)
	require.Equal(t, 1, n)

	writeFile(t, root, FileName, "*.tmp")
	n, err = AppendRules(root, []string{"*.tmp", ".next/"})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	data, _ := os.ReadFile(filepath.Join(root, FileName))
	require.Equal(t, "*.tmp\n.next/\n", string(data))

	n, err = AppendRules(root, []string{"*.tmp"})
	require.NoError(t, err)
	require.Zero(t, n)

	m, err := New(root)
	require.NoError(t, err)
	require.True(t, m.Ignores("app/.next", true))
}

func TestImportGitignore(t *testing.T) {
	root := t.TempDir()
	_, err := ImportGitignore(root)
	require.Error(t, err)
	require.False(t, CanImportGitignore(root))

	writeFile(t, root, GitignoreFile, "# deps\nnode_modules/\n\n*.log\n[Bb]uild/\nfoo\\#\n")
	require.True(t, CanImportGitignore(root))
	res, err := ImportGitignore(root)
	require.NoError(t, err)
	require.Equal(t, 4, res.Added)
	require.Equal(t, []string{"[Bb]uild/", `foo\#`}, res.Unsupported)
	require.False(t, CanImportGitignore(root))

	data, _ := os.ReadFile(filepath.Join(root, FileName))
	require.Equal(t, "# imported from .gitignore\nnode_modules/\n*.log\n[Bb]uild/\nfoo\\#\n", string(data))

	writeFile(t, root, FileName, "keep/\n")
	res, err = ImportGitignore(root)
	require.NoError(t, err)
	require.Equal(t, 4, res.Added)
	data, _ = os.ReadFile(filepath.Join(root, FileName))
	require.Equal(t, "keep/\n\n# imported from .gitignore\nnode_modules/\n*.log\n[Bb]uild/\nfoo\\#\n", string(data))

	res, err = ImportGitignore(root)
	require.NoError(t, err)
	require.Zero(t, res.Added)
}

func TestAppendRules_Errors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, FileName), 0o755))
	_, err := AppendRules(root, []string{"x"})
	require.Error(t, err)

	ro := t.TempDir()
	require.NoError(t, os.Chmod(ro, 0o500))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	if os.Geteuid() != 0 {
		_, err = AppendRules(ro, []string{"x"})
		require.Error(t, err)
	}
}

func TestImportGitignore_BOM(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, GitignoreFile, "\xef\xbb\xbfnode_modules/\n*.log\n")
	writeFile(t, root, FileName, "\xef\xbb\xbfnode_modules/\n")
	res, err := ImportGitignore(root)
	require.NoError(t, err)
	require.Equal(t, 1, res.Added)

	root = t.TempDir()
	writeFile(t, root, GitignoreFile, "\xef\xbb\xbfnode_modules/\n")
	_, err = ImportGitignore(root)
	require.NoError(t, err)
	data, _ := os.ReadFile(filepath.Join(root, FileName))
	require.Equal(t, "# imported from .gitignore\nnode_modules/\n", string(data))
}
