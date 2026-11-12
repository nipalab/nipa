package domain

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nipalab/nipa/internal/snow"
)

type claimKey struct{}

func ContextWithClaim(ctx context.Context, claim Claims) context.Context {
	return context.WithValue(ctx, claimKey{}, claim)
}

func ClaimFromContext(ctx context.Context) (Claims, bool) {
	claim, ok := ctx.Value(claimKey{}).(Claims)
	return claim, ok
}

// Access tokens carry these registered claims; validation does not enforce
// them yet, so tokens issued before they were added stay valid.
const (
	TokenIssuer   = "nipad"
	TokenAudience = "nipa-api"
)

type Claims struct {
	jwt.RegisteredClaims
	UserID       snow.ID   `json:"user_id"`
	IsSuperAdmin bool      `json:"is_superadmin"`
	IsAdmin      bool      `json:"is_admin"`
	OrgID        []snow.ID `json:"org_id"`
	TokenVersion int64     `json:"tv,omitempty"`
	// ServiceTokenID is set when a project service token authenticated the request.
	ServiceTokenID snow.ID `json:"-"`
}
