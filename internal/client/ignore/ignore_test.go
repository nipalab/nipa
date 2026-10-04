package ignore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatcher_Ignores(t *testing.T) {
	tests := []struct {
		name    string
		rules   string
		path    string
		isDir   bool
		ignored bool
	}{
		{name: "basename", rules: "*.log\n", path: "app.log", ignored: true},
		{name: "basename at depth", rules: "*.log\n", path: "var/log/app.log", ignored: true},
		{name: "negation", rules: "*.log\n!keep.log\n", path: "keep.log"},
		{name: "negation last match wins", rules: "!keep.log\n*.log\n", path: "keep.log", ignored: true},
		{name: "anchored root", rules: "/build\n", path: "build", isDir: true, ignored: true},
		{name: "anchored not nested", rules: "/build\n", path: "src/build", isDir: true},
		{name: "unanchored directory anywhere", rules: "build/\n", path: "src/build", isDir: true, ignored: true},
		{name: "directory rule skips files", rules: "build/\n", path: "build"},
		{name: "ignored directory covers children", rules: "build/\n", path: "build/out.o", ignored: true},
		{name: "double star prefix", rules: "**/tmp\n", path: "a/b/tmp", isDir: true, ignored: true},
		{name: "double star middle", rules: "docs/**/tmp\n", path: "docs/a/tmp", isDir: true, ignored: true},
		{name: "double star middle direct", rules: "docs/**/tmp\n", path: "docs/tmp", isDir: true, ignored: true},
		{name: "double star suffix", rules: "build/**\n", path: "build/x/y.o", ignored: true},
		{name: "double star suffix excludes dir itself", rules: "build/**\n", path: "build", isDir: true},
		{name: "question mark", rules: "file?.tmp\n", path: "file1.tmp", ignored: true},
		{name: "question mark matches one rune", rules: "file?.tmp\n", path: "file12.tmp"},
		{name: "comments and blanks", rules: "# comment\n\n*.o\n", path: "x.o", ignored: true},
		{name: "no re-include under ignored directory", rules: "build/\n!build/keep.txt\n", path: "build/keep.txt", ignored: true},
		{name: "no rules", rules: "", path: "anything"},
		{name: "brackets are literal", rules: "[abc].txt\n", path: "[abc].txt", ignored: true},
		{name: "star inside segment", rules: "asset*.png\n", path: "asset1.png", ignored: true},
		{name: "star does not cross slash", rules: "assets/*.png\n", path: "assets/a/b.png"},
		{name: "nested anchored path", rules: "assets/*.png\n", path: "assets/logo.png", ignored: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Matcher{rules: compile(tt.rules)}
			require.Equal(t, tt.ignored, m.Ignores(tt.path, tt.isDir))
		})
	}
}

func TestNew_LoadsBothFilesWithLocalRulesLast(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, FileName), []byte("*.log\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipa", "ignore"), []byte("!keep.log\n"), 0o644))

	m, err := New(root)
	require.NoError(t, err)
	require.False(t, m.Empty())
	require.True(t, m.Ignores("app.log", false))
	require.False(t, m.Ignores("keep.log", false), "local rules are applied after the versioned ones")
}

func TestNew_MissingFiles(t *testing.T) {
	m, err := New(t.TempDir())
	require.NoError(t, err)
	require.True(t, m.Empty())
	require.False(t, m.Ignores("anything", false))
}

func TestNew_ReadError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, FileName), 0o755))

	_, err := New(root)
	require.Error(t, err)
}
