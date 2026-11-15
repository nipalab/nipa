package api

import (
	"net/http"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/http/model"
)

const maintenancePath = "/api/maintenance"

var maintenanceTags = []string{"Maintenance"}

// newMaintenanceWebService serves the public maintenance status on its own
// root: no bearer filter and no rate limit, so clients can always read it.
func newMaintenanceWebService(h *handler.Handler) *restful.WebService {
	ws := new(restful.WebService).ApiVersion("1.0.0")
	ws.Path(maintenancePath).
		Filter(sameOriginFilter).
		Produces(restful.MIME_JSON)
	ws.Route(ws.GET("").
		To(Wrap(h.GetMaintenance)).
		Doc("Server maintenance status (public, never lists org or project windows)").
		Returns(http.StatusOK, "server maintenance status", model.MaintenanceStatusResponse{}).
		Operation("getMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	return ws
}

func setupMaintenanceRouter(ws *restful.WebService, h *handler.Handler) {
	ws.Route(ws.GET("/admin/maintenance").
		To(Wrap(h.GetAdminMaintenance)).
		Doc("Server, org and project maintenance windows (super admin)").
		Returns(http.StatusOK, "maintenance windows", model.AdminMaintenanceResponse{}).
		Operation("getAdminMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.PUT("/admin/maintenance").
		To(Wrap(h.SetAdminMaintenance)).
		Reads(model.SetMaintenanceRequest{}).
		Doc("Schedule or update the server maintenance window (super admin; 409 while MAINTENANCE_FILE controls it)").
		Returns(http.StatusOK, "stored window", model.MaintenanceWindowResponse{}).
		Operation("setAdminMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.DELETE("/admin/maintenance").
		To(Wrap(h.EndAdminMaintenance)).
		Doc("End the server maintenance window (super admin)").
		Returns(http.StatusNoContent, "ended", nil).
		Operation("endAdminMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))

	ws.Route(ws.GET("/orgs/{org}/maintenance").
		To(Wrap(h.GetOrgMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Doc("Server and org maintenance windows (org member); org owners also get the org's project windows").
		Returns(http.StatusOK, "maintenance windows", model.OrgMaintenanceResponse{}).
		Operation("getOrgMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.PUT("/orgs/{org}/maintenance").
		To(Wrap(h.SetOrgMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Reads(model.SetMaintenanceRequest{}).
		Doc("Schedule or update the org maintenance window (org owner or super admin; read_only or full)").
		Returns(http.StatusOK, "stored window", model.MaintenanceWindowResponse{}).
		Operation("setOrgMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.DELETE("/orgs/{org}/maintenance").
		To(Wrap(h.EndOrgMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Doc("End the org maintenance window (org owner or super admin)").
		Returns(http.StatusNoContent, "ended", nil).
		Operation("endOrgMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))

	ws.Route(ws.GET("/orgs/{org}/projects/{project}/maintenance").
		To(Wrap(h.GetProjectMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Param(ws.PathParameter("project", "project slug")).
		Doc("Server, org and project maintenance windows (project read)").
		Returns(http.StatusOK, "maintenance windows", model.ProjectMaintenanceResponse{}).
		Operation("getProjectMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.PUT("/orgs/{org}/projects/{project}/maintenance").
		To(Wrap(h.SetProjectMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Param(ws.PathParameter("project", "project slug")).
		Reads(model.SetMaintenanceRequest{}).
		Doc("Schedule or update the project maintenance window (org owner or super admin; read_only or full)").
		Returns(http.StatusOK, "stored window", model.MaintenanceWindowResponse{}).
		Operation("setProjectMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
	ws.Route(ws.DELETE("/orgs/{org}/projects/{project}/maintenance").
		To(Wrap(h.EndProjectMaintenance)).
		Param(ws.PathParameter("org", "organization slug")).
		Param(ws.PathParameter("project", "project slug")).
		Doc("End the project maintenance window (org owner or super admin)").
		Returns(http.StatusNoContent, "ended", nil).
		Operation("endProjectMaintenance").
		Metadata(restfulspec.KeyOpenAPITags, maintenanceTags))
}
