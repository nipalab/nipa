package api

import (
	"net/http"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
)

func setupAuditRouter(ws *restful.WebService, h *handler.Handler) {
	ws.Route(ws.GET("/admin/audit").
		To(Wrap(h.ListAuditEntries)).
		Param(ws.QueryParameter("actor", "acting user id (base36)")).
		Param(ws.QueryParameter("org", "organization id (base36)")).
		Param(ws.QueryParameter("project", "project id (base36)")).
		Param(ws.QueryParameter("action", "action prefix, e.g. org.member")).
		Param(ws.QueryParameter("target", "target id")).
		Param(ws.QueryParameter("since", "inclusive lower bound (RFC 3339)")).
		Param(ws.QueryParameter("until", "exclusive upper bound (RFC 3339)")).
		Param(ws.QueryParameter("after", "keyset cursor: the last id of the previous page")).
		Param(ws.QueryParameter("limit", "page size (default 50, max 200)")).
		Doc("List audit log entries, newest first (super admin)").
		Returns(http.StatusOK, "entries", model.AuditListResponse{}).
		Operation("listAuditEntries").
		Metadata(restfulspec.KeyOpenAPITags, []string{"Audit"}))
}
