package model

import "time"

type ServiceTokenResponse struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	Scopes     []string   `json:"scopes"`
	CreatedBy  string     `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// Token is the plaintext token, only returned by create.
	Token string `json:"token,omitempty"`
}

type CreateServiceTokenRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type CommitContainsResponse struct {
	Commit    string `json:"commit"`
	Ref       string `json:"ref"`
	RefCommit string `json:"ref_commit,omitempty"`
	Contains  bool   `json:"contains"`
}
