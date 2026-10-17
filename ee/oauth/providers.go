package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/nipalab/nipa/internal/domain"
)

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

type googleProvider struct {
	oauth      *oauth2.Config
	userInfo   string
	httpClient *http.Client
}

func newGoogle(cfg providerConfig) *googleProvider {
	return &googleProvider{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL: "https://oauth2.googleapis.com/token",
			},
		},
		userInfo:   googleUserInfoURL,
		httpClient: http.DefaultClient,
	}
}

func (g *googleProvider) Name() string { return ProviderGoogle }

func (g *googleProvider) AuthCodeURL(state string) string {
	return g.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

func (g *googleProvider) Exchange(ctx context.Context, code string) (*Profile, error) {
	token, err := g.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, domain.NewErrorUser("google sign-in failed")
	}
	client := g.client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.userInfo, nil)
	if err != nil {
		return nil, domain.NewErrorInternalServer(err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, domain.NewErrorUser("google sign-in failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, domain.NewErrorUser("google sign-in failed")
	}
	var info struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, domain.NewErrorUser("google sign-in failed")
	}
	if info.Sub == "" {
		return nil, domain.NewErrorUser("google sign-in failed")
	}
	return &Profile{
		AccountID:     info.Sub,
		Email:         info.Email,
		EmailVerified: info.EmailVerified,
		Name:          info.Name,
		AvatarURL:     info.Picture,
	}, nil
}

func (g *googleProvider) client(ctx context.Context, token *oauth2.Token) *http.Client {
	base := g.httpClient
	if base == nil {
		base = http.DefaultClient
	}
	return &http.Client{Transport: &oauth2.Transport{Source: g.oauth.TokenSource(ctx, token), Base: base.Transport}}
}

const (
	githubUserURL   = "https://api.github.com/user"
	githubEmailsURL = "https://api.github.com/user/emails"
)

type githubProvider struct {
	oauth      *oauth2.Config
	userURL    string
	emailsURL  string
	httpClient *http.Client
}

func newGitHub(cfg providerConfig) *githubProvider {
	return &githubProvider{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"read:user", "user:email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://github.com/login/oauth/authorize",
				TokenURL: "https://github.com/login/oauth/access_token",
			},
		},
		userURL:    githubUserURL,
		emailsURL:  githubEmailsURL,
		httpClient: http.DefaultClient,
	}
}

func (g *githubProvider) Name() string { return ProviderGitHub }

func (g *githubProvider) AuthCodeURL(state string) string {
	return g.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

func (g *githubProvider) Exchange(ctx context.Context, code string) (*Profile, error) {
	token, err := g.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, domain.NewErrorUser("github sign-in failed")
	}
	client := g.client(ctx, token)
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := g.getJSON(ctx, client, g.userURL, &user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, domain.NewErrorUser("github sign-in failed")
	}
	email, verified, err := g.primaryEmail(ctx, client)
	if err != nil {
		return nil, err
	}
	name := user.Name
	if name == "" {
		name = user.Login
	}
	return &Profile{
		AccountID:     fmt.Sprintf("%d", user.ID),
		Email:         email,
		EmailVerified: verified,
		Name:          name,
		AvatarURL:     user.AvatarURL,
	}, nil
}

// primaryEmail picks the account's primary verified email; without one the
// login cannot be matched to an account or create one.
func (g *githubProvider) primaryEmail(ctx context.Context, client *http.Client) (string, bool, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.getJSON(ctx, client, g.emailsURL, &emails); err != nil {
		return "", false, err
	}
	for _, entry := range emails {
		if entry.Primary && entry.Verified {
			return entry.Email, true, nil
		}
	}
	for _, entry := range emails {
		if entry.Verified {
			return entry.Email, true, nil
		}
	}
	return "", false, nil
}

func (g *githubProvider) getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return domain.NewErrorUser("github sign-in failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.NewErrorUser("github sign-in failed")
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return domain.NewErrorUser("github sign-in failed")
	}
	return nil
}

func (g *githubProvider) client(ctx context.Context, token *oauth2.Token) *http.Client {
	base := g.httpClient
	if base == nil {
		base = http.DefaultClient
	}
	return &http.Client{Transport: &oauth2.Transport{Source: g.oauth.TokenSource(ctx, token), Base: base.Transport}}
}
