package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	loginCodeTTLSeconds = 120
	loginCodeBytes      = 32
)

type userRepository interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.User, error)
	Create(ctx context.Context, user domain.User) (*domain.User, error)
}

type identityRepository interface {
	IdentityByProviderAccount(ctx context.Context, provider, providerAccountID string) (*domain.UserIdentity, error)
	IdentityByID(ctx context.Context, id snow.ID) (*domain.UserIdentity, error)
	ListIdentities(ctx context.Context, userID snow.ID) ([]*domain.UserIdentity, error)
	CountIdentities(ctx context.Context, userID snow.ID) (int, error)
	CreateIdentity(ctx context.Context, identity domain.UserIdentity) error
	DeleteIdentity(ctx context.Context, id, userID snow.ID) error
	SetEmailVerified(ctx context.Context, userID snow.ID) error
	SeedOwnerQuota(ctx context.Context, userID snow.ID, quotaBytes int64) error
	CreateOAuthLoginCode(ctx context.Context, code string, userID snow.ID, expiresAt time.Time) error
	ConsumeOAuthLoginCode(ctx context.Context, code string) (snow.ID, time.Time, error)
	DeleteExpiredOAuthLoginCodes(ctx context.Context, before time.Time) error
}

// transactor runs the multi-row writes atomically; *dbtx.Transactor satisfies
// it against a real database.
type transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Config carries the onboarding policy knobs.
type Config struct {
	FreeQuotaBytes int64
}

// OAuth runs social login and signup: it resolves the provider profile to a
// nipad account (existing identity, verified email match or a new account),
// links identities and hands the browser back to the SPA with a one-time code.
type OAuth struct {
	providers map[string]Provider
	states    *stateSigner
	users     userRepository
	repo      identityRepository
	tx        transactor
	node      snow.Node
	cfg       Config
	now       func() time.Time
}

func New(providers map[string]Provider, jwtKey string, users userRepository, repo identityRepository,
	tx transactor, node snow.Node, cfg Config,
) *OAuth {
	return &OAuth{
		providers: providers,
		states:    newStateSigner(jwtKey),
		users:     users,
		repo:      repo,
		tx:        tx,
		node:      node,
		cfg:       cfg,
		now:       time.Now,
	}
}

// Providers lists the enabled provider names.
func (o *OAuth) Providers() []string {
	return ProviderNames(o.providers)
}

// Start builds the provider authorization URL for the login/signup flow
// (linkUser = 0) or for attaching the provider to a logged-in account.
func (o *OAuth) Start(provider, redirect string, linkUser snow.ID) (string, error) {
	p, ok := o.providers[provider]
	if !ok {
		return "", domain.NewErrorNotFound("oauth provider is not enabled")
	}
	state, err := o.states.Sign(redirect, linkUser.Int64())
	if err != nil {
		return "", err
	}
	return p.AuthCodeURL(state), nil
}

// Callback completes the provider round-trip and returns the SPA URL the
// browser must be sent to next. Expected outcomes are encoded as query
// parameters (?code=… for a login handoff, ?oauth=linked, ?oauth_error=…);
// only transport-level failures return an error.
func (o *OAuth) Callback(ctx context.Context, provider, code, rawState string) (string, error) {
	p, ok := o.providers[provider]
	if !ok {
		return "", domain.NewErrorNotFound("oauth provider is not enabled")
	}
	state, err := o.states.Verify(rawState)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(code) == "" {
		return "", domain.NewErrorUser("authorization code is required")
	}
	profile, err := p.Exchange(ctx, code)
	if err != nil {
		return "", err
	}

	base := state.Redirect
	linkUser := snow.ID(state.LinkUser)

	identity, err := o.repo.IdentityByProviderAccount(ctx, provider, profile.AccountID)
	switch {
	case err == nil:
		if linkUser != 0 && identity.UserID != linkUser {
			return withQueryParam(base, "oauth_error", "identity_taken"), nil
		}
		if linkUser != 0 {
			return withQueryParam(base, "oauth", "linked"), nil
		}
		return o.issueLoginCode(ctx, identity.UserID, base, false)
	case !domain.IsErrorNotFound(err):
		return "", err
	}

	if linkUser != 0 {
		if err := o.attachIdentity(ctx, linkUser, provider, profile, false); err != nil {
			return "", err
		}
		return withQueryParam(base, "oauth", "linked"), nil
	}

	if profile.Email == "" {
		return withQueryParam(base, "oauth_error", "email_required"), nil
	}
	if !profile.EmailVerified {
		// An untrusted provider email must never create (and thereby squat) an
		// account: only provider-verified addresses pass the confirmation gate.
		return withQueryParam(base, "oauth_error", "email_required"), nil
	}
	existing, err := o.users.GetByEmail(ctx, profile.Email)
	if err == nil {
		if err := o.attachIdentity(ctx, existing.ID, provider, profile, true); err != nil {
			return "", err
		}
		return o.issueLoginCode(ctx, existing.ID, base, false)
	}
	if !domain.IsErrorNotFound(err) {
		return "", err
	}

	userID, err := o.createAccount(ctx, provider, profile)
	if err != nil {
		return "", err
	}
	return o.issueLoginCode(ctx, userID, base, true)
}

