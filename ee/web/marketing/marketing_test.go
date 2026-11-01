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

func TestLoadSite(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<html>")},
		"pricing/index.html": &fstest.MapFile{Data: []byte("pricing")},
		"about.html":         &fstest.MapFile{Data: []byte("about")},
		"logo.svg":           &fstest.MapFile{Data: []byte("<svg>")},
	}

	pages, files := loadSite(fsys)
	require.Equal(t, "<html>", string(pages["index.html"]))
	require.Equal(t, "pricing", string(pages["pricing"]))
	require.Equal(t, "pricing", string(pages["pricing/index.html"]))
	require.Equal(t, "about", string(pages["about"]))
	require.Equal(t, "about", string(pages["about.html"]))
	require.True(t, files["logo.svg"])
	require.True(t, files["about.html"])
	require.False(t, files["missing"])
}

func TestLoadSiteDirectoryIndexWins(t *testing.T) {
	fsys := fstest.MapFS{
		"about.html":        &fstest.MapFile{Data: []byte("file")},
		"about/index.html":  &fstest.MapFile{Data: []byte("index")},
		"index.html":        &fstest.MapFile{Data: []byte("home")},
		"nested/index.html": &fstest.MapFile{Data: []byte("nested")},
	}

	pages, _ := loadSite(fsys)
	require.Equal(t, "index", string(pages["about"]))
	require.Equal(t, "file", string(pages["about.html"]))
	require.Equal(t, "nested", string(pages["nested"]))
	require.NotContains(t, pages, "")
}

func TestHandlerDoesNotEscapeSite(t *testing.T) {
	sub := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>home</html>")},
		"about.html": &fstest.MapFile{Data: []byte("<html>about</html>")},
	}
	handler := newHandler(sub, []byte("<html>home</html>"))

	get := func(requestPath string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, requestPath, nil))
		require.Equal(t, http.StatusOK, rec.Code, requestPath)
		require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"), requestPath)
		return rec
	}

	// Traversal is normalized to a path inside the embedded site.
	require.Equal(t, "<html>about</html>", get("/web/../about").Body.String())
	require.Equal(t, "<html>about</html>", get("/../about").Body.String())

	// Escaping the site root falls back to the index page.
	require.Equal(t, "<html>home</html>", get("/web/../../etc/passwd").Body.String())
	require.Equal(t, "<html>home</html>", get("/web/%2e%2e/%2e%2e/etc/passwd").Body.String())
}
