package byostorage

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

type slugResolver interface {
	ResolveOrg(ctx context.Context, orgSlug string) (*domain.Organization, error)
	ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (*domain.Organization, *domain.Project, error)
}

type projectAccess interface {
	HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool
}

// RoutesConfig carries the dependencies of the storage server routes.
type RoutesConfig struct {
	Service *Service
	Slugs   slugResolver
	Access  projectAccess
}

type serverRequest struct {
	Name            string `json:"name"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

type patchRequest struct {
	Name            *string `json:"name,omitempty"`
	Endpoint        *string `json:"endpoint,omitempty"`
	Region          *string `json:"region,omitempty"`
	AccessKeyID     *string `json:"access_key_id,omitempty"`
	SecretAccessKey *string `json:"secret_access_key,omitempty"`
	Bucket          *string `json:"bucket,omitempty"`
	Prefix          *string `json:"prefix,omitempty"`
}

type serverResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Endpoint    string    `json:"endpoint"`
	Region      string    `json:"region"`
	Bucket      string    `json:"bucket"`
	Prefix      string    `json:"prefix"`
	AccessKeyID string    `json:"access_key_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type serverListResponse struct {
	Servers []serverResponse `json:"servers"`
}

type serverSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type orgDefaultResponse struct {
	Server    *serverSummary `json:"server"`
	CanManage bool           `json:"can_manage"`
}

type orgDefaultRequest struct {
	StorageServerID string `json:"storage_server_id"`
}

type projectStorageResponse struct {
	External bool           `json:"external"`
	Server   *serverSummary `json:"server"`
}

type testResponse struct {
	OK    bool   `json:"ok"`
	Check string `json:"check,omitempty"`
	Error string `json:"error,omitempty"`
}

type probeErrorResponse struct {
	Error string `json:"error"`
	Check string `json:"check"`
}

// RegisterRoutes adds the storage server routes to the authenticated /api/v1
// WebService. They are registered only when the feature is on, so clients
// can also feature-detect it by the 404.
func RegisterRoutes(ws *restful.WebService, cfg RoutesConfig) {
	h := &handlers{cfg: cfg}
	id := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.Param(ws.PathParameter("id", "storage server id (base36)"))
	}
	org := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.Param(ws.PathParameter("org", "organization slug"))
	}

	ws.Route(ws.GET("/me/storage-servers").To(h.list).
		Doc("List the caller's storage servers; secrets are never returned").
		Returns(http.StatusOK, "storage servers", serverListResponse{}).
		Operation("listStorageServers"))
	ws.Route(ws.POST("/me/storage-servers").To(h.create).
		Reads(serverRequest{}).
		Doc("Register an S3-compatible storage server; it is probed first").
		Returns(http.StatusCreated, "storage server", serverResponse{}).
		Operation("createStorageServer"))
	ws.Route(id(ws.GET("/me/storage-servers/{id}")).To(h.get).
		Doc("Get one of the caller's storage servers").
		Returns(http.StatusOK, "storage server", serverResponse{}).
		Operation("getStorageServer"))
	ws.Route(id(ws.PATCH("/me/storage-servers/{id}")).To(h.update).
		Reads(patchRequest{}).
		Doc("Change a storage server's name, endpoint, region or keys; bucket and prefix are fixed").
		Returns(http.StatusOK, "storage server", serverResponse{}).
		Operation("updateStorageServer"))
	ws.Route(id(ws.DELETE("/me/storage-servers/{id}")).To(h.delete).
		Doc("Delete a storage server no project or organization uses").
		Operation("deleteStorageServer"))
	ws.Route(id(ws.POST("/me/storage-servers/{id}/test")).To(h.test).
		Doc("Probe a storage server again").
		Returns(http.StatusOK, "probe result", testResponse{}).
		Operation("testStorageServer"))

	ws.Route(org(ws.GET("/orgs/{org}/storage-server")).To(h.orgDefault).
		Doc("The organization's default storage server for new projects (org members)").
		Returns(http.StatusOK, "org default", orgDefaultResponse{}).
		Operation("getOrgStorageServer"))
	ws.Route(org(ws.PUT("/orgs/{org}/storage-server")).To(h.setOrgDefault).
		Reads(orgDefaultRequest{}).
		Doc("Set the default storage server for new projects (org owner who is the billing owner)").
		Returns(http.StatusOK, "org default", orgDefaultResponse{}).
		Operation("setOrgStorageServer"))
	ws.Route(org(ws.DELETE("/orgs/{org}/storage-server")).To(h.clearOrgDefault).
		Doc("Clear the default storage server; existing projects keep theirs (org owner)").
		Operation("clearOrgStorageServer"))
	ws.Route(org(ws.GET("/orgs/{org}/projects/{project}/storage")).
		Param(ws.PathParameter("project", "project slug")).
		To(h.projectStorage).
		Doc("Where the project's content is stored (project read)").
		Returns(http.StatusOK, "project storage", projectStorageResponse{}).
		Operation("getProjectStorage"))
}

type handlers struct {
	cfg RoutesConfig
}

func (h *handlers) list(req *restful.Request, resp *restful.Response) {
	servers, err := h.cfg.Service.List(req.Request.Context())
	if err != nil {
		writeError(resp, err)
		return
	}
	body := serverListResponse{Servers: make([]serverResponse, 0, len(servers))}
	for i := range servers {
		body.Servers = append(body.Servers, toServerResponse(&servers[i]))
	}
	writeJSON(resp, http.StatusOK, body)
}

func (h *handlers) create(req *restful.Request, resp *restful.Response) {
	var body serverRequest
	if err := req.ReadEntity(&body); err != nil {
		writeError(resp, domain.NewErrorUser("invalid request body"))
		return
	}
	server, err := h.cfg.Service.Create(req.Request.Context(), ServerInput(body))
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusCreated, toServerResponse(server))
}

