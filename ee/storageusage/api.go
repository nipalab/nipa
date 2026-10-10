package storageusage

import (
	"errors"
	"net/http"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// RoutesConfig carries the dependencies of the storage quota REST routes.
type RoutesConfig struct {
	Ledger *Ledger
	Common *usecase.Common
	Orgs   *usecase.Org
}

// RegisterRoutes adds the storage quota routes to the authenticated /api/v1
// WebService. With the ledger disabled the routes are not registered at all,
// so clients can feature-detect the capability by the 404.
func RegisterRoutes(ws *restful.WebService, cfg RoutesConfig) {
	h := &handlers{cfg: cfg}

	org := func(b *restful.RouteBuilder) *restful.RouteBuilder {
		return b.Param(ws.PathParameter("org", "organization slug"))
	}

	ws.Route(org(
		ws.GET("/orgs/{org}/storage").
			To(h.orgStorage).
			Doc("Organization storage usage, quota and per-project breakdown (org owner)").
			Returns(http.StatusOK, "storage summary", orgStorageResponse{}).
			Operation("getOrgStorage")))

	ws.Route(
		ws.GET("/billing/me").
			To(h.ownerStorage).
			Doc("Billing owner quota, aggregate usage and per-org breakdown").
			Returns(http.StatusOK, "billing summary", ownerStorageResponse{}).
			Operation("getMyBilling"))

	ws.Route(
		ws.PUT("/admin/billing/owners/{user}/quota").
			To(h.setOwnerQuota).
			Param(ws.PathParameter("user", "user id (base36)")).
			Doc("Set or clear an owner's storage quota (super admin)").
			Returns(http.StatusOK, "billing summary", ownerStorageResponse{}).
			Operation("setOwnerQuota"))

	ws.Route(org(
		ws.PUT("/admin/orgs/{org}/billing-owner").
			To(h.setBillingOwner).
			Doc("Transfer an organization to another billing owner (super admin)").
			Returns(http.StatusOK, "storage summary", orgStorageResponse{}).
			Operation("setOrgBillingOwner")))

	ws.Route(org(
		ws.POST("/admin/orgs/{org}/storage/recount").
			To(h.recountOrgStorage).
			Doc("Rebuild the storage ledger and counters of one organization (super admin)").
			Returns(http.StatusOK, "storage summary", orgStorageResponse{}).
			Operation("recountOrgStorage")))
}

type handlers struct {
	cfg RoutesConfig
}

type projectUsageResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
	UsedBytes int64  `json:"used_bytes"`
}

type orgStorageResponse struct {
	UsedBytes  int64                  `json:"used_bytes"`
	QuotaBytes *int64                 `json:"quota_bytes"`
	OwnerID    string                 `json:"owner_id"`
	Projects   []projectUsageResponse `json:"projects"`
}

type ownerOrgResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	UsedBytes int64  `json:"used_bytes"`
}

type ownerStorageResponse struct {
	UserID     string             `json:"user_id"`
	UsedBytes  int64              `json:"used_bytes"`
	QuotaBytes *int64             `json:"quota_bytes"`
	Orgs       []ownerOrgResponse `json:"orgs"`
}

func (h *handlers) orgStorage(req *restful.Request, resp *restful.Response) {
	ctx := req.Request.Context()
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	owner, err := h.cfg.Orgs.IsOrgOwner(ctx, org.ID)
	if err != nil {
		writeError(resp, err)
		return
	}
	if !owner {
		writeError(resp, domain.NewErrorNoPermission())
		return
	}
	summary, err := h.cfg.Ledger.OrgStorage(ctx, org.ID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toOrgStorageResponse(summary))
}

func (h *handlers) ownerStorage(req *restful.Request, resp *restful.Response) {
	claim, ok := domain.ClaimFromContext(req.Request.Context())
	if !ok {
		writeError(resp, domain.NewErrorUnauthorized("authentication required"))
		return
	}
	summary, err := h.cfg.Ledger.OwnerStorage(req.Request.Context(), claim.UserID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toOwnerStorageResponse(summary))
}

