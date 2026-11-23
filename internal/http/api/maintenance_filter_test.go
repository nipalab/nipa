package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

var filterNow = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

func filterService(t *testing.T, windows ...maintenance.Window) *maintenance.Service {
	t.Helper()
	svc := maintenance.NewService().WithClock(func() time.Time { return filterNow })
	require.NoError(t, svc.Apply(context.Background(), maintenance.NewSnapshot(windows, filterNow)))
	return svc
}

func activeWindow(mode maintenance.Mode, org, project string) maintenance.Window {
	w := maintenance.Window{ID: 1, Mode: mode, Message: "upgrade", StartsAt: filterNow.Add(-time.Minute)}
	switch {
	case project != "":
		id := snow.ID(10)
		w.ID, w.ProjectID, w.Org, w.Project = 2, &id, org, project
	case org != "":
		id := snow.ID(20)
		w.ID, w.OrgID, w.Org = 3, &id, org
	}
	return w
}

func maintContainer(svc *maintenance.Service, metrics *obs.Metrics) http.Handler {
	ok := func(_ *restful.Request, resp *restful.Response) { resp.WriteHeader(http.StatusNoContent) }
	a := &API{maintenance: svc, metrics: metrics}
	container := restful.NewContainer()

	v1 := new(restful.WebService).Path("/api/v1")
	a.enforceMaintenance(v1)
	v1.Route(v1.GET("/me").To(ok))
	v1.Route(v1.GET("/orgs").To(ok))
	v1.Route(v1.GET("/orgs/{org}/members").To(ok))
	v1.Route(v1.POST("/orgs/{org}/members").To(ok))
	v1.Route(v1.GET("/orgs/{org}/projects/{project}/tree").To(ok))
	v1.Route(v1.POST("/orgs/{org}/projects/{project}/locks").To(ok))
	v1.Route(v1.PUT("/orgs/{org}/projects/{project}/maintenance").To(ok))
	v1.Route(v1.DELETE("/admin/maintenance").To(ok))
	container.Add(v1)

	authWs := new(restful.WebService).Path("/api/v1/auth")
	a.enforceMaintenance(authWs)
	authWs.Route(authWs.POST("/login").To(ok))
	authWs.Route(authWs.POST("/register").To(ok))
	container.Add(authWs)

	chunks := new(restful.WebService).Path("/api/chunks")
	a.enforceMaintenance(chunks)
	chunks.Route(chunks.GET("/{org}/{project}/{hash}").To(ok))
	container.Add(chunks)

	bundles := new(restful.WebService).Path("/api/chunk-bundles")
	a.enforceMaintenance(bundles)
	bundles.Route(bundles.PUT("/{org}/{project}").To(ok))
	container.Add(bundles)

	hooks := new(restful.WebService).Path("/api/webhooks/paddle")
	a.enforceMaintenance(hooks)
	hooks.Route(hooks.POST("").To(ok))
	container.Add(hooks)
	return container
}

