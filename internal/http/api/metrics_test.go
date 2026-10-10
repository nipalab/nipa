package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/obs"
)

func TestMetricsFilter(t *testing.T) {
	m := obs.NewMetrics()
	req := restful.NewRequest(httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil))
	resp := restful.NewResponse(httptest.NewRecorder())
	chain := &restful.FilterChain{
		Filters: []restful.FilterFunction{metricsFilter(m)},
		Target: func(_ *restful.Request, response *restful.Response) {
			response.WriteHeader(http.StatusCreated)
		},
	}
	chain.ProcessFilter(req, resp)

	scrape := httptest.NewRecorder()
	m.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	require.Contains(t, body, `nipa_http_requests_total{code="201",method="GET",route="unmatched"} 1`)
	require.Contains(t, body, "nipa_http_request_duration_seconds_count")
}

func TestMetricsFilter_SkipsWhenDisabled(t *testing.T) {
	api := NewAPI(nil)
	require.Nil(t, api.metrics)
}
