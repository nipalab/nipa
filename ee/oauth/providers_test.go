package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProvidersConfiguration(t *testing.T) {
	require.Empty(t, NewProviders(ProvidersConfig{}), "without a public base URL every provider stays disabled")
	require.Empty(t, NewProviders(ProvidersConfig{BaseURL: "https://nipa.example"}), "credentials are required")

	providers := NewProviders(ProvidersConfig{
		BaseURL:            "https://nipa.example/",
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
		GitHubClientID:     "github-id",
	})
	require.Len(t, providers, 1, "the github half-configured pair stays disabled")
	google, ok := providers[ProviderGoogle].(*googleProvider)
	require.True(t, ok)
	require.Equal(t, "https://nipa.example/api/v1/auth/oauth/google/callback", google.oauth.RedirectURL)
	require.Equal(t, "google", google.Name())
	require.ElementsMatch(t, []string{"google"}, ProviderNames(providers))

	providers = NewProviders(ProvidersConfig{
		BaseURL:            "https://nipa.example",
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
		GitHubClientID:     "github-id",
		GitHubClientSecret: "github-secret",
	})
	require.Len(t, providers, 2)
	github, ok := providers[ProviderGitHub].(*githubProvider)
	require.True(t, ok)
	require.Equal(t, "https://nipa.example/api/v1/auth/oauth/github/callback", github.oauth.RedirectURL)
	require.Equal(t, "github", github.Name())
	require.ElementsMatch(t, []string{"github", "google"}, ProviderNames(providers))
}

// providerStub is an httptest server standing in for the provider endpoints;
// each path answers with the configured status and body.
type providerStub struct {
	server *httptest.Server
	status map[string]int
	body   map[string]string
}

func newProviderStub(t *testing.T) *providerStub {
	t.Helper()

	stub := &providerStub{status: map[string]int{}, body: map[string]string{}}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := stub.status[r.URL.Path]
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(stub.body[r.URL.Path]))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func TestGoogleExchange(t *testing.T) {
	stub := newProviderStub(t)
	stub.body["/token"] = `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`
	stub.body["/userinfo"] = `{"sub":"1234","email":"alice@example.com","email_verified":true,"name":"Alice","picture":"https://avatars.example/alice.png"}`

	provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://nipa.example/callback"})
	provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
	provider.userInfo = stub.server.URL + "/userinfo"

	authURL := provider.AuthCodeURL("state-1")
	require.Contains(t, authURL, "accounts.google.com")
	require.Contains(t, authURL, "state=state-1")
	require.Contains(t, authURL, "client_id=id")
	require.Contains(t, authURL, "redirect_uri=")

	profile, err := provider.Exchange(context.Background(), "code")
	require.NoError(t, err)
	require.Equal(t, "1234", profile.AccountID)
	require.Equal(t, "alice@example.com", profile.Email)
	require.True(t, profile.EmailVerified)
	require.Equal(t, "Alice", profile.Name)
	require.Equal(t, "https://avatars.example/alice.png", profile.AvatarURL)

	provider.httpClient = nil
	profile, err = provider.Exchange(context.Background(), "code")
	require.NoError(t, err, "the default HTTP client path also exchanges")
	require.Equal(t, "1234", profile.AccountID)
}

func TestGoogleExchangeErrors(t *testing.T) {
	t.Run("token endpoint fails", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.status["/token"] = http.StatusInternalServerError
		provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"

		_, err := provider.Exchange(context.Background(), "code")
		require.Error(t, err)
		require.Equal(t, "google sign-in failed", err.Error())
	})

	t.Run("token response is not json", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `not-json`
		provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"

		_, err := provider.Exchange(context.Background(), "code")
		require.Error(t, err)
	})

	t.Run("userinfo fails", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.status["/userinfo"] = http.StatusForbidden
		provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
		provider.userInfo = stub.server.URL + "/userinfo"

		_, err := provider.Exchange(context.Background(), "code")
		require.Error(t, err)
		require.Equal(t, "google sign-in failed", err.Error())
	})

	t.Run("userinfo is not json", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/userinfo"] = `<html></html>`
		provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
		provider.userInfo = stub.server.URL + "/userinfo"

		_, err := provider.Exchange(context.Background(), "code")
		require.Error(t, err)
	})

	t.Run("userinfo without a subject", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/userinfo"] = `{"email":"alice@example.com","email_verified":true}`
		provider := newGoogle(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
		provider.userInfo = stub.server.URL + "/userinfo"

		_, err := provider.Exchange(context.Background(), "code")
		require.Error(t, err)
	})
}

