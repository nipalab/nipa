package handler

import (
	"context"
	"fmt"
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

// ChunkHandler serves signed-URL chunk transfers over HTTP: bundle uploads
// and single-chunk downloads.
type ChunkHandler struct {
	chunks   *usecase.Chunk
	projects chunkProjectResolver
}

func NewChunkHandler(chunks *usecase.Chunk, projects chunkProjectResolver) *ChunkHandler {
	return &ChunkHandler{chunks: chunks, projects: projects}
}

// BundleReceipt is one entry of a bundle upload's response: the receipt of
// one stored chunk, passed to ConfirmChunkUploads.
type BundleReceipt struct {
	Hash    string `json:"hash"`
	Receipt string `json:"receipt"`
}

// PutBundle stores the chunks of one bundle upload (chunkbundle format)
// through a signed bundle URL, which is the capability: no bearer token. The
// body needs an exact Content-Length within the URL's signed size. That many
// bytes are reserved from the server-wide upload budget before the body is
// read, every entry is hashed and checked before anything is written (any
// failure is a 400 with nothing stored), then each chunk is stored and the
// response is 200 with a JSON array of {hash, receipt}. Nothing is recorded
// in the database here: the project's rows and quota wait for confirm.
func (h *ChunkHandler) PutBundle(appCtx httpApp.AppContext) {
	params, err := chunkurl.ParseBundleParams(appCtx.Request().URL.Query())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	orgSlug, projectSlug := appCtx.PathParameter("org"), appCtx.PathParameter("project")
	if err := h.chunks.VerifyBundleURL(orgSlug, projectSlug, params); err != nil {
		appCtx.HandleError(err)
		return
	}
	request := appCtx.Request()
	size := request.ContentLength
	if size < 0 {
		appCtx.HandleError(domain.NewErrorUser("chunk bundle needs a Content-Length"))
		return
	}
	if size == 0 || size > params.MaxSize {
		appCtx.HandleError(domain.NewErrorUser(fmt.Sprintf("chunk bundle of %d bytes is outside 1..%d", size, params.MaxSize)))
		return
	}
	_, project, err := h.projects.ResolveBySlug(appCtx.Context(), orgSlug, projectSlug)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	trace.SpanFromContext(appCtx.Context()).SetAttributes(attribute.Int64(obs.AttrChunkBytes, size))
	// The body's bytes are reserved from the server-wide budget before it is
	// read and given back once it is stored or rejected, so bodies in memory
	// stay bounded however many clients upload at once.
	release, err := h.chunks.ReserveUploadBuffer(appCtx.Context(), size)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	defer release()
	body := nethttp.MaxBytesReader(appCtx.ResponseWriter(), request.Body, size)
	receipts, err := h.chunks.StoreBundle(appCtx.Context(), project.ID, body, size, params.MaxCount)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	out := make([]BundleReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		out = append(out, BundleReceipt{Hash: receipt.Hash.String(), Receipt: receipt.Receipt})
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, out)
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
