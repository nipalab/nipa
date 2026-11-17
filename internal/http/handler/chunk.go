package handler

import (
	"context"
	"io"
	nethttp "net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/usecase"
)

// chunkProjectResolver resolves the project named by a signed chunk URL. The
// signature is the capability, so no permission is checked.
type chunkProjectResolver interface {
	ResolveBySlug(ctx context.Context, orgSlug, projectSlug string) (*domain.Organization, *domain.Project, error)
}

// ChunkHandler serves signed-URL chunk transfers over HTTP.
type ChunkHandler struct {
	chunks   *usecase.Chunk
	projects chunkProjectResolver
}

func NewChunkHandler(chunks *usecase.Chunk, projects chunkProjectResolver) *ChunkHandler {
	return &ChunkHandler{chunks: chunks, projects: projects}
}

// PutChunk stores one chunk uploaded through a signed transfer URL. The URL
// pins the exact size; the BLAKE3 hash is verified before storing.
func (h *ChunkHandler) PutChunk(appCtx httpApp.AppContext) {
	hashHex := appCtx.PathParameter("hash")
	params, err := chunkurl.ParseParams(appCtx.Request().URL.Query())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if params.Op != chunkurl.OpUpload {
		appCtx.HandleError(domain.NewErrorNoPermission())
		return
	}
	project, err := h.verifyURL(appCtx, hashHex, params)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	hash, err := domain.ParseHashHex(hashHex)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid chunk hash"))
		return
	}
	trace.SpanFromContext(appCtx.Context()).SetAttributes(attribute.String(obs.AttrChunkHash, hashHex))
	request := appCtx.Request()
	if request.ContentLength >= 0 && request.ContentLength != params.Size {
		appCtx.HandleError(domain.NewErrorUser("chunk size mismatch"))
		return
	}
	data, err := io.ReadAll(nethttp.MaxBytesReader(appCtx.ResponseWriter(), request.Body, params.Size+1))
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("unable to read chunk body"))
		return
	}
	if int64(len(data)) != params.Size {
		appCtx.HandleError(domain.NewErrorUser("chunk size mismatch"))
		return
	}
	if err := h.chunks.StoreUploaded(appCtx.Context(), project.ID, hash, data); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}

// GetChunk serves one chunk through a signed download URL.
func (h *ChunkHandler) GetChunk(appCtx httpApp.AppContext) {
	hashHex := appCtx.PathParameter("hash")
	params, err := chunkurl.ParseParams(appCtx.Request().URL.Query())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if params.Op != chunkurl.OpDownload {
		appCtx.HandleError(domain.NewErrorNoPermission())
		return
	}
	project, err := h.verifyURL(appCtx, hashHex, params)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	hash, err := domain.ParseHashHex(hashHex)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid chunk hash"))
		return
	}
	trace.SpanFromContext(appCtx.Context()).SetAttributes(attribute.String(obs.AttrChunkHash, hashHex))
	data, err := h.chunks.Download(appCtx.Context(), project.ID, hash)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteBytes(nethttp.StatusOK, "application/octet-stream", data)
}

func (h *ChunkHandler) verifyURL(appCtx httpApp.AppContext, hashHex string, params chunkurl.Params) (*domain.Project, error) {
	orgSlug, projectSlug := appCtx.PathParameter("org"), appCtx.PathParameter("project")
	if err := h.chunks.VerifyTransferURL(orgSlug, projectSlug, hashHex, params); err != nil {
		return nil, err
	}
	_, project, err := h.projects.ResolveBySlug(appCtx.Context(), orgSlug, projectSlug)
	if err != nil {
		return nil, err
	}
	return project, nil
}