func TestGitHubExchange(t *testing.T) {
	newProvider := func(stub *providerStub) *githubProvider {
		provider := newGitHub(providerConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://nipa.example/callback"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
		provider.userURL = stub.server.URL + "/user"
		provider.emailsURL = stub.server.URL + "/user/emails"
		return provider
	}

	stub := newProviderStub(t)
	stub.body["/token"] = `{"access_token":"tok","token_type":"bearer"}`
	stub.body["/user"] = `{"id":1001,"login":"alice","name":"Alice","avatar_url":"https://avatars.example/alice.png"}`
	stub.body["/user/emails"] = `[{"email":"other@example.com","primary":false,"verified":true},{"email":"alice@example.com","primary":true,"verified":true}]`
	provider := newProvider(stub)

	authURL := provider.AuthCodeURL("state-2")
	require.Contains(t, authURL, "github.com/login/oauth/authorize")
	require.Contains(t, authURL, "state=state-2")
	require.Contains(t, authURL, "user%3Aemail")

	profile, err := provider.Exchange(context.Background(), "code")
	require.NoError(t, err)
	require.Equal(t, "1001", profile.AccountID)
	require.Equal(t, "alice@example.com", profile.Email)
	require.True(t, profile.EmailVerified)
	require.Equal(t, "Alice", profile.Name)
	require.Equal(t, "https://avatars.example/alice.png", profile.AvatarURL)

	provider.httpClient = nil
	profile, err = provider.Exchange(context.Background(), "code")
	require.NoError(t, err, "the default HTTP client path also exchanges")
	require.Equal(t, "1001", profile.AccountID)
}

func TestGitHubExchangeFallsBackToNamedUserAndOtherVerifiedEmails(t *testing.T) {
	stub := newProviderStub(t)
	stub.body["/token"] = `{"access_token":"tok"}`
	stub.body["/user"] = `{"id":7,"login":"bob"}`
	stub.body["/user/emails"] = `[{"email":"bob@example.com","primary":true,"verified":false},{"email":"bob.work@example.com","primary":false,"verified":true}]`
	provider := newGitHub(providerConfig{ClientID: "id", ClientSecret: "secret"})
	provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
	provider.userURL = stub.server.URL + "/user"
	provider.emailsURL = stub.server.URL + "/user/emails"

	profile, err := provider.Exchange(context.Background(), "code")
	require.NoError(t, err)
	require.Equal(t, "bob", profile.Name, "a missing display name falls back to the login")
	require.Equal(t, "bob.work@example.com", profile.Email)
	require.True(t, profile.EmailVerified)
}

func TestGitHubExchangeWithoutVerifiedEmail(t *testing.T) {
	stub := newProviderStub(t)
	stub.body["/token"] = `{"access_token":"tok"}`
	stub.body["/user"] = `{"id":7,"login":"bob","name":"Bob"}`
	stub.body["/user/emails"] = `[{"email":"bob@example.com","primary":true,"verified":false}]`
	provider := newGitHub(providerConfig{ClientID: "id", ClientSecret: "secret"})
	provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
	provider.userURL = stub.server.URL + "/user"
	provider.emailsURL = stub.server.URL + "/user/emails"

	profile, err := provider.Exchange(context.Background(), "code")
	require.NoError(t, err)
	require.Empty(t, profile.Email)
	require.False(t, profile.EmailVerified)
}

func TestGitHubExchangeErrors(t *testing.T) {
	newProvider := func(stub *providerStub) *githubProvider {
		provider := newGitHub(providerConfig{ClientID: "id", ClientSecret: "secret"})
		provider.oauth.Endpoint.TokenURL = stub.server.URL + "/token"
		provider.userURL = stub.server.URL + "/user"
		provider.emailsURL = stub.server.URL + "/user/emails"
		return provider
	}

	t.Run("token endpoint fails", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.status["/token"] = http.StatusBadRequest
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
		require.Equal(t, "github sign-in failed", err.Error())
	})

	t.Run("user endpoint fails", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.status["/user"] = http.StatusInternalServerError
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
		require.Equal(t, "github sign-in failed", err.Error())
	})

	t.Run("user response is not json", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/user"] = `not-json`
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
	})

	t.Run("user response has no id", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/user"] = `{"login":"nobody"}`
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
	})

	t.Run("emails endpoint fails", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/user"] = `{"id":7,"login":"bob"}`
		stub.status["/user/emails"] = http.StatusUnauthorized
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
	})

	t.Run("emails response is not json", func(t *testing.T) {
		stub := newProviderStub(t)
		stub.body["/token"] = `{"access_token":"tok"}`
		stub.body["/user"] = `{"id":7,"login":"bob"}`
		stub.body["/user/emails"] = `nope`
		_, err := newProvider(stub).Exchange(context.Background(), "code")
		require.Error(t, err)
	})
}
