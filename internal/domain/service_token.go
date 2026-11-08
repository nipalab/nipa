package domain

import (
	"slices"
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

// ServiceTokenPrefix starts every service token so the REST auth filter can
// tell it apart from a JWT access token.
const ServiceTokenPrefix = "nipa_st_"

// Service token scopes.
const (
	ServiceTokenScopeRead          = "read"
	ServiceTokenScopeChecksWrite   = "checks:write"
	ServiceTokenScopeWebhooksWrite = "webhooks:write"
)

// ServiceTokenScopes lists every scope in display order.
var ServiceTokenScopes = []string{
	ServiceTokenScopeRead,
	ServiceTokenScopeChecksWrite,
	ServiceTokenScopeWebhooksWrite,
}

// IsValidServiceTokenScope reports whether scope is known.
func IsValidServiceTokenScope(scope string) bool {
	return slices.Contains(ServiceTokenScopes, scope)
}

// ServiceToken is a project-scoped credential for integrations. It acts with
// the permissions of the admin who created it, limited to its project and
// scopes. Only the SHA-256 of the token is stored; Hint keeps the first
// characters so admins can tell tokens apart.
type ServiceToken struct {
	ID         snow.ID    `json:"id"`
	ProjectID  snow.ID    `json:"project_id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	Hint       string     `json:"hint"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  snow.ID    `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// HasScope reports whether the token grants scope.
func (t *ServiceToken) HasScope(scope string) bool {
	return slices.Contains(t.Scopes, scope)
}

// Active reports whether the token is neither revoked nor expired at now.
func (t *ServiceToken) Active(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return t.ExpiresAt == nil || now.Before(*t.ExpiresAt)
}
