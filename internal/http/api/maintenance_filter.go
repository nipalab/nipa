package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
)

// routeClasses lists the routes that stay open in maintenance (public, auth
// and control). Every other route is a read for GET, HEAD and OPTIONS and a
// write otherwise. Keys are "METHOD /full/route/path".
var routeClasses = map[string]maintenance.Class{
	"GET " + maintenancePath:                     maintenance.ClassPublic,
	"GET /api/v1/auth/capabilities":              maintenance.ClassPublic,
	"POST /api/v1/auth/login":                    maintenance.ClassAuth,
	"POST /api/v1/auth/mfa/verify":               maintenance.ClassAuth,
	"POST /api/v1/auth/refresh":                  maintenance.ClassAuth,
	"POST /api/v1/auth/logout":                   maintenance.ClassAuth,
	"GET /api/v1/auth/oauth/{provider}/start":    maintenance.ClassAuth,
	"GET /api/v1/auth/oauth/{provider}/callback": maintenance.ClassAuth,
	"POST /api/v1/auth/oauth/complete":           maintenance.ClassAuth,

	"GET /api/v1/admin/maintenance":                            maintenance.ClassControl,
	"PUT /api/v1/admin/maintenance":                            maintenance.ClassControl,
	"DELETE /api/v1/admin/maintenance":                         maintenance.ClassControl,
	"GET /api/v1/orgs/{org}/maintenance":                       maintenance.ClassControl,
	"PUT /api/v1/orgs/{org}/maintenance":                       maintenance.ClassControl,
	"DELETE /api/v1/orgs/{org}/maintenance":                    maintenance.ClassControl,
	"GET /api/v1/orgs/{org}/projects/{project}/maintenance":    maintenance.ClassControl,
	"PUT /api/v1/orgs/{org}/projects/{project}/maintenance":    maintenance.ClassControl,
	"DELETE /api/v1/orgs/{org}/projects/{project}/maintenance": maintenance.ClassControl,
	"GET /api/v1/me": maintenance.ClassControl,
	"GET /api/v1/orgs/{org}/projects/{project}/permissions/me": maintenance.ClassControl,
}

// RouteClass classifies a route for maintenance by method and full route path.
func RouteClass(method, path string) maintenance.Class {
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	if c, ok := routeClasses[method+" "+path]; ok {
		return c
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return maintenance.ClassRead
	default:
		return maintenance.ClassWrite
	}
}

type maintenanceErrorResponse struct {
	Error       string           `json:"error"`
	Maintenance maintenance.Info `json:"maintenance"`
}

// maintenanceFilter answers 503 for the requests the effective window of the
// route's {org}/{project} does not allow. It must run once the route is
// selected (a WebService filter).
func maintenanceFilter(svc *maintenance.Service, metrics *obs.Metrics) restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		method := req.Request.Method
		class := RouteClass(method, req.SelectedRoutePath())
		refused := svc.Check(class, method, req.PathParameter("org"), req.PathParameter("project"))
		if refused == nil {
			chain.ProcessFilter(req, resp)
			return
		}
		metrics.ObserveMaintenanceRejected("http", string(class), string(refused.Info.Scope))
		writeMaintenance(resp, refused, svc)
	}
}

func writeMaintenance(resp *restful.Response, refused *maintenance.Error, svc *maintenance.Service) {
	h := resp.Header()
	if d := refused.Info.RetryAfter(svc.Now()); d > 0 {
		h.Set("Retry-After", strconv.Itoa(retryAfterSeconds(d)))
	}
	h.Set("X-Nipa-Maintenance", string(refused.Info.Scope)+";"+string(refused.Info.Mode))
	_ = resp.WriteHeaderAndJson(http.StatusServiceUnavailable, maintenanceErrorResponse{
		Error:       refused.Error(),
		Maintenance: refused.Info,
	}, restful.MIME_JSON)
}