// Complete consumes a one-time login code and returns the account for the
// caller to start a session.
func (o *OAuth) Complete(ctx context.Context, code string) (*domain.User, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, domain.NewErrorUser("login code is required")
	}
	userID, expiresAt, err := o.repo.ConsumeOAuthLoginCode(ctx, code)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return nil, errInvalidLoginCode()
		}
		return nil, err
	}
	if expiresAt.Before(o.now()) {
		return nil, errInvalidLoginCode()
	}
	return o.users.GetByID(ctx, userID)
}

// ListIdentities returns the providers linked to the account.
func (o *OAuth) ListIdentities(ctx context.Context, userID snow.ID) ([]*domain.UserIdentity, error) {
	return o.repo.ListIdentities(ctx, userID)
}

// DisconnectIdentity removes one linked provider. The last remaining login
// method cannot be removed while the account has no password.
func (o *OAuth) DisconnectIdentity(ctx context.Context, userID, identityID snow.ID) error {
	identity, err := o.repo.IdentityByID(ctx, identityID)
	if err != nil {
		return err
	}
	if identity.UserID != userID {
		return domain.NewErrorNoPermission()
	}
	count, err := o.repo.CountIdentities(ctx, userID)
	if err != nil {
		return err
	}
	user, err := o.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if count <= 1 && user.Password == "" {
		return domain.NewErrorConflict("cannot disconnect the only login method; set a password first")
	}
	return o.repo.DeleteIdentity(ctx, identityID, userID)
}

// attachIdentity links the provider account to the user; existing unverified
// accounts are activated because a verified provider email proves ownership.
func (o *OAuth) attachIdentity(ctx context.Context, userID snow.ID, provider string, profile *Profile, confirmEmail bool) error {
	return o.tx.WithinTx(ctx, func(ctx context.Context) error {
		if confirmEmail {
			if err := o.repo.SetEmailVerified(ctx, userID); err != nil {
				return err
			}
		}
		return o.repo.CreateIdentity(ctx, domain.UserIdentity{
			ID:                o.node.Generate(),
			UserID:            userID,
			Provider:          provider,
			ProviderAccountID: profile.AccountID,
			Email:             profile.Email,
		})
	})
}

func (o *OAuth) createAccount(ctx context.Context, provider string, profile *Profile) (snow.ID, error) {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = emailLocalPart(profile.Email)
	}
	var userID snow.ID
	err := o.tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err := o.users.Create(ctx, domain.User{
			ID:            o.node.Generate(),
			Name:          name,
			Email:         profile.Email,
			PhotoUrl:      profile.AvatarURL,
			EmailVerified: true,
		})
		if err != nil {
			return err
		}
		userID = user.ID
		if o.cfg.FreeQuotaBytes > 0 {
			if err := o.repo.SeedOwnerQuota(ctx, user.ID, o.cfg.FreeQuotaBytes); err != nil {
				return err
			}
		}
		return o.repo.CreateIdentity(ctx, domain.UserIdentity{
			ID:                o.node.Generate(),
			UserID:            user.ID,
			Provider:          provider,
			ProviderAccountID: profile.AccountID,
			Email:             profile.Email,
		})
	})
	return userID, err
}

func (o *OAuth) issueLoginCode(ctx context.Context, userID snow.ID, base string, created bool) (string, error) {
	code, err := newLoginCode()
	if err != nil {
		return "", domain.NewErrorInternalServer(err.Error())
	}
	now := o.now()
	err = o.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := o.repo.DeleteExpiredOAuthLoginCodes(ctx, now); err != nil {
			return err
		}
		return o.repo.CreateOAuthLoginCode(ctx, code, userID, now.Add(loginCodeTTL()))
	})
	if err != nil {
		return "", err
	}
	target := withQueryParam(base, "code", code)
	if created {
		target = withQueryParam(target, "new", "1")
	}
	return target, nil
}

func newLoginCode() (string, error) {
	buf := make([]byte, loginCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func loginCodeTTL() time.Duration {
	return loginCodeTTLSeconds * time.Second
}

func withQueryParam(base, key, value string) string {
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	return base + separator + key + "=" + value
}

func emailLocalPart(email string) string {
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}

func errInvalidLoginCode() error {
	return domain.NewErrorNotFound("invalid or expired login code")
}
