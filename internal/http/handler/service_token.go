package handler

import (
	nethttp "net/http"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListServiceTokens(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	tokens, err := h.useCase.ServiceToken().List(appCtx.Context(), project.ID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.ServiceTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		resp = append(resp, toServiceTokenResponse(token, ""))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateServiceToken(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateServiceTokenRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	created, plaintext, err := h.useCase.ServiceToken().Create(appCtx.Context(), project.ID, usecase.ServiceTokenInput{
		Name:      body.Name,
		Scopes:    body.Scopes,
		ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toServiceTokenResponse(created, plaintext))
}

func (h *Handler) RevokeServiceToken(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	id, err := parseID(appCtx.PathParameter("id"), "service token")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	revoked, err := h.useCase.ServiceToken().Revoke(appCtx.Context(), project.ID, id)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, toServiceTokenResponse(revoked, ""))
}

func toServiceTokenResponse(token *domain.ServiceToken, plaintext string) model.ServiceTokenResponse {
	scopes := token.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return model.ServiceTokenResponse{
		ID:         token.ID.Base36(),
		ProjectID:  token.ProjectID.Base36(),
		Name:       token.Name,
		Hint:       token.Hint,
		Scopes:     scopes,
		CreatedBy:  token.CreatedBy.Base36(),
		ExpiresAt:  token.ExpiresAt,
		LastUsedAt: token.LastUsedAt,
		RevokedAt:  token.RevokedAt,
		CreatedAt:  token.CreatedAt,
		Token:      plaintext,
	}
}
