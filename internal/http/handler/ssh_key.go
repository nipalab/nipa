package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
)

func (h *Handler) ListMySSHKeys(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	keys, err := h.useCase.SSHKey().List(appCtx.Context(), claims.UserID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.SSHKeyResponse, 0, len(keys))
	for _, key := range keys {
		resp = append(resp, toSSHKeyResponse(key))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) AddMySSHKey(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	body := &model.AddSSHKeyRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	key, err := h.useCase.SSHKey().Add(appCtx.Context(), claims.UserID, body.Name, body.PublicKey)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toSSHKeyResponse(key))
}

func (h *Handler) RemoveMySSHKey(appCtx http.AppContext) {
	claims := appCtx.Claims()
	if claims == nil {
		appCtx.HandleError(domain.NewErrorUnauthorized("authentication required"))
		return
	}
	id, err := parseID(appCtx.PathParameter("key"), "SSH key")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.SSHKey().Delete(appCtx.Context(), claims.UserID, id); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "SSH key removed"})
}

func toSSHKeyResponse(key *domain.SSHPublicKey) model.SSHKeyResponse {
	return model.SSHKeyResponse{
		ID:          key.ID.Base36(),
		Name:        key.Name,
		KeyType:     key.KeyType,
		Fingerprint: key.Fingerprint,
		Comment:     key.Comment,
		CreatedAt:   key.CreatedAt,
	}
}
