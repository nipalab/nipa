package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	imail "github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	orgInvitationTTL   = 7 * 24 * time.Hour
	orgInvitationEvent = "org.invitation"
	maxInvitationToken = 128
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=org_invitation_mock_test.go -package=usecase
type orgInvitationRepository interface {
	Create(ctx context.Context, inv *domain.OrgInvitation) error
	Get(ctx context.Context, id snow.ID) (*domain.OrgInvitation, error)
	GetByTokenHash(ctx context.Context, hash []byte) (*domain.OrgInvitation, error)
	FindPending(ctx context.Context, orgID snow.ID, email string) (*domain.OrgInvitation, error)
	ListByOrg(ctx context.Context, orgID snow.ID, status string) ([]*domain.OrgInvitation, error)
	ListPendingByEmail(ctx context.Context, email string, now time.Time) ([]*domain.OrgInvitation, error)
	ExpireStale(ctx context.Context, orgID snow.ID, email string, now time.Time) error
	Rotate(ctx context.Context, id snow.ID, tokenHash []byte, expiresAt time.Time) (bool, error)
	Resolve(ctx context.Context, id snow.ID, status string) (bool, error)
	Accept(ctx context.Context, id, userID snow.ID, at time.Time) (bool, error)
}

type invitationOutbox interface {
	Enqueue(ctx context.Context, deliveries []*domain.EmailDelivery) error
}

// OrgInvitation runs the consent-based way into an organization: owners invite
// an email address, the address owner accepts after signing in.
type OrgInvitation struct {
	repo    orgInvitationRepository
	orgs    *Org
	users   userLookup
	node    snow.Node
	limiter *ratelimit.Limiter
	outbox  invitationOutbox
	baseURL string
	kick    dispatcherKicker
	now     func() time.Time
}

func NewOrgInvitation(repo orgInvitationRepository, orgs *Org, users userLookup, node snow.Node) *OrgInvitation {
	return &OrgInvitation{repo: repo, orgs: orgs, users: users, node: node, now: time.Now}
}

// WithLimiter throttles invitation creation, resends and token lookups.
func (i *OrgInvitation) WithLimiter(limiter *ratelimit.Limiter) *OrgInvitation {
	i.limiter = limiter
	return i
}

// WithOutbox queues the invitation email; without it invitations are only
// visible in the invitee's own list.
func (i *OrgInvitation) WithOutbox(outbox invitationOutbox, baseURL string) *OrgInvitation {
	i.outbox = outbox
	i.baseURL = baseURL
	return i
}

// WithKicker asks the outbox dispatcher to poll right after an enqueue.
func (i *OrgInvitation) WithKicker(kicker dispatcherKicker) *OrgInvitation {
	i.kick = kicker
	return i
}

func errInvitationNotFound() error {
	return domain.NewErrorNotFound("invitation not found")
}

func hashInvitationToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newInvitationToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, domain.NewErrorInternalServer(err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashInvitationToken(token), nil
}

func (i *OrgInvitation) throttle(ctx context.Context, scope, subject string) error {
	res := i.limiter.Allow(ctx, scope, subject)
	if !res.Allowed {
		return domain.NewErrorTooManyRequests(res.RetryAfter)
	}
	return nil
}

func (i *OrgInvitation) throttleInviter(ctx context.Context, email string) error {
	claim, _ := domain.ClaimFromContext(ctx)
	if err := i.throttle(ctx, ratelimit.ScopeInviteUser, claim.UserID.Base36()); err != nil {
		return err
	}
	if email == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(email))
	return i.throttle(ctx, ratelimit.ScopeInviteEmail, hex.EncodeToString(sum[:]))
}

