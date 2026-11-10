package oauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

const testTermsVersion = "2026-11-10"

func newTermsOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	env := newOAuthEnv(t)
	env.oauth.WithTermsVersion(func() string { return testTermsVersion })
	return env
}

func TestStartCarriesTheAcceptedTermsVersion(t *testing.T) {
	env := newTermsOAuthEnv(t)

	target, _, err := env.oauth.Start(ProviderGitHub, "/", 0, true)
	require.NoError(t, err)
	state, err := env.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Equal(t, testTermsVersion, state.Terms)

	target, _, err = env.oauth.Start(ProviderGitHub, "/", 0, false)
	require.NoError(t, err)
	state, err = env.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Empty(t, state.Terms)

	target, _, err = env.oauth.Start(ProviderGitHub, "/", 42, true)
	require.NoError(t, err)
	state, err = env.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Empty(t, state.Terms, "linking an identity never accepts terms")

	disabled := newOAuthEnv(t)
	target, _, err = disabled.oauth.Start(ProviderGitHub, "/", 0, true)
	require.NoError(t, err)
	state, err = disabled.oauth.states.Verify(stateFromURL(t, target))
	require.NoError(t, err)
	require.Empty(t, state.Terms)
}

func TestCallbackRecordsTermsForANewAccount(t *testing.T) {
	env := newTermsOAuthEnv(t)
	rec := &recordedAudit{}
	env.oauth.WithAudit(rec)
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true, Name: "Alice"}
	state, err := env.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)

	_, err = env.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.NoError(t, err)
	require.Len(t, env.users.users, 1)
	require.Equal(t, []domain.TermsAcceptance{{
		UserID: env.users.users[0].ID, Version: testTermsVersion, Source: "oauth:github", AcceptedAt: env.now,
	}}, env.repo.terms)
	require.Len(t, rec.entries, 1)
	require.Equal(t, testTermsVersion, rec.entries[0].Metadata["terms_version"])
}

func TestCallbackWithoutAcceptanceRecordsNothing(t *testing.T) {
	env := newTermsOAuthEnv(t)
	rec := &recordedAudit{}
	env.oauth.WithAudit(rec)
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err := env.oauth.states.Sign("/", 0, testBinding, "")
	require.NoError(t, err)

	_, err = env.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.NoError(t, err)
	require.Len(t, env.users.users, 1)
	require.Empty(t, env.repo.terms)
	require.NotContains(t, rec.entries[0].Metadata, "terms_version")
}

func TestCallbackIgnoresAStaleTermsVersion(t *testing.T) {
	env := newTermsOAuthEnv(t)
	env.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err := env.oauth.states.Sign("/", 0, testBinding, "2020-01-01")
	require.NoError(t, err)

	_, err = env.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.NoError(t, err)
	require.Empty(t, env.repo.terms)
}

func TestCallbackRecordsTermsForExistingAccounts(t *testing.T) {
	env := newTermsOAuthEnv(t)
	user := &domain.User{ID: 42, Email: "alice@example.com", EmailVerified: true}
	env.users.users = []*domain.User{user}
	identity := &domain.UserIdentity{ID: 9, UserID: 42, Provider: ProviderGitHub, ProviderAccountID: "1001", Email: user.Email}
	env.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	env.repo.byID[9] = identity
	env.provider.profile = &Profile{AccountID: "1001", Email: user.Email, EmailVerified: true}
	state, err := env.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)

	_, err = env.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.NoError(t, err)
	require.Len(t, env.repo.terms, 1)
	require.Equal(t, user.ID, env.repo.terms[0].UserID)

	matched := newTermsOAuthEnv(t)
	matched.users.users = []*domain.User{{ID: 43, Email: "bob@example.com", EmailVerified: true}}
	matched.provider.profile = &Profile{AccountID: "2002", Email: "bob@example.com", EmailVerified: true}
	state, err = matched.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)

	_, err = matched.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.NoError(t, err)
	require.Len(t, matched.repo.terms, 1)
	require.Equal(t, "oauth:github", matched.repo.terms[0].Source)
}

func TestCallbackTermsWriteFailures(t *testing.T) {
	newAccount := newTermsOAuthEnv(t)
	newAccount.repo.termsErr = errors.New("terms down")
	newAccount.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err := newAccount.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)
	_, err = newAccount.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.EqualError(t, err, "terms down")

	existing := newTermsOAuthEnv(t)
	existing.repo.termsErr = errors.New("terms down")
	identity := &domain.UserIdentity{ID: 9, UserID: 42, Provider: ProviderGitHub, ProviderAccountID: "1001"}
	existing.users.users = []*domain.User{{ID: 42, Email: "alice@example.com", EmailVerified: true}}
	existing.repo.identities[identityKey(ProviderGitHub, "1001")] = identity
	existing.provider.profile = &Profile{AccountID: "1001", Email: "alice@example.com", EmailVerified: true}
	state, err = existing.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)
	_, err = existing.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.EqualError(t, err, "terms down")

	matched := newTermsOAuthEnv(t)
	matched.repo.termsErr = errors.New("terms down")
	matched.users.users = []*domain.User{{ID: 43, Email: "bob@example.com", EmailVerified: true}}
	matched.provider.profile = &Profile{AccountID: "2002", Email: "bob@example.com", EmailVerified: true}
	state, err = matched.oauth.states.Sign("/", 0, testBinding, testTermsVersion)
	require.NoError(t, err)
	_, err = matched.oauth.Callback(context.Background(), ProviderGitHub, "code", state, testBinding)
	require.EqualError(t, err, "terms down")
}

func TestStartEndpointPassesAcceptTerms(t *testing.T) {
	server := newOAuthHandlerServer(t, 42)
	server.env.oauth.WithTermsVersion(func() string { return testTermsVersion })

	for _, value := range []string{"1", "true"} {
		resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start?accept_terms="+value, "")
		require.Equal(t, http.StatusFound, resp.StatusCode)
		state, err := server.env.oauth.states.Verify(strings.TrimPrefix(resp.Header.Get("Location"), "https://github.example/authorize?state="))
		require.NoError(t, err)
		require.Equal(t, testTermsVersion, state.Terms)
	}

	resp, _ := server.do(t, http.MethodGet, "/api/v1/auth/oauth/github/start?accept_terms=no", "")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	state, err := server.env.oauth.states.Verify(strings.TrimPrefix(resp.Header.Get("Location"), "https://github.example/authorize?state="))
	require.NoError(t, err)
	require.Empty(t, state.Terms)
}
