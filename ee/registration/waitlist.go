package registration

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const (
	waitlistJoinedEvent  = "user.waitlist_joined"
	waitlistOpenEvent    = "user.waitlist_open"
	defaultWaitlistBatch = 100
	maxWaitlistBatch     = 1000

	WaitlistSourceRegistration = "registration"
	WaitlistSourceOAuth        = "oauth"
)

type waitlistRepository interface {
	Join(ctx context.Context, entry domain.WaitlistEntry) (bool, error)
	List(ctx context.Context, after snow.ID, pending *bool, limit int) ([]*domain.WaitlistEntry, error)
	Delete(ctx context.Context, id snow.ID) (bool, error)
	Pending(ctx context.Context, limit int) ([]*domain.WaitlistEntry, error)
	MarkNotified(ctx context.Context, ids []snow.ID, at time.Time) error
}

// Waitlist keeps the addresses turned away while sign-up is invite-only and
// tells them when they can create an account.
type Waitlist struct {
	repo       waitlistRepository
	invites    signupInviteRepository
	inviteOnly bool
	tx         transactor
	node       snow.Node
	baseURL    string
	outbox     outbox
	kick       dispatcherKicker
	audit      usecase.AuditRecorder
	now        func() time.Time
}

func NewWaitlist(repo waitlistRepository, tx transactor, node snow.Node, baseURL string) *Waitlist {
	return &Waitlist{repo: repo, tx: tx, node: node, baseURL: baseURL, now: time.Now}
}

func (w *Waitlist) WithMailer(outbox outbox, kicker dispatcherKicker) *Waitlist {
	w.outbox = outbox
	w.kick = kicker
	return w
}

func (w *Waitlist) WithAudit(rec usecase.AuditRecorder) *Waitlist {
	w.audit = rec
	return w
}

// WithInvites makes Notify allow the notified addresses to sign up while
// registration stays invite-only.
func (w *Waitlist) WithInvites(invites signupInviteRepository, inviteOnly bool) *Waitlist {
	w.invites = invites
	w.inviteOnly = inviteOnly
	return w
}

// Join lists the address and emails it once; joining again changes nothing.
func (w *Waitlist) Join(ctx context.Context, name, email, source string) error {
	entry := domain.WaitlistEntry{
		ID:     w.node.Generate(),
		Email:  strings.ToLower(strings.TrimSpace(email)),
		Name:   strings.TrimSpace(name),
		Source: source,
	}
	now := w.now()
	created := false
	err := w.tx.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		if created, err = w.repo.Join(ctx, entry); err != nil || !created || w.outbox == nil {
			return err
		}
		delivery, err := w.joinedDelivery(&entry, now)
		if err != nil {
			return err
		}
		return w.outbox.Enqueue(ctx, []*domain.EmailDelivery{delivery})
	})
	if err != nil {
		return err
	}
	if created {
		w.kickDispatcher()
	}
	return nil
}

// List pages the waitlist for a super admin.
func (w *Waitlist) List(ctx context.Context, after snow.ID, pending *bool, limit int) ([]*domain.WaitlistEntry, error) {
	if _, err := requireSuperAdmin(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultWaitlistBatch
	}
	return w.repo.List(ctx, after, pending, limit)
}

func (w *Waitlist) Remove(ctx context.Context, id snow.ID) error {
	claim, err := requireSuperAdmin(ctx)
	if err != nil {
		return err
	}
	return w.tx.WithinTx(ctx, func(ctx context.Context) error {
		deleted, err := w.repo.Delete(ctx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return domain.NewErrorNotFound("waitlist entry not found")
		}
		return w.record(ctx, claim, domain.AuditWaitlistRemoved, id.Base36(), nil)
	})
}

