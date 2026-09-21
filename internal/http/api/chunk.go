package api

import (
	"net/http"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/http/handler"
	"github.com/nipalab/nipa/internal/usecase"
)

// setupChunkRouter registers the signed-URL chunk transfer endpoints. These
// routes are intentionally not behind the Bearer auth filter: the HMAC
// signature carried in the URL is the capability, matching how presigned
// object-store URLs work.
func setupChunkRouter(ws *restful.WebService, h *handler.ChunkHandler) {
	tags := []string{"Chunks"}

	ws.Route(
		ws.PUT("/{org}/{project}/{hash}").
			To(wrap(h.PutChunk)).
			AllowedMethodsWithoutContentType([]string{"PUT"}).
			Param(ws.PathParameter("org", "organization slug")).
			Param(ws.PathParameter("project", "project slug")).
			Param(ws.PathParameter("hash", "chunk hash (hex)")).
			Param(ws.QueryParameter("op", "transfer operation")).
			Param(ws.QueryParameter("size", "exact chunk size in bytes")).
			Param(ws.QueryParameter("exp", "unix expiry")).
			Param(ws.QueryParameter("sig", "HMAC signature")).
			Doc("Upload one chunk via a signed transfer URL").
			Returns(http.StatusNoContent, "stored", nil).
			Operation("putChunk").
			Metadata(restfulspec.KeyOpenAPITags, tags))

	ws.Route(
		ws.GET("/{org}/{project}/{hash}").
			To(wrap(h.GetChunk)).
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

// NewChunkTransferHandler serves only the signed chunk transfer endpoints on a
// fresh container. Useful when the routes are mounted on their own listener.
func NewChunkTransferHandler(chunks *usecase.Chunk) http.Handler {
	ws := new(restful.WebService).ApiVersion("1.0.0")
	ws.Path("/api/chunks")
	setupChunkRouter(ws, handler.NewChunkHandler(chunks))

	container := restful.NewContainer()
	container.Add(ws)
	return container
}
