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

func TestResolveUploadWorkers_Auto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(EnvUploadWorkers, "")

	workers, pinned, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.False(t, pinned)
	require.Zero(t, workers)
}

func TestResolveUploadWorkers_FromConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvUploadWorkers, "")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"uploadWorkers":12}`), 0o644))

	workers, pinned, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 12, workers)
}

func TestResolveUploadWorkers_EnvWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"uploadWorkers":12}`), 0o644))
	t.Setenv(EnvUploadWorkers, " 20 ")

	workers, pinned, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 20, workers)
}

func TestResolveUploadWorkers_InvalidEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(EnvUploadWorkers, "many")

	_, _, err := ResolveUploadWorkers()
	require.ErrorContains(t, err, EnvUploadWorkers)

	t.Setenv(EnvUploadWorkers, "0")
	_, _, err = ResolveUploadWorkers()
	require.ErrorContains(t, err, EnvUploadWorkers)
}

func TestResolveDownloadWorkers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvDownloadWorkers, "")
	t.Setenv(EnvUploadWorkers, "")

	workers, pinned, err := ResolveDownloadWorkers()
	require.NoError(t, err)
	require.False(t, pinned, "nothing set keeps downloads auto-tuned")
	require.Zero(t, workers)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"),
		[]byte(`{"uploadWorkers":12,"downloadWorkers":128}`), 0o644))
	workers, pinned, err = ResolveDownloadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 128, workers, "the config key is read on its own, not uploadWorkers")

	t.Setenv(EnvDownloadWorkers, " 300 ")
	workers, pinned, err = ResolveDownloadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 300, workers, "the environment wins; the client clamps")

	upload, _, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.Equal(t, 12, upload, "the download override leaves uploads alone")
}

func TestResolveDownloadWorkers_Invalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, value := range []string{"many", "0", "-4"} {
		t.Setenv(EnvDownloadWorkers, value)
		_, _, err := ResolveDownloadWorkers()
		require.ErrorContains(t, err, EnvDownloadWorkers)
	}

	t.Setenv(EnvDownloadWorkers, "")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("{"), 0o644))
	_, _, err := ResolveDownloadWorkers()
	require.ErrorContains(t, err, "parse")
}

func TestResolveTracing_DisabledByDefault(t *testing.T) {
	t.Setenv(EnvTracing, "")
	t.Setenv(EnvTracingOTLPEndpoint, "")
	t.Setenv(EnvTracingSampleRatio, "")

	cfg, err := ResolveTracing()
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
}

func TestResolveTracing_Enabled(t *testing.T) {
	t.Setenv(EnvTracing, "1")
	t.Setenv(EnvTracingOTLPEndpoint, " collector:4317 ")
	t.Setenv(EnvTracingSampleRatio, "0.25")

	cfg, err := ResolveTracing()
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.False(t, cfg.Stdout)
	require.Equal(t, "collector:4317", cfg.Endpoint)
	require.InDelta(t, 0.25, cfg.SampleRatio, 0.0001)
}

func TestResolveTracing_Stdout(t *testing.T) {
	t.Setenv(EnvTracing, "stdout")
	t.Setenv(EnvTracingOTLPEndpoint, "")
	t.Setenv(EnvTracingSampleRatio, "")

	cfg, err := ResolveTracing()
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.True(t, cfg.Stdout)
}

func TestResolveTracing_InvalidRatio(t *testing.T) {
	t.Setenv(EnvTracing, "on")
	t.Setenv(EnvTracingSampleRatio, "2")

	_, err := ResolveTracing()
	require.ErrorContains(t, err, EnvTracingSampleRatio)

	t.Setenv(EnvTracingSampleRatio, "half")
	_, err = ResolveTracing()
	require.ErrorContains(t, err, EnvTracingSampleRatio)
}

func TestSetUploadWorkers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvUploadWorkers, "")
	require.NoError(t, SetUploadWorkers(0), "clearing a missing file is fine")
	require.NoError(t, SetUploadWorkers(8))
	workers, pinned, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 8, workers)

	path := filepath.Join(dir, "nipa", "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"diffExternal":"meld","uploadWorkers":4,"future":{"a":1}}`), 0o600))
	require.NoError(t, SetUploadWorkers(0))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "uploadWorkers")
	require.Contains(t, string(data), `"future"`)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "meld", cfg.DiffExternal)

	require.Error(t, SetUploadWorkers(-1))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	require.ErrorContains(t, SetUploadWorkers(2), "parse")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	require.Error(t, SetUploadWorkers(2))
}