func (h *handlers) get(req *restful.Request, resp *restful.Response) {
	id, ok := serverID(req, resp)
	if !ok {
		return
	}
	server, err := h.cfg.Service.Get(req.Request.Context(), id)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toServerResponse(server))
}

func (h *handlers) update(req *restful.Request, resp *restful.Response) {
	id, ok := serverID(req, resp)
	if !ok {
		return
	}
	var body patchRequest
	if err := req.ReadEntity(&body); err != nil {
		writeError(resp, domain.NewErrorUser("invalid request body"))
		return
	}
	server, err := h.cfg.Service.Update(req.Request.Context(), id, ServerPatch(body))
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toServerResponse(server))
}

func (h *handlers) delete(req *restful.Request, resp *restful.Response) {
	id, ok := serverID(req, resp)
	if !ok {
		return
	}
	if err := h.cfg.Service.Delete(req.Request.Context(), id); err != nil {
		writeError(resp, err)
		return
	}
	resp.WriteHeader(http.StatusNoContent)
}

func (h *handlers) test(req *restful.Request, resp *restful.Response) {
	id, ok := serverID(req, resp)
	if !ok {
		return
	}
	err := h.cfg.Service.Test(req.Request.Context(), id)
	var failure *ProbeFailure
	switch {
	case err == nil:
		writeJSON(resp, http.StatusOK, testResponse{OK: true})
	case errors.As(err, &failure):
		writeJSON(resp, http.StatusOK, testResponse{Check: failure.Check, Error: failure.Error()})
	default:
		writeError(resp, err)
	}
}

func (h *handlers) orgDefault(req *restful.Request, resp *restful.Response) {
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	h.writeOrgDefault(req, resp, org.ID)
}

func (h *handlers) setOrgDefault(req *restful.Request, resp *restful.Response) {
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	var body orgDefaultRequest
	if err := req.ReadEntity(&body); err != nil {
		writeError(resp, domain.NewErrorUser("invalid request body"))
		return
	}
	id, err := snow.ParseBase36(body.StorageServerID)
	if err != nil || id == 0 {
		writeError(resp, domain.NewErrorUser("invalid storage_server_id"))
		return
	}
	if err := h.cfg.Service.SetOrgDefault(req.Request.Context(), org.ID, &id); err != nil {
		writeError(resp, err)
		return
	}
	h.writeOrgDefault(req, resp, org.ID)
}

func (h *handlers) clearOrgDefault(req *restful.Request, resp *restful.Response) {
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	if err := h.cfg.Service.SetOrgDefault(req.Request.Context(), org.ID, nil); err != nil {
		writeError(resp, err)
		return
	}
	resp.WriteHeader(http.StatusNoContent)
}

func (h *handlers) writeOrgDefault(req *restful.Request, resp *restful.Response, orgID snow.ID) {
	def, err := h.cfg.Service.OrgDefault(req.Request.Context(), orgID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, orgDefaultResponse{Server: summary(def.Server), CanManage: def.CanManage})
}

func (h *handlers) projectStorage(req *restful.Request, resp *restful.Response) {
	ctx := req.Request.Context()
	_, project, err := h.cfg.Slugs.ResolveBySlug(ctx, req.PathParameter("org"), req.PathParameter("project"))
	if err != nil {
		writeError(resp, err)
		return
	}
	if !h.cfg.Access.HasProjectAccess(ctx, project.ID, domain.PermissionRead) {
		writeError(resp, domain.NewErrorNotFound("project not found"))
		return
	}
	server, err := h.cfg.Service.ProjectServer(ctx, project.ID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, projectStorageResponse{External: server != nil, Server: summary(server)})
}

func (h *handlers) resolveOrg(req *restful.Request, resp *restful.Response) (*domain.Organization, bool) {
	org, err := h.cfg.Slugs.ResolveOrg(req.Request.Context(), req.PathParameter("org"))
	if err != nil {
		writeError(resp, err)
		return nil, false
	}
	return org, true
}

func serverID(req *restful.Request, resp *restful.Response) (snow.ID, bool) {
	id, err := snow.ParseBase36(req.PathParameter("id"))
	if err != nil {
		writeError(resp, domain.NewErrorNotFound("storage server not found"))
		return 0, false
	}
	return id, true
}

func summary(server *domain.StorageServer) *serverSummary {
	if server == nil {
		return nil
	}
	return &serverSummary{ID: server.ID.Base36(), Name: server.Name}
}

func toServerResponse(server *domain.StorageServer) serverResponse {
	return serverResponse{
		ID:          server.ID.Base36(),
		Name:        server.Name,
		Endpoint:    server.Endpoint,
		Region:      server.Region,
		Bucket:      server.Bucket,
		Prefix:      server.Prefix,
		AccessKeyID: server.AccessKeyID,
		CreatedAt:   server.CreatedAt,
		UpdatedAt:   server.UpdatedAt,
	}
}

func writeJSON(resp *restful.Response, status int, body any) {
	_ = resp.WriteHeaderAndJson(status, body, restful.MIME_JSON)
}

func writeError(resp *restful.Response, err error) {
	var failure *ProbeFailure
	if errors.As(err, &failure) {
		_ = resp.WriteHeaderAndJson(http.StatusBadRequest, probeErrorResponse{Error: failure.Error(), Check: failure.Check}, restful.MIME_JSON)
		return
	}
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		_ = resp.WriteHeaderAndJson(domErr.Code, model.NewAPIError(domErr.Message), restful.MIME_JSON)
		return
	}
	_ = resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError("internal unknown error"), restful.MIME_JSON)
}
