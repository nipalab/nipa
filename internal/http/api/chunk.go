package api

import (
	"net/http"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/usecase"
)

// setupChunkRouter registers the signed-URL chunk download endpoint. Like the
// bundle upload route, it is intentionally not behind the Bearer auth filter:
// the HMAC signature carried in the URL is the capability, matching how
// presigned object-store URLs work.
func setupChunkRouter(ws *restful.WebService, h *handler.ChunkHandler) {
	tags := []string{"Chunks"}

	ws.Route(
		ws.GET("/{org}/{project}/{hash}").
			To(Wrap(h.GetChunk)).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("hash", "chunk hash (hex)")).
			Param(ws.QueryParameter("op", "transfer operation")).
			Param(ws.QueryParameter("exp", "unix expiry")).
			Param(ws.QueryParameter("sig", "HMAC signature")).
			Produces("application/octet-stream").
			Doc("Download one chunk via a signed transfer URL").
			Returns(http.StatusOK, "chunk content", nil).
			Operation("getChunk").
			Metadata(restfulspec.KeyOpenAPITags, tags))
}

// setupChunkBundleRouter registers the signed bundle upload endpoint
// (/api/chunk-bundles), outside the Bearer auth filter for the same reason.
func setupChunkBundleRouter(ws *restful.WebService, h *handler.ChunkHandler) {
	ws.Route(
		ws.PUT("/{org}/{project}").
			To(Wrap(h.PutBundle)).
			AllowedMethodsWithoutContentType([]string{"PUT"}).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.QueryParameter("size", "largest body in bytes")).
			Param(ws.QueryParameter("count", "most chunks per body")).
			Param(ws.QueryParameter("exp", "unix expiry")).
			Param(ws.QueryParameter("sig", "HMAC signature")).
			Produces(restful.MIME_JSON).
			Doc("Upload a bundle of chunks via a signed bundle URL").
			Returns(http.StatusOK, "stored; one receipt per chunk", []handler.BundleReceipt{}).
			Operation("putChunkBundle").
			Metadata(restfulspec.KeyOpenAPITags, []string{"Chunks"}))
}

// chunkWebServices returns the signed chunk transfer web services (downloads
// under /api/chunks, bundle uploads under /api/chunk-bundles), each with the
// given filters and the API's maintenance enforcement.
func (a *API) chunkWebServices(h *handler.ChunkHandler, filters ...restful.FilterFunction) []*restful.WebService {
	chunks := new(restful.WebService).ApiVersion("1.0.0").Path("/api/chunks")
	bundles := new(restful.WebService).ApiVersion("1.0.0").Path("/api/chunk-bundles")
	for _, ws := range []*restful.WebService{chunks, bundles} {
		for _, filter := range filters {
			ws.Filter(filter)
		}
		a.enforceMaintenance(ws)
	}
	setupChunkRouter(chunks, h)
	setupChunkBundleRouter(bundles, h)
	return []*restful.WebService{chunks, bundles}
}

// NewChunkTransferHandler serves only the signed chunk transfer endpoints on a
// fresh container. Useful when the routes are mounted on their own listener.
// A non-nil svc enforces its maintenance windows.
func NewChunkTransferHandler(chunks *usecase.Chunk, projects *usecase.Common, svc *maintenance.Service) http.Handler {
	container := restful.NewContainer()
	for _, ws := range (&API{maintenance: svc}).chunkWebServices(handler.NewChunkHandler(chunks, projects)) {
		container.Add(ws)
	}
	return container
}
