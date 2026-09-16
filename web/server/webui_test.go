package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHandler_ServesIndexAtRoot(t *testing.T) {
	rec := get(t, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nipa") {
		t.Fatalf("GET / body does not look like the app: %q", rec.Body.String())
	}
}

func TestHandler_FallsBackToIndexForClientRoutes(t *testing.T) {
	rec := get(t, "/org/project/branches")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /org/project/branches = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nipa") {
		t.Fatalf("fallback body does not contain the app: %q", rec.Body.String())
	}
}

func TestHandler_ContentTypeIsHTML(t *testing.T) {
	rec := get(t, "/org/project")

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

func TestHandler_AssetPathDoesNotFallBack(t *testing.T) {
	rec := get(t, "/assets/missing.js")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/missing.js = %d, want 404 (no SPA fallback for files)", rec.Code)
	}
}