// Create invites an email address. The outcome never depends on whether an
// account exists for the address.
func (i *OrgInvitation) Create(ctx context.Context, org *domain.Organization, email, role string) error {
	if err := i.orgs.requireOrgOwner(ctx, org.ID); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if !imail.ValidAddress(email) {
		return domain.NewErrorUser("invalid email address")
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = domain.OrgRoleMember
	}
	if !domain.IsValidOrgRole(role) {
		return domain.NewErrorUser("invalid org role")
	}
	claim, _ := domain.ClaimFromContext(ctx)
	if err := i.throttleInviter(ctx, email); err != nil {
		return err
	}
	if err := i.ensureNotMember(ctx, org.ID, email); err != nil {
		return err
	}
	token, hash, err := newInvitationToken()
	if err != nil {
		return err
	}
	now := i.now()
	action := domain.AuditOrgInvitationCreated
	var invID snow.ID
	err = auditedWrite(ctx, i.orgs.audit, func(ctx context.Context) error {
		if err := i.repo.ExpireStale(ctx, org.ID, email, now); err != nil {
			return err
		}
		existing, err := i.repo.FindPending(ctx, org.ID, email)
		switch {
		case err == nil && existing.Role == role:
			invID = existing.ID
			action = domain.AuditOrgInvitationResent
			if err := i.rotate(ctx, existing.ID, hash, now); err != nil {
				return err
			}
		case err == nil || domain.IsErrorNotFound(err):
			if err == nil {
				if _, err := i.repo.Resolve(ctx, existing.ID, domain.OrgInvitationRevoked); err != nil {
					return err
				}
			}
			invID = i.node.Generate()
			if err := i.repo.Create(ctx, &domain.OrgInvitation{
				ID:        invID,
				OrgID:     org.ID,
				InvitedBy: claim.UserID,
				Email:     email,
				Role:      role,
				TokenHash: hash,
				ExpiresAt: now.Add(orgInvitationTTL),
			}); err != nil {
				return err
			}
		default:
			return err
		}
		return i.enqueue(ctx, claim.UserID, org.Name, email, token, now)
	}, func() *domain.AuditEntry {
		return invitationAudit(action, org.ID, invID, email, role)
	})
	if err != nil {
		return err
	}
	i.kickDispatcher()
	return nil
}

func (i *OrgInvitation) rotate(ctx context.Context, id snow.ID, hash []byte, now time.Time) error {
	ok, err := i.repo.Rotate(ctx, id, hash, now.Add(orgInvitationTTL))
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewErrorConflict("invitation is not pending")
	}
	return nil
}

// ensureNotMember compares against the member list of the caller's own org
// only, so no other account data is read.
func (i *OrgInvitation) ensureNotMember(ctx context.Context, orgID snow.ID, email string) error {
	members, err := i.orgs.repo.ListMembers(ctx, orgID)
	if err != nil {
		return err
	}
	for _, member := range members {
		if normalizeEmail(member.User.Email) == email {
			return domain.NewErrorConflict("user is already a member")
		}
	}
	return nil
}

