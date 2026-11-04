package grpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

// UserID returns the account id the session's access token was issued for
// (its JWT subject). The token is read without verification: the result only
// labels data, the server stays authoritative.
func (c *Client) UserID(ctx context.Context) (string, error) {
	token, err := c.session.AccessToken(ctx, c.transport.URL())
	if err != nil {
		return "", err
	}
	return tokenSubject(token)
}

func tokenSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", domain.NewTokenError("the stored access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", domain.NewTokenError("the stored access token is not a JWT")
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Subject == "" {
		return "", domain.NewTokenError("the stored access token has no subject")
	}
	return claims.Subject, nil
}
