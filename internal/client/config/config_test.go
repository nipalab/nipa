package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	require.NoError(t, err)
	require.Empty(t, cfg.DiffExternal)
}

func TestLoad_ParsesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"diffExternal":"meld"}`), 0o644))

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "meld", cfg.DiffExternal)
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("{"), 0o644))

	_, err := Load()
	require.ErrorContains(t, err, "parse")
}

func TestResolveDiffExternal_EnvWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"diffExternal":"meld"}`), 0o644))
	t.Setenv(EnvDiffExternal, "code --diff")

	got, err := ResolveDiffExternal()
	require.NoError(t, err)
	require.Equal(t, "code --diff", got)
}

func TestResolveDiffExternal_FromConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"diffExternal":"  meld  "}`), 0o644))
	t.Setenv(EnvDiffExternal, "")

	got, err := ResolveDiffExternal()
	require.NoError(t, err)
	require.Equal(t, "meld", got)
}
