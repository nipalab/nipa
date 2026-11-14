package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListFileLocks(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	locks, err := h.useCase.FileLock().List(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.FileLockResponse, 0, len(locks))
	for _, lock := range locks {
		resp = append(resp, toFileLockResponse(lock))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) LockFile(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.LockFileRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	var opts []usecase.AcquireOption
	if body.TTLSeconds != nil {
		opts = append(opts, usecase.LockTTLSeconds(*body.TTLSeconds))
	}
	lock, err := h.useCase.FileLock().Acquire(appCtx.Context(), project.ID, body.Branch, body.Path, opts...)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toFileLockResponse(lock))
}

func (h *Handler) UnlockFile(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.LockFileRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.FileLock().Release(appCtx.Context(), project.ID, body.Branch, body.Path); err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) RequestFileLockRelease(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	lockID, err := snow.ParseBase36(appCtx.PathParameter("id"))
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid lock id"))
		return
	}
	body := &model.RequestLockReleaseRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	request, err := h.useCase.FileLock().RequestRelease(appCtx.Context(), project.ID, lockID, body.Message)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toFileLockRequestResponse(request))
}

func toFileLockRequestResponse(request *domain.FileLockRequest) model.FileLockRequestResponse {
	return model.FileLockRequestResponse{
		ID:            request.ID.Base36(),
		RequesterID:   request.RequesterID.Base36(),
		RequesterName: request.RequesterName,
		Message:       request.Message,
		RequestedAt:   request.CreatedAt,
	}
}

func toFileLockResponse(lock *domain.FileLock) model.FileLockResponse {
	resp := model.FileLockResponse{
		ID:                 lock.ID.Base36(),
		Path:               lock.Path,
		Branch:             lock.Branch,
		Global:             lock.BranchID == nil,
		HeldBy:             lock.HeldBy.Base36(),
		HeldByName:         lock.HeldByName,
		MergeRequestNumber: lock.MergeRequestNumber,
		AcquiredAt:         lock.AcquiredAt,
		ExpiresAt:          lock.ExpiresAt,
		Requests:           make([]model.FileLockRequestResponse, 0, len(lock.Requests)),
	}
	for _, request := range lock.Requests {
		resp.Requests = append(resp.Requests, toFileLockRequestResponse(request))
	}
	return resp
}