func (h *handlers) setOwnerQuota(req *restful.Request, resp *restful.Response) {
	ctx := req.Request.Context()
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		writeError(resp, domain.NewErrorNoPermission())
		return
	}
	userID, err := snow.ParseBase36(req.PathParameter("user"))
	if err != nil {
		writeError(resp, domain.NewErrorUser("invalid user id"))
		return
	}
	var body struct {
		QuotaBytes *int64 `json:"quota_bytes"`
	}
	if err := req.ReadEntity(&body); err != nil {
		writeError(resp, domain.NewErrorUser("invalid request body"))
		return
	}
	if err := h.cfg.Ledger.SetOwnerQuota(ctx, userID, body.QuotaBytes); err != nil {
		writeError(resp, err)
		return
	}
	summary, err := h.cfg.Ledger.OwnerStorage(ctx, userID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toOwnerStorageResponse(summary))
}

func (h *handlers) setBillingOwner(req *restful.Request, resp *restful.Response) {
	ctx := req.Request.Context()
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		writeError(resp, domain.NewErrorNoPermission())
		return
	}
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := req.ReadEntity(&body); err != nil {
		writeError(resp, domain.NewErrorUser("invalid request body"))
		return
	}
	userID, err := snow.ParseBase36(body.UserID)
	if err != nil {
		writeError(resp, domain.NewErrorUser("invalid user id"))
		return
	}
	if err := h.cfg.Ledger.SetBillingOwner(ctx, org.ID, userID); err != nil {
		writeError(resp, err)
		return
	}
	summary, err := h.cfg.Ledger.OrgStorage(ctx, org.ID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toOrgStorageResponse(summary))
}

func (h *handlers) recountOrgStorage(req *restful.Request, resp *restful.Response) {
	ctx := req.Request.Context()
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		writeError(resp, domain.NewErrorNoPermission())
		return
	}
	org, ok := h.resolveOrg(req, resp)
	if !ok {
		return
	}
	if err := h.cfg.Ledger.RecountOrg(ctx, org.ID); err != nil {
		writeError(resp, err)
		return
	}
	summary, err := h.cfg.Ledger.OrgStorage(ctx, org.ID)
	if err != nil {
		writeError(resp, err)
		return
	}
	writeJSON(resp, http.StatusOK, toOrgStorageResponse(summary))
}

func (h *handlers) resolveOrg(req *restful.Request, resp *restful.Response) (*domain.Organization, bool) {
	org, err := h.cfg.Common.ResolveOrg(req.Request.Context(), req.PathParameter("org"))
	if err != nil {
		writeError(resp, err)
		return nil, false
	}
	return org, true
}

func toOrgStorageResponse(summary *OrgStorage) orgStorageResponse {
	projects := make([]projectUsageResponse, 0, len(summary.Projects))
	for _, project := range summary.Projects {
		projects = append(projects, projectUsageResponse{
			ID:        project.ProjectID.Base36(),
			Slug:      project.Slug,
			Name:      project.Name,
			Deleted:   project.Deleted,
			UsedBytes: project.UsedBytes,
		})
	}
	return orgStorageResponse{
		UsedBytes:  summary.UsedBytes,
		QuotaBytes: summary.QuotaBytes,
		OwnerID:    summary.OwnerID.Base36(),
		Projects:   projects,
	}
}

func toOwnerStorageResponse(summary *OwnerStorage) ownerStorageResponse {
	orgs := make([]ownerOrgResponse, 0, len(summary.Orgs))
	for _, org := range summary.Orgs {
		orgs = append(orgs, ownerOrgResponse{
			ID:        org.OrgID.Base36(),
			Slug:      org.Slug,
			Name:      org.Name,
			UsedBytes: org.UsedBytes,
		})
	}
	return ownerStorageResponse{
		UserID:     summary.UserID.Base36(),
		UsedBytes:  summary.UsedBytes,
		QuotaBytes: summary.QuotaBytes,
		Orgs:       orgs,
	}
}

func writeJSON(resp *restful.Response, status int, body any) {
	_ = resp.WriteHeaderAndJson(status, body, restful.MIME_JSON)
}

func writeError(resp *restful.Response, err error) {
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		_ = resp.WriteHeaderAndJson(domErr.Code, model.NewAPIError(domErr.Message), restful.MIME_JSON)
		return
	}
	_ = resp.WriteHeaderAndJson(http.StatusInternalServerError, model.NewAPIError("internal unknown error"), restful.MIME_JSON)
}
