package marketing

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestHandlerServesRootAndWebPrefix(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/web/", "/web/pricing", "/web/missing-page"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Contains(t, rec.Body.String(), "Nipa", path)
		require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"), path)
	}
}

func TestHandlerRedirectsBareBasePath(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/web", nil))
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	require.Equal(t, "/web/", rec.Header().Get("Location"))
}

func TestHandlerServesPagesAndAssets(t *testing.T) {
	sub := fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<html>home</html>")},
		"pricing/index.html": &fstest.MapFile{Data: []byte("<html>pricing</html>")},
		"about.html":         &fstest.MapFile{Data: []byte("<html>about</html>")},
		"style.css":          &fstest.MapFile{Data: []byte("body{}")},
	}
	handler := newHandler(sub, []byte("<html>home</html>"))

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for _, path := range []string{"/", "/web/", "/web/missing"} {
		rec := get(path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, "<html>home</html>", rec.Body.String(), path)
	}

	for _, path := range []string{"/web/pricing", "/web/pricing/"} {
		rec := get(path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, "<html>pricing</html>", rec.Body.String(), path)
	}

	rec := get("/web/about")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "<html>about</html>", rec.Body.String())

	rec = get("/web/style.css")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "body{}", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "text/css")

	rec = get("/web")
	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	require.Equal(t, "/web/", rec.Header().Get("Location"))
}

func TestLookup(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<html>")},
		"pricing/index.html": &fstest.MapFile{Data: []byte("pricing")},
		"about.html":         &fstest.MapFile{Data: []byte("about")},
		"logo.svg":           &fstest.MapFile{Data: []byte("<svg>")},
	}

	file, ok := lookup(fsys, "pricing")
	require.True(t, ok)
	require.Equal(t, "pricing/index.html", file)

	file, ok = lookup(fsys, "about")
	require.True(t, ok)
	require.Equal(t, "about.html", file)

	file, ok = lookup(fsys, "logo.svg")
	require.True(t, ok)
	require.Equal(t, "logo.svg", file)

	_, ok = lookup(fsys, "missing")
	require.False(t, ok)
}
