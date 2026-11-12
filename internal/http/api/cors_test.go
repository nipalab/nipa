package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"
)

func newCORSContainer(origins []string) *restful.Container {
	c := restful.NewContainer()
	ws := new(restful.WebService).Path("/api/v1").Filter(sameOriginFilter)
	ws.Route(ws.GET("/thing").To(func(_ *restful.Request, resp *restful.Response) {
		resp.WriteHeader(http.StatusNoContent)
	}))
	ws.Route(ws.POST("/thing").To(func(_ *restful.Request, resp *restful.Response) {
		resp.WriteHeader(http.StatusCreated)
	}))
	c.Add(ws)
	c.Filter(corsFilter(origins))
	return c
}

func serveCORS(c http.Handler, method, origin string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://nipa.example.com/api/v1/thing", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

func TestCORSAllowedOrigin(t *testing.T) {
	c := newCORSContainer([]string{"https://app.example.com"})

	rec := serveCORS(c, http.MethodGet, "https://app.example.com", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "https://app.example.com", rec.Header().Get("Access-Control-Allow-Origin"))

	rec = serveCORS(c, http.MethodOptions, "https://APP.example.com", map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "Authorization, Content-Type",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "https://APP.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "POST,GET,PUT,PATCH,DELETE", rec.Header().Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Authorization, Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
	require.Equal(t, "600", rec.Header().Get("Access-Control-Max-Age"))
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))

	rec = serveCORS(c, http.MethodOptions, "https://app.example.com", map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "X-Evil",
	})
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "unlisted request headers fail the preflight")
}

func TestCORSForeignOrigin(t *testing.T) {
	c := newCORSContainer([]string{"https://app.example.com"})

	rec := serveCORS(c, http.MethodGet, "https://evil.example.com", nil)
	require.Equal(t, http.StatusNoContent, rec.Code, "a foreign GET is served but carries no CORS grant")
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = serveCORS(c, http.MethodOptions, "https://evil.example.com", map[string]string{"Access-Control-Request-Method": "POST"})
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Methods"))

	rec = serveCORS(c, http.MethodPost, "https://evil.example.com", nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSWithoutOrigins(t *testing.T) {
	c := newCORSContainer(nil)

	for _, origin := range []string{"https://app.example.com", "http://localhost:5173"} {
		rec := serveCORS(c, http.MethodGet, origin, nil)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), origin)
		rec = serveCORS(c, http.MethodOptions, origin, map[string]string{"Access-Control-Request-Method": "GET"})
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code, origin)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), origin)
	}
}

func TestCORSSameOriginAndNoOrigin(t *testing.T) {
	c := newCORSContainer(nil)

	rec := serveCORS(c, http.MethodPost, "", nil)
	require.Equal(t, http.StatusCreated, rec.Code, "CLI, curl and service tokens send no Origin")
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = serveCORS(c, http.MethodPost, "http://nipa.example.com", nil)
	require.Equal(t, http.StatusCreated, rec.Code, "the same-origin SPA keeps working without a CORS grant")

	rec = serveCORS(c, http.MethodGet, "http://nipa.example.com", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
}
