package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
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
	appCtx.WriteJson(nethttp.StatusOK, resp)
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
	lock, err := h.useCase.FileLock().Acquire(appCtx.Context(), project.ID, body.Branch, body.Path)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toFileLockResponse(lock))
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
	appCtx.WriteJson(nethttp.StatusOK, map[string]bool{"ok": true})
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
	}
	return resp
}
