package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// isolateConfigDir points the user config lookup at a temp dir on every
// platform (XDG on Linux, HOME on macOS, APPDATA on Windows).
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv(EnvDiffTool, "")
	return dir
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte(content), 0o644))
}

func TestLoad_Missing(t *testing.T) {
	isolateConfigDir(t)
	got, err := Load()
	require.NoError(t, err)
	require.Equal(t, Config{}, got)
}

func TestLoad_Parses(t *testing.T) {
	dir := isolateConfigDir(t)
	writeConfig(t, dir, `{"diffTool": "meld $LOCAL $REMOTE", "unknown": true}`)

	got, err := Load()
	require.NoError(t, err)
	require.Equal(t, "meld $LOCAL $REMOTE", got.DiffTool)
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := isolateConfigDir(t)
	writeConfig(t, dir, `{"diffTool":`)

	_, err := Load()
	require.ErrorContains(t, err, "parse user config")
}

func TestPath_NoHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("APPDATA", "")

	_, err := Path()
	require.ErrorContains(t, err, "locate user config dir")
}

func TestLoad_ReadError(t *testing.T) {
	dir := isolateConfigDir(t)
	// a directory at the config path fails the read with a non-NotExist error
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa", "config.json"), 0o755))

	_, err := Load()
	require.ErrorContains(t, err, "read user config")

	_, err = ResolveDiffTool("")
	require.ErrorContains(t, err, "read user config")
}

func TestResolveDiffTool_Precedence(t *testing.T) {
	dir := isolateConfigDir(t)
	writeConfig(t, dir, `{"diffTool": "from-file $LOCAL"}`)

	got, err := ResolveDiffTool("from-flag $LOCAL")
	require.NoError(t, err)
	require.Equal(t, "from-flag $LOCAL", got, "flag wins")

	t.Setenv(EnvDiffTool, "from-env $LOCAL")
	got, err = ResolveDiffTool("")
	require.NoError(t, err)
	require.Equal(t, "from-env $LOCAL", got, "env wins over file")

	t.Setenv(EnvDiffTool, "")
	got, err = ResolveDiffTool("")
	require.NoError(t, err)
	require.Equal(t, "from-file $LOCAL", got, "file is the fallback")

	require.NoError(t, os.Remove(filepath.Join(dir, "nipa", "config.json")))
	got, err = ResolveDiffTool("")
	require.NoError(t, err)
	require.Empty(t, got, "nothing configured yields empty")
}
