package handler

import (
	"io"
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/usecase"
)

// ChunkHandler serves signed-URL chunk transfers over HTTP.
type ChunkHandler struct {
	chunks *usecase.Chunk
}

func NewChunkHandler(chunks *usecase.Chunk) *ChunkHandler {
	return &ChunkHandler{chunks: chunks}
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
	if err := h.verifyURL(appCtx, hashHex, params); err != nil {
		appCtx.HandleError(err)
		return
	}
	hash, err := domain.ParseHashHex(hashHex)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid chunk hash"))
		return
	}
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
	if err := h.chunks.StoreUploaded(appCtx.Context(), hash, data); err != nil {
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
	if err := h.verifyURL(appCtx, hashHex, params); err != nil {
		appCtx.HandleError(err)
		return
	}
	hash, err := domain.ParseHashHex(hashHex)
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid chunk hash"))
		return
	}
	data, err := h.chunks.Download(appCtx.Context(), hash)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteBytes(nethttp.StatusOK, "application/octet-stream", data)
}

func (h *ChunkHandler) verifyURL(appCtx httpApp.AppContext, hashHex string, params chunkurl.Params) error {
	return h.chunks.VerifyTransferURL(
		appCtx.PathParameter("org"),
		appCtx.PathParameter("project"),
		hashHex,
		params,
	)
}
