package byostorage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/snow"
)

type stubSlugs struct{}

func (stubSlugs) ResolveOrg(_ context.Context, slug string) (*domain.Organization, error) {
	if slug != "acme" {
		return nil, domain.NewErrorNotFound("organization not found")
	}
	return &domain.Organization{ID: orgID, Slug: slug}, nil
}

func (s stubSlugs) ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (*domain.Organization, *domain.Project, error) {
	org, err := s.ResolveOrg(ctx, orgSlug)
	if err != nil {
		return nil, nil, err
	}
	if projectSlug != "game" && projectSlug != "plain" {
		return nil, nil, domain.NewErrorNotFound("project not found")
	}
	id := project1
	if projectSlug == "plain" {
		id = snow.ID(31)
	}
	return org, &domain.Project{ID: id, OrgID: org.ID, Slug: projectSlug}, nil
}

type stubAccess struct{ read bool }

func (a stubAccess) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool { return a.read }

type apiServer struct {
	t      *testing.T
	f      *fixture
	server *httptest.Server
}

func newAPIServer(t *testing.T, access stubAccess) *apiServer {
	t.Helper()
	f := newFixture(t)
	ws := new(restful.WebService).Path("/api/v1").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	ws.Filter(func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		if user := req.HeaderParameter("X-User"); user != "" {
			id, _ := snow.ParseBase36(user)
			req.Request = req.Request.WithContext(domain.ContextWithClaim(req.Request.Context(), domain.Claims{UserID: id}))
		}
		chain.ProcessFilter(req, resp)
	})
	RegisterRoutes(ws, RoutesConfig{Service: f.svc, Slugs: stubSlugs{}, Access: access})
	container := restful.NewContainer()
	container.Add(ws)
	server := httptest.NewServer(container)
	t.Cleanup(server.Close)
	return &apiServer{t: t, f: f, server: server}
}

