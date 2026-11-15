package registration

import (
	nethttp "net/http"
	"strconv"
	"time"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
)

type waitlistEntryResponse struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
}

type waitlistListResponse struct {
	Entries    []waitlistEntryResponse `json:"entries"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type notifyWaitlistRequest struct {
	Limit int `json:"limit"`
}

type notifyWaitlistResponse struct {
	Notified int `json:"notified"`
}

// WithWaitlist enables the super-admin waitlist routes.
func (h *Handler) WithWaitlist(waitlist *Waitlist) *Handler {
	h.waitlist = waitlist
	return h
}

func registerWaitlistRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Admin"}
	ws.Route(ws.GET("/admin/waitlist").
		To(api.Wrap(h.ListWaitlist)).
		Param(ws.QueryParameter("pending", "true = not yet notified, false = notified")).
		Param(ws.QueryParameter("after", "cursor (entry id, base36)")).
		Param(ws.QueryParameter("limit", "page size (1..1000, default 100)")).
		Doc("List the sign-up waitlist (super admin)").
		Returns(nethttp.StatusOK, "waitlist", waitlistListResponse{}).
		Operation("listWaitlist").
		Metadata(restfulspec.KeyOpenAPITags, tags))
	ws.Route(ws.POST("/admin/waitlist/notify").
		To(api.Wrap(h.NotifyWaitlist)).
		Reads(notifyWaitlistRequest{}).
		Doc("Tell the oldest waiting addresses they can sign up (super admin)").
		Notes("Emails up to limit (1..1000, default 100) entries not yet notified; while sign-up is invite-only each address is also allowed to sign up").
		Returns(nethttp.StatusOK, "notified", notifyWaitlistResponse{}).
		Operation("notifyWaitlist").
		Metadata(restfulspec.KeyOpenAPITags, tags))
	ws.Route(ws.DELETE("/admin/waitlist/{id}").
		To(api.Wrap(h.RemoveWaitlistEntry)).
		Param(ws.PathParameter("id", "entry id (base36)")).
		Doc("Remove an address from the waitlist (super admin)").
		Returns(nethttp.StatusNoContent, "removed", nil).
		Operation("removeWaitlistEntry").
		Metadata(restfulspec.KeyOpenAPITags, tags))
}

func (h *Handler) ListWaitlist(appCtx httpApp.AppContext) {
	var pending *bool
	if raw := appCtx.QueryParameter("pending"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid pending filter"))
			return
		}
		pending = &value
	}
	var after snow.ID
	if raw := appCtx.QueryParameter("after"); raw != "" {
		id, err := snow.ParseBase36(raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid cursor"))
			return
		}
		after = id
	}
	limit := 0
	if raw := appCtx.QueryParameter("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			appCtx.HandleError(domain.NewErrorUser("invalid limit"))
			return
		}
		limit = value
	}
	page, err := waitlistLimit(limit)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	entries, err := h.waitlist.List(appCtx.Context(), after, pending, page+1)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	response := waitlistListResponse{Entries: make([]waitlistEntryResponse, 0, len(entries))}
	if len(entries) > page {
		entries = entries[:page]
		response.NextCursor = entries[page-1].ID.Base36()
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, waitlistEntryResponse{
			ID:         entry.ID.Base36(),
			Email:      entry.Email,
			Name:       entry.Name,
			Source:     entry.Source,
			CreatedAt:  entry.CreatedAt,
			NotifiedAt: entry.NotifiedAt,
		})
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, response)
}

func (h *Handler) NotifyWaitlist(appCtx httpApp.AppContext) {
	body := &notifyWaitlistRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid request body"))
		return
	}
	notified, err := h.waitlist.Notify(appCtx.Context(), body.Limit)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, notifyWaitlistResponse{Notified: notified})
}

func (h *Handler) RemoveWaitlistEntry(appCtx httpApp.AppContext) {
	id, err := snow.ParseBase36(appCtx.PathParameter("id"))
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid waitlist entry id"))
		return
	}
	if err := h.waitlist.Remove(appCtx.Context(), id); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}
