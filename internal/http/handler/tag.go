package handler

import (
	nethttp "net/http"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const defaultTagLimit = 100

func (h *Handler) ListProjectTags(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	limit := queryInt(appCtx.QueryParameter("limit"), defaultTagLimit)
	var lastID snow.ID
	if raw := appCtx.QueryParameter("last_id"); raw != "" {
		id, err := parseID(raw, "tag")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		lastID = id
	}
	var createdBefore *time.Time
	if raw := appCtx.QueryParameter("last_created_at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid last_created_at"))
			return
		}
		createdBefore = &parsed
	}
	tags, err := h.useCase.Tag().ListTags(appCtx.Context(), project.ID, limit, createdBefore, lastID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.TagResponse, 0, len(tags))
	for _, tag := range tags {
		resp = append(resp, toTagResponse(tag))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) CreateProjectTag(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	body := &model.CreateTagRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(err)
		return
	}
	target := usecase.TagTarget{BranchName: body.From}
	if body.From != "" {
		// Branch names such as "release" are valid base36; prefer the branch.
		if _, err := h.useCase.Branch().GetBranchByName(appCtx.Context(), project.ID, body.From); domain.IsErrorNotFound(err) {
			if id, parseErr := snow.ParseBase36(body.From); parseErr == nil {
				target = usecase.TagTarget{CommitID: &id}
			}
		} else if err != nil {
			appCtx.HandleError(err)
			return
		}
	}
	tag, err := h.useCase.Tag().CreateTag(appCtx.Context(), project.ID, body.Name, target, body.Message)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toTagResponse(tag))
}

func (h *Handler) GetProjectTag(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	tag, err := h.useCase.Tag().GetTagByName(appCtx.Context(), project.ID, appCtx.PathParameter("name"))
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toTagResponse(tag))
}

func (h *Handler) DeleteProjectTag(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if err := h.useCase.Tag().DeleteTag(appCtx.Context(), project.ID, appCtx.PathParameter("name")); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, model.MessageResponse{Message: "tag deleted"})
}

func toTagResponse(tag *domain.Tag) model.TagResponse {
	return model.TagResponse{
		ID:        tag.ID.Base36(),
		Name:      tag.Name,
		CommitID:  tag.CommitID.Base36(),
		Message:   tag.Message,
		UserID:    tag.UserID.Base36(),
		CreatedAt: tag.CreatedAt,
	}
}