func (i *OrgInvitation) List(ctx context.Context, orgID snow.ID, status string) ([]*domain.OrgInvitation, error) {
	if err := i.orgs.requireOrgOwner(ctx, orgID); err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status != "" && !domain.IsValidOrgInvitationStatus(status) {
		return nil, domain.NewErrorUser("invalid invitation status")
	}
	query := status
	if status == domain.OrgInvitationPending || status == domain.OrgInvitationExpired {
		query = ""
	}
	invs, err := i.repo.ListByOrg(ctx, orgID, query)
	if err != nil {
		return nil, err
	}
	now := i.now()
	out := make([]*domain.OrgInvitation, 0, len(invs))
	for _, inv := range invs {
		inv.Status = inv.EffectiveStatus(now)
		if status == "" || inv.Status == status {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (i *OrgInvitation) Revoke(ctx context.Context, orgID, id snow.ID) error {
	inv, err := i.ownedInvitation(ctx, orgID, id)
	if err != nil {
		return err
	}
	return auditedWrite(ctx, i.orgs.audit, func(ctx context.Context) error {
		ok, err := i.repo.Resolve(ctx, inv.ID, domain.OrgInvitationRevoked)
		if err != nil {
			return err
		}
		if !ok {
			return domain.NewErrorConflict("invitation is not pending")
		}
		return nil
	}, func() *domain.AuditEntry {
		return invitationAudit(domain.AuditOrgInvitationRevoked, orgID, inv.ID, inv.Email, inv.Role)
	})
}

// Resend rotates the token and expiry of a pending invitation and mails it again.
func (i *OrgInvitation) Resend(ctx context.Context, orgID, id snow.ID) error {
	inv, err := i.ownedInvitation(ctx, orgID, id)
	if err != nil {
		return err
	}
	if err := i.throttleInviter(ctx, inv.Email); err != nil {
		return err
	}
	token, hash, err := newInvitationToken()
	if err != nil {
		return err
	}
	claim, _ := domain.ClaimFromContext(ctx)
	now := i.now()
	if err := auditedWrite(ctx, i.orgs.audit, func(ctx context.Context) error {
		if err := i.rotate(ctx, inv.ID, hash, now); err != nil {
			return err
		}
		return i.enqueue(ctx, claim.UserID, inv.OrgName, inv.Email, token, now)
	}, func() *domain.AuditEntry {
		return invitationAudit(domain.AuditOrgInvitationResent, orgID, inv.ID, inv.Email, inv.Role)
	}); err != nil {
		return err
	}
	i.kickDispatcher()
	return nil
}

func (i *OrgInvitation) ownedInvitation(ctx context.Context, orgID, id snow.ID) (*domain.OrgInvitation, error) {
	if err := i.orgs.requireOrgOwner(ctx, orgID); err != nil {
		return nil, err
	}
	inv, err := i.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv.OrgID != orgID {
		return nil, errInvitationNotFound()
	}
	return inv, nil
}

// ListMine returns the live invitations addressed to the caller's verified email.
func (i *OrgInvitation) ListMine(ctx context.Context) ([]*domain.OrgInvitation, error) {
	user, err := i.invitee(ctx)
	if err != nil {
		return nil, err
	}
	return i.repo.ListPendingByEmail(ctx, normalizeEmail(user.Email), i.now())
}

// Preview shows an invitation to the signed-in address owner.
func (i *OrgInvitation) Preview(ctx context.Context, token string) (*domain.OrgInvitation, error) {
	inv, _, err := i.forInvitee(ctx, i.byToken(token))
	return inv, err
}

func (i *OrgInvitation) Accept(ctx context.Context, token string) (*domain.OrgInvitation, error) {
	return i.accept(ctx, i.byToken(token))
}

func (i *OrgInvitation) AcceptByID(ctx context.Context, id snow.ID) (*domain.OrgInvitation, error) {
	return i.accept(ctx, i.byID(id))
}

func (i *OrgInvitation) Decline(ctx context.Context, token string) error {
	return i.decline(ctx, i.byToken(token))
}

func (i *OrgInvitation) DeclineByID(ctx context.Context, id snow.ID) error {
	return i.decline(ctx, i.byID(id))
}

type invitationLookup func(ctx context.Context) (*domain.OrgInvitation, error)

func (i *OrgInvitation) byToken(token string) invitationLookup {
	return func(ctx context.Context) (*domain.OrgInvitation, error) {
		if token == "" || len(token) > maxInvitationToken {
			return nil, errInvitationNotFound()
		}
		hash := hashInvitationToken(token)
		inv, err := i.repo.GetByTokenHash(ctx, hash)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(inv.TokenHash, hash) != 1 {
			return nil, errInvitationNotFound()
		}
		return inv, nil
	}
}

func (i *OrgInvitation) byID(id snow.ID) invitationLookup {
	return func(ctx context.Context) (*domain.OrgInvitation, error) {
		return i.repo.Get(ctx, id)
	}
}

func (i *OrgInvitation) invitee(ctx context.Context) (*domain.User, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorUnauthorized("authentication required")
	}
	user, err := i.users.GetByID(ctx, claim.UserID)
	if err != nil {
		return nil, err
	}
	if user.Deleted {
		return nil, domain.NewErrorNoPermission()
	}
	if !user.EmailVerified {
		return nil, domain.NewErrorForbidden("email address not verified")
	}
	return user, nil
}

// forInvitee loads the invitation and answers one generic not-found for
// anything that is not a live invitation for the caller's verified email.
func (i *OrgInvitation) forInvitee(ctx context.Context, lookup invitationLookup) (*domain.OrgInvitation, *domain.User, error) {
	user, err := i.invitee(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := i.throttle(ctx, ratelimit.ScopeInviteToken, user.ID.Base36()); err != nil {
		return nil, nil, err
	}
	inv, err := lookup(ctx)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, nil, errInvitationNotFound()
		}
		return nil, nil, err
	}
	if normalizeEmail(user.Email) != inv.Email || inv.Status != domain.OrgInvitationPending {
		return nil, nil, errInvitationNotFound()
	}
	if inv.EffectiveStatus(i.now()) == domain.OrgInvitationExpired {
		return nil, nil, domain.NewErrorConflict("invitation expired")
	}
	return inv, user, nil
}

func (i *OrgInvitation) accept(ctx context.Context, lookup invitationLookup) (*domain.OrgInvitation, error) {
	inv, user, err := i.forInvitee(ctx, lookup)
	if err != nil {
		return nil, err
	}
	now := i.now()
	err = auditedWrite(ctx, i.orgs.audit, func(ctx context.Context) error {
		return admitSeat(ctx, i.orgs.seats, inv.OrgID, user.ID, func(ctx context.Context) error {
			_, err := i.orgs.repo.MemberRole(ctx, inv.OrgID, user.ID)
			if domain.IsErrorNotFound(err) {
				err = i.orgs.repo.UpsertMember(ctx, inv.OrgID, user.ID, inv.Role)
			}
			if err != nil {
				return err
			}
			ok, err := i.repo.Accept(ctx, inv.ID, user.ID, now)
			if err != nil {
				return err
			}
			if !ok {
				return errInvitationNotFound()
			}
			return nil
		})
	}, func() *domain.AuditEntry {
		entry := invitationAudit(domain.AuditOrgInvitationAccepted, inv.OrgID, inv.ID, inv.Email, inv.Role)
		entry.Metadata["user_id"] = user.ID.Base36()
		return entry
	})
	if err != nil {
		return nil, err
	}
	if i.orgs.cache != nil {
		i.orgs.cache.InvalidateAll()
	}
	inv.Status = domain.OrgInvitationAccepted
	return inv, nil
}

func (i *OrgInvitation) decline(ctx context.Context, lookup invitationLookup) error {
	inv, _, err := i.forInvitee(ctx, lookup)
	if err != nil {
		return err
	}
	return auditedWrite(ctx, i.orgs.audit, func(ctx context.Context) error {
		ok, err := i.repo.Resolve(ctx, inv.ID, domain.OrgInvitationDeclined)
		if err != nil {
			return err
		}
		if !ok {
			return errInvitationNotFound()
		}
		return nil
	}, func() *domain.AuditEntry {
		return invitationAudit(domain.AuditOrgInvitationDeclined, inv.OrgID, inv.ID, inv.Email, inv.Role)
	})
}

func invitationAudit(action string, orgID, invID snow.ID, email, role string) *domain.AuditEntry {
	domainPart := ""
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domainPart = email[at+1:]
	}
	return orgAudit(action, orgID, domain.AuditTargetInvitation, invID.Base36(), map[string]any{
		"role":         role,
		"email_domain": domainPart,
	})
}

