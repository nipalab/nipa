// Package registration implements the enterprise SaaS onboarding: self-service
// registration with email confirmation and the free-tier quota seed. It is
// wired only by the enterprise server when SAAS_MODE is on; the free edition
// registers no routes and stays unchanged.
package registration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const (
	verificationTTLSeconds = 72 * 60 * 60
	verificationTokenBytes = 32
	verificationEvent      = "user.verification"
)

type userRepository interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.User, error)
}

type onboardingRepository interface {
	CreateUnverifiedUser(ctx context.Context, user domain.User) (*domain.User, error)
	UpsertEmailVerification(ctx context.Context, userID snow.ID, tokenHash string, expiresAt time.Time) error
	EmailVerificationByToken(ctx context.Context, tokenHash string) (snow.ID, time.Time, error)
	DeleteEmailVerification(ctx context.Context, userID snow.ID) error
	DeleteExpiredEmailVerifications(ctx context.Context, before time.Time) error
	SetEmailVerified(ctx context.Context, userID snow.ID) error
	SeedOwnerQuota(ctx context.Context, userID snow.ID, quotaBytes int64) error
}

type passwordHasher interface {
	Hash(password string) (string, error)
}

type outbox interface {
	Enqueue(ctx context.Context, deliveries []*domain.EmailDelivery) error
}

type dispatcherKicker interface {
	Kick()
}

// transactor runs the multi-row writes atomically; *dbtx.Transactor satisfies
// it against a real database.
type transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Config carries the registration policy knobs.
type Config struct {
	FreeQuotaBytes int64
	BaseURL        string
}

// Registration runs the self-registration flow: it creates an inactive account,
// queues a verification email and activates the account when the one-time link
// is consumed.
type Registration struct {
	node   snow.Node
	hasher passwordHasher
	users  userRepository
	repo   onboardingRepository
	tx     transactor
	outbox outbox
	kick   dispatcherKicker
	cfg    Config
	now    func() time.Time
}

func New(node snow.Node, hasher passwordHasher, users userRepository, repo onboardingRepository,
	tx transactor, cfg Config,
) *Registration {
	return &Registration{
		node:   node,
		hasher: hasher,
		users:  users,
		repo:   repo,
		tx:     tx,
		cfg:    cfg,
		now:    time.Now,
	}
}

// WithMailer enables verification emails through the durable outbox; the
// kicker asks the dispatcher to poll immediately after an enqueue. Without a
// mailer registration still works, but no verification email is sent.
func (r *Registration) WithMailer(outbox outbox, kicker dispatcherKicker) *Registration {
	r.outbox = outbox
	r.kick = kicker
	return r
}

// Register creates an inactive account with an email verification token and
// queues the confirmation email. The account stays unusable until Verify.
func (r *Registration) Register(ctx context.Context, name, email, password string) error {
	name, email, err := usecase.ValidateUserIdentity(name, email)
	if err != nil {
		return err
	}
	if err := usecase.ValidatePassword(password); err != nil {
		return err
	}
	if _, err := r.users.GetByEmail(ctx, email); err == nil {
		return domain.NewErrorConflict("email already in use")
	} else if !domain.IsErrorNotFound(err) {
		return err
	}
	hash, err := r.hasher.Hash(password)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	token, tokenHash, err := newVerificationToken()
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	now := r.now()
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err := r.repo.CreateUnverifiedUser(ctx, domain.User{
			ID:       r.node.Generate(),
			Name:     name,
			Email:    email,
			Password: hash,
		})
		if err != nil {
			return err
		}
		if err := r.repo.DeleteExpiredEmailVerifications(ctx, now); err != nil {
			return err
		}
		if err := r.repo.UpsertEmailVerification(ctx, user.ID, tokenHash, now.Add(verificationTTL())); err != nil {
			return err
		}
		if r.cfg.FreeQuotaBytes > 0 {
			if err := r.repo.SeedOwnerQuota(ctx, user.ID, r.cfg.FreeQuotaBytes); err != nil {
				return err
			}
		}
		return r.enqueueVerification(ctx, user, token, now)
	})
	if err != nil {
		return err
	}
	r.kickDispatcher()
	if r.outbox == nil {
		slog.Warn("account registered without a verification email: EMAIL_SENDER is off", "email", email)
	}
	return nil
}

