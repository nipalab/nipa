package api

import (
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/obs"
)

func metricsFilter(metrics *obs.Metrics) restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		start := time.Now()
		chain.ProcessFilter(req, resp)
		route := req.SelectedRoutePath()
		if route == "" {
			route = "unmatched"
		}
		metrics.ObserveHTTP(route, req.Request.Method, resp.StatusCode(), time.Since(start))
	}
}