func TestSetDownloadWorkers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvDownloadWorkers, "")
	t.Setenv(EnvUploadWorkers, "")
	require.NoError(t, SetDownloadWorkers(0), "clearing a missing file is fine")
	require.NoError(t, SetDownloadWorkers(128))
	workers, pinned, err := ResolveDownloadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 128, workers)
	_, pinned, err = ResolveUploadWorkers()
	require.NoError(t, err)
	require.False(t, pinned, "the download pin leaves uploads automatic")

	path := filepath.Join(dir, "nipa", "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"uploadWorkers":4,"downloadWorkers":32,"future":{"a":1}}`), 0o600))
	require.NoError(t, SetDownloadWorkers(0))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "downloadWorkers")
	require.Contains(t, string(data), `"future"`)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 4, cfg.UploadWorkers)
	require.Zero(t, cfg.DownloadWorkers)

	require.ErrorContains(t, SetDownloadWorkers(-1), "download workers")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	require.ErrorContains(t, SetDownloadWorkers(2), "parse")
}

func TestSetUploadWorkersUnwritableDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa"), nil, 0o600))
	require.Error(t, SetUploadWorkers(2))
}

func TestSetUploadWorkers_NullFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvUploadWorkers, "")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("null"), 0o644))

	require.NoError(t, SetUploadWorkers(8))
	workers, pinned, err := ResolveUploadWorkers()
	require.NoError(t, err)
	require.True(t, pinned)
	require.Equal(t, 8, workers)
}

func writeUserConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte(body), 0o644))
}

func TestResolveUploadWindowMiB_Default(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(EnvUploadWindowMiB, "")

	mib, set, err := ResolveUploadWindowMiB()
	require.NoError(t, err)
	require.False(t, set)
	require.Zero(t, mib)
}

func TestResolveUploadWindowMiB_FromConfig(t *testing.T) {
	writeUserConfig(t, `{"uploadWindowMiB":64}`)
	t.Setenv(EnvUploadWindowMiB, "")

	mib, set, err := ResolveUploadWindowMiB()
	require.NoError(t, err)
	require.True(t, set)
	require.Equal(t, 64, mib)
}

func TestResolveUploadWindowMiB_EnvWins(t *testing.T) {
	writeUserConfig(t, `{"uploadWindowMiB":64}`)
	t.Setenv(EnvUploadWindowMiB, " 128 ")

	mib, set, err := ResolveUploadWindowMiB()
	require.NoError(t, err)
	require.True(t, set)
	require.Equal(t, 128, mib)
}

func TestResolveUploadWindowMiB_Clamps(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		file string
		want int
	}{
		{name: "env below", env: "1", want: MinUploadWindowMiB},
		{name: "env above", env: "4096", want: MaxUploadWindowMiB},
		{name: "env at min", env: "4", want: 4},
		{name: "env at max", env: "256", want: 256},
		{name: "config below", file: `{"uploadWindowMiB":2}`, want: MinUploadWindowMiB},
		{name: "config above", file: `{"uploadWindowMiB":1000}`, want: MaxUploadWindowMiB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := tc.file
			if file == "" {
				file = `{}`
			}
			writeUserConfig(t, file)
			t.Setenv(EnvUploadWindowMiB, tc.env)

			mib, set, err := ResolveUploadWindowMiB()
			require.NoError(t, err)
			require.True(t, set)
			require.Equal(t, tc.want, mib)
		})
	}
}

func TestResolveUploadWindowMiB_InvalidEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(EnvUploadWindowMiB, "big")
	_, _, err := ResolveUploadWindowMiB()
	require.ErrorContains(t, err, EnvUploadWindowMiB)

	t.Setenv(EnvUploadWindowMiB, "-8")
	_, _, err = ResolveUploadWindowMiB()
	require.ErrorContains(t, err, EnvUploadWindowMiB)
}

func TestResolveUploadWindowMiB_BrokenConfig(t *testing.T) {
	writeUserConfig(t, `{not json`)
	t.Setenv(EnvUploadWindowMiB, "")
	_, _, err := ResolveUploadWindowMiB()
	require.Error(t, err)
}