// Verify consumes a verification token, activates the account and returns it
// for the caller to start a session.
func (r *Registration) Verify(ctx context.Context, token string) (*domain.User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, domain.NewErrorUser("verification token is required")
	}
	userID, expiresAt, err := r.repo.EmailVerificationByToken(ctx, hashToken(token))
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, errInvalidVerification()
		}
		return nil, err
	}
	if expiresAt.Before(r.now()) {
		_ = r.repo.DeleteEmailVerification(ctx, userID)
		return nil, errInvalidVerification()
	}
	var user *domain.User
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.repo.SetEmailVerified(ctx, userID); err != nil {
			return err
		}
		if err := r.repo.DeleteEmailVerification(ctx, userID); err != nil {
			return err
		}
		found, err := r.users.GetByID(ctx, userID)
		if err != nil {
			return err
		}
		user = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ResendVerification reissues the verification token for an unconfirmed
// account. Unknown, confirmed and deleted accounts are silently ignored so the
// endpoint cannot be used to probe for registrations.
func (r *Registration) ResendVerification(ctx context.Context, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return domain.NewErrorUser("email is required")
	}
	user, err := r.users.GetByEmail(ctx, email)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil
		}
		return err
	}
	if user.EmailVerified {
		return nil
	}
	token, tokenHash, err := newVerificationToken()
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	now := r.now()
	err = r.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.repo.DeleteExpiredEmailVerifications(ctx, now); err != nil {
			return err
		}
		if err := r.repo.UpsertEmailVerification(ctx, user.ID, tokenHash, now.Add(verificationTTL())); err != nil {
			return err
		}
		return r.enqueueVerification(ctx, user, token, now)
	})
	if err != nil {
		return err
	}
	r.kickDispatcher()
	return nil
}

func (r *Registration) enqueueVerification(ctx context.Context, user *domain.User, token string, now time.Time) error {
	if r.outbox == nil {
		return nil
	}
	delivery, err := r.verificationDelivery(user, token, now)
	if err != nil {
		return err
	}
	return r.outbox.Enqueue(ctx, []*domain.EmailDelivery{delivery})
}

func (r *Registration) kickDispatcher() {
	if r.kick != nil {
		r.kick.Kick()
	}
}

func (r *Registration) verificationDelivery(user *domain.User, token string, now time.Time) (*domain.EmailDelivery, error) {
	link := r.verificationURL(token)
	subject := "Confirm your email address"
	text := fmt.Sprintf("Hi %s,\n\nConfirm your email address to activate your Nipa account:\n\n%s\n\nThis link expires in 72 hours. If you did not create an account, you can ignore this email.\n", user.Name, link)
	htmlBody := fmt.Sprintf(`<p>Hi %s,</p><p>Confirm your email address to activate your Nipa account:</p><p><a href="%s">Confirm email address</a></p><p>This link expires in 72 hours. If you did not create an account, you can ignore this email.</p>`,
		html.EscapeString(user.Name), html.EscapeString(link))
	deliveryID := r.node.Generate()
	message := mail.Message{
		To:        []string{user.Email},
		Subject:   subject,
		Text:      text,
		HTML:      htmlBody,
		MessageID: fmt.Sprintf("<nipa-verify-%s-%s@%s>", user.ID.Base36(), deliveryID.Base36(), r.messageHost()),
	}
	body, err := mail.EncodeMessage(message)
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	due := now
	return &domain.EmailDelivery{
		ID:            deliveryID,
		Event:         verificationEvent,
		UserID:        user.ID,
		Email:         user.Email,
		Subject:       subject,
		Body:          body,
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: &due,
	}, nil
}

func (r *Registration) verificationURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(r.cfg.BaseURL), "/")
	return base + "/verify?token=" + url.QueryEscape(token)
}

func (r *Registration) messageHost() string {
	if parsed, err := url.Parse(r.cfg.BaseURL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "nipa.local"
}

func verificationTTL() time.Duration {
	return verificationTTLSeconds * time.Second
}

func newVerificationToken() (string, string, error) {
	buf := make([]byte, verificationTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func errInvalidVerification() error {
	return domain.NewErrorNotFound("invalid or expired verification link")
}
