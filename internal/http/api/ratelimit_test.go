package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

func testRateLimit(scope string, burst int) *RateLimit {
	proxies, _ := ratelimit.ParseTrustedProxies("127.0.0.1")
	limiter := ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{scope: {Rate: 0.0001, Burst: burst}})
	return &RateLimit{Limiter: limiter, Proxies: proxies}
}

func serve(h http.Handler, path, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:9999"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func TestRateLimitMiddlewareGlobalPerIP(t *testing.T) {
	h := testRateLimit(ratelimit.ScopeGlobalIP, 2).Middleware(okHandler)

	assert.Equal(t, 200, serve(h, "/x", "198.51.100.1").Code)
	assert.Equal(t, 200, serve(h, "/x", "198.51.100.1").Code)
	rec := serve(h, "/x", "198.51.100.1")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Equal(t, "2", rec.Header().Get("X-RateLimit-Limit"))
	assert.Contains(t, rec.Body.String(), domain.RateLimitMessage)

	assert.Equal(t, 200, serve(h, "/x", "198.51.100.2").Code)
}

func TestRateLimitMiddlewareExemptAndChunkScope(t *testing.T) {
	rl := testRateLimit(ratelimit.ScopeGlobalIP, 1)
	h := rl.Middleware(okHandler)
	for i := 0; i < 5; i++ {
		for _, p := range []string{"/healthz", "/readyz", "/metrics", "/version", "/debug/pprof/heap"} {
			assert.Equal(t, 200, serve(h, p, "").Code, p)
		}
	}
	for i := 0; i < 5; i++ {
		assert.Equal(t, 200, serve(h, "/api/chunks/o/p/h", "").Code)
	}
}

func TestRateLimitMiddlewareSkipsGRPCAndNil(t *testing.T) {
	h := testRateLimit(ratelimit.ScopeGlobalIP, 1).Middleware(okHandler)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/greet.NipaService/X", nil)
		req.ProtoMajor = 2
		req.Header.Set("Content-Type", "application/grpc")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, 200, rec.Code)
	}
	var nilRL *RateLimit
	assert.Equal(t, 200, serve(nilRL.Middleware(okHandler), "/x", "").Code)
}

func filterContainer(filter restful.FilterFunction, pre restful.FilterFunction) http.Handler {
	c := restful.NewContainer()
	ws := new(restful.WebService).Path("/t")
	if pre != nil {
		ws.Filter(pre)
	}
	ws.Filter(filter)
	ws.Route(ws.GET("/ping").To(func(_ *restful.Request, resp *restful.Response) { resp.WriteHeader(http.StatusOK) }))
	ws.Route(ws.OPTIONS("/ping").To(func(_ *restful.Request, resp *restful.Response) { resp.WriteHeader(http.StatusOK) }))
	c.Add(ws)
	return c
}

func TestRateLimitAuthFilter(t *testing.T) {
	h := filterContainer(testRateLimit(ratelimit.ScopeAuthIP, 1).AuthFilter(), nil)
	assert.Equal(t, 200, serve(h, "/t/ping", "198.51.100.1").Code)
	assert.Equal(t, 429, serve(h, "/t/ping", "198.51.100.1").Code)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodOptions, "/t/ping", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, 200, rec.Code)
	}
}

func TestRateLimitUserFilter(t *testing.T) {
	asUser := func(id int64) restful.FilterFunction {
		return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
			ctx := domain.ContextWithClaim(req.Request.Context(), domain.Claims{UserID: snow.ID(id)})
			req.Request = req.Request.WithContext(ctx)
			chain.ProcessFilter(req, resp)
		}
	}
	rl := testRateLimit(ratelimit.ScopeAPIUser, 1)
	u1 := filterContainer(rl.UserFilter(), asUser(1))
	u2 := filterContainer(rl.UserFilter(), asUser(2))

	rec := serve(u1, "/t/ping", "")
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, 429, serve(u1, "/t/ping", "").Code)
	assert.Equal(t, 200, serve(u2, "/t/ping", "").Code)

	anon := filterContainer(rl.UserFilter(), nil)
	assert.Equal(t, 200, serve(anon, "/t/ping", "").Code)
}
