package handler

import (
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (h *Handler) ListAuditEntries(appCtx http.AppContext) {
	opts, err := auditListOptions(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	entries, next, err := h.useCase.Audit().List(appCtx.Context(), opts)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.AuditListResponse{
		Entries:    make([]model.AuditEntryResponse, 0, len(entries)),
		NextCursor: next,
	}
	for _, entry := range entries {
		resp.Entries = append(resp.Entries, toAuditEntryResponse(entry))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, resp)
}

func auditListOptions(appCtx http.AppContext) (usecase.AuditListOptions, error) {
	opts := usecase.AuditListOptions{
		ActionPrefix: appCtx.QueryParameter("action"),
		Target:       appCtx.QueryParameter("target"),
	}
	ids := []struct {
		param string
		dst   **snow.ID
	}{
		{"actor", &opts.Actor},
		{"org", &opts.Org},
		{"project", &opts.Project},
		{"after", &opts.After},
	}
	for _, id := range ids {
		raw := appCtx.QueryParameter(id.param)
		if raw == "" {
			continue
		}
		parsed, err := snow.ParseBase36(raw)
		if err != nil {
			return opts, domain.NewErrorUser("invalid " + id.param)
		}
		*id.dst = &parsed
	}
	times := []struct {
		param string
		dst   **time.Time
	}{
		{"since", &opts.Since},
		{"until", &opts.Until},
	}
	for _, t := range times {
		raw := appCtx.QueryParameter(t.param)
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return opts, domain.NewErrorUser("invalid " + t.param + ": use RFC 3339")
		}
		*t.dst = &parsed
	}
	if raw := appCtx.QueryParameter("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 0 {
			return opts, domain.NewErrorUser("invalid limit")
		}
		opts.Limit = limit
	}
	return opts, nil
}

func toAuditEntryResponse(entry *domain.AuditEntry) model.AuditEntryResponse {
	resp := model.AuditEntryResponse{
		ID:         entry.ID.Base36(),
		OccurredAt: entry.OccurredAt,
		ActorType:  string(entry.ActorType),
		ActorLabel: entry.ActorLabel,
		Action:     entry.Action,
		TargetType: entry.TargetType,
		TargetID:   entry.TargetID,
		Metadata:   entry.Metadata,
		RequestID:  entry.RequestID,
		ClientIP:   entry.ClientIP,
		UserAgent:  entry.UserAgent,
	}
	if resp.Metadata == nil {
		resp.Metadata = map[string]any{}
	}
	if entry.ActorUserID != nil {
		resp.ActorUserID = entry.ActorUserID.Base36()
	}
	if entry.OrgID != nil {
		resp.OrgID = entry.OrgID.Base36()
	}
	if entry.ProjectID != nil {
		resp.ProjectID = entry.ProjectID.Base36()
	}
	return resp
}
