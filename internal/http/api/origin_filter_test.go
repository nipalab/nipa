package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"
)

func TestSameHost(t *testing.T) {
	require.True(t, sameHost("http://example.com:6745", "example.com:6745"))
	require.True(t, sameHost("https://example.com", "example.com"))
	require.False(t, sameHost("http://evil.example", "example.com:6745"))
	require.False(t, sameHost("http://example.com:1234", "example.com:6745"))
	require.False(t, sameHost("://not a url", "example.com"))
}

func TestSameOriginFilter(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		origin     string
		wantStatus int
	}{
		{name: "same origin post", method: http.MethodPost, origin: "http://example.com", wantStatus: http.StatusOK},
		{name: "missing origin post", method: http.MethodPost, wantStatus: http.StatusOK},
		{name: "cross origin post", method: http.MethodPost, origin: "http://evil.example", wantStatus: http.StatusForbidden},
		{name: "malformed origin post", method: http.MethodPost, origin: "://bad", wantStatus: http.StatusForbidden},
		{name: "cross origin get", method: http.MethodGet, origin: "http://evil.example", wantStatus: http.StatusOK},
		{name: "cross origin options", method: http.MethodOptions, origin: "http://evil.example", wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/auth/login", nil)
			req.Host = "example.com"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			chainProcessed := false
			resp := restful.NewResponse(httptest.NewRecorder())
			chain := &restful.FilterChain{
				Filters: []restful.FilterFunction{
					func(_ *restful.Request, _ *restful.Response, _ *restful.FilterChain) {
						chainProcessed = true
					},
				},
			}
			sameOriginFilter(restful.NewRequest(req), resp, chain)

			require.Equal(t, tt.wantStatus, resp.StatusCode())
			require.Equal(t, tt.wantStatus == http.StatusOK, chainProcessed)
		})
	}
}
