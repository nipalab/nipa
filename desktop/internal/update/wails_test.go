package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

type testHost struct{}

func (testHost) Emit(string, ...any) bool                              { return true }
func (testHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (testHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }
func (testHost) Quit()                                                 {}

func writeTarGz(t *testing.T, path, name, content string) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}))
	_, err = tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
}

// serveRelease publishes a signed manifest and its artifact the way a
// release does, so the test exercises the real Wails updater against it.
func serveRelease(t *testing.T, tamper bool) string {
	t.Helper()
	dir := t.TempDir()
	pub, priv := testKey(t)
	old := PublicKey
	PublicKey = encodeKey(pub)
	t.Cleanup(func() { PublicKey = old })

	archive := filepath.Join(dir, "nipa-desktop-test.tar.gz")
	writeTarGz(t, archive, "nipa-desktop", "new build")

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m, err := BuildManifest(ManifestOptions{Version: "v0.2.0", BaseURL: srv.URL + "/download", Key: priv, Now: time.Now()},
		[]Input{{Platform: runtime.GOOS, Arch: runtime.GOARCH, Path: archive}})
	require.NoError(t, err)
	if tamper {
		m.Artifacts[0].Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
	}
	mux.HandleFunc("/update.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(m)
	})
	mux.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir(dir))))
	return srv.URL + "/update.json"
}

func newWailsUpdater(t *testing.T, feed, current string) *updater.Updater {
	t.Helper()
	t.Setenv(EnvFeedURL, feed)
	provider, err := endpoint.New(endpoint.Config{URL: Feed()})
	require.NoError(t, err)
	key, err := Key()
	require.NoError(t, err)
	u := updater.New(testHost{})
	require.NoError(t, u.Init(updater.Config{CurrentVersion: current, Providers: []updater.Provider{provider}, PublicKey: key, Window: updater.WindowNone}))
	return u
}

func TestWailsUpdaterAcceptsSignedManifest(t *testing.T) {
	u := newWailsUpdater(t, serveRelease(t, false), "0.1.0")
	rel, err := u.Check(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rel)
	require.Equal(t, "0.2.0", rel.Version)

	require.NoError(t, u.DownloadAndInstall(context.Background()))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(u.DownloadedPath())) })
	data, err := os.ReadFile(u.DownloadedPath())
	require.NoError(t, err)
	require.Equal(t, "new build", string(data))
}

func TestWailsUpdaterUpToDate(t *testing.T) {
	u := newWailsUpdater(t, serveRelease(t, false), "0.2.0")
	rel, err := u.Check(context.Background())
	require.NoError(t, err)
	require.Nil(t, rel)
}

func TestWailsUpdaterRejectsBadSignature(t *testing.T) {
	u := newWailsUpdater(t, serveRelease(t, true), "0.1.0")
	_, err := u.Check(context.Background())
	require.NoError(t, err)
	require.ErrorContains(t, u.DownloadAndInstall(context.Background()), "signature")
}

func encodeKey(pub []byte) string { return base64.StdEncoding.EncodeToString(pub) }