func (s *apiServer) do(method, path string, user snow.ID, body any) (int, map[string]any) {
	s.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(s.t, err)
	}
	req, err := http.NewRequest(method, s.server.URL+path, bytes.NewReader(raw))
	require.NoError(s.t, err)
	req.Header.Set("Content-Type", "application/json")
	if user != 0 {
		req.Header.Set("X-User", user.Base36())
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestAPI_ServerCRUD(t *testing.T) {
	s := newAPIServer(t, stubAccess{read: true})
	in := validInput()

	status, body := s.do(http.MethodPost, "/api/v1/me/storage-servers", ownerID, serverRequest(in))
	require.Equal(t, http.StatusCreated, status, body)
	id := body["id"].(string)
	require.Equal(t, "nipa", body["prefix"])
	require.Equal(t, "AKID", body["access_key_id"])
	for key := range body {
		require.NotContains(t, key, "secret")
	}

	status, body = s.do(http.MethodGet, "/api/v1/me/storage-servers", ownerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, body["servers"], 1)

	status, body = s.do(http.MethodGet, "/api/v1/me/storage-servers/"+id, ownerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "R2", body["name"])

	status, _ = s.do(http.MethodGet, "/api/v1/me/storage-servers/"+id, otherID, nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = s.do(http.MethodGet, "/api/v1/me/storage-servers/!!", ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)

	status, body = s.do(http.MethodPatch, "/api/v1/me/storage-servers/"+id, ownerID, map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "Renamed", body["name"])

	status, body = s.do(http.MethodPatch, "/api/v1/me/storage-servers/"+id, ownerID, map[string]any{"bucket": "other"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body["error"], "cannot be changed")

	status, body = s.do(http.MethodPost, "/api/v1/me/storage-servers/"+id+"/test", ownerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, body["ok"])

	s.f.opener.probeErr = &s3store.ProbeError{Check: s3store.ProbeCreateOnly, Err: errBoom}
	status, body = s.do(http.MethodPost, "/api/v1/me/storage-servers/"+id+"/test", ownerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, false, body["ok"])
	require.Equal(t, s3store.ProbeCreateOnly, body["check"])

	in.Name = "second"
	status, body = s.do(http.MethodPost, "/api/v1/me/storage-servers", ownerID, serverRequest(in))
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, s3store.ProbeCreateOnly, body["check"])
	s.f.opener.probeErr = nil

	status, _ = s.do(http.MethodPost, "/api/v1/me/storage-servers/"+id+"/test", otherID, nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = s.do(http.MethodDelete, "/api/v1/me/storage-servers/"+id, ownerID, nil)
	require.Equal(t, http.StatusNoContent, status)
	status, _ = s.do(http.MethodDelete, "/api/v1/me/storage-servers/"+id, ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = s.do(http.MethodGet, "/api/v1/me/storage-servers", 0, nil)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestAPI_InvalidBodies(t *testing.T) {
	s := newAPIServer(t, stubAccess{read: true})
	server := s.f.create(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/me/storage-servers"},
		{http.MethodPatch, "/api/v1/me/storage-servers/" + server.ID.Base36()},
		{http.MethodPut, "/api/v1/orgs/acme/storage-server"},
	} {
		req, err := http.NewRequest(tc.method, s.server.URL+tc.path, strings.NewReader("{"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", ownerID.Base36())
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, tc.path)
	}
	status, _ := s.do(http.MethodPost, "/api/v1/me/storage-servers/"+server.ID.Base36()+"/test", 0, nil)
	require.Equal(t, http.StatusUnauthorized, status)
	status, _ = s.do(http.MethodPost, "/api/v1/me/storage-servers/!!/test", ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = s.do(http.MethodPatch, "/api/v1/me/storage-servers/!!", ownerID, map[string]any{})
	require.Equal(t, http.StatusNotFound, status)
	status, _ = s.do(http.MethodDelete, "/api/v1/me/storage-servers/!!", ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAPI_OrgDefaultAndProjectStorage(t *testing.T) {
	s := newAPIServer(t, stubAccess{read: true})
	server := s.f.create(t)

	status, body := s.do(http.MethodGet, "/api/v1/orgs/acme/storage-server", ownerID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Nil(t, body["server"])
	require.Equal(t, true, body["can_manage"])

	status, body = s.do(http.MethodPut, "/api/v1/orgs/acme/storage-server", ownerID, map[string]any{"storage_server_id": server.ID.Base36()})
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, server.ID.Base36(), body["server"].(map[string]any)["id"])

	status, _ = s.do(http.MethodPut, "/api/v1/orgs/acme/storage-server", ownerID, map[string]any{"storage_server_id": ""})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = s.do(http.MethodPut, "/api/v1/orgs/acme/storage-server", otherID, map[string]any{"storage_server_id": server.ID.Base36()})
	require.Equal(t, http.StatusNotFound, status)
	status, _ = s.do(http.MethodGet, "/api/v1/orgs/nope/storage-server", ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)

	require.NoError(t, s.f.svc.Assign(as(ownerID), orgID, project1, nil))
	status, body = s.do(http.MethodGet, "/api/v1/orgs/acme/projects/game/storage", otherID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, body["external"])
	require.Equal(t, "R2", body["server"].(map[string]any)["name"])
	require.NotContains(t, body["server"], "endpoint")

	status, body = s.do(http.MethodGet, "/api/v1/orgs/acme/projects/plain/storage", otherID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, false, body["external"])
	require.Nil(t, body["server"])

	status, _ = s.do(http.MethodGet, "/api/v1/orgs/acme/projects/missing/storage", otherID, nil)
	require.Equal(t, http.StatusNotFound, status)
	s.f.repo.fail["ProjectServer"] = errBoom
	status, _ = s.do(http.MethodGet, "/api/v1/orgs/acme/projects/game/storage", otherID, nil)
	require.Equal(t, http.StatusInternalServerError, status)
	s.f.repo.fail = map[string]error{}

	status, _ = s.do(http.MethodDelete, "/api/v1/orgs/acme/storage-server", ownerID, nil)
	require.Equal(t, http.StatusNoContent, status)
	status, _ = s.do(http.MethodDelete, "/api/v1/orgs/nope/storage-server", ownerID, nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = s.do(http.MethodPut, "/api/v1/orgs/nope/storage-server", ownerID, map[string]any{})
	require.Equal(t, http.StatusNotFound, status)

	s.f.svc.WithOrgs(stubOrgs{})
	status, _ = s.do(http.MethodDelete, "/api/v1/orgs/acme/storage-server", ownerID, nil)
	require.Equal(t, http.StatusForbidden, status)
	status, _ = s.do(http.MethodGet, "/api/v1/orgs/acme/storage-server", ownerID, nil)
	require.Equal(t, http.StatusForbidden, status)
}

func TestAPI_ProjectStorageNeedsRead(t *testing.T) {
	s := newAPIServer(t, stubAccess{read: false})
	status, _ := s.do(http.MethodGet, "/api/v1/orgs/acme/projects/game/storage", otherID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAPI_MaintenanceRouteClasses(t *testing.T) {
	ws := new(restful.WebService).Path("/api/v1")
	RegisterRoutes(ws, RoutesConfig{})
	classes := map[string]maintenance.Class{}
	for _, r := range ws.Routes() {
		classes[r.Method+" "+r.Path] = api.RouteClass(r.Method, r.Path)
	}
	require.Equal(t, map[string]maintenance.Class{
		"GET /api/v1/me/storage-servers":                    maintenance.ClassRead,
		"POST /api/v1/me/storage-servers":                   maintenance.ClassWrite,
		"GET /api/v1/me/storage-servers/{id}":               maintenance.ClassRead,
		"PATCH /api/v1/me/storage-servers/{id}":             maintenance.ClassWrite,
		"DELETE /api/v1/me/storage-servers/{id}":            maintenance.ClassWrite,
		"POST /api/v1/me/storage-servers/{id}/test":         maintenance.ClassWrite,
		"GET /api/v1/orgs/{org}/storage-server":             maintenance.ClassRead,
		"PUT /api/v1/orgs/{org}/storage-server":             maintenance.ClassWrite,
		"DELETE /api/v1/orgs/{org}/storage-server":          maintenance.ClassWrite,
		"GET /api/v1/orgs/{org}/projects/{project}/storage": maintenance.ClassRead,
	}, classes)
}

func TestWriteErrorUnknown(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(restful.NewResponse(rec), errBoom)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
