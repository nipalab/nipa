package registration

import (
	"context"
	nethttp "net/http"
	stdmail "net/mail"
	"strings"
	"time"

	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/internal/domain"
	httpApp "github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const maxInviteDays = 365

type signupInviteRepository interface {
	Upsert(ctx context.Context, invite domain.SignupInvite) (*domain.SignupInvite, error)
	List(ctx context.Context) ([]*domain.SignupInvite, error)
	Delete(ctx context.Context, id snow.ID) (bool, error)
	Allowed(ctx context.Context, email string, now time.Time) (bool, error)
}

// SignupInvites lets a super admin allow single emails to sign up while open
// registration is closed.
type SignupInvites struct {
	repo  signupInviteRepository
	tx    transactor
	node  snow.Node
	audit usecase.AuditRecorder
	now   func() time.Time
}

func NewSignupInvites(repo signupInviteRepository, tx transactor, node snow.Node) *SignupInvites {
	return &SignupInvites{repo: repo, tx: tx, node: node, now: time.Now}
}

func (s *SignupInvites) WithAudit(rec usecase.AuditRecorder) *SignupInvites {
	s.audit = rec
	return s
}

// Allowed reports whether the email holds an unexpired invite.
func (s *SignupInvites) Allowed(ctx context.Context, email string) (bool, error) {
	return s.repo.Allowed(ctx, email, s.now())
}

func requireSuperAdmin(ctx context.Context) (*domain.Claims, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok || !claim.IsSuperAdmin {
		return nil, domain.NewErrorNoPermission()
	}
	return &claim, nil
}

func (s *SignupInvites) record(ctx context.Context, claim *domain.Claims, action string, invite *domain.SignupInvite) error {
	if s.audit == nil {
		return nil
	}
	actor := claim.UserID
	return s.audit.Record(ctx, domain.AuditEntry{
		ActorType:   domain.AuditActorUser,
		ActorUserID: &actor,
		Action:      action,
		TargetType:  "signup_invite",
		TargetID:    invite.ID.Base36(),
		Metadata:    map[string]any{"email": invite.Email},
	})
}

// Create allows the email to sign up, for expiresInDays days (0 = no expiry).
func (s *SignupInvites) Create(ctx context.Context, email string, expiresInDays int) (*domain.SignupInvite, error) {
	claim, err := requireSuperAdmin(ctx)
	if err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if parsed, perr := stdmail.ParseAddress(email); perr != nil || parsed.Address != email {
		return nil, domain.NewErrorUser("invalid email address")
	}
	if expiresInDays < 0 || expiresInDays > maxInviteDays {
		return nil, domain.NewErrorUser("expires_in_days must be between 0 and 365")
	}
	invite := domain.SignupInvite{ID: s.node.Generate(), Email: email, InvitedBy: claim.UserID}
	if expiresInDays > 0 {
		at := s.now().Add(time.Duration(expiresInDays) * 24 * time.Hour)
		invite.ExpiresAt = &at
	}
	var stored *domain.SignupInvite
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if stored, err = s.repo.Upsert(ctx, invite); err != nil {
			return err
		}
		return s.record(ctx, claim, domain.AuditSignupInviteCreated, stored)
	})
	return stored, err
}

func (s *SignupInvites) List(ctx context.Context) ([]*domain.SignupInvite, error) {
	if _, err := requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	return s.repo.List(ctx)
}

func (s *SignupInvites) Revoke(ctx context.Context, id snow.ID) error {
	claim, err := requireSuperAdmin(ctx)
	if err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		deleted, err := s.repo.Delete(ctx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return domain.NewErrorNotFound("signup invite not found")
		}
		return s.record(ctx, claim, domain.AuditSignupInviteRevoked, &domain.SignupInvite{ID: id})
	})
}

type createSignupInviteRequest struct {
	Email         string `json:"email"`
	ExpiresInDays int    `json:"expires_in_days"`
}

type signupInviteResponse struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	InvitedBy string     `json:"invited_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type signupInviteListResponse struct {
	Invites []signupInviteResponse `json:"invites"`
}

func toSignupInviteResponse(invite *domain.SignupInvite) signupInviteResponse {
	return signupInviteResponse{
		ID:        invite.ID.Base36(),
		Email:     invite.Email,
		InvitedBy: invite.InvitedBy.Base36(),
		CreatedAt: invite.CreatedAt,
		ExpiresAt: invite.ExpiresAt,
	}
}

// WithSignupInvites enables the super-admin invite routes.
func (h *Handler) WithSignupInvites(invites *SignupInvites) *Handler {
	h.invites = invites
	return h
}

func registerSignupInviteRoutes(ws *restful.WebService, h *Handler) {
	tags := []string{"Admin"}
	ws.Route(ws.POST("/admin/signup-invites").
		To(api.Wrap(h.CreateSignupInvite)).
		Reads(createSignupInviteRequest{}).
		Doc("Allow an email address to sign up while registration is closed (super admin)").
		Returns(nethttp.StatusCreated, "invite", signupInviteResponse{}).
		Operation("createSignupInvite").
		Metadata(restfulspec.KeyOpenAPITags, tags))
	ws.Route(ws.GET("/admin/signup-invites").
		To(api.Wrap(h.ListSignupInvites)).
		Doc("List signup invites (super admin)").
		Returns(nethttp.StatusOK, "invites", signupInviteListResponse{}).
		Operation("listSignupInvites").
		Metadata(restfulspec.KeyOpenAPITags, tags))
	ws.Route(ws.DELETE("/admin/signup-invites/{id}").
		To(api.Wrap(h.RevokeSignupInvite)).
		Param(ws.PathParameter("id", "invite id (base36)")).
		Doc("Revoke a signup invite (super admin)").
		Returns(nethttp.StatusNoContent, "revoked", nil).
		Operation("revokeSignupInvite").
		Metadata(restfulspec.KeyOpenAPITags, tags))
}

func (h *Handler) CreateSignupInvite(appCtx httpApp.AppContext) {
	body := &createSignupInviteRequest{}
	if err := appCtx.ReadJson(body); err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid request body"))
		return
	}
	invite, err := h.invites.Create(appCtx.Context(), body.Email, body.ExpiresInDays)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	_ = appCtx.WriteJson(nethttp.StatusCreated, toSignupInviteResponse(invite))
}

func (h *Handler) ListSignupInvites(appCtx httpApp.AppContext) {
	invites, err := h.invites.List(appCtx.Context())
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	response := signupInviteListResponse{Invites: make([]signupInviteResponse, 0, len(invites))}
	for _, invite := range invites {
		response.Invites = append(response.Invites, toSignupInviteResponse(invite))
	}
	_ = appCtx.WriteJson(nethttp.StatusOK, response)
}

func (h *Handler) RevokeSignupInvite(appCtx httpApp.AppContext) {
	id, err := snow.ParseBase36(appCtx.PathParameter("id"))
	if err != nil {
		appCtx.HandleError(domain.NewErrorUser("invalid invite id"))
		return
	}
	if err := h.invites.Revoke(appCtx.Context(), id); err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.ResponseWriter().WriteHeader(nethttp.StatusNoContent)
}
