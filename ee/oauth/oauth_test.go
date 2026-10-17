package oauth

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubProvider struct {
	profile *Profile
}

func (p *stubProvider) Name() string { return ProviderGitHub }

func (p *stubProvider) AuthCodeURL(state string) string {
	return "https://github.example/authorize?state=" + url.QueryEscape(state)
}

func (p *stubProvider) Exchange(_ context.Context, _ string) (*Profile, error) {
	return p.profile, nil
}

type stubUsers struct {
	users []*domain.User
}

func (s *stubUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, user := range s.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (s *stubUsers) GetByID(_ context.Context, id snow.ID) (*domain.User, error) {
	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (s *stubUsers) Create(_ context.Context, user domain.User) (*domain.User, error) {
	userCopy := user
	s.users = append(s.users, &userCopy)
	return &userCopy, nil
}

type codeRow struct {
	userID    snow.ID
	expiresAt time.Time
}

type stubIdentityRepo struct {
	identities    map[string]*domain.UserIdentity
	byID          map[snow.ID]*domain.UserIdentity
	quotas        map[snow.ID]int64
	verified      []snow.ID
	codes         map[string]codeRow
	deleted       []snow.ID
	expiredSweeps int
}

func newStubIdentityRepo() *stubIdentityRepo {
	return &stubIdentityRepo{
		identities: map[string]*domain.UserIdentity{},
		byID:       map[snow.ID]*domain.UserIdentity{},
		quotas:     map[snow.ID]int64{},
		codes:      map[string]codeRow{},
	}
}

func identityKey(provider, accountID string) string { return provider + "/" + accountID }

func (s *stubIdentityRepo) IdentityByProviderAccount(_ context.Context, provider, accountID string) (*domain.UserIdentity, error) {
	if identity, ok := s.identities[identityKey(provider, accountID)]; ok {
		return identity, nil
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (s *stubIdentityRepo) IdentityByID(_ context.Context, id snow.ID) (*domain.UserIdentity, error) {
	if identity, ok := s.byID[id]; ok {
		return identity, nil
	}
	return nil, domain.NewErrorRecordNotFound()
}

func (s *stubIdentityRepo) ListIdentities(_ context.Context, userID snow.ID) ([]*domain.UserIdentity, error) {
	identities := []*domain.UserIdentity{}
	for _, identity := range s.identities {
		if identity.UserID == userID {
			identities = append(identities, identity)
		}
	}
	return identities, nil
}

func (s *stubIdentityRepo) CountIdentities(_ context.Context, userID snow.ID) (int, error) {
	count := 0
	for _, identity := range s.identities {
		if identity.UserID == userID {
			count++
		}
	}
	return count, nil
}

func (s *stubIdentityRepo) CreateIdentity(_ context.Context, identity domain.UserIdentity) error {
	identityCopy := identity
	s.identities[identityKey(identity.Provider, identity.ProviderAccountID)] = &identityCopy
	s.byID[identity.ID] = &identityCopy
	return nil
}

func (s *stubIdentityRepo) DeleteIdentity(_ context.Context, id, userID snow.ID) error {
	identity, ok := s.byID[id]
	if !ok || identity.UserID != userID {
		return domain.NewErrorRecordNotFound()
	}
	s.deleted = append(s.deleted, id)
	delete(s.identities, identityKey(identity.Provider, identity.ProviderAccountID))
	delete(s.byID, id)
	return nil
}

func (s *stubIdentityRepo) SetEmailVerified(_ context.Context, userID snow.ID) error {
	s.verified = append(s.verified, userID)
	return nil
}

func (s *stubIdentityRepo) SeedOwnerQuota(_ context.Context, userID snow.ID, quotaBytes int64) error {
	s.quotas[userID] = quotaBytes
	return nil
}

func (s *stubIdentityRepo) CreateOAuthLoginCode(_ context.Context, code string, userID snow.ID, expiresAt time.Time) error {
	s.codes[code] = codeRow{userID: userID, expiresAt: expiresAt}
	return nil
}

func (s *stubIdentityRepo) ConsumeOAuthLoginCode(_ context.Context, code string) (snow.ID, time.Time, error) {
	row, ok := s.codes[code]
	if !ok {
		return 0, time.Time{}, domain.NewErrorRecordNotFound()
	}
	delete(s.codes, code)
	return row.userID, row.expiresAt, nil
}

func (s *stubIdentityRepo) DeleteExpiredOAuthLoginCodes(_ context.Context, _ time.Time) error {
	s.expiredSweeps++
	return nil
}

type oauthEnv struct {
	oauth    *OAuth
	users    *stubUsers
	repo     *stubIdentityRepo
	provider *stubProvider
	now      time.Time
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()

	node, err := snow.NewNode(1)
	require.NoError(t, err)

	env := &oauthEnv{
		users:    &stubUsers{},
		repo:     newStubIdentityRepo(),
		provider: &stubProvider{},
		now:      time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}
	env.oauth = New(
		map[string]Provider{ProviderGitHub: env.provider},
		"test-secret",
		env.users,
		env.repo,
		stubTransactor{},
		node,
		Config{FreeQuotaBytes: 15 * 1024 * 1024 * 1024},
	)
	env.oauth.now = func() time.Time { return env.now }
	return env
}

type stubTransactor struct{}

func (stubTransactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func stateFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func redirectQuery(t *testing.T, target string) url.Values {
	t.Helper()
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	return parsed.Query()
}

func TestStartSignsStateAndSanitizesRedirect(t *testing.T) {
	env := newOAuthEnv(t)

	target, err := env.oauth.Start(ProviderGitHub, "//evil.example", 0)
	require.NoError(t, err)
	state, err := env.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Equal(t, "/", state.Redirect)
	require.Zero(t, state.LinkUser)

	target, err = env.oauth.Start(ProviderGitHub, "/onboarding", 42)
	require.NoError(t, err)
	state, err = env.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Equal(t, "/onboarding", state.Redirect)
	require.Equal(t, int64(42), state.LinkUser)
}

func TestStartRejectsUnknownProvider(t *testing.T) {
	env := newOAuthEnv(t)

	_, err := env.oauth.Start("gitlab", "/", 0)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestCallbackCreatesAccountForNewSocialUser(t *testing.T) {
	env := newOAuthEnv(t)
	env.provider.profile = &Profile{
		AccountID:     "1001",
		Email:         "alice@example.com",
		EmailVerified: true,
		Name:          "Alice",
		AvatarURL:     "https://avatars.example/alice.png",
	}
	state, err := env.oauth.states.Sign("/oauth/callback", 0)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	query := redirectQuery(t, target)
	require.Equal(t, "/oauth/callback", strings.Split(target, "?")[0])
	require.Equal(t, "1", query.Get("new"))
	code := query.Get("code")
	require.NotEmpty(t, code)

	require.Len(t, env.users.users, 1)
	created := env.users.users[0]
	require.Equal(t, "Alice", created.Name)
	require.True(t, created.EmailVerified)
	require.Equal(t, "", created.Password)
	require.Equal(t, "https://avatars.example/alice.png", created.PhotoUrl)
	require.Equal(t, int64(15*1024*1024*1024), env.repo.quotas[created.ID])
	require.Len(t, env.repo.identities, 1)

	user, err := env.oauth.Complete(context.Background(), code)
	require.NoError(t, err)
	require.Equal(t, created.ID, user.ID)
}

func TestCallbackLogsInExistingIdentity(t *testing.T) {
	env := newOAuthEnv(t)
	user := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: true}
	env.users.users = []*domain.User{user}
	identity := &domain.UserIdentity{ID: 9, UserID: 42, Provider: ProviderGitHub, ProviderAccountID: "1001", Email: user.Email}
	env.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	env.repo.byID[9] = identity
	env.provider.profile = &Profile{AccountID: "1001", Email: user.Email, EmailVerified: true}
	state, err := env.oauth.states.Sign("/", 0)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	query := redirectQuery(t, target)
	require.Empty(t, query.Get("new"))
	require.NotEmpty(t, query.Get("code"))
	require.Len(t, env.users.users, 1)
}

func TestCallbackLinksVerifiedEmailToUnconfirmedAccount(t *testing.T) {
	env := newOAuthEnv(t)
	user := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: false}
	env.users.users = []*domain.User{user}
	env.provider.profile = &Profile{AccountID: "1001", Email: user.Email, EmailVerified: true}
	state, err := env.oauth.states.Sign("/", 0)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	require.NotEmpty(t, redirectQuery(t, target).Get("code"))
	require.Equal(t, []snow.ID{42}, env.repo.verified)
	require.Len(t, env.repo.identities, 1)
	require.Len(t, env.users.users, 1)
}

func TestCallbackRejectsUnverifiedEmailConflict(t *testing.T) {
	env := newOAuthEnv(t)
	env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: false}
	state, err := env.oauth.states.Sign("/", 0)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	require.Equal(t, "email_conflict", redirectQuery(t, target).Get("oauth_error"))
	require.Empty(t, env.repo.identities)
}

func TestCallbackRequiresAnEmailForNewAccounts(t *testing.T) {
	env := newOAuthEnv(t)
	env.provider.profile = &Profile{AccountID: "1001", EmailVerified: false}
	state, err := env.oauth.states.Sign("/", 0)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	require.Equal(t, "email_required", redirectQuery(t, target).Get("oauth_error"))
	require.Empty(t, env.users.users)
}

func TestCallbackLinkModeAttachesIdentity(t *testing.T) {
	env := newOAuthEnv(t)
	env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err := env.oauth.states.Sign("/settings/profile", 42)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	query := redirectQuery(t, target)
	require.Equal(t, "/settings/profile", strings.Split(target, "?")[0])
	require.Equal(t, "linked", query.Get("oauth"))
	require.Empty(t, query.Get("code"))
	require.Len(t, env.repo.identities, 1)
	require.Empty(t, env.repo.codes)
}

func TestCallbackLinkModeRejectsForeignIdentity(t *testing.T) {
	env := newOAuthEnv(t)
	env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	identity := &domain.UserIdentity{ID: 9, UserID: 99, Provider: ProviderGitHub, ProviderAccountID: "1001"}
	env.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	env.repo.byID[9] = identity
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err := env.oauth.states.Sign("/settings/profile", 42)
	require.NoError(t, err)

	target, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", state)
	require.NoError(t, err)
	require.Equal(t, "identity_taken", redirectQuery(t, target).Get("oauth_error"))
}

func TestCallbackRejectsTamperedState(t *testing.T) {
	env := newOAuthEnv(t)
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}

	_, err := env.oauth.Callback(context.Background(), ProviderGitHub, "code", "dGFtcGVyZWQ.c2ln")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestCompleteRejectsUnknownAndExpiredCodes(t *testing.T) {
	env := newOAuthEnv(t)

	_, err := env.oauth.Complete(context.Background(), "missing")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)

	env.users.users = []*domain.User{{ID: 42, Email: "alice@example.com"}}
	env.repo.codes["stale"] = codeRow{userID: 42, expiresAt: env.now.Add(-time.Second)}
	_, err = env.oauth.Complete(context.Background(), "stale")
	require.Error(t, err)
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestDisconnectIdentityGuardsTheLastLoginMethod(t *testing.T) {
	env := newOAuthEnv(t)
	passwordless := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: true}
	identity := &domain.UserIdentity{ID: 9, UserID: 42, Provider: ProviderGitHub, ProviderAccountID: "1001"}
	env.users.users = []*domain.User{passwordless}
	env.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	env.repo.byID[9] = identity

	err := env.oauth.DisconnectIdentity(context.Background(), 42, 9)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Empty(t, env.repo.deleted)

	passwordless.Password = "hash"
	err = env.oauth.DisconnectIdentity(context.Background(), 42, 9)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{9}, env.repo.deleted)
}

func TestDisconnectIdentityRejectsForeignIdentity(t *testing.T) {
	env := newOAuthEnv(t)
	env.repo.byID[9] = &domain.UserIdentity{ID: 9, UserID: 99, Provider: ProviderGitHub, ProviderAccountID: "1001"}

	err := env.oauth.DisconnectIdentity(context.Background(), 42, 9)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestStateRoundTripAndExpiry(t *testing.T) {
	signer := newStateSigner("secret")
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	signer.now = func() time.Time { return now }

	state, err := signer.Sign("/onboarding", 7)
	require.NoError(t, err)
	data, err := signer.Verify(state)
	require.NoError(t, err)
	require.Equal(t, "/onboarding", data.Redirect)
	require.Equal(t, int64(7), data.LinkUser)

	now = now.Add(stateTTL + time.Minute)
	_, err = signer.Verify(state)
	require.Error(t, err)
}