// Notify emails the oldest entries not yet told that they can sign up and
// returns how many were notified. While sign-up is invite-only each notified
// address is also allowed to sign up.
func (w *Waitlist) Notify(ctx context.Context, limit int) (int, error) {
	claim, err := requireSuperAdmin(ctx)
	if err != nil {
		return 0, err
	}
	limit, err = waitlistLimit(limit)
	if err != nil {
		return 0, err
	}
	allow := w.inviteOnly && w.invites != nil
	now := w.now()
	notified := 0
	err = w.tx.WithinTx(ctx, func(ctx context.Context) error {
		entries, err := w.repo.Pending(ctx, limit)
		if err != nil || len(entries) == 0 {
			return err
		}
		ids := make([]snow.ID, 0, len(entries))
		deliveries := make([]*domain.EmailDelivery, 0, len(entries))
		for _, entry := range entries {
			if allow {
				invite := domain.SignupInvite{ID: w.node.Generate(), Email: entry.Email, InvitedBy: claim.UserID}
				if _, err := w.invites.Upsert(ctx, invite); err != nil {
					return err
				}
			}
			if w.outbox != nil {
				delivery, err := w.openDelivery(entry, now)
				if err != nil {
					return err
				}
				deliveries = append(deliveries, delivery)
			}
			ids = append(ids, entry.ID)
		}
		if len(deliveries) > 0 {
			if err := w.outbox.Enqueue(ctx, deliveries); err != nil {
				return err
			}
		}
		if err := w.repo.MarkNotified(ctx, ids, now); err != nil {
			return err
		}
		notified = len(ids)
		return w.record(ctx, claim, domain.AuditWaitlistNotified, "", map[string]any{
			"count":       notified,
			"allowlisted": allow,
			"emailed":     len(deliveries) > 0,
		})
	})
	if err != nil {
		return 0, err
	}
	if notified > 0 {
		w.kickDispatcher()
	}
	return notified, nil
}

func waitlistLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return defaultWaitlistBatch, nil
	case limit < 0 || limit > maxWaitlistBatch:
		return 0, domain.NewErrorUser(fmt.Sprintf("limit must be between 1 and %d", maxWaitlistBatch))
	}
	return limit, nil
}

func (w *Waitlist) record(ctx context.Context, claim *domain.Claims, action, targetID string, metadata map[string]any) error {
	if w.audit == nil {
		return nil
	}
	actor := claim.UserID
	if metadata == nil {
		metadata = map[string]any{}
	}
	return w.audit.Record(ctx, domain.AuditEntry{
		ActorType:   domain.AuditActorUser,
		ActorUserID: &actor,
		Action:      action,
		TargetType:  "signup_waitlist",
		TargetID:    targetID,
		Metadata:    metadata,
	})
}

func (w *Waitlist) kickDispatcher() {
	if w.kick != nil {
		w.kick.Kick()
	}
}

func greeting(name string) string {
	if name == "" {
		return "Hi,"
	}
	return "Hi " + name + ","
}

func (w *Waitlist) joinedDelivery(entry *domain.WaitlistEntry, now time.Time) (*domain.EmailDelivery, error) {
	subject := "You're on the Nipa waitlist"
	text := fmt.Sprintf("%s\n\nThanks for your interest in Nipa. Sign-up is by invitation only for now, so we added %s to the waitlist. We will email you as soon as you can create your account.\n\nIf you did not ask to join, you can ignore this email.\n", greeting(entry.Name), entry.Email)
	htmlBody := fmt.Sprintf(`<p>%s</p><p>Thanks for your interest in Nipa. Sign-up is by invitation only for now, so we added <strong>%s</strong> to the waitlist. We will email you as soon as you can create your account.</p><p>If you did not ask to join, you can ignore this email.</p>`,
		html.EscapeString(greeting(entry.Name)), html.EscapeString(entry.Email))
	return w.delivery(entry, waitlistJoinedEvent, "waitlist", subject, text, htmlBody, now)
}

func (w *Waitlist) openDelivery(entry *domain.WaitlistEntry, now time.Time) (*domain.EmailDelivery, error) {
	link := strings.TrimRight(strings.TrimSpace(w.baseURL), "/") + "/register"
	subject := "You can now create your Nipa account"
	text := fmt.Sprintf("%s\n\nGood news: you can now create your Nipa account with %s. Sign up here:\n\n%s\n", greeting(entry.Name), entry.Email, link)
	htmlBody := fmt.Sprintf(`<p>%s</p><p>Good news: you can now create your Nipa account with <strong>%s</strong>.</p><p><a href="%s">Create your account</a></p>`,
		html.EscapeString(greeting(entry.Name)), html.EscapeString(entry.Email), html.EscapeString(link))
	return w.delivery(entry, waitlistOpenEvent, "waitlist-open", subject, text, htmlBody, now)
}

func (w *Waitlist) delivery(entry *domain.WaitlistEntry, event, kind, subject, text, htmlBody string, now time.Time) (*domain.EmailDelivery, error) {
	return newDelivery(w.node, w.baseURL, outgoingMail{
		ref: entry.ID.Base36(), to: entry.Email, event: event, kind: kind,
		subject: subject, text: text, html: htmlBody,
	}, now)
}