func (i *OrgInvitation) kickDispatcher() {
	if i.outbox != nil && i.kick != nil {
		i.kick.Kick()
	}
}

func (i *OrgInvitation) enqueue(ctx context.Context, inviterID snow.ID, orgName, email, token string, now time.Time) error {
	if i.outbox == nil {
		return nil
	}
	inviter := "A teammate"
	if user, err := i.users.GetByID(ctx, inviterID); err == nil && strings.TrimSpace(user.Name) != "" {
		inviter = user.Name
	}
	link := strings.TrimRight(i.baseURL, "/") + "/invitations/" + url.PathEscape(token)
	subject := fmt.Sprintf("%s invited you to join %s on Nipa", inviter, orgName)
	text := fmt.Sprintf("%s invited you to join the organization %s on Nipa.\n\nOpen the invitation, signing in or creating an account with this email address (%s):\n\n%s\n\nThis link expires in 7 days. If you did not expect this invitation, you can ignore this email.\n",
		inviter, orgName, email, link)
	htmlBody := fmt.Sprintf(`<p>%s invited you to join the organization <strong>%s</strong> on Nipa.</p><p><a href="%s">Open the invitation</a></p><p>Sign in or create an account with this email address (%s). This link expires in 7 days. If you did not expect this invitation, you can ignore this email.</p>`,
		html.EscapeString(inviter), html.EscapeString(orgName), html.EscapeString(link), html.EscapeString(email))
	body, err := imail.EncodeMessage(imail.Message{
		To:      []string{email},
		Subject: subject,
		Text:    text,
		HTML:    htmlBody,
	})
	if err != nil {
		return err
	}
	return i.outbox.Enqueue(ctx, []*domain.EmailDelivery{{
		ID:            i.node.Generate(),
		Event:         orgInvitationEvent,
		Email:         email,
		Subject:       subject,
		Body:          body,
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: &now,
	}})
}
