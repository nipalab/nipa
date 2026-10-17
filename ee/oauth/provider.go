// Package oauth implements social login and signup for the enterprise SaaS:
// Google and GitHub OAuth2 providers, account linking and the one-time handoff
// code that lets the SPA pick up the session. It is wired only by the
// enterprise server when SAAS_MODE is on.
package oauth

import (
	"context"
	"strings"
)

// Provider names.
const (
	ProviderGoogle = "google"
	ProviderGitHub = "github"
)

// Profile is the normalized identity a provider reports after a successful
// authorization code exchange.
type Profile struct {
	AccountID     string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

// Provider is one configured OAuth2 identity provider.
type Provider interface {
	Name() string
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (*Profile, error)
}

// Config carries the provider credentials and the public base URL used to
// build callback URLs. A provider without client id and secret is disabled.
type ProvidersConfig struct {
	BaseURL            string
	GoogleClientID     string
	GoogleClientSecret string
	GitHubClientID     string
	GitHubClientSecret string
}

// NewProviders builds the enabled provider set keyed by name.
func NewProviders(cfg ProvidersConfig) map[string]Provider {
	providers := map[string]Provider{}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return providers
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		providers[ProviderGoogle] = newGoogle(newProviderConfig(cfg.GoogleClientID, cfg.GoogleClientSecret, base, ProviderGoogle))
	}
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		providers[ProviderGitHub] = newGitHub(newProviderConfig(cfg.GitHubClientID, cfg.GitHubClientSecret, base, ProviderGitHub))
	}
	return providers
}

type providerConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func newProviderConfig(clientID, clientSecret, base, provider string) providerConfig {
	return providerConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  base + "/api/v1/auth/oauth/" + provider + "/callback",
	}
}

// ProviderNames returns the enabled provider names.
func ProviderNames(providers map[string]Provider) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}