func serveFilter(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRouteClass(t *testing.T) {
	cases := map[string]maintenance.Class{
		"GET /api/maintenance":                                        maintenance.ClassPublic,
		"GET /api/v1/auth/capabilities":                               maintenance.ClassPublic,
		"POST /api/v1/auth/login":                                     maintenance.ClassAuth,
		"POST /api/v1/auth/refresh":                                   maintenance.ClassAuth,
		"GET /api/v1/auth/oauth/{provider}/callback":                  maintenance.ClassAuth,
		"POST /api/v1/auth/register":                                  maintenance.ClassWrite,
		"GET /api/v1/me":                                              maintenance.ClassControl,
		"PATCH /api/v1/me":                                            maintenance.ClassWrite,
		"GET /api/v1/orgs/{org}/projects/{project}/permissions/me":    maintenance.ClassControl,
		"DELETE /api/v1/orgs/{org}/maintenance":                       maintenance.ClassControl,
		"GET /api/v1/orgs/{org}/projects/{project}/tree":              maintenance.ClassRead,
		"HEAD /api/v1/orgs":                                           maintenance.ClassRead,
		"OPTIONS /api/v1/orgs":                                        maintenance.ClassRead,
		"POST /api/webhooks/paddle":                                   maintenance.ClassWrite,
		"PUT /api/chunk-bundles/{org}/{project}":                      maintenance.ClassWrite,
		"GET /api/chunks/{org}/{project}/{hash}":                      maintenance.ClassRead,
		"DELETE /api/v1/orgs/{org}/projects/{project}/permissions/me": maintenance.ClassWrite,
	}
	for key, want := range cases {
		var method, path string
		for i := range key {
			if key[i] == ' ' {
				method, path = key[:i], key[i+1:]
				break
			}
		}
		require.Equal(t, want, RouteClass(method, path), key)
	}
}

func TestMaintenanceFilterRules(t *testing.T) {
	type call struct {
		method, path string
		code         int
	}
	const refused, allowed = http.StatusServiceUnavailable, http.StatusNoContent
	cases := []struct {
		name   string
		window *maintenance.Window
		calls  []call
	}{
		{name: "off", calls: []call{
			{http.MethodPost, "/api/webhooks/paddle", allowed},
			{http.MethodPut, "/api/chunk-bundles/acme/game", allowed},
		}},
		{name: "server read_only", window: ptr(activeWindow(maintenance.ReadOnly, "", "")), calls: []call{
			{http.MethodGet, "/api/v1/orgs/acme/projects/game/tree", allowed},
			{http.MethodGet, "/api/chunks/acme/game/ab", allowed},
			{http.MethodPut, "/api/chunk-bundles/acme/game", refused},
			{http.MethodPost, "/api/v1/orgs/acme/projects/game/locks", refused},
			{http.MethodPost, "/api/v1/auth/register", refused},
			{http.MethodPost, "/api/webhooks/paddle", refused},
			{http.MethodPost, "/api/v1/auth/login", allowed},
			{http.MethodDelete, "/api/v1/admin/maintenance", allowed},
		}},
		{name: "server full", window: ptr(activeWindow(maintenance.Full, "", "")), calls: []call{
			{http.MethodGet, "/api/v1/orgs", refused},
			{http.MethodGet, "/api/chunks/acme/game/ab", refused},
			{http.MethodPost, "/api/webhooks/paddle", refused},
			{http.MethodGet, "/api/v1/me", allowed},
			{http.MethodPost, "/api/v1/auth/login", allowed},
			{http.MethodPut, "/api/v1/orgs/acme/projects/game/maintenance", allowed},
		}},
		{name: "project read_only", window: ptr(activeWindow(maintenance.ReadOnly, "acme", "game")), calls: []call{
			{http.MethodPost, "/api/v1/orgs/acme/projects/game/locks", refused},
			{http.MethodPut, "/api/chunk-bundles/acme/game", refused},
			{http.MethodPost, "/api/v1/orgs/acme/projects/other/locks", allowed},
			{http.MethodPost, "/api/v1/orgs/acme/members", allowed},
			{http.MethodPost, "/api/webhooks/paddle", allowed},
		}},
		{name: "org full", window: ptr(activeWindow(maintenance.Full, "acme", "")), calls: []call{
			{http.MethodGet, "/api/v1/orgs/acme/members", refused},
			{http.MethodGet, "/api/v1/orgs/acme/projects/game/tree", refused},
			{http.MethodGet, "/api/v1/orgs/beta/members", allowed},
			{http.MethodGet, "/api/v1/orgs", allowed},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var windows []maintenance.Window
			if tc.window != nil {
				windows = append(windows, *tc.window)
			}
			h := maintContainer(filterService(t, windows...), nil)
			for _, c := range tc.calls {
				require.Equal(t, c.code, serveFilter(h, c.method, c.path).Code, c.method+" "+c.path)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestMaintenanceFilterResponse(t *testing.T) {
	end := filterNow.Add(90 * time.Second)
	w := activeWindow(maintenance.ReadOnly, "acme", "game")
	w.EndsAt = &end
	metrics := obs.NewMetrics()
	h := maintContainer(filterService(t, w), metrics)

	rec := serveFilter(h, http.MethodPost, "/api/v1/orgs/acme/projects/game/locks")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "90", rec.Header().Get("Retry-After"))
	require.Equal(t, "project;read_only", rec.Header().Get("X-Nipa-Maintenance"))
	var body struct {
		Error       string           `json:"error"`
		Maintenance maintenance.Info `json:"maintenance"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "project acme/game is in maintenance (read-only): upgrade", body.Error)
	require.Equal(t, maintenance.ScopeProject, body.Maintenance.Scope)
	require.Equal(t, "acme/game", body.Maintenance.Project)
	require.Equal(t, "acme", body.Maintenance.Org)
	require.True(t, end.Equal(*body.Maintenance.EndsAt))

	fileSvc := maintenance.NewService().WithClock(func() time.Time { return filterNow })
	snap := maintenance.NewSnapshot(nil, filterNow)
	snap.Server, snap.Source = &maintenance.Window{Mode: maintenance.Full}, maintenance.SourceFile
	require.NoError(t, fileSvc.Apply(context.Background(), snap))
	file := maintContainer(fileSvc, nil)
	rec = serveFilter(file, http.MethodGet, "/api/v1/orgs")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Empty(t, rec.Header().Get("Retry-After"))
	require.Equal(t, "server;full", rec.Header().Get("X-Nipa-Maintenance"))
	require.NotContains(t, rec.Body.String(), "starts_at")

	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	raw, _ := io.ReadAll(scrape.Body)
	require.Contains(t, string(raw), `nipa_maintenance_rejected_total{class="write",scope="project",transport="http"} 1`)
}

func TestEnforceMaintenanceWithoutService(t *testing.T) {
	h := maintContainer(nil, nil)
	require.Equal(t, http.StatusNoContent, serveFilter(h, http.MethodPost, "/api/webhooks/paddle").Code)
}
