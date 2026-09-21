package api

import (
	"net/http"
	"net/url"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/http/model"
)

func sameOriginFilter(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	switch req.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if origin := req.HeaderParameter("Origin"); origin != "" && !sameHost(origin, req.Request.Host) {
			resp.WriteHeaderAndJson(http.StatusForbidden, model.NewAPIError("cross-origin request rejected"), restful.MIME_JSON)
			return
		}
	}
	chain.ProcessFilter(req, resp)
}

func sameHost(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host == host
}
