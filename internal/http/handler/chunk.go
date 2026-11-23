package handler

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nipalab/nipa/internal/chunkproof"
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
// pins the exact size; the BLAKE3 hash is verified before storing. A stored
// chunk is answered 204 with a receipt (chunkproof.ReceiptHeader) the client
// passes to confirm instead of having the server read the chunk back. Nothing
// is recorded in the database here: the project's rows and quota wait for
// confirm.
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
	// The signed size is reserved from the server-wide proxy budget before
	// the body is read and given back once it is stored or rejected, so
	// bodies in memory stay bounded however many clients upload at once.
	release, err := h.chunks.ReserveUploadBuffer(appCtx.Context(), params.Size)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	defer release()
	data, err := readChunkBody(appCtx, request, params.Size)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	receipt, err := h.chunks.StoreUploaded(appCtx.Context(), project.ID, hash, data)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().Header().Set(chunkproof.ReceiptHeader, receipt)
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}

// readChunkBody reads exactly size bytes into one buffer of that size (the
// reserved budget), then makes sure nothing follows; MaxBytesReader stops a
// longer body one byte past the signed size.
func readChunkBody(appCtx httpApp.AppContext, request *nethttp.Request, size int64) ([]byte, error) {
	body := nethttp.MaxBytesReader(appCtx.ResponseWriter(), request.Body, size+1)
	data := make([]byte, size)
	if _, err := io.ReadFull(body, data); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, domain.NewErrorUser("chunk size mismatch")
		}
		return nil, domain.NewErrorUser("unable to read chunk body")
	}
	var extra [1]byte
	if n, _ := io.ReadFull(body, extra[:]); n > 0 {
		return nil, domain.NewErrorUser("chunk size mismatch")
	}
	return data, nil
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
